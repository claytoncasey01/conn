package sessions

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
)

const (
	// askLines is how much of a waiting session's screen is read for its ask: enough that the
	// top of a long command's dialog stays in view.
	askLines = 80
	// askEveryMs is how often a waiting session's screen is read at most.
	askEveryMs = 2000
	// maxReads is how many waiting screens are read at once.
	maxReads = 4
)

// waitingFor is what the registry's waitingFor must say for a parsed dialog to keep its buttons.
// The raw field is compared, not the snapshot's "input needed" default, so a question whose
// entry has no waitingFor loses its buttons: that fails safe (text and OPEN TERMINAL).
func waitingFor(kind string) (string, bool) {
	switch kind {
	case ask.KindPermission:
		return "permission prompt", true
	case ask.KindQuestion:
		return defaultWaiting, true
	}

	return "", false
}

// seen is what a waiting session was last seen asking, and when it was read.
type seen struct {
	sessionID string
	stamp     float64
	ask       *ask.Ask
	// since is when this stamp's ask was first seen; nil until there is one.
	since  *float64
	readAt float64
}

// answeredPrompt is the prompt last answered in a session: a second click before the terminal
// redraws must not press again.
type answeredPrompt struct {
	sessionID string
	stamp     float64
	id        string
}

// askRead is one finished screen read, back on the loop.
type askRead struct {
	pid     string
	session *Session
	stamp   float64
	screen  *string
	now     float64
}

// statusStamp names the prompt a session is stopped on: it changes whenever the session answers
// and asks again.
func statusStamp(reg Reg) float64 {
	if reg.StatusUpdatedAt != nil {
		return *reg.StatusUpdatedAt
	}

	return valueOr(reg.UpdatedAt, 0)
}

// SharedAsk is an ask shown as text only, never as something to click: in a workspace more than
// one live session runs in, or on a dialog the registry does not name the same way.
func SharedAsk(found *ask.Ask, stamp float64) *ask.Ask {
	shared := *found
	shared.Kind = ask.KindUnknown
	shared.Options = []ask.Option{}
	shared.ID = ask.ID(ask.KindUnknown, found.Title, found.Text, shared.Options, stamp)

	return &shared
}

// readAsks starts reading the screen of every session the registry shows as waiting (and that
// runs in a cmux surface). Each is read at most every 2 s, never by two reads at once, at most
// maxReads together (the longest unread first). A read that hangs holds only its own session.
func (s *store) readAsks(ctx context.Context, now float64) {
	s.forgetAsks()
	s.readHostedAsks(now)

	ready := s.dueReads(now)

	js.Sort(ready, func(left, right dueRead) float64 {
		if left.readAt == right.readAt {
			return 0
		}

		if left.readAt < right.readAt {
			return -1
		}

		return 1
	})

	detached := context.WithoutCancel(ctx)

	for _, next := range ready[:min(len(ready), max(0, maxReads-len(s.reading)))] {
		s.reading[next.pid] = true
		go s.readScreen(detached, next, now)
	}
}

// dueRead is a waiting session whose screen is due to be read, with what the read needs taken
// on the loop: the read itself never touches the session, whose registry entry the loop may
// replace meanwhile.
type dueRead struct {
	pid       string
	session   *Session
	processID float64
	stamp     float64
	target    Target
	readAt    float64
}

// dueReads are the waiting sessions in a cmux surface, not being read and not hidden, last read
// 2 s ago or more.
func (s *store) dueReads(now float64) []dueRead {
	keys, runs := s.runKeys()
	ready := []dueRead{}

	for _, pid := range s.order {
		session := s.sessions[pid]
		if session.reg.Status != regWaiting {
			continue
		}

		if s.reading[pid] {
			continue
		}

		// A hidden session's terminal is not read either.
		if s.hidden(session, keys, runs) {
			continue
		}

		target := s.cmuxOf(session)
		if target == nil {
			continue
		}

		readAt := negativeInfinity()
		if last, found := s.asks[pid]; found {
			readAt = last.readAt
		}

		if now-readAt >= askEveryMs {
			ready = append(ready, dueRead{
				pid: pid, session: session, processID: session.reg.PID,
				stamp: statusStamp(session.reg), target: *target, readAt: readAt,
			})
		}
	}

	return ready
}

