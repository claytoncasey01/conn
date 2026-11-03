package archive

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/snapshot"
)

const (
	// MinAgeMs is how recently touched a transcript may be and still be skipped as maybe live.
	MinAgeMs = 120_000
	// FollowUpSlackMs is how long past MinAgeMs the one follow-up pass waits.
	FollowUpSlackMs = 5_000
	headCwdBytes    = 65_536
	headCwdLines    = 100
)

// Progress is how far a pass is.
type Progress struct {
	Total, Done, Imported, Skipped, Failed int
}

// Backfill brings the archive up to date with the transcripts on disk: sessions that ended while
// conn was down are imported, and a row that is only a live checkpoint (its session is over, but
// its final capture never happened) is replaced by a full replay.
type Backfill struct {
	DB    *history.DB
	Root  string
	Env   Env
	Gate  *Gate
	Leads *sessions.Leads
	// LiveIDs are the sessions running now.
	LiveIDs func() map[string]bool
	// MinAgeMs is how recently touched a transcript may be and still be skipped (0 for MinAgeMs).
	MinAgeMs   float64
	OnProgress func(Progress)
	Now        func() float64
}

// HeadCwd is the working folder a transcript names in its first lines, or "": a cheap look that
// lets a transcript of a repo off the list be skipped without replaying it.
func HeadCwd(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}

	defer func() { _ = file.Close() }()

	buffer := make([]byte, headCwdBytes)

	got, err := io.ReadFull(file, buffer)
	if err != nil {
		if got == 0 {
			return ""
		}
	}

	lines := strings.SplitN(js.DecodeUTF8(buffer[:got], false), "\n", headCwdLines+1)

	for _, line := range lines[:min(len(lines), headCwdLines)] {
		if !strings.Contains(line, `"cwd"`) {
			continue
		}

		parsed, err := js.Parse([]byte(line))
		if err != nil {
			continue
		}

		object, _ := parsed.(*js.Object)
		cwd, _ := object.Get("cwd")

		if text, isText := cwd.(string); isText {
			if filepath.IsAbs(text) {
				return text
			}
		}
	}

	return ""
}

// Pass is one pass over every transcript under the root. It says how many transcripts it skipped
// only for being too new.
func (b *Backfill) Pass(ctx context.Context) (Progress, int) {
	started := b.Now()
	tooNew := 0
	found := Discover(b.Root)
	progress := Progress{Total: len(found), Done: 0, Imported: 0, Skipped: 0, Failed: 0}
	b.report(progress)

	for _, file := range found {
		if ctx.Err() != nil {
			break
		}

		outcome, err := b.one(ctx, file)

		switch {
		case err != nil:
			progress.Failed++

			_ = b.DB.RecordFailure(ctx, file.SessionID, err.Error())
		case outcome == outcomeTooNew:
			progress.Skipped++
			tooNew++
		case outcome == outcomeImported:
			progress.Imported++
		default:
			progress.Skipped++
		}

		progress.Done++
		b.report(progress)
	}

	// A failure about a transcript that is no longer there cannot be retried.
	ids := make([]string, 0, len(found))
	for _, file := range found {
		ids = append(ids, file.SessionID)
	}

	_, _ = b.DB.ClearFailuresExcept(ctx, ids, started)

	return progress, tooNew
}

func (b *Backfill) report(progress Progress) {
	if b.OnProgress != nil {
		b.OnProgress(progress)
	}
}

const (
	outcomeSkipped = iota
	outcomeImported
	outcomeTooNew
)

