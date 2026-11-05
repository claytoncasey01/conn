package update_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/testtime"
	"github.com/claytoncasey01/conn/internal/update"
)

const releasesPath = "/claytoncasey01/conn/releases"

// answer is what a fake GitHub says to the latest-release request.
type answer struct {
	status   int
	location string
}

// seenRequest is what the fake server recorded of one request.
type seenRequest struct {
	method string
	path   string
	header http.Header
}

// serve starts a fake release server that gives the same answer to every request and records
// the requests it saw. The caller closes it.
func serve(t *testing.T, given answer) (*httptest.Server, func() []seenRequest) {
	t.Helper()

	var mutex sync.Mutex

	var seen []seenRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		this := seenRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()}

		mutex.Lock()

		seen = append(seen, this)

		mutex.Unlock()

		if given.location != "" {
			location := strings.ReplaceAll(given.location, "{host}", r.Host)
			w.Header().Set("Location", location)
		}

		w.WriteHeader(given.status)
	}))

	requests := func() []seenRequest {
		mutex.Lock()
		defer mutex.Unlock()

		return append([]seenRequest(nil), seen...)
	}

	return server, requests
}

// found is the answer GitHub gives: a 302 to location.
func found(location string) answer {
	return answer{status: http.StatusFound, location: location}
}

// redirect is an answer with another status.
func redirect(status int, location string) answer {
	return answer{status: status, location: location}
}

func TestLatest(t *testing.T) {
	t.Parallel()

	tag := releasesPath + "/tag/"
	bad := update.ErrBadLocation
	tests := []struct {
		name    string
		given   answer
		want    string
		wantErr error
	}{
		{"absolute location", found("http://{host}" + tag + "v1.2.3"), "v1.2.3", nil},
		{"relative location", found(tag + "v1.2.3"), "v1.2.3", nil},
		{"moved permanently", redirect(301, tag+"v2.0.0"), "v2.0.0", nil},
		{"see other", redirect(303, tag+"v2.0.0"), "v2.0.0", nil},
		{"temporary redirect", redirect(307, tag+"v2.0.0"), "v2.0.0", nil},
		{"permanent redirect", redirect(308, tag+"v2.0.0"), "v2.0.0", nil},
		{"pre-release tag", found(tag + "v1.2.3-rc1"), "v1.2.3-rc1", nil},
		{"another host", found("https://example.com" + tag + "v1.2.3"), "", bad},
		{"another scheme", found("https://{host}" + tag + "v1.2.3"), "", bad},
		{"not a version", found(tag + "not-a-version"), "", bad},
		{"extra path", found(tag + "v1.2.3/extra"), "", bad},
		{"a query", found(tag + "v1.2.3?x=1"), "", bad},
		{"a fragment", found(tag + "v1.2.3#x"), "", bad},
		{"encoded letter", found(tag + "v1.2.3-%72c"), "", bad},
		{"the latest page again", found(releasesPath + "/latest"), "", bad},
		{"another repository", found("/other/repo/releases/tag/v1.2.3"), "", bad},
		{"too long", found(tag + "v1.2.3-" + strings.Repeat("a", 600)), "", bad},
		{"no location", found(""), "", bad},
		{"not a redirect", redirect(http.StatusOK, ""), "", update.ErrNoRedirect},
		{"not found", redirect(http.StatusNotFound, ""), "", update.ErrStatus},
		{"server error", redirect(http.StatusInternalServerError, ""), "", update.ErrStatus},
		{"not modified", redirect(http.StatusNotModified, ""), "", update.ErrStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, _ := serve(t, tt.given)
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(5*time.Second))
			defer cancel()

			got, err := update.Latest(ctx, update.NewClient(), server.URL+releasesPath)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("tag = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLatestSendsNothingOfTheUser(t *testing.T) {
	t.Parallel()

	server, requests := serve(t, found(releasesPath+"/tag/v1.2.3"))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(5*time.Second))
	t.Cleanup(cancel)

	_, err := update.Latest(ctx, update.NewClient(), server.URL+releasesPath)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}

	seen := requests()
	if len(seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(seen))
	}

	request := seen[0]

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"method", request.method, http.MethodHead},
		{"path", request.path, releasesPath + "/latest"},
		{"user agent", request.header.Get("User-Agent"), "conn-app"},
		{"cookie", request.header.Get("Cookie"), ""},
		{"authorization", request.header.Get("Authorization"), ""},
		{"referer", request.header.Get("Referer"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestLatestEndsAtTheContextDeadline(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))

	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(100*time.Millisecond))
	defer cancel()

	_, err := update.Latest(ctx, update.NewClient(), server.URL+releasesPath)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the context's deadline", err)
	}
}

func TestLatestRejectsABaseThatIsNotAnAddress(t *testing.T) {
	t.Parallel()

	_, err := update.Latest(context.Background(), update.NewClient(), "://bad")
	if !errors.Is(err, update.ErrBadBase) {
		t.Errorf("error = %v, want ErrBadBase", err)
	}
}

func TestTagFrom(t *testing.T) {
	t.Parallel()

	request := &url.URL{Scheme: "https", Host: "github.com", Path: releasesPath + "/latest"}
	tag := releasesPath + "/tag/"
	bad := update.ErrBadLocation
	tests := []struct {
		name     string
		location string
		want     string
		wantErr  error
	}{
		{"absolute", "https://github.com" + tag + "v1.2.3", "v1.2.3", nil},
		{"relative", tag + "v1.2.3", "v1.2.3", nil},
		{"build metadata", tag + "v1.2.3+7", "v1.2.3+7", nil},
		{"host differs", "https://github.com.evil.example" + tag + "v1.2.3", "", bad},
		{"scheme differs", "http://github.com" + tag + "v1.2.3", "", bad},
		{"user info", "https://a@github.com" + tag + "v1.2.3", "", bad},
		{"port differs", "https://github.com:444" + tag + "v1.2.3", "", bad},
		{"prefix only", releasesPath + "/tag/", "", bad},
		{"no slash before tag", releasesPath + "/tagv1.2.3", "", bad},
		{"uppercase v", tag + "V1.2.3", "", bad},
		{"newline", tag + "v1.2.3%0A", "", bad},
		{"dot segments", releasesPath + "/x/../tag/v1.2.3", "v1.2.3", nil},
		{"tag over 64 bytes", tag + "v1.2.3-" + strings.Repeat("a", 60), "", bad},
		{"location over 512 bytes", tag + "v1.2.3?" + strings.Repeat("a", 512), "", bad},
		{"not a url", "http://[::1", "", bad},
		{"empty", "", "", bad},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := update.TagFrom(tt.location, request, releasesPath)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("tag = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorsNeverEchoTheLocation(t *testing.T) {
	t.Parallel()

	request := &url.URL{Scheme: "https", Host: "github.com", Path: releasesPath + "/latest"}
	hostile := "https://evil.example/HOSTILE-TEXT"

	_, err := update.TagFrom(hostile, request, releasesPath)
	if err == nil {
		t.Fatal("error = nil, want one")
	}

	if strings.Contains(err.Error(), "HOSTILE-TEXT") {
		t.Errorf("error %q echoes the header", err)
	}
}