// forgetAsks drops what no longer stands: an ask of a session that is not waiting any more (or
// is another session now), and a tombstone of a prompt that is over.
func (s *store) forgetAsks() {
	for pid, last := range s.asks {
		session, found := s.sessions[pid]
		if !found {
			delete(s.asks, pid)

			continue
		}

		if session.reg.Status != regWaiting {
			delete(s.asks, pid)

			continue
		}

		if session.reg.SessionID != last.sessionID {
			delete(s.asks, pid)
		}
	}

	for pid, tomb := range s.answered {
		session, found := s.sessions[pid]
		if !found {
			delete(s.answered, pid)

			continue
		}

		if session.reg.Status != regWaiting {
			delete(s.answered, pid)

			continue
		}

		if session.reg.SessionID != tomb.sessionID {
			delete(s.answered, pid)

			continue
		}

		if statusStamp(session.reg) != tomb.stamp {
			delete(s.answered, pid)
		}
	}
}

// readScreen reads one session's screen off the loop, and hands it back. Only while the CLI owns
// its terminal: suspended or exiting, the surface shows the shell, whose text is no ask of the
// agent's (and could draw a fake dialog), so it leaves no ask.
func (s *store) readScreen(ctx context.Context, due dueRead, now float64) {
	result := askRead{pid: due.pid, session: due.session, stamp: due.stamp, screen: nil, now: now}

	if s.ownsTerminal(ctx, due.processID) {
		read := s.deps.Cmux(ctx, readScreenArgs(due.target, askLines))
		if read.Err == nil {
			result.screen = &read.Stdout
		}
	}

	select {
	case s.askDone <- result:
	case <-s.stopped:
	}
}

// ownsTerminal is whether ps says the process is alive and owns its terminal.
func (s *store) ownsTerminal(ctx context.Context, pid float64) bool {
	stat, ended, err := s.statOf(ctx, pid)
	if err != nil {
		return false
	}

	if ended {
		return false
	}

	return ask.OwnsTerminal(stat)
}

// screenRead keeps a finished read. One that returns after the session moved on is dropped, so
// it neither stands for the new prompt nor holds off the next read.
func (s *store) screenRead(result askRead) {
	delete(s.reading, result.pid)

	if s.sessions[result.pid] != result.session {
		return
	}

	if result.session.reg.Status != regWaiting {
		return
	}

	if statusStamp(result.session.reg) != result.stamp {
		return
	}

	var found *ask.Ask
	if result.screen != nil {
		found = s.screenAsk(result.pid, result.session, *result.screen, result.stamp)
	}

	s.record(result.pid, result.session, result.stamp, found, result.now)
}

// screenAsk is what a screen read from a waiting session asks: the dialog it parses to, else the
// screen as unknown text to show, never nothing. Not while the prompt just answered in this
// session is still the wait: a transient frame while the terminal redraws must not replace the
// answered prompt on the page.
func (s *store) screenAsk(pid string, session *Session, screen string, stamp float64) *ask.Ask {
	found := ask.Parse(screen, stamp)
	if found != nil {
		return found
	}

	if s.tombstone(pid, session, stamp) != "" {
		return nil
	}

	return ask.Unread(screen, stamp)
}

// tombstone is the id of the prompt just answered in this session while it is still on the same
// stamp, else "".
func (s *store) tombstone(pid string, session *Session, stamp float64) string {
	tomb, found := s.answered[pid]
	if !found {
		return ""
	}

	if tomb.sessionID != session.reg.SessionID {
		return ""
	}

	if tomb.stamp != stamp {
		return ""
	}

	return tomb.id
}

// justAnswered is whether askID names the prompt just answered in this session, on the same
// status stamp: the same prompt, whatever input count its id holds (see withInputs), since the
// answer's own key moves the count.
func (s *store) justAnswered(pid string, session *Session, stamp float64, askID string) bool {
	answered := s.tombstone(pid, session, stamp)
	if answered == "" {
		return false
	}

	return baseID(answered) == baseID(askID)
}

