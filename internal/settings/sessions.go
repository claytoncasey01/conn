package settings

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Where a session Conn starts opens (Sessions.Where).
const (
	// WhereInside runs the session on a terminal Conn hosts, shown on its pages.
	WhereInside = "inside"
	// WhereTerminal asks a terminal app to run it: cmux, or the person's own terminal command.
	WhereTerminal = "terminal"
	// WhereCommand starts nothing and shows the command for the person to paste.
	WhereCommand = "command"
)

// The placeholders a terminal command may hold, each replaced with the session's value.
const (
	// PlaceholderName is the session's name.
	PlaceholderName = "{name}"
	// PlaceholderDir is the folder the session runs in.
	PlaceholderDir = "{dir}"
	// PlaceholderCommand is the session's command, as one shell line.
	PlaceholderCommand = "{command}"
)

const (
	// SectionSessions is where sessions open.
	SectionSessions = "sessions"

	// terminalArgumentsMax and terminalArgumentUnitsMax bound a terminal command: how many
	// arguments, and how long each, in UTF-16 code units as the page counts them.
	terminalArgumentsMax     = 32
	terminalArgumentUnitsMax = 1024

	whereRule = "set it to inside, terminal or command"
)

var (
	// controlShape is a control character, which no argument of a terminal command holds.
	controlShape = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	// placeholderShape is what an argument of a terminal command holds that reads as a
	// placeholder.
	placeholderShape = regexp.MustCompile(`\{[^{}]*\}`)
)

// Sessions is where the sessions Conn starts open, the terminal command for WhereTerminal (empty:
// cmux), and whether the sessions open at a quit are resumed at the next launch.
type Sessions struct {
	Where           string   `json:"where"`
	TerminalCommand []string `json:"terminalCommand"`
	ResumeOnLaunch  bool     `json:"resumeOnLaunch"`
}

// sessionsDefaults are a fresh install's: inside Conn, no terminal command, resume on launch.
func sessionsDefaults() Sessions {
	return Sessions{Where: WhereInside, TerminalCommand: []string{}, ResumeOnLaunch: true}
}

// wheres are the choices of Sessions.Where.
func wheres() []string { return []string{WhereInside, WhereTerminal, WhereCommand} }

// readSessions reads the section leniently: each field that cannot be read is its default, with
// a note in found.
func readSessions(raw any, present bool, found *problems) Sessions {
	object := section(raw, present, SectionSessions, found)
	value := sessionsDefaults()

	where, wherePresent := object.Get("where")
	if wherePresent {
		chosen, isChoice := whereOf(where)
		if !isChoice {
			found.addf("sessions.where %s is not a choice: using %s", show(where), WhereInside)
		}

		value.Where = chosen
	}

	command, commandPresent := object.Get("terminalCommand")
	if commandPresent {
		read, commandProblems := terminalCommand(command)
		if len(commandProblems) > 0 {
			found.addf("%s: using none", strings.Join(commandProblems, "; "))
		}

		value.TerminalCommand = read
	}

	value.ResumeOnLaunch = boolField(object, "resumeOnLaunch", true, SectionSessions, found)

	return value
}

// validateSessions reads the section as a PUT sends it, noting every problem in found. A field
// left out is its default here; the store keeps the saved one in its place.
func validateSessions(object *js.Object, found *problems) Sessions {
	value := sessionsDefaults()

	where, wherePresent := object.Get("where")
	if wherePresent {
		chosen, isChoice := whereOf(where)
		if !isChoice {
			found.addf("sessions.where %s is not a choice: %s", show(where), whereRule)
		}

		value.Where = chosen
	}

	command, commandPresent := object.Get("terminalCommand")
	if commandPresent {
		read, commandProblems := terminalCommand(command)
		found.list = append(found.list, commandProblems...)
		value.TerminalCommand = read
	}

	resume, resumePresent := object.Get("resumeOnLaunch")
	if resumePresent {
		typed, isBool := resume.(bool)
		if !isBool {
			found.addf("sessions.resumeOnLaunch %s is not true or false: set it to true or false",
				show(resume))
		}

		value.ResumeOnLaunch = typed
	}

	return value
}

