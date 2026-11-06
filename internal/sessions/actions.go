package sessions

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/snapshot"
)

const (
	replyUnitsMax = 4000
	// replyLines is how much of the screen is read to see the input box.
	replyLines = 40

	textNoSession     = "no such live session"
	textSending       = "another message to this session is still being sent"
	textNotForeground = "the session is not in the foreground of its terminal (suspended or " +
		"exiting), so nothing was sent"
	textEnded       = "the session has ended, so nothing was sent"
	textControl     = "control characters are not sent"
	textEndedMidway = "the session has ended while this was being sent; check its terminal " +
		"for what reached it"
)

var (
	// keyPattern is the only thing an answer ever presses for a choice: one digit, 1 to 9.
	keyPattern = regexp.MustCompile(`^[1-9]$`)
	// controlChars are what a reply never types.
	controlChars = regexp.MustCompile(`[\x00-\x09\x0b-\x1f\x7f]`)
)

// terminalOnly are what a session can be stopped on that only the terminal itself can answer.
func terminalOnly(waiting *string) bool {
	if waiting == nil {
		return false
	}

	return slices.Contains([]string{
		"permission prompt", "sandbox request", "dialog open", "worker request",
	}, *waiting)
}

// Reply is an action's answer: an HTTP status and a JSON body.
type Reply struct {
	Code int
	Body *js.Object
}

func failure(code int, message string) Reply {
	body := js.NewObject()
	body.Set("error", message)

	return Reply{Code: code, Body: body}
}

// conflictAsk is a refusal that carries the ask as it is now (the one the snapshot would show).
func conflictAsk(message string, found *ask.Ask) Reply {
	reply := failure(http.StatusConflict, message)
	if found == nil {
		reply.Body.Set("ask", nil)
	} else {
		reply.Body.Set("ask", found)
	}

	return reply
}

func done() Reply {
	body := js.NewObject()
	body.Set("ok", true)

	return Reply{Code: http.StatusOK, Body: body}
}

// replyText is the message a reply would type, or why it is refused: the checks a reply and a
// text answer share.
func replyText(text any) (string, *Reply) {
	raw, isText := text.(string)
	if !isText {
		refused := failure(http.StatusBadRequest, "text must be a string")

		return "", &refused
	}

	message := js.Trim(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n"))

	var refused Reply

	switch {
	case message == "":
		refused = failure(http.StatusBadRequest, "nothing to send")
	case js.Len(message) > replyUnitsMax:
		refused = failure(http.StatusBadRequest, "too long (4000 characters at most)")
	case controlChars.MatchString(message):
		refused = failure(http.StatusBadRequest, textControl)
	case strings.HasPrefix(message, "!"), strings.HasPrefix(message, "/"):
		refused = failure(http.StatusBadRequest, "a leading ! or / would run a shell command "+
			"or slash command; type those in the terminal")
	default:
		return message, nil
	}

	return "", &refused
}

// untypable is why a message cannot be typed into what a screen shows, or nil when the input box
// is visibly empty. cmux does not reliably refuse to type over a half-written prompt.
func untypable(screen string) *Reply {
	var refused Reply

	switch ask.PromptState(screen) {
	case ask.PromptNone:
		refused = failure(http.StatusConflict,
			"could not see the session's input box on screen, so nothing was sent")
	case ask.PromptDraft:
		refused = failure(http.StatusConflict, "the prompt already has text in it (a draft or "+
			"a suggestion); clear it in the terminal first")
	default:
		return nil
	}

	return &refused
}

func where(target Target) []string {
	return []string{"--workspace", target.Workspace, "--surface", target.Surface}
}

// stop sends SIGTERM to a session the registry knows about. One that is working or waiting on a
// prompt, as the page shows it, needs force: the page asks for it from the status it shows.
func (s *store) stop(pid string, force bool, now float64) Reply {
	session, shown := s.shown(pid)
	if !shown {
		return failure(http.StatusNotFound, textNoSession)
	}

	status := snapshot.ShownStatus(session.reg.status(), session.parts(), now)
	if status != regIdle {
		if !force {
			reply := failure(http.StatusConflict, "session is busy")
			reply.Body.Set("busy", true)

			return reply
		}
	}

	err := s.deps.Kill(session.reg.PID)
	if err != nil {
		return failure(http.StatusInternalServerError, killMessage(err))
	}

	s.lastProc = 0
	s.lastReg = 0

	reply := done()
	reply.Body.Set("name", session.reg.name())

	return reply
}

// killMessage is process.kill's message for a failed signal:
// "kill() failed: ESRCH: No such process".
func killMessage(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err.Error()
	}

	names := map[syscall.Errno]string{syscall.ESRCH: "ESRCH", syscall.EPERM: "EPERM"}

	name, known := names[errno]
	if !known {
		return err.Error()
	}

	text := errno.Error()

	return "kill() failed: " + name + ": " + strings.ToUpper(text[:1]) + text[1:]
}