// record keeps what a session's screen asked, as read; the version moves when the ask appeared,
// went or changed.
func (s *store) record(pid string, session *Session, stamp float64, parsed *ask.Ask, now float64) {
	sessionID := session.reg.SessionID
	prev, had := s.asks[pid]
	same := false

	if had {
		if prev.sessionID == sessionID {
			same = prev.stamp == stamp
		}
	}

	var since *float64
	if same {
		since = prev.since
	}

	found := parsed
	if found != nil {
		if since == nil {
			began := waitBegan(stamp, now)
			since = &began
		}

		copied := *found
		copied.Since = *since
		found = &copied
	}

	s.asks[pid] = seen{sessionID: sessionID, stamp: stamp, ask: found, since: since, readAt: now}

	var before *ask.Ask
	if same {
		before = prev.ask
	}

	if askKey(before) != askKey(found) {
		s.version++
	}
}

// waitBegan is when a wait began: when the registry last changed status, not when this dashboard
// first looked (no tab may have been open), unless that time is not a past one.
func waitBegan(stamp, now float64) float64 {
	if stamp <= 0 {
		return now
	}

	if stamp > now {
		return now
	}

	return stamp
}

func askKey(found *ask.Ask) string {
	if found == nil {
		return ""
	}

	return found.ID + nul + found.Kind + nul + found.Text
}

// askFor is what an ask is shown as: unchanged, or text only when another live session runs in
// the same cmux workspace, or when the registry says the session waits on something other than
// this kind of dialog. shared is the workspace check a caller already made (nil to make it now).
func (s *store) askFor(pid string, session *Session, found *ask.Ask, shared *bool) *ask.Ask {
	if found == nil {
		return nil
	}

	misnamed := false
	if wants, named := waitingFor(found.Kind); named {
		misnamed = !sameText(session.reg.WaitingFor, &wants)
	}

	if shared == nil {
		sharing := false
		if target := s.cmuxOf(session); target != nil {
			sharing = s.sharesWorkspace(pid, target.Workspace, true)
		}

		shared = &sharing
	}

	if misnamed {
		return SharedAsk(found, statusStamp(session.reg))
	}

	if *shared {
		return SharedAsk(found, statusStamp(session.reg))
	}

	return found
}

// askOf is the ask last read for this session's current prompt, or nil: an entry from an
// earlier prompt, or another session under the same pid, is never shown.
func (s *store) askOf(pid string, session *Session) *ask.Ask {
	if session.reg.Status != regWaiting {
		return nil
	}

	last, found := s.asks[pid]
	if !found {
		return nil
	}

	if last.sessionID != session.reg.SessionID {
		return nil
	}

	if last.stamp != statusStamp(session.reg) {
		return nil
	}

	return s.askFor(pid, session, last.ask, nil)
}

// sharesWorkspace is whether another live session runs in the same cmux workspace. Without
// lookup no surface is looked up: a session whose surface has not been looked up yet counts as
// sharing it.
func (s *store) sharesWorkspace(pid, workspace string, lookup bool) bool {
	for _, other := range s.order {
		if other == pid {
			continue
		}

		session := s.sessions[other]
		// A hosted session runs on Conn's own terminal, in no cmux workspace.
		if session.hosted != "" {
			continue
		}

		target := session.cmux

		if lookup {
			target = s.cmuxOf(session)
		} else if !session.looked {
			return true
		}

		if target == nil {
			continue
		}

		if target.Workspace == workspace {
			return true
		}
	}

	return false
}

func readScreenArgs(target Target, lines int) []string {
	return []string{
		"read-screen", "--workspace", target.Workspace, "--surface", target.Surface,
		"--lines", strconv.Itoa(lines),
	}
}

// statOf is `ps -o stat=` of a process, or ended when it has ended (ps exits 1 and prints
// nothing). Any other failure is an error.
func (s *store) statOf(ctx context.Context, pid float64) (string, bool, error) {
	result := s.deps.Stat(ctx, pid)
	if result.Err == nil {
		return js.Trim(result.Stdout), false, nil
	}

	if result.Code == 1 {
		if js.Trim(result.Stdout) == "" {
			return "", true, nil
		}
	}

	return "", false, result.Err
}

// cmuxError is what a failed cmux (or ps) run said: the first line of its stderr, short. Never
// the screen, and never the command line, which holds the ids and for a text answer the start
// of the text.
func cmuxError(result run.Result) string {
	line, _, _ := strings.Cut(js.Trim(result.Stderr), "\n")
	line = js.Cut(line, cmuxErrorUnits)

	if line != "" {
		return line
	}

	if result.Killed {
		return "it did not answer in time"
	}

	return "it failed and said nothing"
}

const cmuxErrorUnits = 200

func negativeInfinity() float64 { return math.Inf(-1) }
