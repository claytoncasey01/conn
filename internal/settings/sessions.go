package settings

import (
	"slices"

	"github.com/claytoncasey01/conn/internal/js"
)

// Where a session Conn starts opens (Sessions.Where).
const (
	// WhereInside runs the session on a terminal Conn hosts, shown on its pages.
	WhereInside = "inside"
	// WhereTerminal asks cmux to run it in a new workspace.
	WhereTerminal = "terminal"
	// WhereCommand starts nothing and shows the command for the person to paste.
	WhereCommand = "command"
)

const (
	// SectionSessions is where sessions open.
	SectionSessions = "sessions"

	whereRule = "set it to inside, terminal or command"

	// retiredTerminalCommand is a key an earlier build read: a program of the person's own to
	// open sessions with. No setting may name a program, so it is never read, and a file that
	// holds it says so.
	retiredTerminalCommand = "terminalCommand"
)

// Sessions is where the sessions Conn starts open, and whether the sessions open at a quit are
// resumed at the next launch. No field names a program: a terminal app is cmux (CONN_CMUX).
type Sessions struct {
	Where          string `json:"where"`
	ResumeOnLaunch bool   `json:"resumeOnLaunch"`
}

// sessionsDefaults are a fresh install's: inside Conn, resume on launch.
func sessionsDefaults() Sessions {
	return Sessions{Where: WhereInside, ResumeOnLaunch: true}
}

// wheres are the choices of Sessions.Where.
func wheres() []string { return []string{WhereInside, WhereTerminal, WhereCommand} }

// readSessions reads the section leniently: each field that cannot be read is its default, with
// a note in found. A terminal command an earlier build kept is left unread, with a note.
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

	_, retired := object.Get(retiredTerminalCommand)
	if retired {
		found.addf("sessions.terminalCommand is no longer read: sessions set to open in your " +
			"terminal open in cmux, and saving this section drops it")
	}

	value.ResumeOnLaunch = boolField(object, "resumeOnLaunch", true, SectionSessions, found)

	return value
}

// validateSessions reads the section as a PUT sends it, noting every problem in found. A field
// left out is its default here; the store keeps the saved one in its place. A key that is no
// setting, the retired terminal command among them, is not read, so it is never saved.
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
