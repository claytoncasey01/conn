package newsession_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/newsession"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

// answersGolden is what /api/sessions/new answers, case by case, as the page reads it:
// src/lib/newSession.test.ts reads the same file.
const answersGolden = "../../testdata/answers/sessions/new.json"

// answered is one answer: its status and its body.
type answered struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}

// goldenAPI is an API over a fixed clone folder, opening with answer, dry when dry is set.
func goldenAPI(answer launch.Opened, dry bool, running int) *newsession.API {
	return goldenAPIWith(func(context.Context, pty.Spec) launch.Opened { return answer }, dry,
		running)
}

// goldenAPIWith is goldenAPI opening through open.
func goldenAPIWith(open launch.Opener, dry bool, running int) *newsession.API {
	current := settings.Defaults()
	current.Repos.Allow = []string{"acme/*"}

	return newsession.New(newsession.Deps{
		Settings: func() settings.Settings { return current },
		Repos: func(settings.Settings) []string {
			return []string{"acme/widgets", "acme/gadgets"}
		},
		CloneFor: func(repo string, _ settings.Settings) (string, bool) {
			return cloneFolder, repo == "acme/widgets"
		},
		CheckClone: func(string) error { return nil },
		OriginOf:   func(string) string { return "acme/widgets" },
		Resolve:    func(path string) string { return path },
		Open:       open,
		Claude:     "claude", Dry: dry, Room: roomOf(running),
		NewID: func() (string, error) { return conversationID, nil },
		Now:   time.Now, Cooldown: 0,
	})
}

// commandAPI is goldenAPI with launch's own opener for sessions set to open as a command, so the
// line to paste is launch's, not one written here.
func commandAPI() *newsession.API {
	return goldenAPIWith(launch.New(launch.Deps{
		Sessions: func() settings.Sessions {
			return settings.Sessions{Where: settings.WhereCommand, ResumeOnLaunch: true}
		},
		Start: nil, Cmux: nil, LookPath: nil, Environ: nil,
	}), false, 0)
}

func goldenStart(t *testing.T, api *newsession.API, body string) answered {
	t.Helper()

	reply := start(t, api, body)

	return answered{Status: reply.Code, Body: reply.Body}
}

func TestTheAnswersThePageReadsAreGolden(t *testing.T) {
	t.Parallel()

	inside := launch.Opened{OK: true, HostedID: hostedID}
	choices := goldenAPI(inside, false, 0).Choices()
	named := `{"repo":"acme/widgets","name":"fix the widget"}`
	cases := map[string]answered{
		"choices":  {Status: choices.Code, Body: choices.Body},
		"inside":   goldenStart(t, goldenAPI(inside, false, 0), named),
		"terminal": goldenStart(t, goldenAPI(launch.Opened{OK: true}, false, 0), named),
		"command":  goldenStart(t, commandAPI(), named),
		"dry":      goldenStart(t, goldenAPI(inside, true, 0), named),
		"noClone":  goldenStart(t, goldenAPI(inside, false, 0), `{"repo":"acme/gadgets"}`),
		"badName": goldenStart(t, goldenAPI(inside, false, 0),
			`{"repo":"acme/widgets","name":"\u65e5\u672c"}`),
		"full": goldenStart(t, goldenAPI(inside, false, sessionsFull), named),
		"notStarted": goldenStart(t, goldenAPI(launch.Opened{
			OK: false, Error: "starting the session inside Conn: the program was not found",
		}, false, 0), named),
	}

	got, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(answersGolden, got, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(answersGolden)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Errorf("the answers are\n%s\nwant\n%s", got, want)
	}
}
