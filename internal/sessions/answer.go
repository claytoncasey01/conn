package sessions

import (
	"context"
	"net/http"
	"slices"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/run"
)

const (
	// pickerExtraRows are the picker's Type something and Chat about this rows.
	pickerExtraRows = 2
	// keysMax is the highest key one digit can press.
	keysMax = 9
)

// AnswerBody is what the page sends to answer a prompt: the id of the prompt it showed, and
// either the key of a choice or a text. A nil field is one the body leaves out.
type AnswerBody struct {
	AskID  any
	Choice any
	Text   any
	// HasChoice and HasText say whether the body has the field at all (a null is there).
	HasChoice bool
	HasText   bool
}

// answerStart makes an answer's checks on the loop, before anything is run, and marks the
// session as sending. Nothing to answer is refused before any ps, lookup or screen read.
func (s *store) answerStart(pid string, body AnswerBody) (sendPlan, *Reply) {
	session, shown := s.shown(pid)
	if !shown {
		return refuse(failure(http.StatusNotFound, textNoSession))
	}

	message, refused := answerMessage(body)
	if refused != nil {
		return sendPlan{}, refused
	}

	return s.answerTarget(pid, session, message)
}

// answerMessage is the text an answer would type ("" for a choice), or why the body is refused
// before anything is looked up.
func answerMessage(body AnswerBody) (string, *Reply) {
	askID, named := body.AskID.(string)
	if askID == "" {
		named = false
	}

	if !named {
		return refuseText(failure(http.StatusBadRequest,
			"askId must name the prompt that was shown"))
	}

	if body.HasChoice == body.HasText {
		return refuseText(failure(http.StatusBadRequest, "send either a choice or a text"))
	}

	message := ""

	if body.HasChoice {
		choice, isText := body.Choice.(string)
		if !isText {
			return refuseText(failure(http.StatusBadRequest, textNotAChoice))
		}

		if !keyPattern.MatchString(choice) {
			return refuseText(failure(http.StatusBadRequest, textNotAChoice))
		}
	} else {
		checked, refused := replyText(body.Text)
		if refused != nil {
			return "", refused
		}

		message = checked
	}

	return message, nil
}

const (
	textNotAChoice = "choice must be the key of one of the prompt's options"
	textNotWaiting = "the session is not waiting on a prompt, so nothing was sent"
)

func refuse(reply Reply) (sendPlan, *Reply) { return sendPlan{}, &reply }

func refuseText(reply Reply) (string, *Reply) { return "", &reply }

// answerTarget is the rest of answerStart: the session must be waiting, in a cmux surface, and
// not already being sent to.
func (s *store) answerTarget(pid string, session *Session, message string) (sendPlan, *Reply) {
	if session.reg.Status != regWaiting {
		return refuse(conflictAsk(textNotWaiting, nil))
	}

	target := s.cmuxOf(session)
	if target == nil {
		return refuse(failure(http.StatusConflict, "this session is not running in a cmux "+
			"workspace, so there is no terminal to answer in"))
	}

	// One send per session at a time, shared with reply: two clicks must not both pass the
	// check before either sends.
	if s.sending[pid] {
		return refuse(failure(http.StatusConflict, textSending))
	}

	s.sending[pid] = true

	// Every live session's cmux surface is looked up now, not after the read.
	return sendPlan{
		pid: session.reg.PID, key: pid, target: *target, message: message, session: session,
		stamp: statusStamp(session.reg), shared: s.sharesWorkspace(pid, target.Workspace, true),
		hosted: "", inputs: 0,
	}, nil
}

// answerSend is what the loop decided from a screen read just before the send: the keys to
// send, or why nothing is sent.
type answerSend struct {
	refused *Reply
	choice  string
	text    bool
	askID   string
}

// answerCheck reads the screen just read again, on the loop, with the session's current status
// stamp and the shared-workspace downgrade, and decides. From here to the send nothing is looked
// up: the key answers the screen just read.
func (s *store) answerCheck(plan sendPlan, screen string, body AnswerBody, now float64,
) answerSend {
	session := plan.session
	stamp := statusStamp(session.reg)
	shownAsk := s.readAgain(plan, screen, stamp, now)
	turned := func(message string) answerSend {
		refused := conflictAsk(message, shownAsk)

		return answerSend{refused: &refused, choice: "", text: false, askID: ""}
	}

	askID, _ := body.AskID.(string)
	if message := s.samePrompt(plan, shownAsk, stamp, askID); message != "" {
		return turned(message)
	}

	if body.HasChoice {
		return s.choiceSend(shownAsk, body, turned)
	}

	if shownAsk.Kind != ask.KindText {
		return turned("the prompt on screen does not take text, so nothing was sent")
	}

	if terminalOnly(session.reg.WaitingFor) {
		return turned("it is waiting on a " + *session.reg.WaitingFor +
			": attach to the terminal to answer it")
	}

	return answerSend{refused: nil, choice: "", text: true, askID: shownAsk.ID}
}

