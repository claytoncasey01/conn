package settings_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// TestTheSessionsDefaultsOpenInsideAndResume: a fresh install starts sessions inside Conn and
// resumes on launch.
func TestTheSessionsDefaultsOpenInsideAndResume(t *testing.T) {
	t.Parallel()

	got := js.Stringify(settings.Defaults().Sessions)
	if got != `{"where":"inside","resumeOnLaunch":true,"advisor":"claude-code"}` {
		t.Fatalf("got %s", got)
	}
}

// TestASessionsSectionIsCheckedWhole: a PUT of the sessions section names every problem at once,
// each saying what to set instead, and a good one comes back as it was sent. No key names a
// program: a terminal command sent is not read.
func TestASessionsSectionIsCheckedWhole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		// want is the value a good section comes back as; wantError the refusal of a bad one.
		want      string
		wantError string
	}{
		{
			"each choice", `{"where":"terminal"}`,
			`{"where":"terminal","resumeOnLaunch":true,"advisor":"claude-code"}`, "",
		},
		{
			"the switch", `{"where":"command","resumeOnLaunch":false}`,
			`{"where":"command","resumeOnLaunch":false,"advisor":"claude-code"}`, "",
		},
		{
			"an advisor", `{"advisor":"fable"}`,
			`{"where":"inside","resumeOnLaunch":true,"advisor":"fable"}`, "",
		},
		{
			"an advisor that is no choice", `{"advisor":"claude-opus-5-5"}`, "",
			`sessions.advisor "claude-opus-5-5" is not a choice: ` +
				"set it to claude-code, fable, opus or sonnet",
		},
		{
			"an advisor that is not text", `{"advisor":["opus"]}`, "",
			`sessions.advisor ["opus"] is not a choice: ` +
				"set it to claude-code, fable, opus or sonnet",
		},
		{
			"a terminal command is not read",
			`{"where":"terminal","terminalCommand":["/bin/sh","-c","touch /tmp/x","{command}"]}`,
			`{"where":"terminal","resumeOnLaunch":true,"advisor":"claude-code"}`, "",
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
			"every problem at once", `{"where":"x","resumeOnLaunch":0,"advisor":"haiku"}`, "",
			`sessions.where "x" is not a choice: set it to inside, terminal or command; ` +
				"sessions.resumeOnLaunch 0 is not true or false: set it to true or false; " +
				`sessions.advisor "haiku" is not a choice: ` +
				"set it to claude-code, fable, opus or sonnet",
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

// TestAHandEditedSessionsSectionDropsWhatIsBad: the file is read leniently, each bad field
// falling back to its default with a note.
func TestAHandEditedSessionsSectionDropsWhatIsBad(t *testing.T) {
	t.Parallel()

	loaded := settings.ParseDetailed(parse(t,
		`{"sessions":{"where":"tmux","resumeOnLaunch":"no","advisor":"gpt"}}`), true)

	got := js.Stringify(loaded.Settings.Sessions)
	if got != `{"where":"inside","resumeOnLaunch":true,"advisor":"claude-code"}` {
		t.Fatalf("got %s", got)
	}

	want := []string{
		`sessions.where "tmux" is not a choice: using inside`,
		`sessions.resumeOnLaunch is not true or false ("no"): using true`,
		`sessions.advisor "gpt" is not a choice: using claude-code`,
	}
	if !slices.Equal(loaded.Dropped, want) {
		t.Fatalf("dropped %q", loaded.Dropped)
	}
}

// TestAnOldTerminalCommandIsNeverRead: a settings.json an earlier build wrote with a terminal
// command is read without it, says so, and the next save writes the file without it.
func TestAnOldTerminalCommandIsNeverRead(t *testing.T) {
	t.Parallel()

	dir := withFile(t, `{"repos":{"allow":["acme/*"]},"sessions":{"where":"terminal",`+
		`"terminalCommand":["/bin/sh","-c","touch /tmp/pwned","{command}"]}}`)

	loaded := settings.LoadDetailed(dir)

	got := js.Stringify(loaded.Settings.Sessions)
	if got != `{"where":"terminal","resumeOnLaunch":true,"advisor":"claude-code"}` {
		t.Fatalf("got %s", got)
	}

	if !slices.Equal(loaded.Dropped, []string{"sessions.terminalCommand is no longer read: " +
		"sessions set to open in your terminal open in cmux, and saving this section drops it"}) {
		t.Fatalf("dropped %q", loaded.Dropped)
	}

	store := settings.NewStore(quiet())
	store.Reload(dir)
	mustUpdate(t, store, "sessions", `{"where":"terminal"}`)

	saved, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(saved), "terminalCommand") {
		t.Fatalf("the save kept it:\n%s", saved)
	}
}

// TestAPutOfSessionsKeepsWhatItLeavesOut: a page that sends only where it wants sessions to open
// keeps the resume switch and the advisor that were saved.
func TestAPutOfSessionsKeepsWhatItLeavesOut(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)

	mustUpdate(t, store, "sessions", `{"where":"terminal","resumeOnLaunch":false,"advisor":"opus"}`)
	mustUpdate(t, store, "sessions", `{"where":"command"}`)

	want := `{"where":"command","resumeOnLaunch":false,"advisor":"opus"}`

	got := js.Stringify(store.Get().Sessions)
	if got != want {
		t.Fatalf("got %s", got)
	}
}
