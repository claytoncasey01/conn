package gallery_test

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/testtime"
)

func TestRouteOf(t *testing.T) {
	t.Parallel()

	type route struct {
		path   string
		inside bool
		want   gallery.RouteKind
	}

	none := []string{
		"/api/gallery/", "/api/gallery/install/", "/api/gallery/install/x", "/api/gallery/other",
		"/api/gallery//install", "/api/gallery/Install", "/api/gallery/%69nstall",
	}

	tests := make([]route, 0, len(none)+5)
	tests = append(tests, []route{
		{path: "/api/health", inside: false, want: gallery.RouteNone},
		{path: "/api/galleryx", inside: false, want: gallery.RouteNone},
		{path: "/api/gallery", inside: true, want: gallery.RouteIndex},
		{path: "/api/gallery/install", inside: true, want: gallery.RouteInstall},
		{path: "/api/gallery/uninstall", inside: true, want: gallery.RouteUninstall},
	}...)

	for _, path := range none {
		tests = append(tests, route{path: path, inside: true, want: gallery.RouteNone})
	}

	for _, tt := range tests {
		got, inside := gallery.RouteOf(tt.path)
		if got != tt.want {
			t.Errorf("%s: %d, want %d", tt.path, got, tt.want)
		}

		if inside != tt.inside {
			t.Errorf("%s: inside = %v", tt.path, inside)
		}
	}
}

// fakeClock is the API's clock, in Unix ms, moved by the test.
type fakeClock struct{ at atomic.Int64 }

func (c *fakeClock) now() int64            { return c.at.Load() }
func (c *fakeClock) tick(by time.Duration) { c.at.Add(by.Milliseconds()) }

// counted makes the host's index answer count its reads.
func counted(env *world, next func(read int) answer) *atomic.Int64 {
	reads := &atomic.Int64{}
	env.host.index = func(request *http.Request) (*http.Response, error) {
		read := reads.Add(1)

		return next(int(read))(request)
	}

	return reads
}

func api(env *world, clock *fakeClock, repo string) *gallery.API {
	return gallery.NewAPI(env.gallery(), repo, clock.now)
}

func handle(t *testing.T, endpoint *gallery.API, route gallery.RouteKind, body *js.Object) string {
	t.Helper()

	result := endpoint.Handle(t.Context(), route, body)

	return js.Stringify(map[string]any{"code": result.Code, "body": result.Body})
}

func withID(themeID any) *js.Object {
	object := js.NewObject()
	object.Set("id", themeID)

	return object
}

func TestWithTheIndexOffNothingIsFetched(t *testing.T) {
	t.Parallel()

	env := newWorld(t, entry(t, "ember"))
	endpoint := api(env, &fakeClock{}, "")

	tests := []struct {
		route gallery.RouteKind
		want  string
	}{
		{
			route: gallery.RouteIndex,
			want:  `{"body":{"enabled":false,"repo":null,"themes":[]},"code":200}`,
		},
		{route: gallery.RouteInstall, want: `{"body":{"error":"not found"},"code":404}`},
		{route: gallery.RouteUninstall, want: `{"body":{"error":"not found"},"code":404}`},
		{route: gallery.RouteNone, want: `{"body":{"error":"not found"},"code":404}`},
	}

	for _, tt := range tests {
		got := handle(t, endpoint, tt.route, withID("ember"))
		if got != tt.want {
			t.Errorf("%d: %s, want %s", tt.route, got, tt.want)
		}
	}

	expectList(t, env.host.urls())
	expectList(t, listing(t, env.user))
}

func TestTheIndexAnswersItsEntriesTheDroppedAndWhenItWasRead(t *testing.T) {
	t.Parallel()

	env := newWorld(t, entry(t, "ember"), "junk", 7.0)
	got := handle(t, api(env, &fakeClock{}, "me/mine"), gallery.RouteIndex, nil)

	parsed, err := js.Parse([]byte(got))
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)
	answer, _ := object.Get("body")
	body, _ := answer.(*js.Object)

	keys := body.Keys()
	if js.Stringify(keys) != `["enabled","repo","checkedAt","themes","dropped"]` {
		t.Errorf("keys = %s", js.Stringify(keys))
	}

	dropped, _ := body.Get("dropped")
	if dropped != 2.0 {
		t.Errorf("dropped = %v", dropped)
	}

	listed, _ := body.Get("themes")
	themesList, _ := listed.([]any)

	if len(themesList) != 1 {
		t.Fatalf("themes = %d", len(themesList))
	}

	first, _ := themesList[0].(*js.Object)
	if js.Stringify(first.Keys()) !=
		`["id","name","author","version","description","commit","sha256","files","manifest"]` {
		t.Errorf("entry keys = %s", js.Stringify(first.Keys()))
	}
}