// readAgain is what the screen just read asks, as the page would be shown it. What was just read
// is what the page is shown next: after a refusal it must not get the older read back.
func (s *store) readAgain(plan sendPlan, screen string, stamp, now float64) *ask.Ask {
	session := plan.session
	read := s.screenAsk(plan.key, session, screen, stamp)

	if plan.hosted != "" {
		read = s.keepAnswered(plan.key, session, stamp, withInputs(read, plan.inputs))
	}

	if s.sessions[plan.key] == session {
		if session.reg.Status == regWaiting {
			if stamp == plan.stamp {
				s.record(plan.key, session, stamp, read, now)
			}
		}
	}

	// A hosted session has a terminal of its own: it shares no cmux workspace.
	shared := plan.shared
	if plan.hosted == "" {
		if !shared {
			shared = s.sharesWorkspace(plan.key, plan.target.Workspace, false)
		}
	}

	return s.keptSince(plan.key, session, stamp, s.askFor(plan.key, session, read, &shared))
}

// samePrompt is why the prompt on screen is not the one the page answered, or "". The stamp is
// also in the id; this also refuses a read made while the stamp moved, whatever id came with the
// click.
func (s *store) samePrompt(plan sendPlan, shownAsk *ask.Ask, stamp float64, askID string) string {
	session := plan.session

	switch {
	case s.sessions[plan.key] != session, session.reg.Status != regWaiting, stamp != plan.stamp:
		return textMovedOn
	case shownAsk == nil, shownAsk.ID != askID:
		return textNotShown
	case s.justAnswered(plan.key, session, stamp, shownAsk.ID):
		return "this prompt was just answered and the terminal has not moved on yet, " +
			"so nothing was sent"
	}

	return ""
}

const (
	textMovedOn  = "the session moved on since the prompt was shown, so nothing was sent"
	textNotShown = "the prompt on screen is not the one that was shown, so nothing was sent"
)

// keptSince gives the ask returned with a refusal the since the page showed, while it is the same
// prompt.
func (s *store) keptSince(pid string, session *Session, stamp float64, found *ask.Ask) *ask.Ask {
	if found == nil {
		return nil
	}

	last, had := s.asks[pid]
	if !had {
		return found
	}

	if last.since == nil {
		return found
	}

	if last.sessionID != session.reg.SessionID {
		return found
	}

	if last.stamp != stamp {
		return found
	}

	kept := *found
	kept.Since = *last.since

	return &kept
}

// choiceSend picks the key to press: one printed on the screen as read again, one digit, with
// no Enter; a permission prompt has only its Yes and No to press.
func (s *store) choiceSend(found *ask.Ask, body AnswerBody,
	turned func(string) answerSend,
) answerSend {
	if found.Kind != ask.KindQuestion {
		if found.Kind != ask.KindPermission {
			return turned("the prompt on screen is not a list of choices, so nothing was sent")
		}
	}

	roles := []string{ask.RolePick}
	if found.Kind == ask.KindPermission {
		roles = []string{ask.RoleAllow, ask.RoleDeny}
	}

	choice, _ := body.Choice.(string)
	index := slices.IndexFunc(found.Options, func(option ask.Option) bool {
		if option.Key != choice {
			return false
		}

		return slices.Contains(roles, option.Role)
	})

	if index < 0 {
		refused := failure(http.StatusBadRequest, "that is not one of the choices on the prompt")

		return answerSend{refused: &refused, choice: "", text: false, askID: ""}
	}

	key := found.Options[index].Key
	if !keyPattern.MatchString(key) {
		refused := failure(http.StatusBadRequest, "that is not one of the choices on the prompt")

		return answerSend{refused: &refused, choice: "", text: false, askID: ""}
	}

	// With ten rows or more a 1 could be the start of 10: such a list is answered in the
	// terminal.
	tooLong := slices.ContainsFunc(found.Options, func(option ask.Option) bool {
		return !keyPattern.MatchString(option.Key)
	})
	if found.Kind == ask.KindQuestion {
		if len(found.Options)+pickerExtraRows > keysMax {
			tooLong = true
		}
	}

	if tooLong {
		return turned("this list is too long to answer with one key; answer it in the terminal")
	}

	return answerSend{refused: nil, choice: key, text: false, askID: found.ID}
}

// answered keeps the prompt just answered, so a second click before the terminal redraws does
// not press again, and drops its ask until the next read.
func (s *store) answeredAt(plan sendPlan, askID string) {
	s.answered[plan.key] = answeredPrompt{
		sessionID: plan.session.reg.SessionID, stamp: statusStamp(plan.session.reg), id: askID,
	}
}

// sendKeys presses what answerCheck decided; a failed program comes back.
func (s *store) sendKeys(ctx context.Context, plan sendPlan, decided answerSend) *run.Result {
	var result run.Result

	if decided.text {
		result = s.typeText(ctx, plan.target, plan.message)
	} else {
		result = s.deps.Cmux(ctx, append(append([]string{"send"}, where(plan.target)...), "--",
			decided.choice))
	}

	if result.Err != nil {
		return &result
	}

	return nil
}
