package launch_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	claudePath = "/opt/claude/bin/claude"
	hostedID   = pty.ID("0123456789abcdef0123456789abcdef")
	folder     = "/work/it's here"
)

var (
	errCmuxDown  = errors.New("cmux is not running")
	errSpawn     = errors.New("too many sessions are running")
	errNotOnPath = errors.New("executable file not found in $PATH")
)

// rig is an Opener's world: what each of its calls was given, and what they answer.
type rig struct {
	mu        sync.Mutex
	sessions  settings.Sessions
	started   []pty.Spec
	cmuxCalls [][]string
	startErr  error
	cmuxOK    bool
	found     string
	foundErr  error
}

func newRig(where string) *rig {
	return &rig{
		mu:       sync.Mutex{},
		sessions: settings.Sessions{Where: where, ResumeOnLaunch: true},
		cmuxOK:   true, found: claudePath,
	}
}

func (r *rig) opener() launch.Opener {
	return launch.New(launch.Deps{
		Sessions: func() settings.Sessions { return r.sessions },
		Start: func(_ context.Context, spec pty.Spec) (pty.ID, error) {
			r.mu.Lock()
			defer r.mu.Unlock()

			r.started = append(r.started, spec)
			if r.startErr != nil {
				return "", r.startErr
			}

			return hostedID, nil
		},
		Cmux: func(_ context.Context, name, cwd, command string) cmux.Opened {
			r.mu.Lock()
			defer r.mu.Unlock()

			r.cmuxCalls = append(r.cmuxCalls, []string{name, cwd, command})
			if !r.cmuxOK {
				return cmux.Opened{OK: false, Error: errCmuxDown.Error(), ManualCommand: "pasted"}
			}

			return cmux.Opened{OK: true, Error: "", ManualCommand: ""}
		},
		LookPath: func(string) (string, error) { return r.found, r.foundErr },
		Environ:  func() []string { return []string{"HOME=/home/me", "CONN_PORT=7777"} },
	})
}

func reviewSpec() pty.Spec {
	return pty.Spec{
		Argv: []string{"claude", "--name", "pr-7-acme-app", "--model", "opus", "Review it."},
		Dir:  folder, Env: nil, Name: "pr-7-acme-app",
	}
}

// TestInsideStartsTheProgramItselfOnConnsTerminal: inside Conn, the host is given the program
// found on PATH as argv[0] (no shell, no wrapper), the same arguments and folder, and the login
// environment; nothing is asked of cmux or a terminal.
func TestInsideStartsTheProgramItselfOnConnsTerminal(t *testing.T) {
	t.Parallel()

	world := newRig(settings.WhereInside)
	opened := world.opener()(t.Context(), reviewSpec())

	if !opened.OK {
		t.Fatalf("not opened: %+v", opened)
	}

	if opened.HostedID != hostedID {
		t.Errorf("hosted id %q", opened.HostedID)
	}

	if len(world.started) != 1 {
		t.Fatalf("started %d", len(world.started))
	}

	want := []string{claudePath, "--name", "pr-7-acme-app", "--model", "opus", "Review it."}
	if !slices.Equal(world.started[0].Argv, want) {
		t.Errorf("argv %q", world.started[0].Argv)
	}

	if world.started[0].Dir != folder {
		t.Errorf("folder %q", world.started[0].Dir)
	}

	if !slices.Equal(world.started[0].Env, []string{"HOME=/home/me", "CONN_PORT=7777"}) {
		t.Errorf("environment %q", world.started[0].Env)
	}

	if len(world.cmuxCalls) != 0 {
		t.Error("a terminal app was asked")
	}
}

// TestInsideRunsAnAbsoluteProgramAsItIs: CONN_CLAUDE naming a path is never looked up.
func TestInsideRunsAnAbsoluteProgramAsItIs(t *testing.T) {
	t.Parallel()

	world := newRig(settings.WhereInside)
	world.found = "/elsewhere/claude"
	spec := reviewSpec()
	spec.Argv[0] = "/usr/local/bin/claude"

	world.opener()(t.Context(), spec)

	if world.started[0].Argv[0] != "/usr/local/bin/claude" {
		t.Errorf("argv[0] %q", world.started[0].Argv[0])
	}
}