// one handles a transcript. An error's message is recorded as the transcript's failure, so it
// is passed on as the archive wrote it:
//   - a teammate's transcript whose lead's is in the same folder is skipped: the lead's entry
//     holds it;
//   - a final row whose transcript has not grown, a live session, a file touched lately and a
//     helper run are skipped, and so is a file gone or empty by replay time;
//   - no row: imported as backfill;
//   - a checkpoint, or a final row whose transcript has grown (a resume): replayed in full and
//     written as final, keeping the row's source, name, kind, PR and outcome.
func (b *Backfill) one(ctx context.Context, file Transcript) (int, error) {
	sessionID := file.SessionID
	if b.LiveIDs()[sessionID] {
		return outcomeSkipped, nil
	}

	// A pass that starts before the first list is saved reads nothing at all.
	if b.settingUp() {
		return outcomeSkipped, nil
	}

	settled, final, err := b.DB.FinalSize(ctx, sessionID)
	if err != nil {
		return outcomeSkipped, fmt.Errorf("%w", err)
	}

	if final {
		if file.Size <= settled {
			return outcomeSkipped, nil
		}
	}

	if b.teammate(ctx, file) {
		return outcomeSkipped, nil
	}

	if b.Now()-file.MtimeMs < b.minAge() {
		return outcomeTooNew, nil
	}

	if b.offTheList(file.Path) {
		return outcomeSkipped, nil
	}

	return b.replay(ctx, file, final)
}

// settingUp is whether the first list is still to be saved.
func (b *Backfill) settingUp() bool {
	if b.Gate == nil {
		return false
	}

	return b.Gate.Setup()
}

// offTheList is whether a transcript's folder exists in a repo off the list: it is skipped from
// its head.
func (b *Backfill) offTheList(path string) bool {
	if b.Gate == nil {
		return false
	}

	repo := folderRepo(HeadCwd(path), b.Env)
	if repo == "" {
		return false
	}

	return !b.Gate.admits(repo)
}

// replay replays a transcript in full and writes it; a file gone or empty by now, and a helper
// run, are skipped.
func (b *Backfill) replay(ctx context.Context, file Transcript, final bool) (int, error) {
	replayed, found := Replay(file.Path, b.Leads)
	if !found {
		_ = b.DB.ClearFailure(ctx, file.SessionID)

		return outcomeSkipped, nil
	}

	if strings.HasPrefix(replayed.Parts.Lead.Title, claude.HelperPrefix) {
		return outcomeSkipped, nil
	}

	return b.write(ctx, replayed, final)
}

func (b *Backfill) minAge() float64 {
	if b.MinAgeMs == 0 {
		return MinAgeMs
	}

	return b.MinAgeMs
}

// teammate is whether a transcript is a teammate's with its lead's at hand: part of the lead's
// entry. A checkpoint a build without teammates wrote for it would be counted twice, so it goes.
func (b *Backfill) teammate(ctx context.Context, file Transcript) bool {
	tag := sessions.ReadTeamHead(file.Path).Tag
	if tag == nil {
		return false
	}

	lead := b.Leads.Path(file.Path, *tag, sessions.LeadOpts{Limit: -1, FirstAt: nil, First: ""})
	if lead == "" {
		return false
	}

	_, _ = b.DB.DeleteIfNotFinal(ctx, file.SessionID)
	_ = b.DB.ClearFailure(ctx, file.SessionID)

	return true
}

// write imports a new session, or repairs the row it has.
func (b *Backfill) write(ctx context.Context, replayed Replayed, settled bool) (int, error) {
	sessionID := replayed.SessionID

	had, found, err := b.DB.Get(ctx, sessionID)
	if err != nil {
		return outcomeSkipped, fmt.Errorf("%w", err)
	}

	if !found {
		fresh := FromReplay(replayed, ReplayOpts{
			Source: sourceBackfill, Kind: sessions.KindAdhoc, Name: "", PR: nil, Run: nil, PID: 0,
			StartedAt: 0, Cwd: "", NotFinal: false,
		}, b.Env)
		if !b.Gate.admits(fresh.Repo) {
			return outcomeSkipped, nil
		}

		added, err := b.DB.InsertIfAbsent(ctx, fresh)
		if err != nil {
			return outcomeSkipped, fmt.Errorf("%w", err)
		}

		_ = b.DB.ClearFailure(ctx, sessionID)

		if added {
			return outcomeImported, nil
		}

		return outcomeSkipped, nil
	}

	return b.repair(ctx, replayed, had, settled)
}

