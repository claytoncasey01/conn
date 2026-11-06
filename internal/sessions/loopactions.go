package sessions

import (
	"context"
	"net/http"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
)

// The actions on a live session. Each makes its checks on the loop's goroutine, runs ps and
// cmux off it, and comes back to the loop for anything that reads or changes the session list.
// They go on after the page that asked has gone.

// Stop sends SIGTERM to a session (see store.stop).
func (l *Loop) Stop(ctx context.Context, pid string, force bool) Reply {
	if sessionID, hosted := hostedID(pid); hosted {
		return l.stopHosted(ctx, sessionID, force)
	}

	reply := failure(http.StatusServiceUnavailable, ErrStopped.Error())

	_ = l.do(ctx, func(state *store) { reply = state.stop(pid, force, state.now()) })

	return reply
}

// Reply types a message into a session's own terminal: only where cmux can name the exact
// surface, only into an empty prompt, and never text the prompt would read as a shell escape or
// slash command.
func (l *Loop) Reply(ctx context.Context, pid string, text any) Reply {
	if sessionID, hosted := hostedID(pid); hosted {
		return l.replyHosted(ctx, sessionID, text)
	}

	ctx = context.WithoutCancel(ctx)

	var (
		plan    sendPlan
		refused *Reply
	)

	err := l.do(ctx, func(state *store) { plan, refused = state.replyStart(pid, text) })
	if err != nil {
		return failure(http.StatusServiceUnavailable, err.Error())
	}

	if refused != nil {
		return *refused
	}

	defer l.finishSend(ctx, pid)

	// Look at the screen first and only send into a prompt that is visibly empty, and only
	// while the CLI owns the keyboard: suspended or just exited, the surface shows the shell,
	// and typed text plus Enter would run as a command.
	screen, turned, failed := l.store.ownedScreen(ctx, plan, replyLines)
	if failed != nil {
		return l.store.failedSend(ctx, plan.pid, *failed)
	}

	if turned != nil {
		return *turned
	}

	if refusal := untypable(screen); refusal != nil {
		return *refusal
	}

	result := l.store.typeText(ctx, plan.target, plan.message)
	if result.Err != nil {
		return l.store.failedSend(ctx, plan.pid, result)
	}

	return done()
}

// finishSend frees the session for the next send.
func (l *Loop) finishSend(ctx context.Context, pid string) {
	_ = l.do(ctx, func(state *store) { delete(state.sending, pid) })
}

// Answer answers the prompt a session's page showed: a numbered choice of a pick-list or
// permission prompt, or a text for an empty input box. The screen is read again right before the
// send and must still parse, with the session's current status stamp and the shared-workspace
// downgrade, to that same prompt. A refusal returns the ask as it is now, and nothing is retried.
func (l *Loop) Answer(ctx context.Context, pid string, body AnswerBody) Reply {
	if sessionID, hosted := hostedID(pid); hosted {
		return l.answerHosted(ctx, sessionID, body)
	}

	ctx = context.WithoutCancel(ctx)

	var (
		plan    sendPlan
		refused *Reply
	)

	err := l.do(ctx, func(state *store) { plan, refused = state.answerStart(pid, body) })
	if err != nil {
		return failure(http.StatusServiceUnavailable, err.Error())
	}

	if refused != nil {
		return *refused
	}

	sent := false

	defer func() {
		_ = l.do(ctx, func(state *store) {
			delete(state.sending, pid)

			// The prompt is answered: the page must not keep offering it until the next read.
			if sent {
				delete(state.asks, pid)
				state.version++
			}
		})
	}()

	screen, turned, failed := l.store.ownedScreen(ctx, plan, askLines)
	if failed != nil {
		return l.store.failedSend(ctx, plan.pid, *failed)
	}

	if turned != nil {
		return *turned
	}

	var decided answerSend

	_ = l.do(ctx, func(state *store) {
		decided = state.answerCheck(plan, screen, body, state.now())
	})

	if decided.refused != nil {
		return *decided.refused
	}

	if failedSend := l.store.sendKeys(ctx, plan, decided); failedSend != nil {
		return l.store.failedSend(ctx, plan.pid, *failedSend)
	}

	_ = l.do(ctx, func(state *store) { state.answeredAt(plan, decided.askID) })
	sent = true

	return done()
}

// OpenTerminal makes the session's cmux workspace the one in front, so its terminal can be
// answered by hand. Nothing is typed.
func (l *Loop) OpenTerminal(ctx context.Context, pid string) Reply {
	if sessionID, hosted := hostedID(pid); hosted {
		return l.openHosted(ctx, sessionID)
	}

	ctx = context.WithoutCancel(ctx)

	var target *Target

	reply := failure(http.StatusNotFound, textNoSession)

	_ = l.do(ctx, func(state *store) {
		session, shown := state.shown(pid)
		if !shown {
			return
		}

		target = state.cmuxOf(session)
		if target == nil {
			reply = failure(http.StatusConflict, "this session is not running in a cmux "+
				"workspace, so there is no terminal to open")
		}
	})

	if target == nil {
		return reply
	}

	result := l.store.deps.Cmux(ctx, []string{"workspace", "select", target.Workspace})
	if result.Err != nil {
		return failure(http.StatusBadGateway, "cmux would not open it: "+cmuxError(result))
	}

	return done()
}

