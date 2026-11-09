package update_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
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
	// second, when set, is a second Location header.
	second string
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

		if given.second != "" {
			w.Header().Add("Location", given.second)
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

// checkContext is a context that ends after the check's time limit, stretched for slow machines.
func checkContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(update.CheckTimeout))
	t.Cleanup(cancel)

	return ctx
}

// found is the answer GitHub gives: a 302 to location.
func found(location string) answer {
	return answer{status: http.StatusFound, location: location}
}

// twice is a 302 with two Location headers.
func twice(first, second string) answer {
	return answer{status: http.StatusFound, location: first, second: second}
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
		{"two locations", twice(tag+"v1.2.3", tag+"v1.2.4"), "", bad},
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

			ctx := checkContext(t)

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

	ctx := checkContext(t)

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

	// A slow machine stretches the wait, but never past half the client's own header timeout, or
	// that timeout would end the request before the context does.
	wait := min(testtime.Scaled(100*time.Millisecond), update.ResponseHeaderTimeout/2)

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	_, err := update.Latest(ctx, update.NewClient(), server.URL+releasesPath)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the context's deadline", err)
	}

	if !errors.Is(err, update.ErrRequest) {
		t.Errorf("error = %v, want ErrRequest", err)
	}
}

func TestLatestEndsAtACancelledContext(t *testing.T) {
	t.Parallel()

	server, _ := serve(t, found(releasesPath+"/tag/v1.2.3"))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := update.Latest(ctx, update.NewClient(), server.URL+releasesPath)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want the context's cancellation", err)
	}

	if !errors.Is(err, update.ErrRequest) {
		t.Errorf("error = %v, want ErrRequest", err)
	}
}

func TestLatestMakesOneRequestWhenTheAnswerRedirectsAgain(t *testing.T) {
	t.Parallel()

	server, requests := serve(t, found(releasesPath+"/latest"))
	t.Cleanup(server.Close)

	ctx := checkContext(t)

	_, err := update.Latest(ctx, update.NewClient(), server.URL+releasesPath)
	if !errors.Is(err, update.ErrBadLocation) {
		t.Errorf("error = %v, want ErrBadLocation", err)
	}

	if got := len(requests()); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// serveRaw starts a TCP server that answers every connection with the given bytes, whatever the
// request, and returns its address. It stops with the test.
func serveRaw(t *testing.T, response string) string {
	t.Helper()

	var config net.ListenConfig

	listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			answerRaw(conn, response)
		}
	}()

	return listener.Addr().String()
}

// answerRaw reads one request's headers, writes the response and closes the connection.
func answerRaw(conn net.Conn, response string) {
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(testtime.Scaled(update.CheckTimeout)))

	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		if line == "\r\n" {
			break
		}
	}

	_, _ = conn.Write([]byte(response))
}

func TestLatestHidesWhatTheServerSends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
	}{
		{"malformed status line", "HOSTILE-TEXT\r\n\r\n"},
		{"malformed status code", "HTTP/1.1 HOSTILE-TEXT OK\r\n\r\n"},
		{"bad content length", "HTTP/1.1 302 Found\r\nContent-Length: HOSTILE-TEXT\r\n\r\n"},
		{"bad header", "HTTP/1.1 302 Found\r\nHOSTILE-TEXT\r\n\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			address := serveRaw(t, tt.response)

			ctx := checkContext(t)

			_, err := update.Latest(ctx, update.NewClient(), "http://"+address+releasesPath)
			if !errors.Is(err, update.ErrRequest) {
				t.Fatalf("error = %v, want ErrRequest", err)
			}

			if strings.Contains(err.Error(), "HOSTILE-TEXT") {
				t.Errorf("error %q repeats what the server sent", err)
			}
		})
	}
}

func TestLatestRejectsAnUnusableBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
	}{
		{"not an address", "://bad"},
		{"user and password", "http://user:secret@127.0.0.1:1" + releasesPath},
		{"user only", "http://user@127.0.0.1:1" + releasesPath},
		{"query", "http://127.0.0.1:1" + releasesPath + "?a=1"},
		{"empty query", "http://127.0.0.1:1" + releasesPath + "?"},
		{"fragment", "http://127.0.0.1:1" + releasesPath + "#top"},
		{"no scheme", "example.com" + releasesPath},
		{"empty", ""},
		{"path only", releasesPath},
		{"scheme other than http", "ftp://example.com" + releasesPath},
		{"file scheme", "file:///tmp" + releasesPath},
		{"no host", "https://" + releasesPath},
		{"no host with http", "http:///x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := update.Latest(context.Background(), update.NewClient(), tt.base)
			if !errors.Is(err, update.ErrBadBase) {
				t.Errorf("error = %v, want ErrBadBase", err)
			}
		})
	}
}

