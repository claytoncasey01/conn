package openwork

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The run states.
const (
	stateStarting    = "starting"
	stateResearching = "researching"
	stateAsking      = "asking"
	stateBuilding    = "building"
	statePROpen      = "pr_open"
	stateFailed      = "failed"
	stateInterrupted = "interrupted"
	stateCancelled   = "cancelled"
)

// The run stages: research, ask, build, pr.
const (
	stageResearch = "research"
	stageAsk      = "ask"
	stageBuild    = "build"
	stagePR       = "pr"
)

// The agent states.
const (
	agentQueued  = "queued"
	agentRunning = "running"
	agentDone    = "done"
	agentError   = "error"
)

// The kinds of log line.
const (
	logStatus = "status"
	logBranch = "branch"
	logAgent  = "agent"
	logSource = "source"
	logAsk    = "ask"
	logBuild  = "build"
	logPR     = "pr"
	logError  = "error"
)

const (
	// logLinesMax is how many log lines a run keeps.
	logLinesMax = 200
	// agentsCount is how many research agents a run has.
	agentsCount = 4
	// fileMode and folderMode are what a plain write and mkdir make, the umask taking its share,
	// so the files have the modes hand-made ones have.
	fileMode   = 0o666
	folderMode = 0o777
)

// Agent is one of a run's four research agents.
type Agent struct {
	// ID is jira, confluence, slack or code.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Model is the model shown ("" until it runs).
	Model string `json:"model"`
	State string `json:"state"`
	// Finding is one line on what it found, or the error.
	Finding   string   `json:"finding"`
	StartedAt *float64 `json:"startedAt"`
	EndedAt   *float64 `json:"endedAt"`
}

// Answered is a question the sources answered.
type Answered struct {
	Q   string `json:"q"`
	A   string `json:"a"`
	Src string `json:"src"`
	// Where is the page, thread or comment it came from.
	Where string `json:"where"`
	Conf  string `json:"conf"`
}

// Choice is one answer offered for a question.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Question is a question the sources could not answer.
type Question struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Why is what was searched and why nothing settled it.
	Why     string   `json:"why"`
	Choices []Choice `json:"choices"`
	// Answer is the chosen choice id, or the person's own words; nil until answered.
	Answer *string `json:"answer"`

	// answerFirst is that the answer is written before the choices, as the dry run's questions
	// are.
	answerFirst bool
}

// questionAnswerFirst is a question in the dry run's order.
type questionAnswerFirst struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Why     string   `json:"why"`
	Answer  *string  `json:"answer"`
	Choices []Choice `json:"choices"`
}

// MarshalJSON writes the question with its keys in the order its object was made.
func (q *Question) MarshalJSON() ([]byte, error) {
	if q.answerFirst {
		return js.Marshal(questionAnswerFirst{
			ID: q.ID, Text: q.Text, Why: q.Why, Answer: q.Answer, Choices: q.Choices,
		}), nil
	}

	type plain Question

	return js.Marshal((*plain)(q)), nil
}

// LogLine is one line of a run's log.
type LogLine struct {
	At   float64 `json:"at"`
	Kind string  `json:"kind"`
	Text string  `json:"text"`
}

// Session is the build session, once it exists: read from the session list and git.
type Session struct {
	// Name is the ticket key.
	Name string `json:"name"`
	// SessionID is the session's id on the Sessions page (its pid), once it is up.
	SessionID *string  `json:"sessionId"`
	PID       *float64 `json:"pid"`
	Status    *string  `json:"status"`
	// CommitsAhead and Dirty are the commits on the branch and the files not yet committed.
	CommitsAhead float64 `json:"commitsAhead"`
	Dirty        float64 `json:"dirty"`
}

// PR is a run's pull request and what GitHub last said of it.
type PR struct {
	URL    string           `json:"url"`
	Number float64          `json:"number"`
	Status *prstatus.Status `json:"status,omitempty"`
}

