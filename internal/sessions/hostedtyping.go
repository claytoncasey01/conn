package sessions

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/pty"
)

// Typing into a hosted session. Every write is a pty.Host.WriteIfStill against the screen read
// it was decided from: it types nothing if other input reached the program since that read (or
// is still being written, or came too shortly before it for the screen to show it), if a
// program the session started has the terminal or reads a line on it, or if the screen no
// longer shows what was checked. A message is typed in two writes: the text into a box the
// screen shows empty, then Enter as soon as the screen shows that text in the box and no
// dialog, and only if nothing else was typed after the text, so an Enter never sends anything
// but the message, and never lands on a dialog that came up meanwhile.

const (
	// pasteStart and pasteEnd bracket a message as a paste, as a terminal does for a paste, so a
	// line break in it is part of the message and not Enter. They are sent only to a program
	// that asked for them (mode 2004); Claude Code does.
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
	enterKey   = "\r"

	// enterWaitMax bounds the wait for the typed message to show in the box before Enter. The
	// wait ends as soon as the program draws it.
	enterWaitMax = 2 * time.Second

	// pastedPrefix starts what Claude Code shows in its box for a long paste instead of the text.
	pastedPrefix = "[Pasted text"

	textScreenChanged = "the screen changed while this was being sent, so nothing was sent"
	textOtherInput    = "something else was typed into the session's terminal just now, so " +
		"nothing was sent"
	textPasteOpen = "a paste into the session's terminal was cut short and is still open; " +
		"open its terminal and press a key to close it, then check the screen"
	textNotTaking = "the session did not take the input in time; check its terminal for " +
		"what reached it"
	textNotSent = "the message is in the session's input box but was not sent, because its " +
		"terminal changed meanwhile; send it from its terminal"
	textLineMode = "a program the session started is reading a line on its terminal (a " +
		"password, say), so nothing was sent"
	textNoPaste = "the session's terminal does not take pastes, so a message of several lines " +
		"was not sent"
)

// typeReply types a reply into a hosted session: into an input box the screen shows empty,
// then Enter.
func (l *Loop) typeReply(ctx context.Context, sessionID pty.ID, message string) Reply {
	host := l.store.deps.Host

	lines, mark, err := host.ScreenLines(sessionID, replyLines)
	if err != nil {
		return hostedFailure(err)
	}

	if refusal := untypable(ask.ScreenText(lines)); refusal != nil {
		return *refusal
	}

	reply, _ := l.typeMessage(ctx, typedInto{
		sessionID: sessionID, mark: mark, count: replyLines, still: emptyBox,
	}, message)

	return reply
}

// typeAnswer types what an answer decided: one key into the prompt it was decided from, or a
// text into that prompt's box, then Enter.
func (l *Loop) typeAnswer(
	ctx context.Context, plan sendPlan, decided answerSend, mark pty.ScreenMark,
) (Reply, bool) {
	still := promptStillShown(plan.stamp, plan.inputs, decided.askID)
	if decided.text {
		return l.typeMessage(ctx, typedInto{
			sessionID: plan.hosted, mark: mark, count: askLines,
			still: still,
		}, plan.message)
	}

	_, err := l.store.deps.Host.WriteIfStill(plan.hosted, mark, askLines, still,
		[]byte(decided.choice))
	if err != nil {
		return hostedFailure(err), mayHaveTyped(err)
	}

	return done(), true
}

// mayHaveTyped is whether a refused write may have typed some of its input: one that ran out of
// time in the program's Write. Every other refusal types nothing.
func mayHaveTyped(err error) bool { return errors.Is(err, pty.ErrInputBlocked) }

// typedInto is where a message is typed: the session, the screen read it was decided from, and
// the check of count screen lines that read must still pass.
type typedInto struct {
	sessionID pty.ID
	mark      pty.ScreenMark
	count     int
	still     func(lines []string) bool
}

