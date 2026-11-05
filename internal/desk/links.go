package desk

import (
	"net/url"
	"strings"
)

// Link is where a link the page follows goes.
type Link int

// The three places a link can go.
const (
	// LinkInApp is a page of the server the window shows: it loads in the window.
	LinkInApp Link = iota
	// LinkBrowser is any other web page (a pull request, a Slack thread): the default browser
	// opens it, because the window shows one Conn and nothing else.
	LinkBrowser
	// LinkRefused is anything that is not an http or https address: it is not opened at all,
	// because handing the system an arbitrary scheme would let a page start any program.
	LinkRefused
)

// LinkTarget is where a link to rawURL goes from a window that shows the server at serverURL
// (empty while the splash shows). Only http and https open; an address on the server's own
// origin (scheme, host and port, compared without regard to case) stays in the window, and any
// other opens in the browser.
func LinkTarget(rawURL, serverURL string) Link {
	link, err := url.Parse(rawURL)
	if err != nil {
		return LinkRefused
	}

	if !isWeb(link) {
		return LinkRefused
	}

	server, err := url.Parse(serverURL)
	if err != nil {
		return LinkBrowser
	}

	if origin(link) == origin(server) {
		return LinkInApp
	}

	return LinkBrowser
}

// isWeb says whether address is an http or https address with a host.
func isWeb(address *url.URL) bool {
	if address.Host == "" {
		return false
	}

	switch strings.ToLower(address.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

// origin is the scheme, host and port of address, in lower case.
func origin(address *url.URL) string {
	return strings.ToLower(address.Scheme + "://" + address.Host)
}
