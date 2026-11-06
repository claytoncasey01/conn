package openwork

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
)

// The dry run's steps, in ms.
const (
	dryBranchMs   = 900
	dryMoveMs     = 700
	dryAgentMs    = 1400
	dryFindingsMs = 1200
	dryChecks     = 5
	prOpen        = "open"
	reviewNeeded  = "REVIEW_REQUIRED"
	reviewDone    = "APPROVED"
	mergeBlocked  = "BLOCKED"
	// The dry PR's polls: pending (two checks running), pending (one), green, approved; merged
	// after that. Odd tickets fail their checks until FIX CHECKS.
	dryPollFirst    = 0
	dryPollPending  = 1
	dryPollPassing  = 2
	dryPollApproved = 3
	dryPendingFirst = 2
	dryOddTickets   = 2
	// dryAhead and dryDirty are the dry build session's commits and changed files.
	dryAhead = 3
	dryDirty = 2
)

// dryPipeline is the whole pipeline on timers, touching nothing outside this process. Each step
// waits, then stops if the run was cancelled meanwhile.
func (w *Work) dryPipeline(step *task, record *Record) {
	wait := func(ms int) bool {
		step.outside(func() { w.deps.Sleep(w.ctx, time.Duration(ms)*time.Millisecond) })

		return live(record)
	}

	for _, leg := range record.Repos {
		if !wait(dryBranchMs) {
			return
		}

		w.log(record, logBranch, "Branch "+leg.Branch+" created from origin/"+leg.Base+
			" (dry run)")
		leg.WorktreeReady = true
	}

	if !wait(dryMoveMs) {
		return
	}

	category := categoryInProgress
	record.JiraStatus = "In Progress"
	record.TicketCategory = &category
	w.log(record, logStatus, "Jira "+record.Key+" is now In Progress (dry run)")
	w.dryResearch(record)

	finds := []string{
		"ticket, 2 linked issues, 5 comments", "2 pages, the export spec", "4 threads in #team",
		"exportRows(), the nightly job, 6 files",
	}

	for index := range record.Agents {
		if !wait(dryAgentMs) {
			return
		}

		agent := record.Agents[index]
		ended := w.now()
		agent.State = agentDone
		agent.EndedAt = &ended
		agent.Finding = finds[min(index, len(finds)-1)]
		w.log(record, logSource, agent.Name+": "+agent.Finding)
	}

	if !wait(dryFindingsMs) {
		return
	}

	w.dryAsk(record)
}

func (w *Work) dryResearch(record *Record) {
	record.State = stateResearching
	now := w.now()

	for _, agent := range record.Agents {
		agent.State = agentRunning
		agent.StartedAt = &now
		agent.Model = shownModel(agent.ID, record.Model)
	}

	w.log(record, logAgent, "Four research agents started")
}

func (w *Work) dryAsk(record *Record) {
	text := strings.ReplaceAll(dryFindings, "KEY", record.Key)

	var findings struct {
		Answered  []Answered  `json:"answered"`
		Questions []*Question `json:"questions"`
	}

	err := json.Unmarshal([]byte(text), &findings)
	if err != nil {
		panic("openwork: the dry run's findings are not JSON: " + err.Error())
	}

	for _, question := range findings.Questions {
		question.answerFirst = true
	}

	record.Answered = findings.Answered
	record.Questions = findings.Questions
	record.State = stateAsking
	record.Stage = stageAsk
	w.log(record, logSource, "Research finished: 3 answered from sources, 2 for you")
	w.log(record, logAsk, "2 questions need you")
}

// dryStatus is a leg's dry PR: pending, then green (or red on odd tickets until FIX CHECKS is
// pressed), approved, merged. Each leg walks it on its own.
func (w *Work) dryStatus(record *Record, leg *Leg) prstatus.Status {
	polled := legKey(record.Key, leg.Repo)
	poll := w.dryPolls[polled]
	w.dryPolls[polled] = poll + 1

	failing := int(ticketNumber(record.Key))%dryOddTickets == 1
	if w.dryFixed[polled] {
		failing = false
	}

	polledAt := w.now()
	checks := func(state string, pending int, failed ...string) prstatus.Checks {
		return prstatus.Checks{
			State: state, Total: dryChecks, Pending: pending, Failed: append([]string{}, failed...),
		}
	}
	status := func(state string, checks prstatus.Checks, review, mergeable string) prstatus.Status {
		return prstatus.Status{
			State: state, Checks: checks, Review: review, Mergeable: mergeable, At: polledAt,
		}
	}

	switch {
	case poll == dryPollFirst:
		return status(prOpen, checks(checksPending, dryPendingFirst), reviewNeeded, mergeBlocked)
	case failing:
		return status(prOpen, checks(checksFailing, 0, "Backend tests"), reviewNeeded, "UNSTABLE")
	case poll == dryPollPending:
		return status(prOpen, checks(checksPending, 1), reviewNeeded, mergeBlocked)
	case poll == dryPollPassing:
		return status(prOpen, checks(checksPassing, 0), reviewNeeded, mergeBlocked)
	case poll == dryPollApproved:
		return status(prOpen, checks(checksPassing, 0), reviewDone, "CLEAN")
	}

	return status(prMerged, checks(checksPassing, 0), reviewDone, "")
}