// TestInsideRefusesAProgramNotFoundAtAnAbsolutePath: a program not on PATH, or found only at a
// relative path, starts nothing, and the person gets the line to paste.
func TestInsideRefusesAProgramNotFoundAtAnAbsolutePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		found    string
		foundErr error
	}{
		{"not on PATH", "", errNotOnPath},
		{"a relative path", "bin/claude", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newRig(settings.WhereInside)
			world.found, world.foundErr = tt.found, tt.foundErr

			opened := world.opener()(t.Context(), reviewSpec())
			if opened.OK {
				t.Fatal("opened")
			}

			if len(world.started) != 0 {
				t.Fatal("the host was asked")
			}

			if !strings.Contains(opened.Error, "CONN_CLAUDE") {
				t.Errorf("error %q", opened.Error)
			}

			if opened.ManualCommand != launch.PasteLine(reviewSpec()) {
				t.Errorf("manual command %q", opened.ManualCommand)
			}
		})
	}
}

// TestInsideSaysWhyTheHostRefused: a host that cannot start it leaves the line to paste.
func TestInsideSaysWhyTheHostRefused(t *testing.T) {
	t.Parallel()

	world := newRig(settings.WhereInside)
	world.startErr = errSpawn

	opened := world.opener()(t.Context(), reviewSpec())
	if opened.OK {
		t.Fatal("opened")
	}

	if !strings.Contains(opened.Error, errSpawn.Error()) {
		t.Errorf("error %q", opened.Error)
	}

	if opened.ManualCommand == "" {
		t.Error("no line to paste")
	}
}

// TestInATerminalCmuxGetsTheOneShellLine: set to open sessions in a terminal, cmux opens a
// workspace named for the session, in its folder, running ShellLine(argv), as it did.
func TestInATerminalCmuxGetsTheOneShellLine(t *testing.T) {
	t.Parallel()

	world := newRig(settings.WhereTerminal)

	opened := world.opener()(t.Context(), reviewSpec())
	if !opened.OK {
		t.Fatalf("not opened: %+v", opened)
	}

	want := []string{
		"pr-7-acme-app", folder, "claude --name pr-7-acme-app --model opus 'Review it.'",
	}

	if len(world.cmuxCalls) != 1 {
		t.Fatalf("cmux calls %q", world.cmuxCalls)
	}

	if !slices.Equal(world.cmuxCalls[0], want) {
		t.Errorf("cmux got %q", world.cmuxCalls[0])
	}

	if len(world.started) != 0 {
		t.Error("the host was asked")
	}

	world.cmuxOK = false

	failed := world.opener()(t.Context(), reviewSpec())
	if failed.OK {
		t.Fatal("a failed cmux opened")
	}

	if failed.ManualCommand != "pasted" {
		t.Errorf("manual command %q", failed.ManualCommand)
	}

	if failed.Tried != "a cmux workspace" {
		t.Errorf("tried %q", failed.Tried)
	}
}

// TestAsACommandNothingStarts: the person is shown the line to paste: into the folder, then the
// program, through ShellLine.
func TestAsACommandNothingStarts(t *testing.T) {
	t.Parallel()

	world := newRig(settings.WhereCommand)

	opened := world.opener()(t.Context(), reviewSpec())
	if opened.OK {
		t.Fatal("opened")
	}

	want := `cd '/work/it'\''s here' && claude --name pr-7-acme-app --model opus 'Review it.'`
	if opened.ManualCommand != want {
		t.Errorf("manual command %q", opened.ManualCommand)
	}

	if len(world.started)+len(world.cmuxCalls) != 0 {
		t.Error("something was started")
	}
}

// TestASpecNoSessionMayStartFromStartsNothing: no program, a relative folder, no name, a NUL or
// an argument too long are refused, all at once, wherever sessions open.
func TestASpecNoSessionMayStartFromStartsNothing(t *testing.T) {
	t.Parallel()

	for _, tt := range specsNoSessionMayStartFrom() {
		for _, where := range []string{
			settings.WhereInside, settings.WhereTerminal, settings.WhereCommand,
		} {
			t.Run(tt.name+" "+where, func(t *testing.T) {
				t.Parallel()
				checkNothingStarted(t, where, tt.spec)
			})
		}
	}
}