func TestAFailedReadAnswersItsMessageWithNoThemes(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	env.host.index = reply(429, "")

	got := handle(t, api(env, &fakeClock{}, "me/mine"), gallery.RouteIndex, nil)
	want := `{"body":{"enabled":true,"repo":"me/mine","themes":[],"error":"GitHub is limiting ` +
		`requests (429); try the theme index again in a few minutes"},"code":200}`

	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestInstallAndUninstallPassTheIDAsItCame(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text
	endpoint := api(env, &fakeClock{}, "me/mine")

	tests := []struct {
		route gallery.RouteKind
		body  *js.Object
		want  string
	}{
		{
			route: gallery.RouteInstall, body: withID("ember"),
			want: `{"body":{"ok":true,"id":"ember","version":"1.0.0"},"code":200}`,
		},
		{
			route: gallery.RouteUninstall, body: withID(7.0),
			want: `{"body":{"error":"That is not a theme id"},"code":400}`,
		},
		{
			route: gallery.RouteInstall, body: js.NewObject(),
			want: `{"body":{"error":"That is not a theme id"},"code":400}`,
		},
		{
			route: gallery.RouteUninstall, body: withID("ember"),
			want: `{"body":{"ok":true,"id":"ember","version":""},"code":200}`,
		},
	}

	for _, tt := range tests {
		got := handle(t, endpoint, tt.route, tt.body)
		if got != tt.want {
			t.Errorf("%s, want %s", got, tt.want)
		}
	}
}

func TestTheIndexIsReadOnceAMinute(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	reads := counted(env, func(int) answer { return reply(200, indexText(entry(t, "ember"))) })
	clock := &fakeClock{}
	clock.at.Store(1000)
	endpoint := api(env, clock, "me/mine")

	handle(t, endpoint, gallery.RouteIndex, nil)
	clock.tick(59_999 * time.Millisecond)

	again := handle(t, endpoint, gallery.RouteIndex, nil)
	if reads.Load() != 1 {
		t.Errorf("reads = %d inside the minute", reads.Load())
	}

	matches(t, again, `"id":"ember"`)

	clock.tick(time.Millisecond)
	handle(t, endpoint, gallery.RouteIndex, nil)

	if reads.Load() != 2 {
		t.Errorf("reads = %d after the minute", reads.Load())
	}
}

func TestAFailedReadIsKeptForFiveSecondsOnly(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	reads := counted(env, func(read int) answer {
		if read == 1 {
			return offline
		}

		return reply(200, indexText())
	})
	clock := &fakeClock{}
	endpoint := api(env, clock, "me/mine")

	matches(t, handle(t, endpoint, gallery.RouteIndex, nil), `Could not reach`)
	clock.tick(4_999 * time.Millisecond)
	matches(t, handle(t, endpoint, gallery.RouteIndex, nil), `Could not reach`)

	if reads.Load() != 1 {
		t.Errorf("reads = %d inside 5 s", reads.Load())
	}

	clock.tick(time.Millisecond)

	matches(t, handle(t, endpoint, gallery.RouteIndex, nil), `"checkedAt"`)

	if reads.Load() != 2 {
		t.Errorf("reads = %d", reads.Load())
	}
}

func TestTwoGETsAtOnceShareOneRead(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	release := make(chan struct{})
	reads := counted(env, func(int) answer {
		return func(request *http.Request) (*http.Response, error) {
			<-release

			return reply(200, indexText(entry(t, "ember")))(request)
		}
	})
	endpoint := api(env, &fakeClock{}, "me/mine")

	answers := make(chan string, 2)

	var started sync.WaitGroup

	for range 2 {
		started.Go(func() { answers <- handle(t, endpoint, gallery.RouteIndex, nil) })
	}

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for endpoint.Reads() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the two GETs never met on one read")
		}

		time.Sleep(time.Millisecond)
	}

	close(release)
	started.Wait()

	first, second := <-answers, <-answers
	if first != second {
		t.Errorf("answers differ:\n%s\n%s", first, second)
	}

	if reads.Load() != 1 {
		t.Errorf("reads = %d", reads.Load())
	}
}

func TestInstallNeverUsesTheKeptIndex(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text
	endpoint := api(env, &fakeClock{}, "me/mine")

	handle(t, endpoint, gallery.RouteIndex, nil)
	handle(t, endpoint, gallery.RouteInstall, withID("ember"))

	indexReads := 0

	for _, url := range env.host.urls() {
		if url == indexAt {
			indexReads++
		}
	}

	if indexReads != 2 {
		t.Errorf("index reads = %d, want one for the GET and one for the install", indexReads)
	}
}
