package themes

import (
	"net/url"
	"strings"
)

const routePrefix = "/api/themes"

// RouteKind is what a path under /api/themes asks for.
type RouteKind int

// The routes: the list, one theme's stylesheet, or nothing (a 404).
const (
	RouteNone RouteKind = iota
	RouteList
	RouteCSS
)

// Route is a path under /api/themes. ID is decoded, so CSS judges what was meant.
type Route struct {
	Kind RouteKind
	ID   string
}

// RouteOf reads an escaped path: /api/themes (or with a trailing slash) is the list, and
// /api/themes/<id>/theme.css a stylesheet. It is false for a path outside /api/themes.
func RouteOf(escaped string) (Route, bool) {
	rest, found := strings.CutPrefix(escaped, routePrefix)
	if !found {
		return Route{}, false
	}

	if rest == "" {
		return Route{Kind: RouteList, ID: ""}, true
	}

	rest, found = strings.CutPrefix(rest, "/")
	if !found {
		// /api/themesx is another path.
		return Route{}, false
	}

	if rest == "" {
		return Route{Kind: RouteList, ID: ""}, true
	}

	// <id>/theme.css
	const cssSegments = 2

	segments := strings.Split(rest, "/")
	if len(segments) != cssSegments {
		return Route{Kind: RouteNone, ID: ""}, true
	}

	if segments[1] != StyleFile {
		return Route{Kind: RouteNone, ID: ""}, true
	}

	if segments[0] == "" {
		return Route{Kind: RouteNone, ID: ""}, true
	}

	id, err := url.PathUnescape(segments[0])
	if err != nil {
		return Route{Kind: RouteNone, ID: ""}, true
	}

	return Route{Kind: RouteCSS, ID: id}, true
}
