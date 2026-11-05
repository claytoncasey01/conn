package update

import (
	"context"
	"crypto/tls"
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
	// GitHub answers a HEAD in well under a second, so a longer wait is a stuck server.
	responseHeaderTimeout = 3 * time.Second

	// dialFallbackDelay is how long a dial waits for the first address family before it tries
	// the other one at the same time. It is net's default, stated as a decision made here.
	dialFallbackDelay = 300 * time.Millisecond

	// idleConnectionTimeout is how long an idle connection would be kept. None is kept, because
	// keep-alives are off, so any positive value does; zero would mean no limit.
	idleConnectionTimeout = time.Second

	// expectContinueTimeout is zero because the request has no body to wait to send.
	expectContinueTimeout = 0

	// connectionsMax is the most connections open at once, in total and per host: the check
	// makes one request at a time. For idle connections it is the smallest limit the transport
	// takes, as zero would mean no limit.
	connectionsMax = 1

	// headerBytesMax bounds all the response headers together. GitHub's security headers take a
	// few kilobytes, so this leaves room and still stops a flood.
	headerBytesMax = 64 << 10

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
	// ErrRequest means the request could not be made or answered. It stands in for the error
	// from net/http, which can hold text the server sent; a context error stays reachable.
	ErrRequest = errors.New("the request failed")
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
			Timeout:       dialTimeout,
			FallbackDelay: dialFallbackDelay,
			KeepAlive:     -1,
		}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		ExpectContinueTimeout:  expectContinueTimeout,
		IdleConnTimeout:        idleConnectionTimeout,
		MaxIdleConns:           connectionsMax,
		MaxIdleConnsPerHost:    connectionsMax,
		MaxConnsPerHost:        connectionsMax,
		MaxResponseHeaderBytes: headerBytesMax,
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
	baseURL, err := parseBase(base)
	if err != nil {
		return "", err
	}

	requestURL := baseURL.JoinPath("latest")

	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("building the request: %w", ErrBadBase)
	}

	request.Header.Set("User-Agent", clientName)

	response, err := client.Do(request)
	if err != nil {
		return "", requestError(err)
	}

	defer func() { _ = response.Body.Close() }()

	if slices.Contains(redirectStatuses(), response.StatusCode) {
		locations := response.Header.Values("Location")
		if len(locations) != 1 {
			return "", fmt.Errorf("%d location headers: %w", len(locations), ErrBadLocation)
		}

		basePath := strings.TrimRight(baseURL.Path, "/")

		return tagFrom(locations[0], requestURL, basePath)
	}

	return "", statusError(response.StatusCode)
}

// parseBase reads the releases address. It must be an http or https address with a host and carry
// no user, query or fragment, so nothing but the path can reach the request, and no credential
// can be sent.
func parseBase(base string) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("the releases address is not an address: %w", ErrBadBase)
	}

	if baseURL.User != nil {
		return nil, fmt.Errorf("the releases address has a user: %w", ErrBadBase)
	}

	if baseURL.RawQuery != "" {
		return nil, fmt.Errorf("the releases address has a query: %w", ErrBadBase)
	}

	if baseURL.ForceQuery {
		return nil, fmt.Errorf("the releases address has a query: %w", ErrBadBase)
	}

	if baseURL.Fragment != "" {
		return nil, fmt.Errorf("the releases address has a fragment: %w", ErrBadBase)
	}

	if !slices.Contains(schemes(), baseURL.Scheme) {
		return nil, fmt.Errorf("the releases address is not http or https: %w", ErrBadBase)
	}

	if baseURL.Host == "" {
		return nil, fmt.Errorf("the releases address has no host: %w", ErrBadBase)
	}

	return baseURL, nil
}

// schemes are the address schemes the check speaks.
func schemes() []string { return []string{"http", "https"} }

// requestError replaces an error from net/http by ErrRequest, because it can hold text the
// server sent. Only a context error is kept, so a caller can still tell a deadline or a
// cancellation from any other failure.
func requestError(err error) error {
	const prefix = "asking for the latest release"

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w: %w", prefix, ErrRequest, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w: %w", prefix, ErrRequest, context.Canceled)
	default:
		return fmt.Errorf("%s: %w", prefix, ErrRequest)
	}
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