// TestNewClientKeepsItsLimits pins the client's TLS floor and every named limit of its transport,
// so a change to one is a decision shown in review.
func TestNewClientKeepsItsLimits(t *testing.T) {
	t.Parallel()

	client := update.NewClient()

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is a %T, want *http.Transport", client.Transport)
	}

	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig = nil, want a minimum version")
	}

	const kilobyte = 1 << 10

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"client timeout", client.Timeout, 5 * time.Second},
		{"cookie jar", client.Jar, http.CookieJar(nil)},
		{"TLS minimum version", transport.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12)},
		{"TLS handshake timeout", transport.TLSHandshakeTimeout, 3 * time.Second},
		{"response header timeout", transport.ResponseHeaderTimeout, 3 * time.Second},
		{"expect continue timeout", transport.ExpectContinueTimeout, time.Duration(0)},
		{"idle connection timeout", transport.IdleConnTimeout, time.Second},
		{"idle connections", transport.MaxIdleConns, 1},
		{"idle connections per host", transport.MaxIdleConnsPerHost, 1},
		{"connections per host", transport.MaxConnsPerHost, 1},
		{"response header bytes", transport.MaxResponseHeaderBytes, int64(64 * kilobyte)},
		{"keep-alives disabled", transport.DisableKeepAlives, true},
		{"HTTP/2 attempted", transport.ForceAttemptHTTP2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
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
		{"encoded slash", releasesPath + "/tag/v1.2.3%2Fextra", "", bad},
		{"encoded dot dots", releasesPath + "/tag/%2e%2e/v1.2.3", "", bad},
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

func TestAppAttached(t *testing.T) {
	t.Parallel()

	storage := "https://objects.example.com/release-assets/1?signature=abc"
	tooLong := "https://objects.example.com/" + strings.Repeat("a", 4096)
	tests := []struct {
		name    string
		given   answer
		want    bool
		wantErr error
	}{
		{"redirect to the storage host", found(storage), true, nil},
		{"redirect with an over-long location", found(tooLong), true, nil},
		{"redirect with no location", found(""), true, nil},
		{"redirect with two locations", twice(storage, storage), true, nil},
		{"moved permanently", redirect(http.StatusMovedPermanently, storage), true, nil},
		{"temporary redirect", redirect(http.StatusTemporaryRedirect, storage), true, nil},
		{"served directly", redirect(http.StatusOK, ""), true, nil},
		{"not attached yet", redirect(http.StatusNotFound, ""), false, nil},
		{"server error", redirect(http.StatusInternalServerError, ""), false, update.ErrAppStatus},
		{"forbidden", redirect(http.StatusForbidden, ""), false, update.ErrAppStatus},
		{"not modified", redirect(http.StatusNotModified, ""), false, update.ErrAppStatus},
		{"no content", redirect(http.StatusNoContent, ""), false, update.ErrAppStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, requests := serve(t, tt.given)
			defer server.Close()

			ctx := checkContext(t)

			got, err := update.AppAttached(
				ctx, update.NewClient(), server.URL+releasesPath, "v1.2.3",
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("attached = %v, want %v", got, tt.want)
			}

			if count := len(requests()); count != 1 {
				t.Errorf("requests = %d, want 1: a redirect is never followed", count)
			}
		})
	}
}

func TestAppAttachedSendsNothingOfTheUser(t *testing.T) {
	t.Parallel()

	server, requests := serve(t, found(releasesPath+"/download/v1.2.3/checksums-app.txt"))
	t.Cleanup(server.Close)

	ctx := checkContext(t)

	_, err := update.AppAttached(ctx, update.NewClient(), server.URL+releasesPath, "v1.2.3")
	if err != nil {
		t.Fatalf("AppAttached: %v", err)
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
		{"path", request.path, releasesPath + "/download/v1.2.3/checksums-app.txt"},
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

func TestAppAttachedRefusesATagThatIsNotAVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tag  string
	}{
		{"empty", ""},
		{"path", "v1.2.3/../../evil"},
		{"query", "v1.2.3?x=1"},
		{"not a version", "latest"},
		{"too long", "v1.2.3-" + strings.Repeat("a", update.MaxTagBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, requests := serve(t, found(""))
			defer server.Close()

			ctx := checkContext(t)

			_, err := update.AppAttached(ctx, update.NewClient(), server.URL+releasesPath, tt.tag)
			if err == nil {
				t.Error("error = nil, want one")
			}

			if count := len(requests()); count != 0 {
				t.Errorf("requests = %d, want 0", count)
			}
		})
	}
}

func TestAppAttachedRejectsAnUnusableBase(t *testing.T) {
	t.Parallel()

	_, err := update.AppAttached(
		context.Background(), update.NewClient(), "ftp://example.com"+releasesPath, "v1.2.3",
	)
	if !errors.Is(err, update.ErrBadBase) {
		t.Errorf("error = %v, want ErrBadBase", err)
	}
}

func TestAppAttachedEndsAtTheContextDeadline(t *testing.T) {
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

	// As for Latest, the wait stays under half the client's own header timeout.
	wait := min(testtime.Scaled(100*time.Millisecond), update.ResponseHeaderTimeout/2)

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	_, err := update.AppAttached(ctx, update.NewClient(), server.URL+releasesPath, "v1.2.3")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the context's deadline", err)
	}

	if !errors.Is(err, update.ErrRequest) {
		t.Errorf("error = %v, want ErrRequest", err)
	}
}

func TestAppAttachedHidesWhatTheServerSends(t *testing.T) {
	t.Parallel()

	address := serveRaw(t, "HTTP/1.1 HOSTILE-TEXT OK\r\n\r\n")

	ctx := checkContext(t)

	_, err := update.AppAttached(
		ctx, update.NewClient(), "http://"+address+releasesPath, "v1.2.3",
	)
	if !errors.Is(err, update.ErrRequest) {
		t.Fatalf("error = %v, want ErrRequest", err)
	}

	if strings.Contains(err.Error(), "HOSTILE-TEXT") {
		t.Errorf("error %q repeats what the server sent", err)
	}
}
