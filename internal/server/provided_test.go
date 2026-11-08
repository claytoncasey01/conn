package server_test

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/claytoncasey01/conn/internal/provided"
	"github.com/claytoncasey01/conn/internal/server"
)

// providedRig is a server over one provided skill and one agent, with the data and Claude folders
// in a temp folder.
func providedRig(t *testing.T) (*rig, provided.Layout) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	layout := provided.Layout{
		Data: filepath.Join(base, "data"), Claude: filepath.Join(base, "claude"),
	}

	items, err := provided.ReadCatalog(
		fstest.MapFS{"search.md": {Data: []byte("agent")}},
		fstest.MapFS{"deep-review/SKILL.md": {Data: []byte("skill")}},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provided.Start(layout, items, false)
	if err != nil {
		t.Fatal(err)
	}

	return &rig{
		handler: &server.Server{
			UI: bundle(), Secret: testKey(t), Logger: log.New(io.Discard, "", 0),
			Provided: provided.New(provided.Deps{Layout: layout, Items: items, Writable: true}),
		},
		data: base,
	}, layout
}

func TestProvidedListIsOpenButAnInstallNeedsOwnPage(t *testing.T) {
	t.Parallel()

	rig, layout := providedRig(t)
	link := filepath.Join(layout.Claude, "skills", "deep-review")
	install := request{
		method: "POST", target: "/api/provided/install", body: `{"name":"deep-review"}`,
	}

	t.Run("the list", func(t *testing.T) {
		t.Parallel()

		got := rig.get(t, "/api/provided")
		if got.status != 200 {
			t.Fatalf("status = %d: %s", got.status, got.body)
		}

		if !strings.Contains(got.body, `"name":"deep-review","state":"notInstalled"`) {
			t.Errorf("body = %s", got.body)
		}
	})

	t.Run("an install without Conn's header", func(t *testing.T) {
		t.Parallel()

		refused := install
		refused.headers = map[string]string{"Origin": ownOrigin}

		got := rig.send(t, refused)
		if got.status != 403 {
			t.Errorf("status = %d, want 403", got.status)
		}
	})

	t.Run("a wrong method", func(t *testing.T) {
		t.Parallel()

		got := rig.get(t, "/api/provided/install")
		if got.status != 405 {
			t.Errorf("status = %d, want 405", got.status)
		}
	})

	t.Run("a path that names nothing", func(t *testing.T) {
		t.Parallel()

		notFoundJSON(t, rig.get(t, "/api/provided/other"))
	})

	_, err := os.Lstat(link)
	if !os.IsNotExist(err) {
		t.Fatalf("a refused request linked the skill (err %v)", err)
	}
}

func TestProvidedInstallThenRemove(t *testing.T) {
	t.Parallel()

	rig, layout := providedRig(t)
	link := filepath.Join(layout.Claude, "skills", "deep-review")

	got := rig.send(t, request{
		method: "POST", target: "/api/provided/install", body: `{"name":"deep-review"}`,
	})
	if got.status != 200 {
		t.Fatalf("install = %d: %s", got.status, got.body)
	}

	_, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("not linked: %v", err)
	}

	got = rig.send(t, request{
		method: "POST", target: "/api/provided/remove", body: `{"name":"deep-review"}`,
	})
	if got.status != 200 {
		t.Fatalf("remove = %d: %s", got.status, got.body)
	}

	_, err = os.Lstat(link)
	if !os.IsNotExist(err) {
		t.Errorf("still linked after remove (err %v)", err)
	}
}

func TestProvidedRoutesAreLeftOutWithoutAnAPI(t *testing.T) {
	t.Parallel()

	handler := &server.Server{UI: bundle(), Secret: testKey(t), Logger: log.New(io.Discard, "", 0)}
	rig := &rig{handler: handler, data: t.TempDir()}

	got := rig.get(t, "/api/provided")
	if strings.Contains(got.body, "agents") {
		t.Errorf("body = %s", got.body)
	}
}