// sendPlan is a send the loop allowed: to which process and surface, or for a hosted session,
// to which of the host's sessions.
type sendPlan struct {
	pid     float64
	key     string
	target  Target
	message string
	session *Session
	stamp   float64
	shared  bool
	hosted  pty.ID
	// inputs is the hosted session's input count at the screen read the send was decided from:
	// a hosted ask's id holds the count it was read at (see withInputs).
	inputs uint64
}

// replyStart makes a reply's checks on the loop and marks the session as sending.
func (s *store) replyStart(pid string, text any) (sendPlan, *Reply) {
	session, shown := s.shown(pid)
	if !shown {
		refused := failure(http.StatusNotFound, textNoSession)

		return sendPlan{}, &refused
	}

	message, refused := replyText(text)
	if refused != nil {
		return sendPlan{}, refused
	}

	target := s.cmuxOf(session)
	if target == nil {
		refused := failure(http.StatusConflict, "this session is not running in a cmux "+
			"workspace, so there is no terminal to type into")

		return sendPlan{}, &refused
	}

	if session.reg.Status == regWaiting {
		if terminalOnly(session.reg.WaitingFor) {
			refused := failure(http.StatusConflict, "it is waiting on a "+*session.reg.WaitingFor+
				": attach to the terminal to answer it")

			return sendPlan{}, &refused
		}
	}

	// The screen check and the send are separate cmux calls: a second message checked in
	// between would see a prompt that is still empty, so one send per session at a time.
	if s.sending[pid] {
		refused := failure(http.StatusConflict, textSending)

		return sendPlan{}, &refused
	}

	s.sending[pid] = true

	return sendPlan{
		pid: session.reg.PID, key: pid, target: *target, message: message, session: session,
		stamp: 0, shared: false, hosted: "", inputs: 0,
	}, nil
}

// typeText pastes a message into a surface's input box and presses Enter once.
func (s *store) typeText(ctx context.Context, target Target, message string) run.Result {
	pasted := s.deps.Cmux(ctx, append(append([]string{"send"}, where(target)...), "--paste", "--",
		message))
	if pasted.Err != nil {
		return pasted
	}

	return s.deps.Cmux(ctx, append(append([]string{"send-key"}, where(target)...), "enter"))
}

// ownedScreen reads a screen once the CLI is known to own its terminal. A refusal comes back as
// a reply; a failed program as its result.
func (s *store) ownedScreen(ctx context.Context, plan sendPlan, lines int) (
	string, *Reply, *run.Result,
) {
	stat, ended, result := s.statResult(ctx, plan.pid)
	if result != nil {
		return "", nil, result
	}

	if ended {
		refused := conflictAsk(textEnded, nil)

		return "", &refused, nil
	}

	if !ask.OwnsTerminal(stat) {
		refused := failure(http.StatusConflict, textNotForeground)

		return "", &refused, nil
	}

	read := s.deps.Cmux(ctx, readScreenArgs(plan.target, lines))
	if read.Err != nil {
		return "", nil, &read
	}

	return read.Stdout, nil, nil
}

// statResult is statOf with the failed run kept, for its message.
func (s *store) statResult(ctx context.Context, pid float64) (string, bool, *run.Result) {
	result := s.deps.Stat(ctx, pid)
	if result.Err == nil {
		return js.Trim(result.Stdout), false, nil
	}

	if result.Code == 1 {
		if js.Trim(result.Stdout) == "" {
			return "", true, nil
		}
	}

	return "", false, &result
}

// failedSend is the answer to a send whose program failed: a CLI that exited mid-way makes ps or
// cmux fail, which is the session ending, not cmux refusing.
func (s *store) failedSend(ctx context.Context, pid float64, result run.Result) Reply {
	_, ended, _ := s.statOf(ctx, pid)
	if ended {
		return conflictAsk(textEndedMidway, nil)
	}

	return failure(http.StatusBadGateway, "cmux would not send it: "+cmuxError(result))
}
