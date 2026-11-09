// Package advisor is the choice of the model a session's advisor runs on, for the sessions Conn
// starts: Claude Code's own setting, or one model family. A choice is one of a closed list, and
// the arguments it adds to claude's command line come from this table alone, so no text from a
// request or a settings file ever reaches a command line.
//
// Claude Code fixes a session's advisor when the session starts and keeps it on every resume,
// whatever a resume is passed, so only a session's first start carries the choice.
package advisor

import (
	"slices"
	"strings"
)

// The choices. ClaudeCode passes nothing, so the person's own Claude Code setting (advisorModel
// in ~/.claude/settings.json, or its default) applies; the others name a model family by its
// alias, which Claude Code resolves to the newest model of that family.
const (
	ClaudeCode = "claude-code"
	Fable      = "fable"
	Opus       = "opus"
	Sonnet     = "sonnet"
)

// Default is a fresh install's choice: Claude Code's own, so an upgrade changes no one's sessions.
const Default = ClaudeCode

// settingsFlag is claude's option for settings that apply to this one session, over the person's
// own: the only way to set the advisor of a session from its command line.
const settingsFlag = "--settings"

// Choices are the choices, in the order a page lists them.
func Choices() []string { return []string{ClaudeCode, Fable, Opus, Sonnet} }

// IsChoice is whether choice is one of Choices.
func IsChoice(choice string) bool { return slices.Contains(Choices(), choice) }

// List is the choices as a sentence says them: "claude-code, fable, opus or sonnet".
func List() string {
	choices := Choices()
	last := len(choices) - 1

	return strings.Join(choices[:last], ", ") + " or " + choices[last]
}

// Requested is the advisor a start request chose, from its advisor field (nil when it has none):
// "" when it chose none, so the settings' choice applies; the choice when it is one; and false
// for anything else, which the request is refused for.
func Requested(raw any) (string, bool) {
	if raw == nil {
		return "", true
	}

	choice, isText := raw.(string)
	if !isText {
		return "", false
	}

	if !IsChoice(choice) {
		return "", false
	}

	return choice, true
}

// Or is chosen, or saved (the settings' choice) when chosen is "".
func Or(chosen, saved string) string {
	if chosen == "" {
		return saved
	}

	return chosen
}

// Args are what claude's command line adds for choice: none for ClaudeCode, else the settings
// that set the advisor, as two arguments. A choice that is not one is a program bug: callers check
// with IsChoice first.
func Args(choice string) []string {
	switch choice {
	case ClaudeCode:
		return nil
	case Fable:
		return []string{settingsFlag, `{"advisorModel":"fable"}`}
	case Opus:
		return []string{settingsFlag, `{"advisorModel":"opus"}`}
	case Sonnet:
		return []string{settingsFlag, `{"advisorModel":"sonnet"}`}
	}

	panic("advisor: Args was given " + choice + ", which is not a choice")
}