// InputState is what a session's input box shows right now (ask.PromptEmpty, PromptDraft or
// PromptNone), or "unknown" whenever it cannot be told for certain. A hosted session's is read
// from the host's screen.
func (l *Loop) InputState(ctx context.Context, pid string) string {
	if sessionID, hosted := hostedID(pid); hosted {
		return l.hostedInputState(sessionID)
	}

	var plan sendPlan

	found := false

	_ = l.do(ctx, func(state *store) {
		session, live := state.sessions[pid]
		if !live {
			return
		}

		target := state.cmuxOf(session)
		if target == nil {
			return
		}

		plan = sendPlan{
			pid: session.reg.PID, key: pid, target: *target, message: "", session: session,
			stamp: 0, shared: false, hosted: "", inputs: 0,
		}
		found = true
	})

	if !found {
		return inputUnknown
	}

	prompt, read := l.store.readPrompt(ctx, plan)
	if !read {
		return inputUnknown
	}

	return prompt
}

const inputUnknown = "unknown"

// readPrompt reads a session's input box fresh, and is false while the CLI does not own the
// terminal or a program failed.
func (s *store) readPrompt(ctx context.Context, plan sendPlan) (string, bool) {
	stat := s.deps.Stat(ctx, plan.pid)
	if stat.Err != nil {
		return "", false
	}

	if !ask.OwnsTerminal(js.Trim(stat.Stdout)) {
		return "", false
	}

	read := s.deps.Cmux(ctx, readScreenArgs(plan.target, replyLines))
	if read.Err != nil {
		return "", false
	}

	return ask.PromptState(read.Stdout), true
}

// CloseWorkspace closes a session's whole cmux workspace, or stops a hosted session through the
// host (closeHosted). cmux refuses a workspace with a
// running process, so this uses --force, which ends every process in it: another live session in
// the same workspace is a refusal. The screen is read again right before the close.
func (l *Loop) CloseWorkspace(ctx context.Context, pid string) Reply {
	if sessionID, hosted := hostedID(pid); hosted {
		return l.closeHosted(ctx, sessionID)
	}

	ctx = context.WithoutCancel(ctx)

	var plan sendPlan

	reply := failure(http.StatusNotFound, textNoSession)
	started := false

	_ = l.do(ctx, func(state *store) { plan, reply, started = state.closeStart(pid) })

	if !started {
		return reply
	}

	defer func() { _ = l.do(ctx, func(state *store) { delete(state.closing, pid) }) }()

	stat := l.store.deps.Stat(ctx, plan.pid)
	if stat.Err != nil {
		return failure(http.StatusBadGateway, "cmux would not close it: "+cmuxError(stat))
	}

	if !ask.OwnsTerminal(js.Trim(stat.Stdout)) {
		return failure(http.StatusConflict, "the session is not in the foreground of its "+
			"terminal (suspended or exiting), so nothing was closed")
	}

	read := l.store.deps.Cmux(ctx, readScreenArgs(plan.target, replyLines))
	if read.Err != nil {
		return failure(http.StatusBadGateway, "cmux would not close it: "+cmuxError(read))
	}

	if ask.PromptState(read.Stdout) != ask.PromptEmpty {
		return failure(http.StatusConflict,
			"the session's input box is not visibly empty, so nothing was closed")
	}

	closed := l.store.deps.Cmux(ctx,
		[]string{"workspace", "close", plan.target.Workspace, "--force"})
	if closed.Err != nil {
		return failure(http.StatusBadGateway, "cmux would not close it: "+cmuxError(closed))
	}

	return done()
}

// closeStart makes a close's checks on the loop and marks the session as closing.
func (s *store) closeStart(pid string) (sendPlan, Reply, bool) {
	session, live := s.sessions[pid]
	if !live {
		return sendPlan{}, failure(http.StatusNotFound, textNoSession), false
	}

	target := s.cmuxOf(session)
	if target == nil {
		return sendPlan{}, failure(http.StatusConflict, "this session is not running in a cmux "+
			"workspace, so there is nothing to close"), false
	}

	if s.closing[pid] {
		return sendPlan{}, failure(http.StatusConflict, "this session is already being closed"),
			false
	}

	if s.sharesWorkspace(pid, target.Workspace, true) {
		return sendPlan{}, failure(http.StatusConflict,
			"another session is running in this workspace"), false
	}

	s.closing[pid] = true

	return sendPlan{
		pid: session.reg.PID, key: pid, target: *target, message: "", session: session,
		stamp: 0, shared: false, hosted: "", inputs: 0,
	}, done(), true
}

// ReadAsks starts reading the waiting sessions' screens now (a page that opens gets them read at
// once, not at the next tick).
func (l *Loop) ReadAsks(ctx context.Context) {
	_ = l.do(ctx, func(state *store) { state.readAsks(ctx, state.now()) })
}
