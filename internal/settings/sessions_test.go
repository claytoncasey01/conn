package settings_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// TestTheSessionsDefaultsOpenInsideAndResume: a fresh install starts sessions inside Conn, has no
// terminal command of its own and resumes on launch.
func TestTheSessionsDefaultsOpenInsideAndResume(t *testing.T) {
	t.Parallel()

	got := js.Stringify(settings.Defaults().Sessions)
	if got != `{"where":"inside","terminalCommand":[],"resumeOnLaunch":true}` {
		t.Fatalf("got %s", got)
	}
}

// TestASessionsSectionIsCheckedWhole: a PUT of the sessions section names every problem at once,
// each saying what to set instead, and a good one comes back as it was sent.
func TestASessionsSectionIsCheckedWhole(t *testing.T) {
	t.Parallel()

	const kitty = `"/Applications/kitty.app/Contents/MacOS/kitty"`

	tests := []struct {
		name string
		body string
		// want is the value a good section comes back as; wantError the refusal of a bad one.
		want      string
		wantError string
	}{
		{
			"each choice", `{"where":"terminal"}`,
			`{"where":"terminal","terminalCommand":[],"resumeOnLaunch":true}`, "",
		},
		{
			"a terminal command",
			`{"where":"terminal","terminalCommand":[` + kitty + `,"--directory","{dir}",` +
				`"--title={name}","sh","-c","{command}"],"resumeOnLaunch":false}`,
			`{"where":"terminal","terminalCommand":[` + kitty + `,"--directory","{dir}",` +
				`"--title={name}","sh","-c","{command}"],"resumeOnLaunch":false}`, "",
		},
		{
			"an unknown choice", `{"where":"tmux"}`, "",
			`sessions.where "tmux" is not a choice: set it to inside, terminal or command`,
		},
		{
			"a choice that is not text", `{"where":1}`, "",
			`sessions.where 1 is not a choice: set it to inside, terminal or command`,
		},
		{
			"a switch that is not a switch", `{"resumeOnLaunch":"yes"}`, "",
			`sessions.resumeOnLaunch "yes" is not true or false: set it to true or false`,
		},
		{
			"no command placeholder", `{"terminalCommand":[` + kitty + `,"--directory","{dir}"]}`,
			"", "sessions.terminalCommand has no {command}: add it as the argument the " +
				"terminal runs",
		},
		{
			"a program that is not a path", `{"terminalCommand":["kitty","{command}"]}`, "",
			`sessions.terminalCommand starts with "kitty": start it with the terminal ` +
				"program's absolute path",
		},
		{
			"a program that is a placeholder", `{"terminalCommand":["{command}"]}`, "",
			`sessions.terminalCommand starts with "{command}": start it with the terminal ` +
				"program's absolute path; sessions.terminalCommand has no {command}: add it as " +
				"the argument the terminal runs",
		},
		{
			"an unknown placeholder", `{"terminalCommand":[` + kitty + `,"{cmd}","{command}"]}`,
			"", `sessions.terminalCommand "{cmd}" has an unknown placeholder: use {name}, ` +
				"{dir} or {command}",
		},
		{
			"a control character", `{"terminalCommand":[` + kitty + `,"a\nb","{command}"]}`, "",
			`sessions.terminalCommand "a\nb" holds a control character: remove it`,
		},
		{
			"an empty argument", `{"terminalCommand":[` + kitty + `,"","{command}"]}`, "",
			`sessions.terminalCommand has an empty argument: remove it`,
		},
		{
			"an argument that is not text", `{"terminalCommand":[` + kitty + `,7,"{command}"]}`,
			"", `sessions.terminalCommand 7 is not text: write each argument as text`,
		},
		{
			"not a list", `{"terminalCommand":"kitty {command}"}`, "",
			`sessions.terminalCommand "kitty {command}" is not a list: write it as a list ` +
				"of arguments",
		},
		{
			"every problem at once",
			`{"where":"x","terminalCommand":["k"],"resumeOnLaunch":0}`, "",
			`sessions.where "x" is not a choice: set it to inside, terminal or command; ` +
				`sessions.terminalCommand starts with "k": start it with the terminal ` +
				"program's absolute path; sessions.terminalCommand has no {command}: add it as " +
				`the argument the terminal runs; sessions.resumeOnLaunch 0 is not true or false: ` +
				"set it to true or false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checked := settings.ValidateSection("sessions", parse(t, tt.body))
			if checked.Error != tt.wantError {
				t.Fatalf("error %q, want %q", checked.Error, tt.wantError)
			}

			if tt.wantError != "" {
				return
			}

			if got := js.Stringify(checked.Value); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// TestATerminalCommandIsBounded: a list too long, or an argument too long, is refused.
func TestATerminalCommandIsBounded(t *testing.T) {
	t.Parallel()

	many := `["/bin/t"` + strings.Repeat(`,"a"`, 40) + `,"{command}"]`
	long := `["/bin/t","` + strings.Repeat("a", 2000) + `","{command}"]`

	tests := []struct {
		name, body, want string
	}{
		{
			"too many", `{"terminalCommand":` + many + `}`,
			"sessions.terminalCommand holds 42 arguments: at most 32",
		},
		{
			"too long", `{"terminalCommand":` + long + `}`,
			"sessions.terminalCommand has an argument of 2000 characters: at most 1024",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checked := settings.ValidateSection("sessions", parse(t, tt.body))
			if checked.Error != tt.want {
				t.Fatalf("error %q, want %q", checked.Error, tt.want)
			}
		})
	}
}

// TestAHandEditedSessionsSectionDropsWhatIsBad: the file is read leniently, each bad field
// falling back to its default with a note.
func TestAHandEditedSessionsSectionDropsWhatIsBad(t *testing.T) {
	t.Parallel()

	loaded := settings.ParseDetailed(parse(t,
		`{"sessions":{"where":"tmux","terminalCommand":["kitty"],"resumeOnLaunch":"no"}}`), true)

	got := js.Stringify(loaded.Settings.Sessions)
	if got != `{"where":"inside","terminalCommand":[],"resumeOnLaunch":true}` {
		t.Fatalf("got %s", got)
	}

	want := []string{
		`sessions.where "tmux" is not a choice: using inside`,
		`sessions.terminalCommand starts with "kitty": start it with the terminal program's ` +
			"absolute path; sessions.terminalCommand has no {command}: add it as the argument " +
			"the terminal runs: using none",
		`sessions.resumeOnLaunch is not true or false ("no"): using true`,
	}
	if !slices.Equal(loaded.Dropped, want) {
		t.Fatalf("dropped %q", loaded.Dropped)
	}
}

// TestAPutOfSessionsKeepsWhatItLeavesOut: a page that sends only where it wants sessions to open
// keeps the terminal command and the resume switch that were saved.
func TestAPutOfSessionsKeepsWhatItLeavesOut(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)

	mustUpdate(t, store, "sessions", `{"where":"terminal","terminalCommand":["/bin/t",`+
		`"{command}"],"resumeOnLaunch":false}`)
	mustUpdate(t, store, "sessions", `{"where":"command"}`)

	want := `{"where":"command","terminalCommand":["/bin/t","{command}"],` +
		`"resumeOnLaunch":false}`

	got := js.Stringify(store.Get().Sessions)
	if got != want {
		t.Fatalf("got %s", got)
	}
}