// namedSpec is a spec a table row names.
type namedSpec struct {
	name string
	spec pty.Spec
}

// specsNoSessionMayStartFrom are specs no session may start from, one problem each.
func specsNoSessionMayStartFrom() []namedSpec {
	return []namedSpec{
		{"no program", pty.Spec{Argv: nil, Dir: "/w", Env: nil, Name: "n"}},
		{"a relative folder", pty.Spec{Argv: []string{"claude"}, Dir: "w", Env: nil, Name: "n"}},
		{"no name", pty.Spec{Argv: []string{"claude"}, Dir: "/w", Env: nil, Name: ""}},
		{"a NUL in an argument", pty.Spec{
			Argv: []string{"claude", "a\x00b"}, Dir: "/w", Env: nil, Name: "n",
		}},
		{"a NUL in the folder", pty.Spec{
			Argv: []string{"claude"}, Dir: "/w\x00/x", Env: nil, Name: "n",
		}},
		{"a NUL in the name", pty.Spec{
			Argv: []string{"claude"}, Dir: "/w", Env: nil, Name: "n\x00",
		}},
		{"Ctrl-U in the folder", pty.Spec{
			Argv: []string{"claude"}, Dir: "/w/a\x15touch /tmp/pwned #", Env: nil, Name: "n",
		}},
		{"a newline in the folder", pty.Spec{
			Argv: []string{"claude"}, Dir: "/w/a\nb", Env: nil, Name: "n",
		}},
		{"an escape in an argument", pty.Spec{
			Argv: []string{"claude", "a\x1b[2Jb"}, Dir: "/w", Env: nil, Name: "n",
		}},
		{"a right-to-left override in the name", pty.Spec{
			Argv: []string{"claude"}, Dir: "/w", Env: nil, Name: "a\u202eb",
		}},
		{"a C1 control in the folder", pty.Spec{
			Argv: []string{"claude"}, Dir: "/w/a\u009bb", Env: nil, Name: "n",
		}},
		{"bytes that are not UTF-8 in an argument", pty.Spec{
			Argv: []string{"claude", "a\xffb"}, Dir: "/w", Env: nil, Name: "n",
		}},
		{"an argument too long", pty.Spec{
			Argv: []string{"claude", strings.Repeat("a", 200<<10)}, Dir: "/w", Env: nil,
			Name: "n",
		}},
	}
}

// checkNothingStarted opens spec where sessions open and fails unless nothing started, no line
// to paste was given, and Check calls it invalid.
func checkNothingStarted(t *testing.T, where string, spec pty.Spec) {
	t.Helper()

	world := newRig(where)

	opened := world.opener()(t.Context(), spec)
	if opened.OK {
		t.Fatal("opened")
	}

	if opened.ManualCommand != "" {
		t.Errorf("a line to paste: %q", opened.ManualCommand)
	}

	if len(world.started)+len(world.cmuxCalls) != 0 {
		t.Error("something was started")
	}

	if !errors.Is(launch.Check(spec), launch.ErrInvalidSession) {
		t.Error("not ErrInvalidSession")
	}
}

// TestCheckNamesEveryProblem: one spec with three problems gets all three.
func TestCheckNamesEveryProblem(t *testing.T) {
	t.Parallel()

	err := launch.Check(pty.Spec{
		Argv: []string{"a\x00", "b\x15"}, Dir: "rel\n", Env: nil, Name: "\u202e",
	})

	for _, want := range []string{
		"argument 0 holds a NUL", "not an absolute path",
		"argument 1 holds a control character", "the folder holds a control character",
		"the name holds a control character",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}
}