// dryTickets are the dry run's sprint: synthetic, in the project PROJ.
func dryTickets() []any {
	parsed, err := js.Parse([]byte(dryList))
	if err != nil {
		panic("openwork: the dry run's sprint is not JSON: " + err.Error())
	}

	tickets, _ := parsed.([]any)

	return tickets
}

// dryFindings are what the dry run's research settles and asks (KEY is the run's key).
const dryFindings = `{"answered":[{"q":"Why does this happen?","a":"The export closes the ` +
	`file before the last batch is written, so the final rows are lost.","src":"SLACK",` +
	`"where":"#team, last week","conf":"HIGH"},{"q":"Which formats are in scope?","a":"Every ` +
	`format the export offers.","src":"CONFLUENCE","where":"Export spec, Scope","conf":"HIGH"},` +
	`{"q":"Does a retry path exist?","a":"Not yet; requested in the ticket comments.",` +
	`"src":"JIRA","where":"KEY, comment 3","conf":"MEDIUM"}],"questions":[{"id":"q1","text":` +
	`"Should the retry re-send rows older than 90 days?","why":"No source says how far back ` +
	`to go.","answer":null,"choices":[{"id":"c1","label":"SEND EVERYTHING MISSED"},{"id":"c2",` +
	`"label":"ONLY THE LAST 90 DAYS"},{"id":"c3","label":"LIST THEM, I WILL PICK"}]},{"id":` +
	`"q2","text":"Which timestamp goes on a row the export already closed?","why":"Slack ` +
	`mentions both. The spec is silent.","answer":null,"choices":[{"id":"c1","label":"THE ` +
	`ORIGINAL TIMESTAMP"},{"id":"c2","label":"THE TIME OF THE RETRY"}]}]}`

// dryList is the dry run's sprint.
const dryList = `[{"key":"PROJ-1","title":"Export saves an empty file when the range is ` +
	`narrow","type":"Bug","status":"In Progress","statusCategory":"indeterminate","priority":` +
	`"High","assignee":"Jane Doe","mine":true,"desc":"A narrow date range can save the export ` +
	`with no rows when the report lists selectable columns.","links":3,"comments":5},` +
	`{"key":"PROJ-2","title":"Nightly job skipped when the batch closes first","type":"Bug",` +
	`"status":"To Do","statusCategory":"new","priority":"High","assignee":"Jane Doe","mine":` +
	`true,"desc":"The batch closes before the nightly job runs, so the job skips it.","links":` +
	`2,"comments":4},{"key":"PROJ-3","title":"Summary report reads the selected account",` +
	`"type":"Story","status":"To Do","statusCategory":"new","priority":"Medium","assignee":` +
	`"Jane Doe","mine":true,"desc":"The report ignores the account chosen in the sidebar.",` +
	`"links":1,"comments":2},{"key":"PROJ-4","title":"Year rollover keeps stale choices",` +
	`"type":"Story","status":"To Do","statusCategory":"new","priority":"Medium","assignee":` +
	`"Sam Roe","mine":false,"desc":"Rollover copies a choice forward without checking the ` +
	`option still exists.","links":4,"comments":1},{"key":"PROJ-5","title":"Correction leaves ` +
	`no trace on the audit report","type":"Bug","status":"To Do","statusCategory":"new",` +
	`"priority":"Low","assignee":"","mine":false,"desc":"A correction changes stored values ` +
	`but the report shows no sign of it.","links":2,"comments":0},{"key":"PROJ-6","title":` +
	`"Logo slug check in the admin dropdown","type":"Task","status":"In Review",` +
	`"statusCategory":"indeterminate","priority":"Low","assignee":"Jane Doe","mine":true,` +
	`"desc":"Warn when a name does not slugify to an existing logo file.","links":1,` +
	`"comments":1}]`
