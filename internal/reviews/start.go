package reviews

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/claytoncasey01/conn/internal/advisor"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The HTTP codes the Reviews API answers with.
const (
	codeBadRequest = 400
	codeNotFound   = 404
	codeServer     = 500
)

var plainDigits = regexp.MustCompile(`^[1-9]\d*$`)

// Reply is an API answer: the HTTP code and the JSON body, its keys in order.
type Reply struct {
	Code int
	Body *js.Object
}

func bad(code int, why string) Reply {
	body := js.NewObject()
	body.Set("error", why)

	return Reply{Code: code, Body: body}
}

func okReply(fields ...any) Reply {
	body := js.NewObject()
	body.Set("ok", true)

	for index := 0; index+1 < len(fields); index += 2 {
		name, _ := fields[index].(string)
		body.Set(name, fields[index+1])
	}

	return Reply{Code: codeOK, Body: body}
}

// prNumber is a positive safe integer from a number or from a string of plain digits (not
// '12abc', '1e3', '0x7', ' 7'); false otherwise.
func prNumber(value any) (int64, bool) {
	if found, isText := value.(string); isText {
		if !plainDigits.MatchString(found) {
			return 0, false
		}

		return safeInteger(js.ToNumber(found))
	}

	return safeInteger(value)
}

// Start is REVIEW pressed for PR number of repo (as the request sent them), on model, with the
// advisor the request chose (nil for the settings'): it refuses before any git or cmux call, else
// launches. An error only for a program fault (a 500).
func (r *Reviews) Start(ctx context.Context, repo, number, model, chosenAdvisor any) (
	Reply, error,
) {
	name, isText := repo.(string)
	if !all(isText, repos.IsName(name)) {
		return bad(codeBadRequest, "not a repository"), nil
	}

	num, ok := prNumber(number)
	if !ok {
		return bad(codeBadRequest, "not a PR number"), nil
	}

	chosen, _ := model.(string)
	if !isModel(chosen) {
		return bad(codeBadRequest, "model must be sonnet or opus"), nil
	}

	advisorChoice, isChoice := advisor.Requested(chosenAdvisor)
	if !isChoice {
		return bad(codeBadRequest, "advisor must be "+advisor.List()), nil
	}

	key, err := PRKey(name, num)
	if err != nil {
		return Reply{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deps.Config.Dry {
		return r.startDry(key, num)
	}

	return r.start(ctx, key, num, picks{model: chosen, advisor: advisorChoice})
}

// picks are the models a press chose: the session's, and its advisor's ("" for the settings').
type picks struct {
	model   string
	advisor string
}

func (r *Reviews) startDry(key string, number int64) (Reply, error) {
	for _, row := range r.dryView(r.now(), refreshCount{Seq: 0, Done: 0}).Rows {
		if row.Key != key {
			continue
		}

		if row.Ended != nil {
			break
		}

		if row.NoClone != nil {
			return bad(codeBusy, *row.NoClone), nil
		}

		name, err := r.names.ReviewName(row.Repo, number, row.Launches)
		if err != nil {
			return Reply{}, err
		}

		return okReply("name", name), nil
	}

	return bad(codeNotFound, "that PR is not in the list"), nil
}

// launchable is a PR REVIEW may start on, with what the launch needs.
type launchable struct {
	pr    *PR
	row   *Row
	entry *Entry
	clone string
}

func (r *Reviews) start(ctx context.Context, key string, number int64, chosen picks) (
	Reply, error,
) {
	current, _ := r.settings()
	chosen.advisor = advisor.Or(chosen.advisor, current.Sessions.Advisor)

	if reason := r.disabledReason(&current); reason != nil {
		reply := bad(codeBusy, *reason)
		reply.Body.Set("disabledReason", *reason)

		return reply, nil
	}

	// A launch that cannot be saved is lost at the next restart: its worktree is never swept and
	// its session never closed.
	if !r.writable {
		return bad(codeBusy, r.notSaving()), nil
	}

	target, refusal := r.target(key, &current)
	if refusal != nil {
		return *refusal, nil
	}

	refusal, err := r.refuseBeside(key, number, target)
	if refusal != nil {
		return *refusal, err
	}

	return r.launch(ctx, key, number, chosen, target)
}

// target is the PR a press names when it can be launched: listed now, searched at the last
// poll, with a clone.
func (r *Reviews) target(key string, current *settings.Settings) (launchable, *Reply) {
	now := r.now()
	pull, found := r.prs.Get(key)

	var row *Row

	if found {
		if repos.Allowed(pull.Repo, current.Repos.Allow) {
			row = r.rowFor(pull, now, nil, r.shownSources(current))
		}
	}

	if anyOf(row == nil, r.file == nil) {
		refusal := bad(codeNotFound, "that PR is not in the list, or is not open")

		return launchable{}, &refusal
	}

	if failedOn := r.failedPatterns(); len(failedOn) > 0 {
		if repos.Allowed(pull.Repo, failedOn) {
			refusal := bad(codeBusy, pull.Repo+" could not be searched at the last poll, so this "+
				"row may be out of date: try again once it can")

			return launchable{}, &refusal
		}
	}

	clone := r.cloneOf(pull.Repo)
	if clone == "" {
		refusal := bad(codeBusy, "No local clone of "+pull.Repo+": set a folder in Settings")

		return launchable{}, &refusal
	}

	return launchable{pr: pull, row: row, entry: TrackOf(r.file, key, now), clone: clone}, nil
}

func (r *Reviews) failedPatterns() []string {
	out := make([]string, 0, len(r.failed))
	for _, failed := range r.failed {
		out = append(out, failed.Pattern)
	}

	return out
}

// refuseBeside refuses a second review beside one that is running, starting or being closed.
func (r *Reviews) refuseBeside(key string, number int64, target launchable) (*Reply, error) {
	base, err := r.names.ReviewName(target.pr.Repo, number, 0)
	if err != nil {
		return &Reply{}, err
	}

	if r.finishing[key] {
		refusal := bad(codeBusy, "a previous review of this PR is still being closed: give it "+
			"a few seconds")

		return &refusal, nil
	}

	live := r.deps.Sessions()
	digits := strconv.FormatInt(number, 10)

	// Any folder: your own session under the name counts, so a second review never opens beside
	// one.
	if find(live, func(x *sessions.LiveSession) bool {
		return r.names.IsReviewNameOf(x.Name, target.pr.Repo, number)
	}) != nil {
		refusal := bad(codeBusy, "a session named "+base+" is already running")

		return &refusal, nil
	}

	// A review the version before this one started (named pr-<n> or pr-<n>-<k>, in
	// reviews/pr-<n>/) is one of this number: it may be this PR's.
	oldFolder := filepath.Join(r.dataDir(), "reviews", "pr-"+digits)
	oldName := regexp.MustCompile(`^pr-` + digits + `(?:-\d+)?$`)

	if find(live, func(x *sessions.LiveSession) bool {
		return all(oldName.MatchString(x.Name), Within(x.Cwd, oldFolder))
	}) != nil {
		refusal := bad(codeBusy, "a review of PR #"+digits+" started by an earlier conn is "+
			"still running: close it first")

		return &refusal, nil
	}

	return r.refuseRestart(key, target.entry), nil
}

// refuseRestart refuses a double click, and a second paid review beside one that is still
// coming up (claude can take longer than restartMs to register its session).
func (r *Reviews) refuseRestart(key string, entry *Entry) *Reply {
	now := r.now()
	recent := r.starting[key]

	for _, launch := range entry.Launches {
		if now-launch.At < restartMs {
			recent = true
		}
	}

	if recent {
		refusal := bad(codeBusy, "a review of this PR was just started: give it a few seconds")

		return &refusal
	}

	if len(entry.Launches) == 0 {
		return nil
	}

	newest := entry.Launches[len(entry.Launches)-1]
	if all(newest.SessionID == nil, newest.ClosedAt == nil, now-newest.At < appearMs) {
		refusal := bad(codeBusy, "a review of this PR was started a moment ago and has not "+
			"appeared yet")

		return &refusal
	}

	return r.refuseResuming(newest, now)
}

// refuseResuming refuses a second review beside an open launch while this launch of Conn
// resumes its last quit's sessions, and until they have had the time to appear: its session may
// be one the launch resumes, not yet registered. (One that is live is refused before, by name.)
func (r *Reviews) refuseResuming(newest *Launch, now float64) *Reply {
	if newest.ClosedAt != nil {
		return nil
	}

	if !r.resumeHeld(now) {
		return nil
	}

	refusal := bad(codeBusy, "a review of this PR may be resuming after Conn's launch: give it "+
		"a few minutes")

	return &refusal
}

func (r *Reviews) launch(
	ctx context.Context, key string, number int64, chosen picks, target launchable,
) (Reply, error) {
	// The commit is known, or no re-review line is sent rather than a wrong one.
	var sha *string

	if last := mine(target.pr, *r.me); last != nil {
		if shaShape.MatchString(last.Commit) {
			commit := last.Commit
			sha = &commit
		}
	}

	// The re-review line goes in for a re-review, and for any launch after the first.
	if !anyOf(target.row.State == StateRereview, len(target.entry.Launches) > 0) {
		sha = nil
	}

	name, err := r.names.ReviewName(target.pr.Repo, number, len(target.entry.Launches))
	if err != nil {
		return Reply{}, err
	}

	// Cleared however the launch ends, a panicking one too, so the PR is never left locked.
	r.starting[key] = true
	defer delete(r.starting, key)

	var result LaunchResult

	r.outside(func() {
		result = r.deps.Launch(ctx, LaunchOpts{
			Repo: target.pr.Repo, Number: number, BaseRef: target.pr.BaseRef,
			Model: chosen.model, Advisor: chosen.advisor, Name: name, RepoPath: target.clone,
			DataDir: "", ReReviewSha: sha,
		})
	})

	if !result.OK {
		// The session did not start, or opens as a command: the worktree and the prompt are
		// there, and the session the command opens is this launch's, tracked like any other.
		if launchedByHand(result) {
			r.recordLaunch(target, chosen.model, name, result, false)
		}

		return failedReply(result), nil
	}

	r.recordLaunch(target, chosen.model, name, result, true)

	return okReply("name", name), nil
}

// launchedByHand is a launch whose worktree is ready and whose session the person starts from the
// command to paste.
func launchedByHand(result LaunchResult) bool {
	if result.ManualCommand == "" {
		return false
	}

	return result.Worktree != ""
}

// recordLaunch records a launch; started is that its session really started.
func (r *Reviews) recordLaunch(
	target launchable, model, name string, result LaunchResult, started bool,
) {
	launchedAt := r.now()

	// A session that really started owns the worktree from here, and the earlier launches are
	// retired. One launched by hand has started nothing yet: the earlier session keeps its
	// tracking, and its close after the review, until one does.
	for _, launch := range target.entry.Launches {
		if !started {
			break
		}

		if launch.ClosedAt == nil {
			r.markClosed(launch, launchedAt)
		}
	}

	target.entry.Launches = append(target.entry.Launches, &Launch{
		At: launchedAt, Model: model, Name: name, HeadSha: target.pr.HeadSha,
		Worktree:  result.Worktree,
		SessionID: nil, ClosedAt: nil, Clone: target.clone,
	})

	r.save()
	r.bump()
}

// failedReply is a launch's failure as the page shows it: the error, and the command to paste
// when there is one.
func failedReply(result LaunchResult) Reply {
	reply := okReply("error", result.Error)
	reply.Body.Set("ok", false)

	if result.ManualCommand != "" {
		reply.Body.Set("manualCommand", result.ManualCommand)
	}

	return reply
}

// PlaceLegacy puts an old review record the migration could not place under repo (the Settings
// page asks), or every one (number is "all"). The repo must be allowed.
func (r *Reviews) PlaceLegacy(number, repo any) (Reply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deps.Config.Dry {
		return bad(codeBusy, "nothing is kept in the dry view"), nil
	}

	which := placeAll
	if number != placeAll {
		num, ok := prNumber(number)
		if !ok {
			return bad(codeBadRequest, "not a PR number"), nil
		}

		which = strconv.FormatInt(num, 10)
	}

	current, _ := r.settings()

	name, isText := repo.(string)
	if !all(isText, repos.IsName(name)) {
		return bad(codeBadRequest, "not a repository: write it as owner/repo"), nil
	}

	if !repos.Allowed(name, current.Repos.Allow) {
		return bad(codeBadRequest, name+" is not an allowed repository"), nil
	}

	if r.file == nil {
		return bad(codeBusy, "the review records are not read yet"), nil
	}

	if !r.writable {
		return bad(codeBusy, r.notSaving()), nil
	}

	moved, err := PlaceLegacy(r.file, which, name, r.now())
	if err != nil {
		return Reply{}, err
	}

	if moved == 0 {
		return bad(codeBusy, "there is no such old record, or that repository already has a "+
			"record for that PR"), nil
	}

	r.save()
	r.forcePoll = true
	r.bump()

	return okReply("placed", moved), nil
}
