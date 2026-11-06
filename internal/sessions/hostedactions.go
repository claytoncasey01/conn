package sessions

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/snapshot"
)

// The actions on a hosted session. Each is reached by the id the host issued, before anything
// looks at cmux: the route's id is looked up in the host's record, never matched against text.
// What is typed goes through the host's one writer, after the screen check, and only while the
// screen still passes it when the write's turn comes.

const (
	// hostedStopTimeout bounds a stop of a hosted session. It is above the host's own bound (its
	// grace, then its wait after the kill), so it never cuts a stop short; it is there so a stop
	// whose page has gone still ends.
	hostedStopTimeout = 15 * time.Second
)

// c1Chars are the C1 controls, which a reply to a hosted session never types either: a program
// reading its input could take one for the start of a control sequence.
var c1Chars = regexp.MustCompile(`[\x{80}-\x{9f}]`)

// hostedID is the host's id a route named, and false for anything else (a pid).
func hostedID(id string) (pty.ID, bool) {
	parsed, err := pty.ParseID(id)
	if err != nil {
		return "", false
	}

	return parsed, true
}

// stopHosted stops a hosted session's program and everything it started, through the host. A
// busy one needs force, as a cmux one does.
func (l *Loop) stopHosted(ctx context.Context, sessionID pty.ID, force bool) Reply {
	reply := failure(http.StatusServiceUnavailable, ErrStopped.Error())
	name := ""
	proceed := false

	_ = l.do(ctx, func(state *store) {
		name, reply, proceed = state.hostedStopStart(sessionID, force, state.now())
	})

	if !proceed {
		return reply
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hostedStopTimeout)
	defer cancel()

	err := l.store.deps.Host.Stop(stopCtx, sessionID)
	if err != nil {
		return failure(http.StatusInternalServerError, "could not stop it: "+err.Error())
	}

	stopped := done()
	stopped.Body.Set("name", name)

	return stopped
}

// hostedStopStart makes a hosted stop's checks on the loop: the session's name, or why it is
// refused.
func (s *store) hostedStopStart(sessionID pty.ID, force bool, now float64) (string, Reply, bool) {
	row, found := s.hostedRow(sessionID)
	if !found {
		return "", failure(http.StatusNotFound, textNoSession), false
	}

	if row.session == nil {
		return row.info.Name, done(), true
	}

	status := snapshot.ShownStatus(row.session.reg.status(), row.session.parts(), now)
	if status != regIdle {
		if !force {
			reply := failure(http.StatusConflict, "session is busy")
			reply.Body.Set("busy", true)

			return "", reply, false
		}
	}

	return row.session.reg.name(), done(), true
}

// replyHosted types a message into a hosted session: only into an input box its screen shows
// empty, only while the screen is still that one, and never text the prompt would read as a
// shell escape or slash command.
func (l *Loop) replyHosted(ctx context.Context, sessionID pty.ID, text any) Reply {
	ctx = context.WithoutCancel(ctx)

	var (
		message string
		refused *Reply
	)

	err := l.do(ctx, func(state *store) {
		message, refused = state.hostedReplyStart(sessionID, text)
	})
	if err != nil {
		return failure(http.StatusServiceUnavailable, err.Error())
	}

	if refused != nil {
		return *refused
	}

	defer l.finishSend(ctx, string(sessionID))

	return l.typeReply(ctx, sessionID, message)
}

// hostedReplyStart makes a hosted reply's checks on the loop and marks the session as sending.
func (s *store) hostedReplyStart(sessionID pty.ID, text any) (string, *Reply) {
	row, found := s.hostedRow(sessionID)
	if !found {
		return refuseText(failure(http.StatusNotFound, textNoSession))
	}

	// An ended session is refused by the host, which knows it first.
	message, refused := hostedText(text)
	if refused != nil {
		return "", refused
	}

	if row.session != nil {
		if row.session.reg.Status == regWaiting {
			if terminalOnly(row.session.reg.WaitingFor) {
				return refuseText(failure(http.StatusConflict, "it is waiting on a "+
					*row.session.reg.WaitingFor+": attach to the terminal to answer it"))
			}
		}
	}

	return message, s.startSending(string(sessionID))
}

// hostedText is replyText, also refusing the C1 controls.
func hostedText(text any) (string, *Reply) {
	message, refused := replyText(text)
	if refused != nil {
		return "", refused
	}

	if c1Chars.MatchString(message) {
		return refuseText(failure(http.StatusBadRequest, textControl))
	}

	return message, nil
}

// startSending marks a session as sending, or is why it cannot be: one send per session at a
// time, so two cannot both pass the screen check before either types.
func (s *store) startSending(key string) *Reply {
	if s.sending[key] {
		refused := failure(http.StatusConflict, textSending)

		return &refused
	}

	s.sending[key] = true

	return nil
}

