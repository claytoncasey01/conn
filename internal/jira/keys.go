// Package jira is what Conn knows of Jira: which ticket keys are the configured projects', and
// the two helper runs that reach it through the claude.ai Atlassian connector, the sprint read
// and a ticket's status move.
//
// The project keys are settings (atlassian.jira.projects), so the pattern for a ticket key is
// built from them each time it is asked for and never typed. Everything here reads the settings
// it is given at call time: a change in Settings reaches the next call with no restart. A ticket
// key from Jira or from a record on disk is data: it is checked against this pattern before it
// reaches a path, a branch name, a query or a prompt.
package jira

import (
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/settings"
)

// anyKey is a Jira key as a whole, of any project: the shape that is safe to accept on the way
// in (a route, a run record read from disk). Whether it is one of the configured projects is
// IsKey.
var anyKey = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}-[0-9]{1,9}$`)

// AnyKey is whether key has the shape of a Jira key of any project.
func AnyKey(key string) bool { return anyKey.MatchString(key) }

// Projects are the configured project keys.
func Projects(current settings.Settings) []string { return current.Atlassian.Jira.Projects }

// On is whether Open work and everything that depends on Jira is on: the Atlassian module is
// enabled (never while setup) and at least one project is listed.
func On(current settings.Settings) bool {
	if len(current.Repos.Allow) == 0 {
		return false
	}

	if !current.Atlassian.Enabled {
		return false
	}

	return len(Projects(current)) > 0
}

// PatternSource is `^(?:KEY|KEY2)-\d{1,9}$` for the configured projects, "" while Open work is
// off. The keys passed the settings grammar, so they hold no regex syntax.
func PatternSource(current settings.Settings) string {
	if !On(current) {
		return ""
	}

	return `^(?:` + strings.Join(Projects(current), "|") + `)-\d{1,9}$`
}

// Pattern is the ticket key pattern for the configured projects, nil while Open work is off.
func Pattern(current settings.Settings) *regexp.Regexp {
	source := PatternSource(current)
	if source == "" {
		return nil
	}

	return regexp.MustCompile(source)
}

// IsKey is whether key is a ticket in one of the configured projects. False while Open work is
// off.
func IsKey(current settings.Settings, key string) bool {
	pattern := Pattern(current)
	if pattern == nil {
		return false
	}

	return pattern.MatchString(key)
}

// KeyOf is the key of the Jira ticket a session is named after, "" for anything else: while
// Open work is off no session is a ticket session.
func KeyOf(current settings.Settings, name string) string {
	if IsKey(current, name) {
		return name
	}

	return ""
}
