package claude

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Ways a run fails, for errors.Is. The message shown is the error's own text.
var (
	// ErrNoResult is a run that ended without a result line.
	ErrNoResult = errors.New("without a result")
	// ErrReported is a run whose result says it failed; the text is the run's own.
	ErrReported = errors.New("claude reported an error")
	// ErrDenied is a run that had a tool denied, when the caller is not Lenient.
	ErrDenied = errors.New("tools were denied")
	// ErrNoStructured is a run asked for a schema that answered without one.
	ErrNoStructured = errors.New("no structured answer came back")
	// ErrTimedOut is a run stopped by its time limit.
	ErrTimedOut = errors.New("timed out")
	// ErrCancelled is a run stopped by its caller.
	ErrCancelled = errors.New("cancelled")
)

const (
	// stderrShownMax is the most of a run's error output a failure shows, in UTF-16 units.
	stderrShownMax = 300
	// reportedShownMax is the most of a failed result's text an error shows, in UTF-16 units.
	reportedShownMax = 400

	claudeAIPrefix = "claude.ai "
)

// Answer is what a run answered: Structured (a parsed JSON value) when Opts.Schema was set, else
// Text.
type Answer struct {
	Text       string
	Structured any
}

// reportedError is a failed result: its text, which is ErrReported.
type reportedError struct{ text string }

func (e reportedError) Error() string { return e.text }

func (e reportedError) Unwrap() error { return ErrReported }

// stream reads a run's output, one JSON line at a time, and keeps its result line.
type stream struct {
	opts  Opts
	final *js.Object
}

// read takes one line. A line that is not a JSON object is skipped.
func (s *stream) read(line []byte) {
	parsed, err := js.Parse(line)
	if err != nil {
		return
	}

	event, isObject := parsed.(*js.Object)
	if !isObject {
		return
	}

	switch text(event, "type") {
	case "system":
		if text(event, "subtype") == "init" {
			s.started(event)
		}
	case "assistant":
		s.stepped(event)
	case "result":
		s.final = event
	}
}

// started reports the connectors the run cares about that are not signed in.
func (s *stream) started(event *js.Object) {
	needing := []string{}

	for _, item := range list(event, "mcp_servers") {
		server, isObject := item.(*js.Object)
		if !isObject {
			continue
		}

		name, watched := watchedConnector(server)
		if !watched {
			continue
		}

		if slices.Contains(needing, name) {
			continue
		}

		needing = append(needing, name)
	}

	if len(needing) == 0 {
		return
	}

	if s.opts.OnAuth != nil {
		s.opts.OnAuth(needing)
	}
}

// watchedConnector is the claude.ai name ("Atlassian") of a server that needs signing in, when it
// is a connector a helper run cares about: the rest of a long connector list being signed out is
// not news.
func watchedConnector(server *js.Object) (string, bool) {
	if text(server, "status") != "needs-auth" {
		return "", false
	}

	name := text(server, "name")
	for _, watched := range []string{"Atlassian", "Slack"} {
		if strings.HasPrefix(name, claudeAIPrefix+watched) {
			return strings.TrimSuffix(strings.TrimPrefix(name, claudeAIPrefix), " MCP"), true
		}
	}

	return "", false
}

// stepped reports each tool the model called, except the tool search it uses to load others.
func (s *stream) stepped(event *js.Object) {
	if s.opts.OnProgress == nil {
		return
	}

	message, isObject := value(event, "message").(*js.Object)
	if !isObject {
		return
	}

	for _, item := range list(message, "content") {
		block, isBlock := item.(*js.Object)
		if !isBlock {
			continue
		}

		if text(block, "type") != "tool_use" {
			continue
		}

		name := text(block, "name")
		if name == "" {
			continue
		}

		if name == "ToolSearch" {
			continue
		}

		s.opts.OnProgress(StepLabel(name, value(block, "input")))
	}
}

// outcome is what a run that ended by itself answered. exit is its exit code as Node prints it
// ("1", or "null" when a signal ended it) and stderr its error output.
func outcome(final *js.Object, opts Opts, exit, stderr string) (Answer, error) {
	if final == nil {
		detail := ""
		if stderr != "" {
			detail = ": " + js.Slice(js.Trim(stderr), stderrShownMax)
		}

		return Answer{}, fmt.Errorf("claude exited %s %w%s", exit, ErrNoResult, detail)
	}

	if js.Truthy(value(final, "is_error")) {
		reported := ErrReported.Error()
		if result := value(final, "result"); result != nil {
			reported = js.String(result)
		}

		return Answer{}, reportedError{text: js.Slice(reported, reportedShownMax)}
	}

	err := denials(final, opts)
	if err != nil {
		return Answer{}, err
	}

	return answered(final, opts)
}

// denials fails a run that had a tool denied, or tells a Lenient caller which were.
func denials(final *js.Object, opts Opts) error {
	denied := deniedTools(final)
	if len(denied) == 0 {
		return nil
	}

	if !opts.Lenient {
		return fmt.Errorf("%w: %s", ErrDenied, strings.Join(denied, ", "))
	}

	if opts.OnDenied != nil {
		opts.OnDenied(denied)
	}

	return nil
}

// answered is the answer in a good result line: the structured answer when a schema was asked
// for, else the text.
func answered(final *js.Object, opts Opts) (Answer, error) {
	if opts.Schema != "" {
		structured := value(final, "structured_output")
		if structured == nil {
			return Answer{}, ErrNoStructured
		}

		return Answer{Text: "", Structured: structured}, nil
	}

	answer := ""
	if result := value(final, "result"); result != nil {
		answer = js.String(result)
	}

	return Answer{Text: answer, Structured: nil}, nil
}

// deniedTools are the names in the result's permission denials, each once, in order.
func deniedTools(final *js.Object) []string {
	denied := []string{}

	for _, item := range list(final, "permission_denials") {
		name := ""
		if denial, isObject := item.(*js.Object); isObject {
			name = joinedText(value(denial, "tool_name"))
		}

		if slices.Contains(denied, name) {
			continue
		}

		denied = append(denied, name)
	}

	return denied
}

// joinedText is a value as Array.prototype.join writes it: null and absent are empty.
func joinedText(item any) string {
	if item == nil {
		return ""
	}

	return js.String(item)
}

// value is the object's value at key; nil when the key is absent or holds null.
func value(object *js.Object, key string) any {
	found, _ := object.Get(key)

	return found
}

// text is the object's string at key, or "" when it holds anything else.
func text(object *js.Object, key string) string {
	found, _ := value(object, key).(string)

	return found
}

// list is the object's array at key, or nothing when it holds anything else.
func list(object *js.Object, key string) []any {
	found, _ := value(object, key).([]any)

	return found
}
