package sessions

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/pty"
)

// Typing into a hosted session. Every write is a pty.Host.WriteIfStill against the screen read
// it was decided from: it types nothing if other input reached the program since that read (or
// too shortly before it for the screen to show it), if a program the session started has the
// terminal, or if the screen no longer shows what was checked. A message is typed in two
// writes: the text into a box the screen shows empty, then Enter once the screen shows the text
// in the box and no dialog, so an Enter never lands on a dialog that came up meanwhile.

const (
	// pasteStart and pasteEnd bracket a message as a paste, as a terminal does for a paste, so a
	// line break in it is part of the message and not Enter. They are sent only to a program
	// that asked for them (mode 2004); Claude Code does.
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
	enterKey   = "\r"

	// enterPoll is how often the screen is read for the typed message, and enterWaitMax bounds
	// the wait: well above the host's settle window for input, which every read before Enter
	// must be past.
	enterPoll    = 50 * time.Millisecond
	enterWaitMax = 2 * time.Second

	textScreenChanged = "the screen changed while this was being sent, so nothing was sent"
	textOtherInput    = "something else was typed into the session's terminal just now, so " +
		"nothing was sent"
	textNotTaking = "the session did not take the input in time; check its terminal for " +
		"what reached it"
	textNotSent = "the message is in the session's input box but was not sent, because its " +
		"screen changed; send it from its terminal"
	textNoPaste = "the session's terminal does not take pastes, so a message of several lines " +
		"was not sent"
)

// typeReply types a reply into a hosted session: into an input box the screen shows empty,
// then Enter.
func (l *Loop) typeReply(sessionID pty.ID, message string) Reply {
	host := l.store.deps.Host

	lines, mark, err := host.ScreenLines(sessionID, replyLines)
	if err != nil {
		return hostedFailure(err)
	}

	if refusal := untypable(ask.ScreenText(lines)); refusal != nil {
		return *refusal
	}

	return l.typeMessage(sessionID, mark, replyLines, emptyBox, message)
}

// typeAnswer types what an answer decided: one key into the prompt it was decided from, or a
// text into that prompt's box, then Enter.
func (l *Loop) typeAnswer(plan sendPlan, decided answerSend, mark pty.ScreenMark) Reply {
	still := promptStillShown(plan.stamp, decided.askID)
	if decided.text {
		return l.typeMessage(plan.hosted, mark, askLines, still, plan.message)
	}

	err := l.store.deps.Host.WriteIfStill(plan.hosted, mark, askLines, still,
		[]byte(decided.choice))
	if err != nil {
		return hostedFailure(err)
	}

	return done()
}

// typeMessage types message while the screen read with mark still passes still, then presses
// Enter once the screen shows it in the box.
func (l *Loop) typeMessage(sessionID pty.ID, mark pty.ScreenMark, count int,
	still func([]string) bool, message string,
) Reply {
	keys, refused := messageKeys(message, mark.BracketedPaste)
	if refused != nil {
		return *refused
	}

	err := l.store.deps.Host.WriteIfStill(sessionID, mark, count, still, keys)
	if err != nil {
		return hostedFailure(err)
	}

	return l.pressEnter(sessionID)
}

// messageKeys is what types message: as a paste where the program takes pastes, else as keys,
// which only a message of one line can be.
func messageKeys(message string, bracketedPaste bool) ([]byte, *Reply) {
	if bracketedPaste {
		return []byte(pasteStart + message + pasteEnd), nil
	}

	if strings.Contains(message, "\n") {
		refused := failure(http.StatusConflict, textNoPaste)

		return nil, &refused
	}

	return []byte(message), nil
}

// pressEnter presses Enter once a screen read past the settle window shows the message typed in
// the input box and no dialog, within enterWaitMax.
func (l *Loop) pressEnter(sessionID pty.ID) Reply {
	host := l.store.deps.Host

	for range enterWaitMax / enterPoll {
		time.Sleep(enterPoll)

		lines, mark, err := host.ScreenLines(sessionID, replyLines)
		if err != nil {
			return hostedFailure(err)
		}

		if !mark.Quiet {
			continue
		}

		if !draftShown(lines) {
			continue
		}

		err = host.WriteIfStill(sessionID, mark, replyLines, draftShown, []byte(enterKey))
		if errors.Is(err, pty.ErrScreenChanged) {
			return failure(http.StatusConflict, textNotSent)
		}

		if err != nil {
			return hostedFailure(err)
		}

		return done()
	}

	return failure(http.StatusConflict, textNotSent)
}

// emptyBox is whether a screen shows an empty input box.
func emptyBox(lines []string) bool { return untypable(ask.ScreenText(lines)) == nil }

// draftShown is whether a screen shows text in the input box and no dialog.
func draftShown(lines []string) bool {
	screen := ask.ScreenText(lines)
	if ask.PromptState(screen) != ask.PromptDraft {
		return false
	}

	return ask.Parse(screen, 0) == nil
}

// promptStillShown is whether a screen still shows the prompt askID names, with the status
// stamp the answer was checked with.
func promptStillShown(stamp float64, askID string) func(lines []string) bool {
	return func(lines []string) bool {
		found := ask.Parse(ask.ScreenText(lines), stamp)
		if found == nil {
			return false
		}

		return found.ID == askID
	}
}

// hostedFailure is the answer to a read or a write the host refused. Nothing in it is the
// screen or the input.
func hostedFailure(err error) Reply {
	switch {
	case errors.Is(err, pty.ErrScreenChanged):
		return conflictAsk(textScreenChanged, nil)
	case errors.Is(err, pty.ErrOtherInput):
		return conflictAsk(textOtherInput, nil)
	case errors.Is(err, pty.ErrNotForeground):
		return conflictAsk(textNotForeground, nil)
	case errors.Is(err, pty.ErrEnded), errors.Is(err, pty.ErrNoSession):
		return conflictAsk(textEnded, nil)
	case errors.Is(err, pty.ErrInputBlocked):
		return failure(http.StatusBadGateway, textNotTaking)
	}

	return failure(http.StatusInternalServerError, "could not type into the session: "+
		err.Error())
}
