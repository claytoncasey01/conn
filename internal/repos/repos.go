// Package repos is the repository allowlist's grammar and the one function every reader asks.
// Pure: no files, no settings, no clock. Names are checked against the ASCII grammar BEFORE they
// are lowercased, so a lookalike (the Kelvin sign lowercases to a plain k) can never become an
// allowed name by case folding.
package repos

import (
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// The reasons a pattern is refused, shown under the field it was typed in.
const (
	reasonNotText   = "a pattern is text"
	reasonEmpty     = "a pattern cannot be empty"
	reasonEveryRepo = "that would allow every repository: name an owner, as owner/*"
	reasonStarPlace = "* is allowed only as the whole repository part, as owner/*"
	reasonOwner     = "the owner starts with a letter or digit and may only hold letters, " +
		"digits, dots, dashes and underscores"
	reasonNoSlash   = "write it as owner/repo or owner/*"
	reasonSlashes   = "write it as owner/repo or owner/*, with one slash"
	reasonNameChars = "owner and repository may only hold letters, digits, dots, dashes and " +
		"underscores, the owner starts with a letter or digit, the repository does not start " +
		"with a dash, and neither may be only dots"
)

// ownerWildcard ends a pattern that allows every repository of an owner.
const ownerWildcard = "/*"

var (
	nameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	onlyDots  = regexp.MustCompile(`^\.+$`)
	owner     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	stars     = regexp.MustCompile(`^\*+$`)
	ownerStar = regexp.MustCompile(`^([^/]+)/\*$`)
	remote    = regexp.MustCompile(`(?i)^` +
		`(?:https://(?:[^@/` + js.SpaceClass + `]+@)?github\.com/` +
		`|git@github\.com:` +
		`|ssh://git@github\.com/)` +
		`([^/` + js.SpaceClass + `]+/[^/` + js.SpaceClass + `]+?)` +
		`(?:\.git)?/?$`)
)

// IsName reports whether name is `owner/repo` of GitHub name characters. The owner starts with a
// letter or digit and the repo with a letter, digit, dot or underscore (.github is a real repo
// name), so a name is never a dash-led word a process would read as an option; neither segment
// may be only dots (., ..). Safe in a search string, a path and an argument.
func IsName(name string) bool {
	if !nameShape.MatchString(name) {
		return false
	}

	_, repo, _ := strings.Cut(name, "/")

	return !onlyDots.MatchString(repo)
}

// Parsed is one allowlist entry as typed: the pattern when OK, else the reason it is refused.
type Parsed struct {
	OK      bool   `json:"ok"`
	Pattern string `json:"pattern,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func refuse(reason string) Parsed { return Parsed{OK: false, Pattern: "", Reason: reason} }

func accept(pattern string) Parsed { return Parsed{OK: true, Pattern: pattern, Reason: ""} }

// ParsePattern reads one allowlist entry: `owner/repo` or `owner/*`, trimmed and lowercased, else
// the reason it is refused. raw may be any decoded JSON value; only a string can pass.
func ParsePattern(raw any) Parsed {
	typed, isString := raw.(string)
	if !isString {
		return refuse(reasonNotText)
	}

	text := js.Trim(typed)
	if text == "" {
		return refuse(reasonEmpty)
	}

	if strings.Contains(text, "*") {
		return parseWildcard(text)
	}

	if !strings.Contains(text, "/") {
		return refuse(reasonNoSlash)
	}

	if strings.Count(text, "/") != 1 {
		return refuse(reasonSlashes)
	}

	if !IsName(text) {
		return refuse(reasonNameChars)
	}

	return accept(strings.ToLower(text))
}

// parseWildcard reads a pattern holding a *, which only `owner/*` may.
func parseWildcard(text string) Parsed {
	if stars.MatchString(text) {
		return refuse(reasonEveryRepo)
	}

	if text == "*/*" {
		return refuse(reasonEveryRepo)
	}

	match := ownerStar.FindStringSubmatch(text)
	if match == nil {
		return refuse(reasonStarPlace)
	}

	if !owner.MatchString(match[1]) {
		return refuse(reasonOwner)
	}

	return accept(strings.ToLower(match[1]) + ownerWildcard)
}

// Allowed reports whether repo is covered by the list. No list (setup) and an empty list allow
// nothing; a malformed repo is never allowed, even by a wildcard.
func Allowed(repo string, allow []string) bool {
	if len(allow) == 0 {
		return false
	}

	if !IsName(repo) {
		return false
	}

	name := strings.ToLower(repo)
	repoOwner, _, _ := strings.Cut(name, "/")

	for _, entry := range allow {
		if covers(strings.ToLower(entry), name, repoOwner) {
			return true
		}
	}

	return false
}

// covers reports whether one lowercase pattern allows the lowercase name, whose owner is given.
func covers(pattern, name, nameOwner string) bool {
	if pattern == name {
		return true
	}

	patternOwner, isWildcard := strings.CutSuffix(pattern, ownerWildcard)
	if !isWildcard {
		return false
	}

	return patternOwner == nameOwner
}

// FromRemote is the lowercase `owner/repo` of a GitHub https, ssh or git@ remote URL; "" for
// another host, a path or anything else.
func FromRemote(url string) string {
	match := remote.FindStringSubmatch(js.Trim(url))
	if match == nil {
		return ""
	}

	if !IsName(match[1]) {
		return ""
	}

	return strings.ToLower(match[1])
}