// answerHosted answers the prompt a hosted session's page showed, as Answer does: the screen is
// read again from the host's model right before the key is pressed, and must still parse, with
// the session's current status stamp, to that same prompt; the key is pressed only while the
// screen still shows that prompt when the write's turn comes.
func (l *Loop) answerHosted(ctx context.Context, sessionID pty.ID, body AnswerBody) Reply {
	ctx = context.WithoutCancel(ctx)

	var (
		plan    sendPlan
		refused *Reply
	)

	err := l.do(ctx, func(state *store) {
		plan, refused = state.hostedAnswerStart(sessionID, body)
	})
	if err != nil {
		return failure(http.StatusServiceUnavailable, err.Error())
	}

	if refused != nil {
		return *refused
	}

	sent := false

	defer func() {
		_ = l.do(ctx, func(state *store) {
			delete(state.sending, string(sessionID))

			// The prompt is answered: the page must not keep offering it until the next read.
			if sent {
				delete(state.asks, plan.key)
				state.version++
			}
		})
	}()

	reply := l.pressHosted(ctx, plan, body)
	sent = reply.Code == http.StatusOK

	return reply
}

// pressHosted reads the screen, decides on the loop, and types what was decided.
func (l *Loop) pressHosted(ctx context.Context, plan sendPlan, body AnswerBody) Reply {
	host := l.store.deps.Host

	lines, mark, err := host.ScreenLines(plan.hosted, askLines)
	if err != nil {
		return hostedFailure(err)
	}

	plan.inputs = mark.Inputs

	var decided answerSend

	_ = l.do(ctx, func(state *store) {
		decided = state.answerCheck(plan, ask.ScreenText(lines), body, state.now())
	})

	if decided.refused != nil {
		return *decided.refused
	}

	// The answer is recorded before it is typed, so a read while it is being typed already shows
	// the prompt under the id it is answered as, locked (see keepAnswered).
	_ = l.do(ctx, func(state *store) { state.answeredAt(plan, decided.askID) })

	reply, typed := l.typeAnswer(ctx, plan, decided, mark)
	if reply.Code == http.StatusOK {
		return reply
	}

	// A refusal that typed nothing leaves the prompt as it was; one that may have typed (the
	// text is in the box, or a write ran out of time) keeps it answered.
	if !typed {
		_ = l.do(ctx, func(state *store) { state.forgetAnswer(plan, decided.askID) })
	}

	return reply
}

// hostedAnswerStart makes a hosted answer's checks on the loop, before the screen is read, and
// marks the session as sending. Only a registered session the registry shows waiting is
// answered: its status stamp names the prompt.
func (s *store) hostedAnswerStart(sessionID pty.ID, body AnswerBody) (sendPlan, *Reply) {
	row, found := s.hostedRow(sessionID)
	if !found {
		return refuse(failure(http.StatusNotFound, textNoSession))
	}

	message, refused := answerMessage(body)
	if refused != nil {
		return sendPlan{}, refused
	}

	if body.HasText {
		if c1Chars.MatchString(message) {
			return refuse(failure(http.StatusBadRequest, textControl))
		}
	}

	if row.info.ExitCode != nil {
		return refuse(conflictAsk(textEnded, nil))
	}

	if row.session == nil {
		return refuse(conflictAsk(textNotWaiting, nil))
	}

	if row.session.reg.Status != regWaiting {
		return refuse(conflictAsk(textNotWaiting, nil))
	}

	if sending := s.startSending(string(sessionID)); sending != nil {
		return sendPlan{}, sending
	}

	return sendPlan{
		pid: row.session.reg.PID, key: row.key, target: Target{Workspace: "", Surface: ""},
		message: message, session: row.session, stamp: statusStamp(row.session.reg),
		shared: false, hosted: sessionID, inputs: 0,
	}, nil
}

// openHosted is OPEN TERMINAL for a hosted session: its terminal is Conn's own, on the page, so
// there is nothing to bring to the front.
func (l *Loop) openHosted(ctx context.Context, sessionID pty.ID) Reply {
	reply := failure(http.StatusServiceUnavailable, ErrStopped.Error())

	_ = l.do(ctx, func(state *store) {
		_, found := state.hostedRow(sessionID)
		if !found {
			reply = failure(http.StatusNotFound, textNoSession)

			return
		}

		reply = done()
	})

	return reply
}

// hostedAgentPage is one agent page of a hosted session that has registered and still runs.
func (s *store) hostedAgentPage(sessionID pty.ID, agentID string) ([]byte, bool) {
	row, found := s.hostedRow(sessionID)
	if !found {
		return nil, false
	}

	if row.session == nil {
		return nil, false
	}

	return s.agentPage(row.session, agentID)
}
