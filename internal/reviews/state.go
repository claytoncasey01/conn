// Package reviews is the Reviews tab: the pull requests the chosen review sources list (GitHub
// asking for my review, a post in a chosen Slack channel, a Slack DM, or every open PR of an
// allowed repo), the state each is in, the REVIEW press that opens a review session in a
// worktree of its own, and the closing of those sessions once they are done. What Conn itself
// remembers is kept in reviews.json; everything else is read again from GitHub and Slack.
package reviews

import (
	"slices"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// The states a row is in, in the order the list shows them.
const (
	StateRereview  = "rereview"
	StateNew       = "new"
	StateReviewing = "reviewing"
	StateWaiting   = "waiting"
	StateDone      = "done"
)

// The review states GitHub reports.
const (
	reviewApproved = "APPROVED"
	reviewPending  = "PENDING"
)

// How a PR stopped being open.
const (
	EndedMerged = "merged"
	EndedClosed = "closed"
	endedGone   = "gone"
)

// How the author asked again.
const (
	askedGitHub = "github"
	askedSlack  = "slack"
)

// endedKeepMs is how long a merged or closed PR stays in the list.
const endedKeepMs = 24 * 3_600_000

// Review is one review on a PR; the caller passes only the latest per author.
type Review struct {
	Author string `json:"author"`
	// State is APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED or PENDING.
	State string `json:"state"`
	// Commit is the commit the review was made on.
	Commit      string  `json:"commit"`
	SubmittedAt float64 `json:"submittedAt"`
}

// PR is what GitHub says about one pull request.
type PR struct {
	// Repo is `owner/repo` as GitHub reports it (its casing, for display; keys use it
	// lowercased).
	Repo        string  `json:"repo"`
	Number      int64   `json:"number"`
	Title       string  `json:"title"`
	Author      string  `json:"author"`
	AuthorIsBot bool    `json:"authorIsBot"`
	URL         string  `json:"url"`
	BaseRef     string  `json:"baseRef"`
	HeadSha     string  `json:"headSha"`
	IsDraft     bool    `json:"isDraft"`
	CreatedAt   float64 `json:"createdAt"`
	UpdatedAt   float64 `json:"updatedAt"`
	// Checks is passing, failing, pending or none.
	Checks string `json:"checks"`
	// Requested are the logins of the users (not teams) whose review is requested.
	Requested []string `json:"requested"`
	Reviews   []Review `json:"reviews"`
	// Ended is merged or closed; nil while it is open.
	Ended   *string  `json:"ended"`
	EndedAt *float64 `json:"endedAt"`
}

// Sources are the review sources that are on; a PR is listed only when one of them lists it.
type Sources struct {
	// Requested is "Requests me".
	Requested bool
	// Channels are the chosen channels while Slack is on; nil when the channel source is off
	// (Slack off, or no channel).
	Channels []string
	// DMs is whether the DM scan's finds count (Slack on and the DM switch on).
	DMs bool
	// AllOpen is "every open non-draft PR".
	AllOpen bool
}

// RowSession is the live review session of a row.
type RowSession struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// DeriveContext is what a row is derived against besides the PR and its record.
type DeriveContext struct {
	// Me is the reviewer's GitHub login.
	Me string
	// Sources are the sources that are on: nothing is listed because of one that is off.
	Sources Sources
	// Session is the live review session for this PR, if any.
	Session *RowSession
	Now     float64
}

// Row is one PR as the Reviews tab lists it.
type Row struct {
	// Key is `owner/repo#number`, lowercase.
	Key  string `json:"key"`
	Repo string `json:"repo"`
	// NoClone says why REVIEW cannot start here: there is no local clone of the repo.
	NoClone *string `json:"noClone"`
	Number  int64   `json:"number"`
	Title   string  `json:"title"`
	Author  string  `json:"author"`
	URL     string  `json:"url"`
	BaseRef string  `json:"baseRef"`
	State   string  `json:"state"`
	// ReAsked is how the author asked again (state rereview): github or slack.
	ReAsked *string `json:"reAsked"`
	// Updated is that the head moved past the commit reviewed.
	Updated  bool    `json:"updated"`
	SlackURL *string `json:"slackUrl"`
	DMURL    *string `json:"dmUrl"`
	// InChannel is false when only "every open PR" lists it while the channel scan is on.
	InChannel bool        `json:"inChannel"`
	Checks    string      `json:"checks"`
	CreatedAt float64     `json:"createdAt"`
	UpdatedAt float64     `json:"updatedAt"`
	Ended     *string     `json:"ended"`
	Session   *RowSession `json:"session"`
	Launches  int         `json:"launches"`
	// Stale is that a pattern covering this repo could not be searched at the last poll.
	Stale bool `json:"stale,omitempty"`
}

// same compares two names (logins, repos) whatever their case, as Bun compared their
// toLowerCase: strings.EqualFold also folds letters lowercasing leaves apart (ſ and s).
func same(first, second string) bool {
	lower := strings.ToLower(first)

	return lower == strings.ToLower(second)
}

// keyOf is the key of a PR whose repo and number came from GitHub.
func keyOf(repo string, number int64) string {
	return strings.ToLower(repo) + "#" + strconv.FormatInt(number, 10)
}

// mine is my latest review that is not an unsent draft, or nil.
func mine(pr *PR, me string) *Review {
	for index := range pr.Reviews {
		review := &pr.Reviews[index]
		if !same(review.Author, me) {
			continue
		}

		if review.State == reviewPending {
			continue
		}

		return review
	}

	return nil
}

func requestsMe(pr *PR, me string) bool {
	return slices.ContainsFunc(pr.Requested, func(login string) bool { return same(login, me) })
}

// listed is what lists a PR, source by source: GitHub asking me (now, or before my review
// dropped the request), a post in a chosen channel, a DM.
type listed struct {
	byRequest bool
	post      *SlackPost
	dm        *DMPost
}

func listedBy(pull *PR, tracking *Entry, ctx *DeriveContext) listed {
	src := ctx.Sources
	out := listed{byRequest: false, post: nil, dm: nil}

	if src.Requested {
		if tracking.Requested {
			out.byRequest = true
		} else {
			out.byRequest = requestsMe(pull, ctx.Me)
		}
	}

	if src.Channels != nil {
		if tracking.Slack != nil {
			channel := tracking.Slack.Channel
			if channel == nil {
				out.post = tracking.Slack
			} else if slices.Contains(src.Channels, *channel) {
				out.post = tracking.Slack
			}
		}
	}

	if src.DMs {
		out.dm = tracking.DM
	}

	return out
}

func (l listed) any(allOpen bool) bool {
	if l.byRequest {
		return true
	}

	if l.post != nil {
		return true
	}

	if l.dm != nil {
		return true
	}

	return allOpen
}

// shownEnded is whether a PR that is not open is still listed: tracked, and ended within the day.
// One that is open is listed unless it is a draft.
func shownEnded(pull *PR, tracking *Entry, latest *Review, now float64) bool {
	if pull.Ended == nil {
		return !pull.IsDraft
	}

	tracked := tracking.Slack != nil
	if tracking.DM != nil {
		tracked = true
	}

	if len(tracking.Launches) > 0 {
		tracked = true
	}

	if latest != nil {
		tracked = true
	}

	if !tracked {
		return false
	}

	if pull.EndedAt == nil {
		return false
	}

	return now-*pull.EndedAt < endedKeepMs
}

// headMoved is whether an open PR's head is past the commit my latest review was on.
func headMoved(pull *PR, latest *Review) bool {
	if pull.Ended != nil {
		return false
	}

	if latest == nil {
		return false
	}

	return latest.Commit != pull.HeadSha
}

// DeriveRow is whether a PR is listed, and in which state; nil when it is not listed. Pure: the
// clock comes in as ctx.Now.
func DeriveRow(pull *PR, tracking *Entry, ctx *DeriveContext, key string) *Row {
	if same(pull.Author, ctx.Me) {
		return nil
	}

	// A pending review is an unsent draft, not a review.
	latest := mine(pull, ctx.Me)
	listing := listedBy(pull, tracking, ctx)

	if !listing.any(ctx.Sources.AllOpen) {
		return nil
	}

	if !shownEnded(pull, tracking, latest, ctx.Now) {
		return nil
	}

	updated := headMoved(pull, latest)
	state, reAsked := stateOf(pull, tracking, ctx, latest, listing)

	row := &Row{
		Key: key, Repo: pull.Repo, NoClone: nil, Number: pull.Number, Title: pull.Title,
		Author: pull.Author, URL: pull.URL, BaseRef: pull.BaseRef, State: state, ReAsked: reAsked,
		Updated: updated, SlackURL: nil, DMURL: nil, InChannel: false, Checks: pull.Checks,
		CreatedAt: pull.CreatedAt, UpdatedAt: pull.UpdatedAt, Ended: pull.Ended,
		Session: ctx.Session, Launches: len(tracking.Launches), Stale: false,
	}

	if listing.post != nil {
		row.SlackURL = &listing.post.Permalink
	}

	if listing.dm != nil {
		row.DMURL = &listing.dm.Permalink
	}

	// Listed by "every open PR" alone while the channel source is on: tagged, since nobody asked
	// for it in a channel. Silent with Slack off.
	row.InChannel = ctx.Sources.Channels == nil
	if listing.byRequest {
		row.InChannel = true
	}

	if listing.post != nil {
		row.InChannel = true
	}

	if listing.dm != nil {
		row.InChannel = true
	}

	return row
}

// stateOf is a listed PR's state and how it was asked again.
func stateOf(
	pull *PR, tracking *Entry, ctx *DeriveContext, latest *Review, listing listed,
) (string, *string) {
	if pull.Ended != nil {
		return StateDone, nil
	}

	if ctx.Session != nil {
		return StateReviewing, nil
	}

	if latest == nil {
		return StateNew, nil
	}

	// GitHub drops a reviewer's request once they review, so a request naming me beside my own
	// latest review is a new one: even after an approval.
	if requestsMe(pull, ctx.Me) {
		asked := askedGitHub

		return StateRereview, &asked
	}

	if latest.State != reviewApproved {
		// A later DM asks like a later post in the thread does (each only while its source is
		// on).
		if askedSince(tracking, listing, latest.SubmittedAt) {
			asked := askedSlack

			return StateRereview, &asked
		}
	}

	if latest.State == reviewApproved {
		return StateDone, nil
	}

	return StateWaiting, nil
}

func askedSince(tracking *Entry, listing listed, since float64) bool {
	if listing.post != nil {
		if tracking.SlackAskAt != nil {
			if *tracking.SlackAskAt > since {
				return true
			}
		}
	}

	if listing.dm != nil {
		if tracking.DMLastAt != nil {
			if *tracking.DMLastAt > since {
				return true
			}
		}
	}

	return false
}

// stateOrder is the order states are listed in.
func stateOrder(state string) float64 {
	return float64(slices.Index(
		[]string{StateRereview, StateNew, StateReviewing, StateWaiting, StateDone}, state))
}

// SortRows is re-reviews first, then new, reviewing, waiting, done; the most recently updated
// first within a state. Stable, as Bun's sort is.
func SortRows(rows []*Row) []*Row {
	sorted := slices.Clone(rows)

	js.Sort(sorted, func(first, second *Row) float64 {
		byState := stateOrder(first.State) - stateOrder(second.State)
		if byState != 0 {
			return byState
		}

		return second.UpdatedAt - first.UpdatedAt
	})

	return sorted
}
