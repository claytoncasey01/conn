package openwork

import (
	"errors"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	modelHaiku  = "haiku"
	modelSonnet = "sonnet"
	// categoryNew is Jira's to-do status category; categoryInProgress its in-progress one.
	categoryNew        = "new"
	categoryInProgress = "indeterminate"
	// shTimeout is a git or gh call's limit when its caller names none.
	shTimeout = 120 * time.Second
)

// textError is an error that is its text.
type textError string

func (e textError) Error() string { return string(e) }

// sh runs a command for the work in flight: it is not stopped by a cancelled run (git and gh
// finish what they started), only by the server stopping.
func (w *Work) sh(name string, args []string, dir string, timeout time.Duration) (string, error) {
	if timeout == 0 {
		timeout = shTimeout
	}

	return w.deps.Sh(w.ctx, name, args, dir, timeout)
}

// isActive is whether a run is in one of its working states.
func isActive(state string) bool {
	switch state {
	case stateStarting, stateResearching, stateAsking, stateBuilding:
		return true
	}

	return false
}

// Start starts a run for a ticket of the list: refused while Open work is off, for a key that is
// not one of the projects', a model that is not haiku or sonnet, a ticket that already has a
// run, or one not in the list; a ticket that is someone else's or not to do asks to confirm.
func (w *Work) Start(key string, body *js.Object) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.on() {
		return notFound(jira.Off)
	}

	// Checked against the pattern built from the projects in Settings: the key reaches a branch
	// name, a folder name and prompts.
	if !jira.IsKey(w.settings(), key) {
		return badRequest(key + " is not a ticket key in a Jira project listed in Settings")
	}

	model := modelOf(field(body, "model"))
	if model == "" {
		return badRequest("model must be haiku or sonnet")
	}

	had, found := w.runs.Get(key)
	if found {
		if isActive(had.State) {
			return conflict(key + " already has a run (" + had.State + ")")
		}

		if had.State == statePROpen {
			return conflict(key + " already has a run (" + had.State + ")")
		}
	}

	if w.running[key] {
		return conflict("the last stage of " + key + " is still ending: try again in a moment")
	}

	return w.startTicket(key, model, had, js.Truthy(field(body, "force")))
}

func modelOf(value any) string {
	switch value {
	case modelSonnet:
		return modelSonnet
	case modelHaiku:
		return modelHaiku
	}

	return ""
}

func (w *Work) ticket(key string) *js.Object {
	for _, ticket := range w.list.Tickets {
		object, isObject := ticket.(*js.Object)
		if !isObject {
			continue
		}

		if field(object, "key") == key {
			return object
		}
	}

	return nil
}

// confirm refuses a start until the person confirms what: a ticket someone else's, or one not
// to do.
func confirm(message, what string) Reply {
	reply := conflict(message)
	reply.Body.Set("needsConfirm", what)

	return reply
}

func (w *Work) startTicket(key, model string, had *Record, force bool) Reply {
	ticket := w.ticket(key)
	if ticket == nil {
		return notFound(key + " is not in the current sprint list")
	}

	if !force {
		if refusal, refused := unconfirmed(key, ticket); refused {
			return refusal
		}
	}

	target := w.target()
	if target.Clone == "" {
		return conflict(reasonOr(target.Reason))
	}

	if target.Base == "" {
		return conflict(reasonOr(target.Reason))
	}

	if refusal, refused := w.baseMoved(key, had, target); refused {
		return refusal
	}

	record := w.newRecord(key, model, ticket, target)
	w.runs.Set(key, record)
	w.log(record, logStatus, "Run started for "+key+": branch from "+target.Base+
		", research on "+strings.ToUpper(model))
	w.pipeline(record)

	return ok()
}

// baseMoved refuses a new run over an old one whose branch was cut from another base than runs
// start from now.
func (w *Work) baseMoved(key string, had *Record, target Target) (Reply, bool) {
	if w.deps.Dry {
		return Reply{}, false
	}

	if had == nil {
		return Reply{}, false
	}

	if !had.WorktreeReady {
		return Reply{}, false
	}

	if had.Base == target.Base {
		return Reply{}, false
	}

	return conflict(key + " already has " + had.Branch + " cut from " + had.Base + ", and runs " +
		"start from " + target.Base + " now. Remove " + had.Worktree + " and the branch " +
		"first."), true
}

// unconfirmed asks to confirm a ticket assigned to someone else, or not in a to-do status.
func unconfirmed(key string, ticket *js.Object) (Reply, bool) {
	if js.Truthy(field(ticket, "assignee")) {
		if !js.Truthy(field(ticket, "mine")) {
			return confirm(key+" is assigned to "+js.String(field(ticket, "assignee")),
				"assignee"), true
		}
	}

	if field(ticket, "statusCategory") != categoryNew {
		return confirm(key+" is "+jsText(ticket, "status")+", not in a To Do status",
			"status"), true
	}

	return Reply{}, false
}