// Record is a run as it is kept on disk: the run the page shows plus what only the server needs.
// Its fields are in the order a run's keys are first set, which is the order the file and the
// snapshot write them in.
type Record struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Base is the branch the run started from; PRBase is its PR's target, the same branch.
	Base     string `json:"base"`
	PRBase   string `json:"prBase"`
	Model    string `json:"model"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	State    string `json:"state"`
	Stage    string `json:"stage"`
	// StartedAt and UpdatedAt are ms epochs.
	StartedAt float64 `json:"startedAt"`
	UpdatedAt float64 `json:"updatedAt"`
	// JiraStatus is the ticket's status as this run last set or saw it.
	JiraStatus    string      `json:"jiraStatus"`
	Agents        []*Agent    `json:"agents"`
	Answered      []Answered  `json:"answered"`
	Questions     []*Question `json:"questions"`
	Log           []LogLine   `json:"log"`
	Session       *Session    `json:"session"`
	PR            *PR         `json:"pr"`
	Error         *string     `json:"error"`
	ManualCommand *string     `json:"manualCommand"`
	Brief         string      `json:"brief"`
	Desc          string      `json:"desc"`
	WorktreeReady bool        `json:"worktreeReady"`
	TicketStatus  string      `json:"ticketStatus"`
	// TicketCategory is the ticket's status category when the run started; a run from before
	// categories has none.
	TicketCategory *string `json:"ticketCategory,omitempty"`
	// Repo and Clone are the repository the run builds in and the clone it branched from; a run
	// from before Settings has neither.
	Repo  *string `json:"repo,omitempty"`
	Clone *string `json:"clone,omitempty"`
	// ClaudeSessionID is Claude's own id for the build session, kept so it can be resumed.
	ClaudeSessionID *string `json:"claudeSessionId,omitempty"`
	// Cleaned is that the worktree was removed after the PR merged or closed.
	Cleaned *bool `json:"cleaned,omitempty"`

	// read is the record as its file held it: a record is written back with its keys where the
	// file had them (a key this code does not know included), and the keys added since after them.
	read *js.Object
}

// Run is a run as the page sees it: the record without what only the server needs.
type Run struct {
	Key             string      `json:"key"`
	Title           string      `json:"title"`
	Base            string      `json:"base"`
	PRBase          string      `json:"prBase"`
	Model           string      `json:"model"`
	Branch          string      `json:"branch"`
	Worktree        string      `json:"worktree"`
	State           string      `json:"state"`
	Stage           string      `json:"stage"`
	StartedAt       float64     `json:"startedAt"`
	UpdatedAt       float64     `json:"updatedAt"`
	JiraStatus      string      `json:"jiraStatus"`
	Agents          []*Agent    `json:"agents"`
	Answered        []Answered  `json:"answered"`
	Questions       []*Question `json:"questions"`
	Log             []LogLine   `json:"log"`
	Session         *Session    `json:"session"`
	PR              *PR         `json:"pr"`
	Error           *string     `json:"error"`
	ManualCommand   *string     `json:"manualCommand"`
	ClaudeSessionID *string     `json:"claudeSessionId,omitempty"`
	Cleaned         *bool       `json:"cleaned,omitempty"`
}

func (r *Record) view() Run {
	return Run{
		Key: r.Key, Title: r.Title, Base: r.Base, PRBase: r.PRBase, Model: r.Model,
		Branch: r.Branch, Worktree: r.Worktree, State: r.State, Stage: r.Stage,
		StartedAt: r.StartedAt, UpdatedAt: r.UpdatedAt, JiraStatus: r.JiraStatus,
		Agents: r.Agents, Answered: r.Answered, Questions: r.Questions, Log: r.Log,
		Session: r.Session, PR: r.PR, Error: r.Error, ManualCommand: r.ManualCommand,
		ClaudeSessionID: r.ClaudeSessionID, Cleaned: r.Cleaned,
	}
}

func (r *Record) cleaned() bool {
	if r.Cleaned == nil {
		return false
	}

	return *r.Cleaned
}

// newAgents are the four research agents, queued.
func newAgents() []*Agent {
	agents := make([]*Agent, 0, agentsCount)

	for _, agent := range []struct{ id, name string }{
		{"jira", "Jira reader"},
		{"confluence", "Confluence reader"},
		{"slack", "Slack reader"},
		{"code", "Code mapper"},
	} {
		agents = append(agents, &Agent{
			ID: agent.id, Name: agent.name, Model: "", State: agentQueued, Finding: "",
			StartedAt: nil, EndedAt: nil,
		})
	}

	return agents
}

// readRecord is the run a file holds, or false when it is not one of Conn's: not a JSON object,
// a key that is not a ticket key (the key names the file and goes into commands), or fields of
// the wrong type. A run made when the base was one of three named choices recorded that choice
// as base; the branch it started from is the one its PR targets. A run whose research stage was
// running when the server stopped is interrupted.
func readRecord(data []byte) (*Record, bool) {
	parsed, err := js.Parse(data)
	if err != nil {
		return nil, false
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return nil, false
	}

	key, _ := object.Get("key")

	text, isText := key.(string)
	if !isText {
		return nil, false
	}

	if !jira.AnyKey(text) {
		return nil, false
	}

	record := &Record{}

	err = json.Unmarshal(data, record)
	if err != nil {
		return nil, false
	}

	// A null agent or question is a record no code path could use.
	if slices.Contains(record.Agents, nil) {
		return nil, false
	}

	if slices.Contains(record.Questions, nil) {
		return nil, false
	}

	record.read = object

	return settleRead(record, object), true
}

func settleRead(record *Record, object *js.Object) *Record {
	prBase, _ := object.Get("prBase")

	branch, ok := settings.ParseBranch(prBase)
	if ok {
		record.Base = branch
	}

	switch record.State {
	case stateStarting, stateResearching:
		record.State = stateInterrupted
		why := "The dashboard restarted while this stage was running. Retry starts it again."
		record.Error = &why

		for _, agent := range record.Agents {
			stopAgent(agent)
		}
	}

	return record
}

// stopAgent marks an agent that was queued or running as failed.
func stopAgent(agent *Agent) {
	switch agent.State {
	case agentRunning, agentQueued:
		agent.State = agentError
	}
}

// writeRecord writes the run's file; a failure is ignored: the run goes on in memory.
func writeRecord(runsDir string, record *Record) {
	path := filepath.Join(runsDir, record.Key+".json")
	_ = os.WriteFile(path, record.ordered(record, func(string) bool { return false }), fileMode)
}

// viewJSON is the run as the page sees it.
func (r *Record) viewJSON() []byte { return r.ordered(r.view(), internalKey) }

// internalKey is whether a record's key is what only the server needs.
func internalKey(key string) bool {
	switch key {
	case "brief", "desc", "worktreeReady", "ticketStatus", "ticketCategory", "repo", "clone":
		return true
	}

	return false
}

// ordered is value as JSON with its keys in the order the record holds them: those of the
// file the record was read from where they were (one this code does not know kept as it was
// read), then the ones added since, and the same inside each object the file held. A record
// made here has its fields' order.
func (r *Record) ordered(value any, skip func(key string) bool) []byte {
	data := js.Marshal(value)
	if r.read == nil {
		return data
	}

	parsed, err := js.Parse(data)
	if err != nil {
		return data
	}

	full, _ := parsed.(*js.Object)
	out := js.NewObject()

	for _, key := range r.read.Keys() {
		if skip(key) {
			continue
		}

		was, _ := r.read.Get(key)

		if current, present := full.Get(key); present {
			out.Set(key, orderLike(current, was, orderDepthMax))

			continue
		}

		out.Set(key, was)
	}

	for _, key := range full.Keys() {
		if _, present := out.Get(key); !present {
			current, _ := full.Get(key)
			out.Set(key, current)
		}
	}

	return []byte(js.Stringify(out))
}

// orderDepthMax bounds how deep orderLike follows a record's objects: a run's deepest is a
// choice of a question (record, questions, question, choices, choice).
const orderDepthMax = 8

// orderLike is current with each object's keys in the order was (the same place in the file)
// had them, then its new keys.
func orderLike(current, was any, depth int) any {
	if depth == 0 {
		return current
	}

	switch value := current.(type) {
	case *js.Object:
		before, isObject := was.(*js.Object)
		if isObject {
			return orderObject(value, before, depth)
		}
	case []any:
		before, isList := was.([]any)
		if isList {
			return orderList(value, before, depth)
		}
	}

	return current
}

func orderObject(current, before *js.Object, depth int) *js.Object {
	out := js.NewObject()

	for _, key := range before.Keys() {
		if entry, present := current.Get(key); present {
			old, _ := before.Get(key)
			out.Set(key, orderLike(entry, old, depth-1))
		}
	}

	for _, key := range current.Keys() {
		if _, present := out.Get(key); !present {
			entry, _ := current.Get(key)
			out.Set(key, entry)
		}
	}

	return out
}

func orderList(current, before []any, depth int) []any {
	out := make([]any, len(current))

	for index, entry := range current {
		out[index] = entry
		if index < len(before) {
			out[index] = orderLike(entry, before[index], depth-1)
		}
	}

	return out
}

// loadRecords are the runs in the runs folder, in the order of their file names. A folder lists
// its files in an order that depends on the filesystem (by name on one, by hash on another), so
// the names are sorted: the same runs come back in the same order everywhere.
func loadRecords(runsDir string) []*Record {
	folder, err := os.Open(runsDir)
	if err != nil {
		return nil
	}

	names, _ := folder.Readdirnames(-1)
	_ = folder.Close()

	slices.Sort(names)

	records := []*Record{}

	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(runsDir, name))
		if readErr != nil {
			continue
		}

		record, ok := readRecord(data)
		if ok {
			records = append(records, record)
		}
	}

	return records
}
