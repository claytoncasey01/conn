package sessions

import (
	"context"
	"errors"
	"net/http"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/pty"
)

// textTypedJustNow is a close refused because input reached the session too shortly before.
const textTypedJustNow = "something was typed into the session's terminal just now, so nothing " +
	"was closed"

// Reviews' close of a finished review, for a session Conn hosts: the input box is read from the
// host's screen, and the close is a stop through the host, never cmux. It is refused while the
// box holds anything, while input was typed too shortly before the read for the screen to show
// it, while another close of the same session runs, and, checked again in the session's write
// turn as the stop is sent (pty.Host.StopIfStill), when input came after the read, a program
// claude started holds the terminal, or the box no longer shows empty.

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

	lines, mark, err := l.store.deps.Host.ScreenLines(sessionID, replyLines)
	if err != nil {
		return closeFailure(err)
	}

	if !mark.Quiet {
		return failure(http.StatusConflict, textTypedJustNow)
	}

	if !emptyBox(lines) {
		return failure(http.StatusConflict,
			"the session's input box is not visibly empty, so nothing was closed")
	}

	stopCtx, cancel := context.WithTimeout(ctx, hostedStopTimeout)
	defer cancel()

	// The stop is sent in the session's write turn, only if nothing was typed since the read,
	// claude still holds its terminal and the box is still empty.
	err = l.store.deps.Host.StopIfStill(stopCtx, sessionID, mark, replyLines, emptyBox)
	if err != nil {
		return closeFailure(err)
	}

	return done()
}

// closeFailure is a hosted close the host refused or could not finish, in a close's words.
func closeFailure(err error) Reply {
	switch {
	// Before ErrOtherInput, which it wraps.
	case errors.Is(err, pty.ErrPasteOpen):
		return failure(http.StatusConflict, "a paste into the session's terminal is open, so "+
			"nothing was closed")
	case errors.Is(err, pty.ErrInputBlocked):
		return failure(http.StatusConflict, "the session is not taking input, so nothing was "+
			"closed")
	case errors.Is(err, pty.ErrNotForeground):
		return failure(http.StatusConflict, "a program the session started has its terminal, "+
			"so nothing was closed")
	case errors.Is(err, pty.ErrLineMode):
		return failure(http.StatusConflict, "a program the session started is reading a line "+
			"on its terminal, so nothing was closed")
	case errors.Is(err, pty.ErrOtherInput):
		return failure(http.StatusConflict, textTypedJustNow)
	case errors.Is(err, pty.ErrScreenChanged):
		return failure(http.StatusConflict, "the session's screen changed while it was being "+
			"closed, so nothing was closed")
	case errors.Is(err, pty.ErrEnded):
		return failure(http.StatusNotFound, textNoSession)
	case errors.Is(err, pty.ErrNoSession):
		return failure(http.StatusNotFound, textNoSession)
	}

	return failure(http.StatusInternalServerError, "could not stop it: "+err.Error())
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