// jsText is a ticket's field as a template literal writes it: undefined when it is absent.
func jsText(ticket *js.Object, key string) string {
	value, present := ticket.Get(key)
	if !present {
		return "undefined"
	}

	return js.String(value)
}

func text(ticket *js.Object, key string) string {
	value, _ := field(ticket, key).(string)

	return value
}

func (w *Work) newRecord(key, model string, ticket *js.Object, target Target) *Record {
	now := w.now()
	title := text(ticket, "title")

	slug := Slug(title)
	if slug == "" {
		slug = "work"
	}

	category := text(ticket, "statusCategory")
	repo, clone := target.Repo, target.Clone
	lower := strings.ToLower(key)

	// Runs start from the branch Settings names (else the clone's default branch) and the PR
	// goes back into it. The worktree is a sibling of the clone, named for the clone and the
	// ticket; the run records it, and cleanup removes the path the run recorded.
	return &Record{
		Key: key, Title: title, Base: target.Base, PRBase: target.Base, Model: model,
		Branch: lower + "-" + slug, Worktree: siblingOf(clone, lower), State: stateStarting,
		Stage: stageResearch, StartedAt: now, UpdatedAt: now,
		JiraStatus: text(ticket, "status"), Agents: newAgents(), Answered: []Answered{},
		Questions: []*Question{}, Log: []LogLine{}, Session: nil, PR: nil, Error: nil,
		ManualCommand: nil, Brief: "", Desc: text(ticket, "desc"), WorktreeReady: false,
		TicketStatus: text(ticket, "status"), TicketCategory: &category, Repo: &repo,
		Clone: &clone, ClaudeSessionID: nil, Cleaned: nil,
	}
}

func siblingOf(clone, lowerKey string) string {
	return joinPath(dirOf(clone), baseOf(clone)+"-"+lowerKey)
}

// pipeline makes the worktree, moves the ticket into progress and researches it.
func (w *Work) pipeline(record *Record) {
	w.running[record.Key] = true

	w.launch(func(step *task) {
		defer delete(w.running, record.Key)

		err := w.runPipeline(step, record)
		if err == nil {
			return
		}

		if live(record) {
			w.fail(record, err.Error())
		}
	})
}

// pipelineRefusal is why the pipeline may not run: Open work off, a key not of a listed
// project, or (not dry) a build problem.
func (w *Work) pipelineRefusal(record *Record) error {
	if !w.on() {
		return textError(jira.Off)
	}

	if !jira.IsKey(w.settings(), record.Key) {
		return textError(record.Key + " is not in a Jira project listed in Settings")
	}

	if w.deps.Dry {
		return nil
	}

	if problem := w.buildProblem(record); problem != "" {
		return textError(problem)
	}

	return nil
}

func (w *Work) runPipeline(step *task, record *Record) error {
	err := w.pipelineRefusal(record)
	if err != nil {
		return err
	}

	if w.deps.Dry {
		w.dryPipeline(step, record)

		return nil
	}

	if !record.WorktreeReady {
		err = w.makeWorktree(step, record)
		if err != nil {
			return err
		}

		if !live(record) {
			return nil
		}
	}

	// Atlassian can be turned off while a git call is out: the move and the research each
	// refuse to call it then. A ticket that was to do goes into an in-progress status, whatever
	// the workflow calls it.
	if record.TicketCategory != nil {
		if *record.TicketCategory == categoryNew {
			w.moveJira(step, record, jira.MoveStarted())
		}
	}

	if !live(record) {
		return nil
	}

	return w.research(step, record)
}

func (w *Work) makeWorktree(step *task, record *Record) error {
	// The branch it starts from is read from a record on disk: only a name git can take as one
	// argument.
	from, valid := settings.ParseBranch(record.Base)
	if !valid {
		return textError(record.Base + " is not a branch name")
	}

	// The clone is looked up again, never taken from the record: a repository that has left
	// the allowlist is not fetched, and a changed setting is followed.
	if problem := w.buildProblem(record); problem != "" {
		return textError(problem)
	}

	clone := w.target().Clone
	w.log(record, logBranch, "Fetching origin/"+from)

	var err error

	step.outside(func() { _, err = w.sh("git", []string{"fetch", "origin", from}, clone, 0) })

	if err != nil {
		return err
	}

	if exists(record.Worktree) {
		err = w.reuseWorktree(step, record)
	} else {
		err = w.addWorktree(step, record, clone, from)
	}

	if err != nil {
		return err
	}

	record.WorktreeReady = true
	w.touch(record)

	return nil
}

func (w *Work) reuseWorktree(step *task, record *Record) error {
	head := ""

	step.outside(func() {
		out, err := w.sh("git", []string{argRevParse, "--abbrev-ref", "HEAD"}, record.Worktree, 0)
		if err == nil {
			head = js.Trim(out)
		}
	})

	if head != record.Branch {
		on := head
		if on == "" {
			on = "another checkout"
		}

		return textError(record.Worktree + " already exists on " + on + ", not " + record.Branch)
	}

	w.log(record, logBranch, "Reusing "+record.Worktree)

	return nil
}