// whereOf is raw when it is one of the choices, else WhereInside and false.
func whereOf(raw any) (string, bool) {
	text, isText := raw.(string)
	if !isText {
		return WhereInside, false
	}

	if !slices.Contains(wheres(), text) {
		return WhereInside, false
	}

	return text, true
}

// terminalCommand reads a terminal command: the arguments, or none and every problem with them.
// An empty list is no command of the person's own.
func terminalCommand(raw any) ([]string, []string) {
	list, isList := raw.([]any)
	if !isList {
		return []string{}, []string{fmt.Sprintf("sessions.terminalCommand %s is not a list: "+
			"write it as a list of arguments", show(raw))}
	}

	if len(list) == 0 {
		return []string{}, nil
	}

	if len(list) > terminalArgumentsMax {
		return []string{}, []string{fmt.Sprintf("sessions.terminalCommand holds %d arguments: "+
			"at most %d", len(list), terminalArgumentsMax)}
	}

	arguments := make([]string, 0, len(list))

	var found []string

	for _, entry := range list {
		argument, problem := terminalArgument(entry)
		if problem != "" {
			found = append(found, problem)
		}

		arguments = append(arguments, argument)
	}

	found = append(found, terminalShape(arguments)...)
	if len(found) > 0 {
		return []string{}, found
	}

	return arguments, nil
}

// terminalArgument is one argument of a terminal command, and what is wrong with it ("" for
// nothing). Text is answered even when it is refused, so the command's shape is judged on it.
func terminalArgument(entry any) (string, string) {
	argument, isText := entry.(string)
	if !isText {
		return "", fmt.Sprintf("sessions.terminalCommand %s is not text: write each argument "+
			"as text", show(entry))
	}

	if argument == "" {
		return "", "sessions.terminalCommand has an empty argument: remove it"
	}

	if js.Len(argument) > terminalArgumentUnitsMax {
		return argument, fmt.Sprintf("sessions.terminalCommand has an argument of %d characters: "+
			"at most %d", js.Len(argument), terminalArgumentUnitsMax)
	}

	if controlShape.MatchString(argument) {
		return argument, fmt.Sprintf("sessions.terminalCommand %s holds a control character: "+
			"remove it", show(argument))
	}

	for _, placeholder := range placeholderShape.FindAllString(argument, -1) {
		if !slices.Contains(placeholders(), placeholder) {
			return argument, fmt.Sprintf("sessions.terminalCommand %s has an unknown placeholder: "+
				"use {name}, {dir} or {command}", show(argument))
		}
	}

	return argument, ""
}

// terminalShape is what is wrong with a terminal command as a whole: its program must be an
// absolute path, run as it is, and one of its arguments must take the command.
func terminalShape(arguments []string) []string {
	var found []string

	program := arguments[0]
	if !absoluteProgram(program) {
		found = append(found, fmt.Sprintf("sessions.terminalCommand starts with %s: start it "+
			"with the terminal program's absolute path", show(program)))
	}

	takesCommand := false

	for _, argument := range arguments[1:] {
		if strings.Contains(argument, PlaceholderCommand) {
			takesCommand = true
		}
	}

	if !takesCommand {
		found = append(found, "sessions.terminalCommand has no {command}: add it as the "+
			"argument the terminal runs")
	}

	return found
}

// absoluteProgram is whether program is an absolute path with no placeholder in it.
func absoluteProgram(program string) bool {
	if placeholderShape.MatchString(program) {
		return false
	}

	return filepath.IsAbs(program)
}

// placeholders are the placeholders a terminal command may hold.
func placeholders() []string {
	return []string{PlaceholderName, PlaceholderDir, PlaceholderCommand}
}
