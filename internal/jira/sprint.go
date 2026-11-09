package jira

import (
	"context"
	"math"
	"slices"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// SprintDeps are what a sprint read reads and calls.
type SprintDeps struct {
	Settings settings.Settings
	// DataDir holds jira-site.json.
	DataDir string
	Run     Run
	// Save keeps the site the read reported; nil saves it into DataDir.
	Save func(cloudID, user any) error
	// OnProgress and OnAuth are the run's, passed on.
	OnProgress func(line string)
	OnAuth     func(needing []string)
}

// Sprint is the sprint read's answer: the tickets as the list shows them, and whether the sprint
// held more.
type Sprint struct {
	Tickets   []*js.Object
	Truncated bool
}

// categories are Jira's three status categories, the stable part of every workflow; anything
// else is unknown.
func categories() []string { return []string{"new", categoryInProgress, "done"} }

// FetchSprint reads the open sprint's work for the configured projects that is not done and is
// the connected user's or nobody's. No key pattern, no call: the module is off, in setup, or
// lists no project.
func FetchSprint(ctx context.Context, deps SprintDeps) (Sprint, error) {
	pattern := Pattern(deps.Settings)
	if pattern == nil {
		return Sprint{}, errOff
	}

	projects := Projects(deps.Settings)
	site := ReadSite(deps.DataDir)

	answer, err := deps.Run(ctx, claude.Opts{
		Prompt: sprintPrompt(site, projects), Model: modelHaiku, Name: "sprint",
		AllowedTools: IssueTools(), DisallowedTools: nil, Dir: "", Schema: ticketSchema,
		BudgetUSD: sprintBudgetUSD, Timeout: sprintTimeout, Lenient: false, NoMCP: false,
		OnDenied: nil, OnProgress: deps.OnProgress, OnAuth: deps.OnAuth,
	})
	if err != nil {
		return Sprint{}, err
	}

	save := deps.Save
	if save == nil {
		save = func(cloudID, user any) error { return SaveSite(deps.DataDir, cloudID, user) }
	}

	return sprintFrom(answer.Structured, sprintRules{
		keyMatches: pattern.MatchString, firstProject: projects[0], save: save,
	})
}

type sprintRules struct {
	keyMatches   func(key string) bool
	firstProject string
	save         func(cloudID, user any) error
}

// rawItems are the tickets of the answer as JavaScript iterates them: an array's items, a
// string's characters (none of which is a ticket), or nothing.
func rawItems(answer any) []any {
	object, isObject := answer.(*js.Object)
	if !isObject {
		return nil
	}

	tickets, _ := object.Get("tickets")

	switch value := tickets.(type) {
	case []any:
		return value
	case string:
		items := []any{}
		for range js.Len(value) {
			items = append(items, nil)
		}

		return items
	}

	return nil
}

func sprintFrom(answer any, rules sprintRules) (Sprint, error) {
	items := rawItems(answer)
	if len(items) == 0 {
		return Sprint{}, errNoTickets
	}

	object, _ := answer.(*js.Object)
	cloudID, _ := object.Get("cloudId")
	user, _ := object.Get("me")

	err := rules.save(cloudID, user)
	if err != nil {
		return Sprint{}, err
	}

	account := accountOf(user)
	tickets := []*js.Object{}

	for _, item := range items {
		raw, isObject := item.(*js.Object)
		if !isObject {
			continue
		}

		key, isText := field(raw, "key").(string)
		if !isText {
			continue
		}

		// A ticket in another project is ignored, and the key is checked before it can reach a
		// path, a branch name or a prompt.
		if !rules.keyMatches(key) {
			continue
		}

		ticket, err := ticketOf(raw, key, account)
		if err != nil {
			return Sprint{}, err
		}

		tickets = append(tickets, ticket)
	}

	if len(tickets) == 0 {
		return Sprint{}, noneLookedError{project: rules.firstProject}
	}

	return Sprint{Tickets: tickets, Truncated: truncated(field(object, "totalInSprint"),
		len(tickets))}, nil
}

type noneLookedError struct{ project string }

func (e noneLookedError) Error() string {
	return "Jira returned tickets but none looked like " + e.project + "-123"
}

func accountOf(me any) any {
	object, isObject := me.(*js.Object)
	if !isObject {
		return nil
	}

	return field(object, "accountId")
}

func field(object *js.Object, key string) any {
	value, _ := object.Get(key)

	return value
}

// setPresent sets key unless the value is absent: JSON.stringify leaves an undefined out.
func setPresent(object *js.Object, key string, from *js.Object) {
	value, present := from.Get(key)
	if present {
		object.Set(key, value)
	}
}

// or is JavaScript's `value || fallback`.
func or(value, fallback any) any {
	if js.Truthy(value) {
		return value
	}

	return fallback
}

func ticketOf(raw *js.Object, key string, account any) (*js.Object, error) {
	ticket := js.NewObject()
	ticket.Set("key", key)
	setPresent(ticket, "title", raw)
	ticket.Set("type", or(field(raw, "type"), "Task"))
	setPresent(ticket, "status", raw)

	category := ""

	if text, isText := field(raw, "statusCategory").(string); isText {
		if slices.Contains(categories(), text) {
			category = text
		}
	}

	ticket.Set("statusCategory", category)
	ticket.Set("priority", or(field(raw, "priority"), ""))
	ticket.Set("assignee", or(field(raw, "assignee"), ""))
	ticket.Set("mine", mine(field(raw, "assigneeAccountId"), account))

	desc, err := descOf(field(raw, "desc"))
	if err != nil {
		return nil, err
	}

	ticket.Set("desc", desc)

	links := field(raw, "links")
	if links == nil {
		links = 0.0
	}

	ticket.Set("links", links)
	ticket.Set("comments", nil)

	return ticket, nil
}

func mine(assignee, account any) bool {
	if !js.Truthy(assignee) {
		return false
	}

	return strictEqual(assignee, account)
}

// descOf is `(t.desc ?? ”).slice(0, 500)`: a string's first 500 units, an array's first 500
// items, and a TypeError for anything else that has no slice.
func descOf(desc any) (any, error) {
	switch value := desc.(type) {
	case nil:
		return "", nil
	case string:
		return js.Cut(value, maxDescUnits), nil
	case []any:
		return value[:min(len(value), maxDescUnits)], nil
	}

	return nil, textError(`(t.desc ?? "").slice is not a function. (In '(t.desc ?? "")` +
		`.slice(0, 500)', '(t.desc ?? "").slice' is undefined)`)
}

// truncated is `(totalInSprint ?? kept) > kept`.
func truncated(total any, kept int) bool {
	if total == nil {
		return false
	}

	return numberOf(total) > float64(kept)
}

// strictEqual is JavaScript's === for parsed JSON values: an object or array equals only itself.
func strictEqual(left, right any) bool {
	switch value := left.(type) {
	case string, float64, bool:
		return left == right
	case nil:
		return right == nil
	case *js.Object:
		other, isObject := right.(*js.Object)
		if !isObject {
			return false
		}

		return value == other
	}

	return false
}

// numberOf is Number(value) for a parsed JSON scalar; NaN for anything else.
func numberOf(value any) float64 {
	switch scalar := value.(type) {
	case float64:
		return scalar
	case string:
		return js.ToNumber(scalar)
	case bool:
		if scalar {
			return 1
		}

		return 0
	}

	return math.NaN()
}
