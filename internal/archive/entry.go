package archive

import (
	"errors"
	"math"
	"strings"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/snapshot"
)

const (
	nameSnippet = 60
	idShown     = 8

	outcomeEnded    = "ended"
	outcomePROpened = "PR opened"
	outcomeMerged   = "merged"
)

// Outcome is what came of a session: merged, PR opened or ended.
func Outcome(kind string, run *sessions.RunFacts) string {
	if kind != sessions.KindBuild {
		return outcomeEnded
	}

	if run == nil {
		return outcomeEnded
	}

	if run.PRState == outcomeMerged {
		return outcomeMerged
	}

	if run.State == "pr_open" {
		return outcomePROpened
	}

	return outcomeEnded
}

// NameFor is what an entry is called: the registry name, else the transcript title, else the
// start of the first prompt (whitespace collapsed, at most 60 UTF-16 units counting a trailing
// ellipsis), else the start of the session id. Live checkpoints and replays both name through
// here, so they agree.
func NameFor(registryName, title, firstPrompt, sessionID string) string {
	if registryName != "" {
		return registryName
	}

	if title != "" {
		return title
	}

	prompt := js.Trim(js.CollapseSpace(firstPrompt))
	if prompt == "" {
		return js.Cut(sessionID, idShown)
	}

	if js.Len(prompt) > nameSnippet {
		return js.Cut(prompt, nameSnippet-1) + "…"
	}

	return prompt
}

// facts are what an entry is built from besides the parts.
type facts struct {
	sessionID, path, source, kind string
	pr                            *snapshot.PR
	run                           *sessions.RunFacts
	// name, pid, cwd and startedAt are the registry's, when the session had them.
	name           string
	pid, startedAt float64
	cwd            string
	final          bool
	transcriptSize *float64
	// transcriptCwd and firstAt come from the transcript lines, when a replay supplies them.
	transcriptCwd string
	firstAt       float64
}

// Env is what building an entry reads besides the session: the repo of a folder that exists, and
// the ticket key a session's name is ("" for none).
type Env struct {
	RepoOf    func(folder string) string
	TicketKey func(name string) string
}

// lastActivity is the last thing any part of the session did: a background agent can outlive the
// lead.
func lastActivity(parts snapshot.Parts) float64 {
	last := parts.Lead.LastAt
	for _, agent := range parts.Agents {
		last = math.Max(last, agent.LastAt)
	}

	return last
}

// firstTruthy is a || b || c || 0.
func firstTruthy(values ...float64) float64 {
	for _, value := range values {
		if js.Truthy(value) {
			return value
		}
	}

	return 0
}

func firstText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

// build is one entry from a session's parts, replayed or live. Every fixed live field is set here,
// and the same end, name and clock go to every builder, so the lead agrees wherever it is shown.
func build(parts snapshot.Parts, fact facts, env Env) history.Entry {
	lead := parts.Lead
	name := NameFor(fact.name, lead.Title, lead.Task, fact.sessionID)
	cwd := firstText(fact.cwd, fact.transcriptCwd)
	// The transcript's own first line, not the registry's start: a resumed session's registry
	// start is the resume, while its cost and lines cover the whole file.
	startedAt := firstTruthy(lead.StartedAt, fact.firstAt, fact.startedAt)
	endedAt := math.Max(lastActivity(parts), startedAt)
	repo := folderRepo(cwd, env)

	var repoField *string
	if repo != "" {
		repoField = &repo
	}

	live := snapshot.Live{
		ID: fact.sessionID, PID: fact.pid, SessionID: fact.sessionID, Name: name, Cwd: cwd,
		Repo: repoField, Status: snapshot.StatusIdle, WaitingFor: nil, CanReply: false, Ask: nil,
		Shells: 0, PR: fact.pr, StartedAt: startedAt, UpdatedAt: lead.LastAt, Team: nil,
		Hosted: false, ExitCode: nil,
	}

	detail := snapshot.Build(parts, live, endedAt, snapshot.Opts{
		Trim: false, Kind: fact.kind, Ended: true, EndedAt: &endedAt,
	})
	detail.Archived = &snapshot.Archived{
		EndedAt: endedAt, Source: fact.source, TranscriptAvailable: true, ResumedFrom: nil,
	}

	entry := history.Entry{
		SessionID: fact.sessionID, Name: name, Cwd: cwd, Project: detail.Project,
		Branch: detail.Branch, Kind: fact.kind, Model: detail.Model, StartedAt: startedAt,
		EndedAt: endedAt,
		CostUSD: detail.CostUSD, Agents: float64(detail.AgentsTotal),
		AdvisorCalls: float64(detail.AdvisorCalls), Errors: float64(detail.Errors),
		Outcome: Outcome(fact.kind, fact.run), Repo: repo, PRNumber: nil, TicketKey: nil,
		Source: fact.source, ResumedFrom: nil, TranscriptPath: fact.path, Detail: detail,
		AgentDetails: agentPages(parts, endedAt, name), FirstPrompt: lead.Task,
		FinalMessage: lead.FinalText, SearchText: searchText(parts, name), Final: fact.final,
		TranscriptSize: fact.transcriptSize, Named: fact.name != "",
	}

	if lead.Title != "" {
		entry.Named = true
	}

	if fact.pr != nil {
		number := fact.pr.Number
		entry.PRNumber = &number
	}

	if key := env.TicketKey(name); key != "" {
		entry.TicketKey = &key
	}

	return entry
}

