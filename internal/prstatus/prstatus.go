// Package prstatus reads a pull request's state from `gh pr view --json`: open, draft, merged or
// closed, its review decision and mergeability, and its checks reduced to one word.
package prstatus

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// Fields are the fields asked of `gh pr view --json`.
const Fields = "state,isDraft,reviewDecision,mergeStateStatus,statusCheckRollup"

const (
	nameUnits   = 40
	failedKept  = 6
	namedKept   = 4
	safeInteger = 1<<53 - 1
)

var (
	errNotPR   = errors.New("not a PR number")
	errNotRepo = errors.New("not a repo name")
)

// Checks is a PR's checks: none, pending, passing or failing, with the failures named.
type Checks struct {
	State   string   `json:"state"`
	Total   int      `json:"total"`
	Pending int      `json:"pending"`
	Failed  []string `json:"failed"`
}

// Status is what the dashboard shows of a PR.
type Status struct {
	State     string  `json:"state"`
	Checks    Checks  `json:"checks"`
	Review    string  `json:"review"`
	Mergeable string  `json:"mergeable"`
	At        float64 `json:"at"`
}

// CleanName cuts a check name down to plain words: it goes into a message typed at a session.
func CleanName(name string) string {
	kept := strings.Map(func(char rune) rune {
		if plain(char) {
			return char
		}

		return -1
	}, name)

	return js.Cut(js.Trim(kept), nameUnits)
}

// plain is [A-Za-z0-9 ._\-/()+:#].
func plain(char rune) bool {
	if char >= 'a' {
		return char <= 'z'
	}

	if char >= 'A' {
		if char <= 'Z' {
			return true
		}
	}

	if char >= '0' {
		if char <= '9' {
			return true
		}
	}

	return strings.ContainsRune(" ._-/()+:#", char)
}

func text(check *js.Object, key string) string {
	value, present := check.Get(key)
	if !present {
		return ""
	}

	if value == nil {
		return ""
	}

	return js.String(value)
}

func truthy(check *js.Object, key string) bool {
	value, _ := check.Get(key)

	return js.Truthy(value)
}

// checkName is c.name ?? c.context ?? ”.
func checkName(check *js.Object) string {
	for _, key := range []string{"name", "context"} {
		value, present := check.Get(key)
		if present {
			if value != nil {
				return js.String(value)
			}
		}
	}

	return ""
}

// SummarizeChecks reduces a check rollup to passing, failing, pending or none, keeping the names
// of the failures. A rollup entry that is not an object is skipped.
func SummarizeChecks(rollup any) Checks {
	items, _ := rollup.([]any)
	failed := []string{}
	pending := 0

	for _, item := range items {
		check, isObject := item.(*js.Object)
		if !isObject {
			check = js.NewObject()
		}

		name := CleanName(checkName(check))

		switch verdict(check) {
		case verdictFailedStatus:
			failed = append(failed, orDefault(name, "status check"))
		case verdictFailedRun:
			failed = append(failed, orDefault(name, "check"))
		case verdictPending:
			pending++
		}
	}

	unique := []string{}

	for _, name := range failed {
		if !slices.Contains(unique, name) {
			unique = append(unique, name)
		}
	}

	return Checks{
		State: checksState(len(items), len(failed), pending), Total: len(items),
		Pending: pending, Failed: unique[:min(len(unique), failedKept)],
	}
}

func checksState(total, failed, pending int) string {
	switch {
	case total == 0:
		return "none"
	case failed > 0:
		return "failing"
	case pending > 0:
		return "pending"
	}

	return "passing"
}

const (
	verdictNone = iota
	verdictPending
	verdictFailedStatus
	verdictFailedRun
)

// verdict is what one check says: a status context (failed on FAILURE or ERROR, pending on
// PENDING or EXPECTED) or a check run (pending until COMPLETED, then failed on a failing
// conclusion).
func verdict(check *js.Object) int {
	state := text(check, "state")

	if statusContext(check) {
		switch state {
		case "FAILURE", "ERROR":
			return verdictFailedStatus
		case "PENDING", "EXPECTED":
			return verdictPending
		}

		return verdictNone
	}

	if truthy(check, "status") {
		if text(check, "status") != "COMPLETED" {
			return verdictPending
		}
	}

	switch text(check, "conclusion") {
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return verdictFailedRun
	}

	return verdictNone
}

// statusContext is c.__typename === 'StatusContext' || (c.state && !c.status).
func statusContext(check *js.Object) bool {
	kind, _ := check.Get("__typename")
	if kind == "StatusContext" {
		return true
	}

	if !truthy(check, "state") {
		return false
	}

	return !truthy(check, "status")
}

func orDefault(name, fallback string) string {
	if name == "" {
		return fallback
	}

	return name
}

// From reads `gh pr view --json ...` output, polled at polledAt (ms).
func From(view *js.Object, polledAt float64) Status {
	state := "open"
	raw, _ := view.Get("state")

	switch {
	case raw == "MERGED":
		state = "merged"
	case raw == "CLOSED":
		state = "closed"
	case truthy(view, "isDraft"):
		state = "draft"
	}

	rollup, _ := view.Get("statusCheckRollup")
	review, _ := view.Get("reviewDecision")
	mergeable, _ := view.Get("mergeStateStatus")
	reviewText, _ := review.(string)
	mergeableText, _ := mergeable.(string)

	return Status{
		State: state, Checks: SummarizeChecks(rollup), Review: reviewText,
		Mergeable: mergeableText, At: polledAt,
	}
}

// FixChecksMessage is the text typed at a session to fix red checks. It names the checks and says
// where to read them, in repo (named to gh, never left to its default, which in a fork can be the
// upstream); log text from CI is never pasted in.
func FixChecksMessage(number float64, failed []string, repo string) (string, error) {
	if number < 1 {
		return "", errNotPR
	}

	if number > safeInteger {
		return "", errNotPR
	}

	if math.Trunc(number) != number {
		return "", errNotPR
	}

	if !repos.IsName(repo) {
		return "", errNotRepo
	}

	names := []string{}

	for _, name := range failed {
		if clean := CleanName(name); clean != "" {
			names = append(names, clean)
		}
	}

	listed := ""
	if len(names) > 0 {
		listed = " (" + strings.Join(names[:min(len(names), namedKept)], ", ") + ")"
	}

	pr := strconv.FormatFloat(number, 'f', -1, 64)

	return "CI failed on PR #" + pr + listed + ". Read what failed with `gh pr checks " + pr +
		" -R " + repo + "` and `gh run view --log-failed -R " + repo +
		"`, fix the cause, commit and push to this branch.", nil
}