// typeMessage types message while the screen read with into.mark still passes into.still, then
// presses Enter once the screen shows it in the box. It also says whether anything may have
// been typed.
func (l *Loop) typeMessage(ctx context.Context, into typedInto, message string) (Reply, bool) {
	keys, refused := messageKeys(message, into.mark.BracketedPaste)
	if refused != nil {
		return *refused, false
	}

	typed, err := l.store.deps.Host.WriteIfStill(into.sessionID, into.mark, into.count,
		into.still, keys)
	if err != nil {
		return hostedFailure(err), mayHaveTyped(err)
	}

	return l.pressEnter(ctx, into.sessionID, typed, message), true
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

// pressEnter presses Enter as soon as the screen shows message in the input box and no dialog,
// within enterWaitMax, and only if nothing was typed after it (typed is the mark of its own
// write). The message is in the box by then: every refusal but the session ending says so.
func (l *Loop) pressEnter(
	ctx context.Context, sessionID pty.ID, typed pty.ScreenMark, message string,
) Reply {
	host := l.store.deps.Host
	shown := draftOf(message)

	ctx, cancel := context.WithTimeout(ctx, enterWaitMax)
	defer cancel()

	_, err := host.WaitScreen(ctx, sessionID, replyLines, shown)
	if err == nil {
		_, err = host.WriteIfStill(sessionID, typed, replyLines, shown, []byte(enterKey))
	}

	switch {
	case err == nil:
		return done()
	case errors.Is(err, pty.ErrEnded):
		return conflictAsk(textEnded, nil)
	case errors.Is(err, pty.ErrNoSession):
		return conflictAsk(textEnded, nil)
	}

	return failure(http.StatusConflict, textNotSent)
}

// emptyBox is whether a screen shows an empty input box.
func emptyBox(lines []string) bool { return untypable(ask.ScreenText(lines)) == nil }

// draftOf is whether a screen shows message in the input box, and no dialog: the box's first
// line is the start of the message's first line (the rest may wrap), or what Claude Code shows
// for a long paste.
func draftOf(message string) func(lines []string) bool {
	first, _, _ := strings.Cut(message, "\n")

	return func(lines []string) bool {
		screen := ask.ScreenText(lines)

		text, found := ask.PromptText(screen)
		if !found {
			return false
		}

		if !boxHolds(text, first) {
			return false
		}

		return ask.Parse(screen, 0) == nil
	}
}

// boxHolds is whether text, an input box's first line, shows a message whose first line is
// first.
func boxHolds(text, first string) bool {
	if text == "" {
		return false
	}

	if strings.HasPrefix(text, pastedPrefix) {
		return true
	}

	return strings.HasPrefix(first, text)
}

// promptStillShown is whether a screen still shows the prompt askID names, with the status
// stamp and input count the answer was checked with.
func promptStillShown(stamp float64, inputs uint64, askID string) func(lines []string) bool {
	return func(lines []string) bool {
		found := withInputs(ask.Parse(ask.ScreenText(lines), stamp), inputs)
		if found == nil {
			return false
		}

		return found.ID == askID
	}
}

// withInputs is a hosted session's ask with the session's input count at the read in its id: a
// prompt read again after input reached the session (a viewer answered it, and an identical one
// came up) is another ask, and a click on the one shown before is refused. While the prompt the
// page just answered stands, keepAnswered gives it back under the id it was answered as.
func withInputs(found *ask.Ask, inputs uint64) *ask.Ask {
	if found == nil {
		return nil
	}

	counted := *found
	counted.ID = found.ID + countSeparator + strconv.FormatUint(inputs, 10)

	return &counted
}

// countSeparator ends an ask's own id and starts the input count withInputs adds.
const countSeparator = "."

// baseID is an ask's id without the input count withInputs adds.
func baseID(askID string) string {
	base, _, _ := strings.Cut(askID, countSeparator)

	return base
}

// keepAnswered is found under the id it was answered as, while it is the prompt just answered
// in this session on the same status stamp: the answer's own key, or any input after it, moves
// the count but not the prompt, so the page stays on the answered id, which it holds locked, and
// a second click on it is refused as just answered. Any other ask is found as it is.
//
// The rule has no time limit: it holds until the registry's status stamp moves or the session
// changes. So an identical prompt drawn again on the same stamp (two identical dialogs queued
// back to back, with no stamp between them) stays locked under the first's id and is answered in
// the terminal. The host cannot tell it from the first still drawn, and unlocking it after a
// while would let a second press through whenever Claude Code is slow to redraw.
func (s *store) keepAnswered(pid string, session *Session, stamp float64,
	found *ask.Ask,
) *ask.Ask {
	if found == nil {
		return nil
	}

	if !s.justAnswered(pid, session, stamp, found.ID) {
		return found
	}

	kept := *found
	kept.ID = s.tombstone(pid, session, stamp)

	return &kept
}

// hostedFailure is the answer to a read or a write the host refused. Nothing in it is the
// screen or the input.
func hostedFailure(err error) Reply {
	switch {
	case errors.Is(err, pty.ErrScreenChanged):
		return conflictAsk(textScreenChanged, nil)
	case errors.Is(err, pty.ErrPasteOpen):
		return conflictAsk(textPasteOpen, nil)
	case errors.Is(err, pty.ErrOtherInput):
		return conflictAsk(textOtherInput, nil)
	case errors.Is(err, pty.ErrNotForeground):
		return conflictAsk(textNotForeground, nil)
	case errors.Is(err, pty.ErrLineMode):
		return conflictAsk(textLineMode, nil)
	case errors.Is(err, pty.ErrEnded):
		return conflictAsk(textEnded, nil)
	case errors.Is(err, pty.ErrNoSession):
		return conflictAsk(textEnded, nil)
	case errors.Is(err, pty.ErrInputBlocked):
		return failure(http.StatusBadGateway, textNotTaking)
	}

	return failure(http.StatusInternalServerError, "could not type into the session: "+
		err.Error())
}