// repair replaces a row with the full replay. The registry facts live only in the row, so they
// are carried over.
func (b *Backfill) repair(ctx context.Context, replayed Replayed, had history.Stored,
	settled bool,
) (int, error) {
	sessionID := replayed.SessionID
	detail := had.Detail
	pid, _ := detailValue(detail, "pid").(float64)
	cwd, _ := detailValue(detail, "cwd").(string)

	entry := FromReplay(replayed, ReplayOpts{
		Source: had.Row.Source, Kind: had.Row.Kind, Name: had.Row.Name, PR: storedPR(detail),
		Run: nil, PID: pid, StartedAt: had.Row.StartedAt, Cwd: cwd, NotFinal: false,
	}, b.Env)

	rowRepo := ""
	if had.Row.Repo != nil {
		rowRepo = *had.Row.Repo
	}

	if !b.Gate.admits(firstText(entry.Repo, rowRepo)) {
		return outcomeSkipped, nil
	}

	entry.Outcome = had.Row.Outcome

	// The row's name may be only a snippet of the first prompt: keep what the row had.
	info, _, err := b.DB.ResumeInfo(ctx, sessionID)
	if err != nil {
		return outcomeSkipped, fmt.Errorf("%w", err)
	}

	entry.Named = info.Named
	if replayed.Parts.Lead.Title != "" {
		entry.Named = true
	}

	err = b.DB.Upsert(ctx, entry)
	if err != nil {
		return outcomeSkipped, fmt.Errorf("%w", err)
	}

	// The transcript grew past the summarised session (a resume): the summary is stale.
	if settled {
		_ = b.DB.MarkSummaryStale(ctx, sessionID)
	}

	_ = b.DB.ClearFailure(ctx, sessionID)

	return outcomeImported, nil
}

func detailValue(detail *js.Object, key string) any {
	value, _ := detail.Get(key)

	return value
}

// storedPR is the PR a stored detail names, nil for none.
func storedPR(detail *js.Object) *snapshot.PR {
	stored, _ := detailValue(detail, "pr").(*js.Object)
	if stored == nil {
		return nil
	}

	number, _ := detailValue(stored, "number").(float64)
	url, _ := detailValue(stored, "url").(string)

	return &snapshot.PR{Number: number, URL: url}
}

// Runner runs the backfill one pass at a time. A pass is asked for at startup, when the
// allowlist changes and, once, a little after a pass that skipped a transcript only for being too
// new. A request made while a pass runs makes one more pass after it, so two never overlap.
type Runner struct {
	mutex    sync.Mutex
	running  bool
	again    bool
	followUp bool
	pass     func() int
	// followAfter is how long a follow-up pass waits.
	followAfter time.Duration
}

// NewRunner runs pass, which says how many transcripts were too new.
func NewRunner(pass func() int, followUp time.Duration) *Runner {
	return &Runner{
		mutex: sync.Mutex{}, running: false, again: false, followUp: false, pass: pass,
		followAfter: followUp,
	}
}

// Request asks for a pass: it starts now when none is running, else once the running one ends.
func (r *Runner) Request() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.running {
		r.again = true

		return
	}

	r.running = true

	go r.loop()
}

func (r *Runner) loop() {
	for {
		r.mutex.Lock()
		r.again = false
		r.mutex.Unlock()

		tooNew := r.pass()

		r.mutex.Lock()

		// One follow-up at a time: it waits for the transcripts that were too new to age.
		if tooNew > 0 {
			if !r.followUp {
				r.followUp = true

				time.AfterFunc(r.followAfter, func() {
					r.mutex.Lock()
					r.followUp = false
					r.mutex.Unlock()
					r.Request()
				})
			}
		}

		if !r.again {
			r.running = false
			r.mutex.Unlock()

			return
		}

		r.mutex.Unlock()
	}
}
