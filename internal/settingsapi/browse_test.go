package settingsapi_test

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/settingsapi"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var (
	errSAML        = errors.New("SAML enforcement")
	errRateLimited = errors.New("rate limited")
)

// ghSaidError is an error in gh's own words, which start with a capital.
type ghSaidError string

func (g ghSaidError) Error() string { return string(g) }

const errSignIn = ghSaidError(
	"To get started with GitHub CLI, please run: gh auth login\nmore detail")

const (
	loginCall = "api user --jq .login"
	orgsCall  = "api user/orgs?per_page=100 --jq .[].login"
	listCall  = "--limit 100 --json nameWithOwner --jq .[].nameWithOwner"
)

func list(owner string) string { return "repo list " + owner + " " + listCall }

func signedIn() map[string]string {
	return map[string]string{
		loginCall:         "jane\n",
		orgsCall:          "acme\nacme-labs\n",
		list("jane"):      "jane/widgets\njane/notes\n",
		list("acme"):      "acme/infra\nAcme/App\n",
		list("acme-labs"): "",
	}
}

func browse(t *testing.T, world *world) (int, string) {
	t.Helper()

	return handle(t, world.api(), settingsapi.Route{Kind: settingsapi.KindBrowse}, "")
}

func TestBrowse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		answer   func(string) (string, error)
		wantCode int
		wantBody string
	}{
		{
			name:     "each owner with its repositories, the signed-in login first",
			answer:   table(signedIn()),
			wantCode: 200,
			wantBody: `{"owners":[{"owner":"jane","repos":["jane/widgets","jane/notes"]},` +
				`{"owner":"acme","repos":["acme/infra","Acme/App"]},` +
				`{"owner":"acme-labs","repos":[]}]}`,
		},
		{
			name: "a bad owner is not listed, a bad or foreign repository is dropped",
			answer: table(map[string]string{
				loginCall: "jane",
				orgsCall:  "--evil\nbad name\n..\nacme\n",
				list("jane"): "jane/ok\njane/../x\nother/stolen\njane/wid gets\njane/q?x\n\n" +
					"   \njane/<b>x</b>\n",
				list("acme"): "acme/fine\n",
			}),
			wantCode: 200,
			wantBody: `{"owners":[{"owner":"jane","repos":["jane/ok"]},` +
				`{"owner":"acme","repos":["acme/fine"]}]}`,
		},
		{
			name: "an owner that could not be listed is named",
			answer: func(key string) (string, error) {
				if strings.HasPrefix(key, "repo list acme ") {
					return "", errSAML
				}

				return table(signedIn())(key)
			},
			wantCode: 200,
			wantBody: `{"owners":[{"owner":"jane","repos":["jane/widgets","jane/notes"]},` +
				`{"owner":"acme-labs","repos":[]}],"failed":["acme"]}`,
		},
		{
			name:     "gh that cannot say who is signed in",
			answer:   func(string) (string, error) { return "", errSignIn },
			wantCode: 502,
			wantBody: `{"error":"To get started with GitHub CLI, please run: gh auth login"}`,
		},
		{
			name: "every owner failed",
			answer: func(key string) (string, error) {
				if strings.HasPrefix(key, "repo ") {
					return "", errRateLimited
				}

				return table(signedIn())(key)
			},
			wantCode: 502,
			wantBody: `{"error":"rate limited"}`,
		},
		{
			name: "a login with an underscore",
			answer: table(map[string]string{
				loginCall:         "jane_acme\n",
				orgsCall:          "acme\n",
				list("jane_acme"): "jane_acme/notes\n",
				list("acme"):      "acme/infra\n",
			}),
			wantCode: 200,
			wantBody: `{"owners":[{"owner":"jane_acme","repos":["jane_acme/notes"]},` +
				`{"owner":"acme","repos":["acme/infra"]}]}`,
		},
		{
			name:     "no login",
			answer:   func(string) (string, error) { return "", nil },
			wantCode: 502,
			wantBody: `{"error":"gh did not report a login"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(withAllow("acme/*"))
			world.gh.answer = tt.answer

			code, body := browse(t, world)
			if code != tt.wantCode {
				t.Errorf("code = %d, want %d", code, tt.wantCode)
			}

			if body != tt.wantBody {
				t.Errorf("body = %s\nwant %s", body, tt.wantBody)
			}
		})
	}
}

func TestBrowseNeverPassesABadOwnerToGh(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow("acme/*"))
	world.gh.answer = table(map[string]string{
		loginCall: "jane", orgsCall: "--evil\nbad name\n..\nacme\n",
		list("jane"): "", list("acme"): "",
	})
	browse(t, world)

	for _, call := range world.gh.callList() {
		for _, bad := range []string{"--evil", "bad name", " .. "} {
			if strings.Contains(call, bad) {
				t.Errorf("gh %s passed %q", call, bad)
			}
		}
	}
}

func TestBrowseSaysWhatLoginItCouldNotUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		login string
		want  string
	}{
		{login: "--evil", want: `{"error":"gh reported a login conn cannot use: \"--evil\""}`},
		{login: "ja ne", want: `{"error":"gh reported a login conn cannot use: \"ja ne\""}`},
		{login: "jane/x", want: `{"error":"gh reported a login conn cannot use: \"jane/x\""}`},
	}

	for _, tt := range tests {
		t.Run(tt.login, func(t *testing.T) {
			t.Parallel()

			world := newWorld(withAllow("acme/*"))
			world.gh.answer = func(key string) (string, error) {
				if key == loginCall {
					return tt.login, nil
				}

				return "", nil
			}

			code, body := browse(t, world)
			if code != 502 {
				t.Errorf("code = %d, want 502", code)
			}

			if body != tt.want {
				t.Errorf("body = %s, want %s", body, tt.want)
			}

			for _, call := range world.gh.callList() {
				if strings.HasPrefix(call, "repo ") {
					t.Errorf("gh %s ran", call)
				}
			}
		})
	}
}

func TestBrowseWithGitHubOffRunsNothing(t *testing.T) {
	t.Parallel()

	state := withAllow("acme/*")
	state.GitHub.Enabled = false

	world := newWorld(state)
	world.gh.answer = table(signedIn())

	code, _ := browse(t, world)
	if code != 409 {
		t.Errorf("code = %d, want 409", code)
	}

	if len(world.gh.callList()) != 0 {
		t.Errorf("gh ran: %q", world.gh.callList())
	}
}

func TestBrowseInSetupWorks(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow())
	world.gh.answer = table(signedIn())

	code, _ := browse(t, world)
	if code != 200 {
		t.Errorf("code = %d, want 200: browse is how a first list gets chosen", code)
	}
}

func TestBrowseRunsGhOnlyWhenCalled(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow("acme/*"))
	world.gh.answer = table(signedIn())
	api := world.api()

	get(t, api)

	if len(world.gh.callList()) != 0 {
		t.Fatalf("GET ran gh: %q", world.gh.callList())
	}

	handle(t, api, settingsapi.Route{Kind: settingsapi.KindBrowse}, "")

	if len(world.gh.callList()) == 0 {
		t.Error("browse ran no gh")
	}
}

func TestBrowseCapsTheOwners(t *testing.T) {
	t.Parallel()

	organizations := make([]string, 0, 60)
	for index := range 60 {
		organizations = append(organizations, "org"+strconv.Itoa(index))
	}

	world := newWorld(withAllow("acme/*"))
	world.gh.answer = func(key string) (string, error) {
		switch key {
		case loginCall:
			return "jane", nil
		case orgsCall:
			return strings.Join(organizations, "\n"), nil
		}

		return "", nil
	}

	_, body := browse(t, world)
	if count := strings.Count(body, `"owner":`); count != settingsapi.MaxBrowseOwners {
		t.Errorf("owners = %d, want %d", count, settingsapi.MaxBrowseOwners)
	}
}

func TestTwoBrowsesAtOnceShareOneRunOfGh(t *testing.T) {
	t.Parallel()

	var (
		guard  sync.Mutex
		logins int
	)

	release := make(chan struct{})
	world := newWorld(withAllow("acme/*"))
	world.gh.answer = func(key string) (string, error) {
		if key == loginCall {
			guard.Lock()
			logins++
			guard.Unlock()

			<-release
		}

		return table(signedIn())(key)
	}

	api := world.api()
	bodies := make(chan string, 2)

	for range 2 {
		go func() {
			_, body := handle(t, api, settingsapi.Route{Kind: settingsapi.KindBrowse}, "")
			bodies <- body
		}()
	}

	waitFor(t, func() bool { return api.BrowseWaiting() == 2 })
	close(release)

	first, second := <-bodies, <-bodies
	if first != second {
		t.Errorf("answers differ:\n%s\n%s", first, second)
	}

	handle(t, api, settingsapi.Route{Kind: settingsapi.KindBrowse}, "")

	if logins != 2 {
		t.Errorf("logins = %d, want one shared run and one for the later press", logins)
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	limit := testtime.Scaled(5 * time.Second)

	deadline := time.Now().Add(limit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for a condition that never held", limit)
		}

		time.Sleep(time.Millisecond)
	}
}
