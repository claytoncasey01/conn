package sessions

import (
	"context"
	"net/http"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/pty"
)

// Reviews' close of a finished review, for a session Conn hosts: the input box is read from the
// host's screen, and the close is a stop through the host, never cmux. It is refused while the
// box holds anything, while input was typed too shortly before the read for the screen to show
// it, and while another close of the same session runs.

// hostedInputState is what a hosted session's input box shows (ask.PromptEmpty, PromptDraft or
// PromptNone), or inputUnknown when the host does not hold it running or input was typed too
// shortly before for its screen to show.
func (l *Loop) hostedInputState(sessionID pty.ID) string {
	lines, mark, err := l.store.deps.Host.ScreenLines(sessionID, replyLines)
	if err != nil {
		return inputUnknown
	}

	if !mark.Quiet {
		return inputUnknown
	}

	return ask.PromptState(ask.ScreenText(lines))
}

// closeHosted stops a hosted session whose box the screen shows empty, through the host.
func (l *Loop) closeHosted(ctx context.Context, sessionID pty.ID) Reply {
	ctx = context.WithoutCancel(ctx)
	key := string(sessionID)

	reply := failure(http.StatusNotFound, textNoSession)
	started := false

	_ = l.do(ctx, func(state *store) { reply, started = state.hostedCloseStart(sessionID) })

	if !started {
		return reply
	}

	defer func() { _ = l.do(ctx, func(state *store) { delete(state.closing, key) }) }()

	if l.hostedInputState(sessionID) != ask.PromptEmpty {
		return failure(http.StatusConflict,
			"the session's input box is not visibly empty, so nothing was closed")
	}

	stopCtx, cancel := context.WithTimeout(ctx, hostedStopTimeout)
	defer cancel()

	err := l.store.deps.Host.Stop(stopCtx, sessionID)
	if err != nil {
		return failure(http.StatusInternalServerError, "could not stop it: "+err.Error())
	}

	return done()
}

// hostedCloseStart makes a hosted close's checks on the loop and marks the session as closing:
// it must be running and registered, and not being closed already.
func (s *store) hostedCloseStart(sessionID pty.ID) (Reply, bool) {
	row, found := s.hostedRow(sessionID)
	if !found {
		return failure(http.StatusNotFound, textNoSession), false
	}

	if row.session == nil {
		return failure(http.StatusNotFound, textNoSession), false
	}

	key := string(sessionID)
	if s.closing[key] {
		return failure(http.StatusConflict, "this session is already being closed"), false
	}

	s.closing[key] = true

	return done(), true
}