func (w *Work) addWorktree(step *task, record *Record, clone, from string) error {
	branchExists := false

	step.outside(func() {
		_, err := w.sh("git", []string{
			argRevParse, "--verify", "--quiet",
			"refs/heads/" + record.Branch,
		}, clone, 0)
		branchExists = err == nil
	})

	args := []string{argWorktree, "add", "-b", record.Branch, record.Worktree, "origin/" + from}
	if branchExists {
		args = []string{argWorktree, "add", record.Worktree, record.Branch}
	}

	var err error

	step.outside(func() { _, err = w.sh("git", args, clone, 0) })

	if err != nil {
		return err
	}

	w.log(record, logBranch, "Branch "+record.Branch+" created from origin/"+from+" in "+
		record.Worktree)

	return nil
}

func moveWhat(move jira.Move) string {
	if move.Named() {
		return move.Status()
	}

	if move.Started() {
		return "an In Progress status"
	}

	return "its review status"
}

// moveJira moves the run's ticket; a failure is logged, never the run's.
func (w *Work) moveJira(step *task, record *Record, move jira.Move) {
	what := moveWhat(move)

	if !w.on() {
		w.log(record, logStatus, "Atlassian is off in Settings: "+record.Key+" stays "+
			record.JiraStatus)

		return
	}

	w.log(record, logStatus, "Moving "+record.Key+" to "+what)

	ctx, done := w.track(record.Key)

	var (
		moved jira.Moved
		err   error
	)

	step.outside(func() { moved, err = w.deps.Transition(ctx, record.Key, move) })
	done()

	if err != nil {
		w.moveFailed(record, what, err)

		return
	}

	w.moved(record, move, moved)
}

func (w *Work) moved(record *Record, move jira.Move, moved jira.Moved) {
	record.JiraStatus = moved.To
	record.TicketStatus = moved.To

	category := ""
	if move.Started() {
		category = categoryInProgress
	}

	record.TicketCategory = &category
	w.record(moduleAtlassian, true, "Open work moved a Jira ticket")

	if moved.Moved {
		w.log(record, logStatus, "Jira "+record.Key+" is now "+record.JiraStatus)

		return
	}

	why := "it is already there"

	if !move.Named() {
		if !move.Started() {
			why = "the workflow has no single review status"
		}
	}

	w.log(record, logStatus, "Jira "+record.Key+" was not moved: "+why+", so it stays "+
		record.JiraStatus)
}

// moveFailed logs a move that failed. A move stopped because the run was interrupted says
// nothing about the connector, nor does one refused before any call; one the workflow refused
// is the connector answering.
func (w *Work) moveFailed(record *Record, what string, err error) {
	if !live(record) {
		return
	}

	refused, isRefused := errors.AsType[*jira.MoveRefusedError](err)
	_, signedOut := errors.AsType[*jira.SignedOutError](err)

	switch {
	case signedOut:
		w.record(moduleAtlassian, false, "Atlassian is signed out: sign in to the claude.ai "+
			"Atlassian connector")
	case !isRefused:
		w.record(moduleAtlassian, false, "Open work could not move a Jira ticket")
	case refused.Called:
		w.record(moduleAtlassian, true, "Open work asked Jira to move a ticket")
	}

	w.log(record, logError, "Could not move "+record.Key+" to "+what+": "+err.Error())
}

// Cancel stops a run that is working: its helper runs are stopped, and the branch, the worktree
// and a build session stay.
func (w *Work) Cancel(key string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	if !isActive(record.State) {
		return conflict(key + " is " + record.State)
	}

	if w.busyPR[key] {
		return conflict("a pull request is being opened, wait for it to finish")
	}

	record.State = stateCancelled

	w.kill(key)

	for _, agent := range record.Agents {
		stopAgent(agent)
	}

	said := "Run cancelled. The branch and worktree stay."
	if record.Stage == stageBuild {
		said = "Run cancelled. The branch and worktree stay, and the session keeps running " +
			"until you close it."
	}

	w.log(record, logStatus, said)

	return ok()
}

// Retry starts a failed or interrupted run's pipeline again.
func (w *Work) Retry(key string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	switch record.State {
	case stateFailed, stateInterrupted:
	default:
		return conflict(key + " is " + record.State)
	}

	// An interrupted stage whose git call is still out is the same run: a second pipeline would
	// race it, so wait for the first to end.
	if w.running[key] {
		return conflict("the last stage of " + key + " is still ending: try again in a moment")
	}

	record.Error = nil
	record.State = stateStarting
	record.Agents = newAgents()
	w.log(record, logStatus, "Retrying")
	w.pipeline(record)

	return ok()
}
