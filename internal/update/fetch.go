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
	// checkTimeout bounds one request, from connecting to the last header. A check makes one
	// request, or two when it finds a newer release.
	checkTimeout = 5 * time.Second

	// maxLocationBytes is the longest Location header read. A real one is about 60 bytes.
	maxLocationBytes = 512

	// maxTagBytes is the longest release tag accepted, so a tag is short enough to show.
	maxTagBytes = 64

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

	// maxConnections is the most connections open at once, in total and per host: the check
	// makes one request at a time. For idle connections it is the smallest limit the transport
	// takes, as zero would mean no limit.
	maxConnections = 1

	// maxHeaderBytes bounds all the response headers together. GitHub's security headers take a
	// few kilobytes, so this leaves room and still stops a flood.
	maxHeaderBytes = 64 << 10

	// clientName is the only thing the request says about the sender.
	clientName = "conn-app"

	// appChecksumsName is the release file whose presence says the app is attached. The release
	// workflow uploads it with the app's zip, and install.sh asks for it to tell whether a
	// release has an app.
	appChecksumsName = "checksums-app.txt"
)

// Sentinel errors for what was wrong with the answer. None of them carries text from the answer,
// which came from the network.
var (
	// errBadBase means the releases address given is not a usable address.
	errBadBase = errors.New("the releases address is not usable")
	// errNoRedirect means the server answered without pointing to a release.
	errNoRedirect = errors.New("the answer did not point to a release")
	// errStatus means the server answered with a status that is not a redirect.
	errStatus = errors.New("unexpected status")
	// errRequest means the request could not be made or answered. It stands in for the error
	// from net/http, which can hold text the server sent; a context error stays reachable.
	errRequest = errors.New("the request failed")
	// errBadLocation means the redirect did not point to a release tag on the same server.
	errBadLocation = errors.New("bad redirect location")
	// errAppStatus means the answer about the release's app file was neither there nor missing.
	errAppStatus = errors.New("unexpected status for the release's app file")
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
		MaxIdleConns:           maxConnections,
		MaxIdleConnsPerHost:    maxConnections,
		MaxConnsPerHost:        maxConnections,
		MaxResponseHeaderBytes: maxHeaderBytes,
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

	response, err := head(ctx, client, requestURL, "asking for the latest release")
	if err != nil {
		return "", err
	}

	defer func() { _ = response.Body.Close() }()

	if slices.Contains(redirectStatuses(), response.StatusCode) {
		locations := response.Header.Values("Location")
		if len(locations) != 1 {
			return "", fmt.Errorf("%d location headers: %w", len(locations), errBadLocation)
		}

		basePath := strings.TrimRight(baseURL.Path, "/")

		return tagFrom(locations[0], requestURL, basePath)
	}

	return "", statusError(response.StatusCode)
}

// AppAttached asks base (the releases address) whether the app is attached to the release tag,
// by asking for the release's checksums-app.txt. GitHub names a release as the latest as soon as
// it is published, and the app is attached up to half an hour later, or never when its build
// fails. Only the status is read: no header, no body, and no redirect is followed. The address is
// built from base and a tag of the version shape alone.
func AppAttached(ctx context.Context, client *http.Client, base, tag string) (bool, error) {
	baseURL, err := parseBase(base)
	if err != nil {
		return false, err
	}

	if len(tag) > maxTagBytes {
		return false, fmt.Errorf("asking whether the release has its app: %w", errTagTooLong)
	}

	if !versionPattern.MatchString(tag) {
		return false, fmt.Errorf("asking whether the release has its app: %w", errNotVersion)
	}

	requestURL := baseURL.JoinPath("download", tag, appChecksumsName)

	response, err := head(ctx, client, requestURL, "asking whether the release has its app")
	if err != nil {
		return false, err
	}

	defer func() { _ = response.Body.Close() }()

	return attachedFrom(response.StatusCode)
}

// attachedFrom reads the status of the request for checksums-app.txt. GitHub redirects a file
// that is attached to its storage host; a server that serves the file itself, such as one for
// trying a build, answers 200. Both mean attached. A 404 means not attached yet, and any other
// status cannot tell.
func attachedFrom(status int) (bool, error) {
	if status == http.StatusOK {
		return true, nil
	}

	if slices.Contains(redirectStatuses(), status) {
		return true, nil
	}

	if status == http.StatusNotFound {
		return false, nil
	}

	return false, fmt.Errorf("status %d: %w", status, errAppStatus)
}