// TestNoLineToPasteHoldsAByteBeyondPrintableASCII: a folder holding a letter beyond ASCII (or a
// line separator) still starts inside Conn, where no shell reads it, but neither showing the
// command nor a failed start offers a line to paste, and both say why.
func TestNoLineToPasteHoldsAByteBeyondPrintableASCII(t *testing.T) {
	t.Parallel()

	for _, folder := range []string{"/work/x\u0112touch pwned;#", "/work/a\u2028b"} {
		spec := reviewSpec()
		spec.Dir = folder

		inside := newRig(settings.WhereInside)
		if !inside.opener()(t.Context(), spec).OK {
			t.Errorf("%q did not open inside Conn", folder)
		}

		inside.startErr = errSpawn
		failedInside := inside.opener()(t.Context(), spec)

		shown := newRig(settings.WhereCommand).opener()(t.Context(), spec)

		for _, opened := range []launch.Opened{failedInside, shown} {
			if opened.ManualCommand != "" {
				t.Errorf("a line to paste: %q", opened.ManualCommand)
			}

			if !strings.Contains(opened.Error, "no command to paste") {
				t.Errorf("error %q", opened.Error)
			}
		}
	}
}

// TestInvisibleCharactersInTheNameOrFolderStartNothing: a name or folder holding a zero-width
// space, a word joiner, a byte order mark or a tag character, shown differently from what it is,
// is refused. The joiners and the emoji presentation selector are part of how emoji and Persian
// or Indic words are spelled: those names and folders start inside Conn, with no line to paste.
func TestInvisibleCharactersInTheNameOrFolderStartNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sessionName, dir string
		refused                bool
	}{
		{"a zero-width space in the name", "pr-1\u200b", "/w", true},
		{"a word joiner in the name", "pr-1\u2060x", "/w", true},
		{"a byte order mark in the folder", "n", "/w/a\ufeffb", true},
		{"a tag character in the folder", "n", "/w/a\U000e0041b", true},
		{"a technologist in the name", "\U0001f469\u200d\U0001f4bb notes", "/w", false},
		{"a red heart in the folder", "n", "/w/notes-\u2764\ufe0f", false},
		{"a rainbow flag in the name", "\U0001f3f3\ufe0f\u200d\U0001f308", "/w", false},
		{
			"a Persian word with a non-joiner in the folder", "n",
			"/w/\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := pty.Spec{
				Argv: []string{"claude", "--name", tt.sessionName}, Dir: tt.dir, Env: nil,
				Name: tt.sessionName,
			}

			refused := errors.Is(launch.Check(spec), launch.ErrInvalidSession)
			if refused != tt.refused {
				t.Fatalf("refused %v, want %v", refused, tt.refused)
			}

			if tt.refused {
				return
			}

			inside := newRig(settings.WhereInside).opener()(t.Context(), spec)
			if !inside.OK {
				t.Errorf("did not start inside Conn: %+v", inside)
			}

			shown := launch.New(commandDeps())(t.Context(), spec)
			if shown.ManualCommand != "" {
				t.Errorf("a line to paste: %q", shown.ManualCommand)
			}
		})
	}
}

// TestCheckNameIsChecksRuleForTheName: a name is refused by CheckName exactly when Check refuses
// a spec for its name alone, so a caller can check a name before it builds the spec.
func TestCheckNameIsChecksRuleForTheName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sessionName string
		refused           bool
	}{
		{"a plain name", "fix the widget", false},
		{"an emoji", "\U0001f469\u200d\U0001f4bb notes", false},
		{"no name", "", true},
		{"a NUL", "a\x00b", true},
		{"an escape", "a\x1b[31mb", true},
		{"a line break", "a\nb", true},
		{"a bidi override", "a\u202eb", true},
		{"a zero-width space", "a\u200bb", true},
		{"bytes that are not UTF-8", "a\xffb", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			refused := errors.Is(launch.CheckName(tt.sessionName), launch.ErrInvalidSession)
			if refused != tt.refused {
				t.Fatalf("refused %v, want %v", refused, tt.refused)
			}

			spec := pty.Spec{Argv: []string{"claude"}, Dir: "/w", Env: nil, Name: tt.sessionName}
			if errors.Is(launch.Check(spec), launch.ErrInvalidSession) != tt.refused {
				t.Errorf("Check disagrees: refused %v", !tt.refused)
			}
		})
	}
}
