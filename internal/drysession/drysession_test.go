package drysession_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/drysession"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// waitLimit bounds every wait here, and waitPoll is how often a wait looks.
const (
	waitLimit = 5 * time.Second
	waitPoll  = 10 * time.Millisecond
)

var errRealSpawner = errors.New("the real spawner was asked")

// host is a Host whose spawner scripts dry sessions and records every other spec it is given.
func host(t *testing.T) (*pty.Host, *[]pty.Spec) {
	t.Helper()

	var given []pty.Spec

	spawner := drysession.Spawner(func(spec pty.Spec) (pty.Process, error) {
		given = append(given, spec)

		return nil, errRealSpawner
	})

	made := pty.NewHost(pty.Config{
		Spawner: spawner, Limits: pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(waitLimit))
		defer cancel()

		_ = made.StopAll(ctx)
	})

	return made, &given
}

// screenHolds waits until the session's screen holds text.
func screenHolds(t *testing.T, made *pty.Host, sessionID pty.ID, text string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		lines, _, err := made.ScreenLines(sessionID, 24)
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(strings.Join(lines, "\n"), text) {
			return
		}

		time.Sleep(waitPoll)
	}

	lines, _, _ := made.ScreenLines(sessionID, 24)
	t.Fatalf("the screen never held %q:\n%s", text, strings.Join(lines, "\n"))
}

// TestADrySessionRunsOnTheHostAndEchoes: a dry spec starts a scripted session the host holds
// like any other, which shows its banner, echoes what is typed and answers Enter; the real
// spawner is never asked.
func TestADrySessionRunsOnTheHostAndEchoes(t *testing.T) {
	t.Parallel()

	made, given := host(t)

	sessionID, err := made.Start(t.Context(), drysession.Spec("/work/PROJ-1", "PROJ-1"))
	if err != nil {
		t.Fatal(err)
	}

	screenHolds(t, made, sessionID, `Dry run: a stand-in for claude, "PROJ-1"`)

	err = made.Write(sessionID, []byte("hello\x1b[Ax\x7f!\r"))
	if err != nil {
		t.Fatal(err)
	}

	screenHolds(t, made, sessionID, "hello!")
	screenHolds(t, made, sessionID, "(dry run) Noted: nothing was sent.")

	if len(*given) != 0 {
		t.Fatalf("the real spawner was given %d specs", len(*given))
	}
}

// TestADrySessionStops: a stop ends it at once, as a polite signal would.
func TestADrySessionStops(t *testing.T) {
	t.Parallel()

	made, _ := host(t)

	sessionID, err := made.Start(t.Context(), drysession.Spec("/work/PROJ-2", "PROJ-2"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	err = made.Stop(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	for _, info := range made.Live() {
		if info.ID != sessionID {
			continue
		}

		if info.ExitCode == nil {
			t.Fatal("still running")
		}

		if *info.ExitCode != 143 {
			t.Errorf("exit code %d", *info.ExitCode)
		}
	}
}

// TestOnlyTheDryProgramIsScripted: any other program, a lookalike included, goes to the real
// spawner as it is.
func TestOnlyTheDryProgramIsScripted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		program string
	}{
		{"claude", "/usr/local/bin/claude"},
		{"a longer path", drysession.Program + "/x"},
		{"a shorter path", "/dev/null/conn-dry"},
		{"another case", strings.ToUpper(drysession.Program)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			made, given := host(t)
			spec := pty.Spec{Argv: []string{tt.program}, Dir: "/w", Env: nil, Name: "n"}

			_, err := made.Start(t.Context(), spec)
			if !errors.Is(err, errRealSpawner) {
				t.Fatalf("err %v", err)
			}

			if len(*given) != 1 {
				t.Fatalf("the real spawner was given %d specs", len(*given))
			}

			if !slices.Equal((*given)[0].Argv, []string{tt.program}) {
				t.Errorf("argv %q", (*given)[0].Argv)
			}
		})
	}
}

// TestTheRealSpawnerStartsNothingForTheDryProgram: a dry spec that reached the real spawner
// fails: there is no program at Program.
func TestTheRealSpawnerStartsNothingForTheDryProgram(t *testing.T) {
	t.Parallel()

	process, err := pty.Spawn(drysession.Spec(t.TempDir(), "PROJ-3"))
	if err == nil {
		_ = process.Kill()

		t.Fatal("the real spawner started the dry program")
	}
}

// TestDrySessionsHaveProcessIDsNoSystemGives: each reports its own id, above any real one.
func TestDrySessionsHaveProcessIDsNoSystemGives(t *testing.T) {
	t.Parallel()

	made, _ := host(t)

	for _, name := range []string{"PROJ-4", "PROJ-5"} {
		_, err := made.Start(t.Context(), drysession.Spec("/w", name))
		if err != nil {
			t.Fatal(err)
		}
	}

	pids := map[int]bool{}

	for _, info := range made.Live() {
		if info.PID <= 4_194_304 {
			t.Errorf("pid %d is one a system could give", info.PID)
		}

		pids[info.PID] = true
	}

	if len(pids) != 2 {
		t.Errorf("pids %v", pids)
	}
}

// TestTheBannerDrawsNoControlCharacter: a name holding C0, DEL, C1 or a bidi control is drawn
// without them.
func TestTheBannerDrawsNoControlCharacter(t *testing.T) {
	t.Parallel()

	made, _ := host(t)

	name := "PROJ-9" + "\x1b[2J\x7f\u009b\u202e"

	sessionID, err := made.Start(t.Context(), drysession.Spec("/w", name))
	if err != nil {
		t.Fatal(err)
	}

	screenHolds(t, made, sessionID, `"PROJ-9[2J"`)
}