func folderRepo(folder string, env Env) string {
	if folder == "" {
		return ""
	}

	return env.RepoOf(folder)
}

// agentPages are the lead's and every agent's page, every tool call kept.
func agentPages(parts snapshot.Parts, endedAt float64, name string) *js.Object {
	pages := js.NewObject()
	ids := make([]string, 0, 1+len(parts.Agents))
	ids = append(ids, "lead")

	for _, agent := range parts.Agents {
		ids = append(ids, agent.ID)
	}

	for _, id := range ids {
		page, found := snapshot.BuildAgent(parts, id, endedAt, snapshot.AgentOpts{
			ToolCallLimit: 0, Ended: true, EndedAt: &endedAt, Name: &name, Status: nil,
		})
		if found {
			pages.Set(id, page)
		}
	}

	return pages
}

// searchText is what the History search reads: the name, the task, the final text, each agent's
// description, task and result, and every tool call's summary.
func searchText(parts snapshot.Parts, name string) string {
	lead := parts.Lead
	pieces := []string{name, lead.Task, lead.FinalText}

	for _, agent := range parts.Agents {
		description := ""
		if agent.Meta.Description != nil {
			description = *agent.Meta.Description
		}

		pieces = append(pieces, description, agent.Task, agent.Result)
	}

	for _, call := range lead.ToolCalls {
		pieces = append(pieces, call.Summary)
	}

	for _, agent := range parts.Agents {
		for _, call := range agent.ToolCalls {
			pieces = append(pieces, call.Summary)
		}
	}

	kept := pieces[:0]

	for _, piece := range pieces {
		if piece != "" {
			kept = append(kept, piece)
		}
	}

	return strings.Join(kept, "\n")
}

// ReplayOpts are the registry facts and the kind a replayed entry carries.
type ReplayOpts struct {
	Source, Kind, Name string
	PR                 *snapshot.PR
	Run                *sessions.RunFacts
	PID, StartedAt     float64
	Cwd                string
	// NotFinal marks a replay that is not the whole session (not used by conn's own captures).
	NotFinal bool
}

// FromReplay is the entry for a transcript replayed to its end. The registry facts are optional:
// a backfilled session has none, and the name then falls back to its title, then its id.
func FromReplay(replayed Replayed, opts ReplayOpts, env Env) history.Entry {
	size := replayed.Size

	return build(replayed.Parts, facts{
		sessionID: replayed.SessionID, path: replayed.Path, source: opts.Source, kind: opts.Kind,
		pr: opts.PR, run: opts.Run, name: opts.Name, pid: opts.PID, startedAt: opts.StartedAt,
		cwd: opts.Cwd, final: !opts.NotFinal, transcriptSize: &size,
		transcriptCwd: replayed.Cwd, firstAt: replayed.FirstAt,
	}, env)
}

// errGone is a transcript that is gone, or holds no line, by replay time.
var errGone = errors.New("the transcript is gone or has no lines")

var (
	errNotFound = errors.New("entry not found")
	errEmpty    = errors.New("the summary came back empty")
)