// head makes one HEAD request to target, saying only the client name, and returns the answer for
// the caller to read and close. doing says what the request is for, in the error.
func head(
	ctx context.Context,
	client *http.Client,
	target *url.URL,
	doing string,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", errBadBase)
	}

	request.Header.Set("User-Agent", clientName)

	response, err := client.Do(request)
	if err != nil {
		return nil, requestError(doing, err)
	}

	return response, nil
}

// parseBase reads the releases address. It must be an http or https address with a host and carry
// no user, query or fragment, so nothing but the path can reach the request, and no credential
// can be sent.
func parseBase(base string) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("the releases address is not an address: %w", errBadBase)
	}

	if baseURL.User != nil {
		return nil, fmt.Errorf("the releases address has a user: %w", errBadBase)
	}

	if baseURL.RawQuery != "" {
		return nil, fmt.Errorf("the releases address has a query: %w", errBadBase)
	}

	if baseURL.ForceQuery {
		return nil, fmt.Errorf("the releases address has a query: %w", errBadBase)
	}

	if baseURL.Fragment != "" {
		return nil, fmt.Errorf("the releases address has a fragment: %w", errBadBase)
	}

	if !slices.Contains(schemes(), baseURL.Scheme) {
		return nil, fmt.Errorf("the releases address is not http or https: %w", errBadBase)
	}

	if baseURL.Host == "" {
		return nil, fmt.Errorf("the releases address has no host: %w", errBadBase)
	}

	return baseURL, nil
}

// schemes are the address schemes the check speaks.
func schemes() []string { return []string{"http", "https"} }

// requestError replaces an error from net/http by errRequest, because it can hold text the
// server sent, and says what the request was for. Only a context error is kept, so a caller can
// still tell a deadline or a cancellation from any other failure.
func requestError(doing string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w: %w", doing, errRequest, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w: %w", doing, errRequest, context.Canceled)
	default:
		return fmt.Errorf("%s: %w", doing, errRequest)
	}
}

// statusError says why a status that is not a redirect cannot name a release.
func statusError(status int) error {
	if status < http.StatusOK {
		return fmt.Errorf("status %d: %w", status, errStatus)
	}

	if status < http.StatusMultipleChoices {
		return fmt.Errorf("status %d: %w", status, errNoRedirect)
	}

	return fmt.Errorf("status %d: %w", status, errStatus)
}

// tagFrom reads the release tag out of a Location header. The location is resolved against the
// request address, must keep its scheme and host, and must be exactly basePath plus /tag/ plus a
// tag of the version shape. Anything else is an error that says what was wrong and never repeats
// the header.
func tagFrom(location string, request *url.URL, basePath string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("no location: %w", errBadLocation)
	}

	if len(location) > maxLocationBytes {
		return "", fmt.Errorf("location too long: %w", errBadLocation)
	}

	target, err := request.Parse(location)
	if err != nil {
		return "", fmt.Errorf("location is not an address: %w", errBadLocation)
	}

	if target.Scheme != request.Scheme {
		return "", fmt.Errorf("location has another scheme: %w", errBadLocation)
	}

	if target.Host != request.Host {
		return "", fmt.Errorf("location has another host: %w", errBadLocation)
	}

	if target.User != nil {
		return "", fmt.Errorf("location has a user: %w", errBadLocation)
	}

	if target.RawQuery != "" {
		return "", fmt.Errorf("location has a query: %w", errBadLocation)
	}

	if target.Fragment != "" {
		return "", fmt.Errorf("location has a fragment: %w", errBadLocation)
	}

	if target.RawPath != "" {
		return "", fmt.Errorf("location has an encoded path: %w", errBadLocation)
	}

	return tagIn(target.Path, basePath)
}

// tagIn takes the tag off the end of a path that must be basePath plus /tag/ plus the tag.
func tagIn(path, basePath string) (string, error) {
	tag, found := strings.CutPrefix(path, basePath+"/tag/")
	if !found {
		return "", fmt.Errorf("location is not a tag of this repository: %w", errBadLocation)
	}

	if len(tag) > maxTagBytes {
		return "", fmt.Errorf("tag too long: %w", errBadLocation)
	}

	if !versionPattern.MatchString(tag) {
		return "", fmt.Errorf("tag is not a version: %w", errBadLocation)
	}

	return tag, nil
}
