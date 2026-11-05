package update

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	// checkTimeout bounds one whole check, from connecting to the last header.
	checkTimeout = 5 * time.Second

	// locationBytesMax is the longest Location header read. A real one is about 60 bytes.
	locationBytesMax = 512

	// tagBytesMax is the longest release tag accepted, so a tag is short enough to show.
	tagBytesMax = 64

	// dialTimeout bounds connecting to the server.
	dialTimeout = 3 * time.Second

	// tlsHandshakeTimeout bounds the TLS handshake.
	tlsHandshakeTimeout = 3 * time.Second

	// responseHeaderTimeout bounds the wait for the response headers after the request is sent.
	responseHeaderTimeout = checkTimeout

	// headerBytesMax bounds all the response headers together. GitHub's security headers take a
	// few kilobytes, so this leaves room and still stops a flood.
	headerBytesMax = 64 << 10

	// idleConnectionsMax is how many idle connections the client keeps: none, because the
	// connection is closed after each check.
	idleConnectionsMax = 0

	// clientName is the only thing the request says about the sender.
	clientName = "conn-app"
)

// Sentinel errors for what was wrong with the answer. None of them carries text from the answer,
// which came from the network.
var (
	// ErrBadBase means the releases address given is not a usable address.
	ErrBadBase = errors.New("the releases address is not usable")
	// ErrNoRedirect means the server answered without pointing to a release.
	ErrNoRedirect = errors.New("the answer did not point to a release")
	// ErrStatus means the server answered with a status that is not a redirect.
	ErrStatus = errors.New("unexpected status")
	// ErrBadLocation means the redirect did not point to a release tag on the same server.
	ErrBadLocation = errors.New("bad redirect location")
)

// redirectStatuses are the statuses that can point to the latest release.
func redirectStatuses() []int {
	return []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}
}

// NewClient returns the HTTP client for the check. It uses the proxy settings of the environment,
// because the person may be behind a corporate proxy, follows no redirect, and keeps no
// connection, cookie or credential.
func NewClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: -1,
		}).DialContext,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		MaxResponseHeaderBytes: headerBytesMax,
		MaxIdleConns:           idleConnectionsMax,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      true,
	}

	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:     nil,
		Timeout: checkTimeout,
	}
}

// Latest asks base (the releases address) where /latest points and returns the release tag it
// names. The answer is hostile input: only a redirect to a tag on the same server is believed,
// and the body is never read.
func Latest(ctx context.Context, client *http.Client, base string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("reading the releases address: %w", ErrBadBase)
	}

	basePath := strings.TrimRight(baseURL.Path, "/")
	requestURL := baseURL.JoinPath("latest")

	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("building the request: %w", err)
	}

	request.Header.Set("User-Agent", clientName)

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("asking for the latest release: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	if slices.Contains(redirectStatuses(), response.StatusCode) {
		return tagFrom(response.Header.Get("Location"), requestURL, basePath)
	}

	return "", statusError(response.StatusCode)
}

// statusError says why a status that is not a redirect cannot name a release.
func statusError(status int) error {
	if status < http.StatusOK {
		return fmt.Errorf("status %d: %w", status, ErrStatus)
	}

	if status < http.StatusMultipleChoices {
		return fmt.Errorf("status %d: %w", status, ErrNoRedirect)
	}

	return fmt.Errorf("status %d: %w", status, ErrStatus)
}

// tagFrom reads the release tag out of a Location header. The location is resolved against the
// request address, must keep its scheme and host, and must be exactly basePath plus /tag/ plus a
// tag of the version shape. Anything else is an error that says what was wrong and never repeats
// the header.
func tagFrom(location string, request *url.URL, basePath string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("no location: %w", ErrBadLocation)
	}

	if len(location) > locationBytesMax {
		return "", fmt.Errorf("location too long: %w", ErrBadLocation)
	}

	target, err := request.Parse(location)
	if err != nil {
		return "", fmt.Errorf("location is not an address: %w", ErrBadLocation)
	}

	if target.Scheme != request.Scheme {
		return "", fmt.Errorf("location has another scheme: %w", ErrBadLocation)
	}

	if target.Host != request.Host {
		return "", fmt.Errorf("location has another host: %w", ErrBadLocation)
	}

	if target.User != nil {
		return "", fmt.Errorf("location has a user: %w", ErrBadLocation)
	}

	if target.RawQuery != "" {
		return "", fmt.Errorf("location has a query: %w", ErrBadLocation)
	}

	if target.Fragment != "" {
		return "", fmt.Errorf("location has a fragment: %w", ErrBadLocation)
	}

	if target.RawPath != "" {
		return "", fmt.Errorf("location has an encoded path: %w", ErrBadLocation)
	}

	return tagIn(target.Path, basePath)
}

// tagIn takes the tag off the end of a path that must be basePath plus /tag/ plus the tag.
func tagIn(path, basePath string) (string, error) {
	tag, found := strings.CutPrefix(path, basePath+"/tag/")
	if !found {
		return "", fmt.Errorf("location is not a tag of this repository: %w", ErrBadLocation)
	}

	if len(tag) > tagBytesMax {
		return "", fmt.Errorf("tag too long: %w", ErrBadLocation)
	}

	if !versionPattern.MatchString(tag) {
		return "", fmt.Errorf("tag is not a version: %w", ErrBadLocation)
	}

	return tag, nil
}
