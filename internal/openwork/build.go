package openwork

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// answerUnitsMax is how much of an answer is kept.
	answerUnitsMax = 600
	// resumeGapMs is how long after a session was opened for a run another may be.
	resumeGapMs = 30_000
	// delegationFile and briefFile are what the build session is started with.
	delegationFile = "delegation.md"
	briefFile      = "brief.md"
	connFolder     = ".conn"
)

// sessionUUID is Claude's session id shape: only that goes into a command.
var sessionUUID = regexp.MustCompile(
	`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

// Delegation is appended to the build session's system prompt: Sonnet edits, named Haiku agents
// do the looking.
const Delegation = "Work split for this session:\n" +
	"- You (Sonnet) edit code. Do not run broad searches, symbol greps or doc lookups yourself.\n" +
	"- Mapping the repo or finding the files for a goal: spawn the `search` agent (Haiku).\n" +
	"- Finding every call site or usage of a symbol, string or config key: spawn the `grep` " +
	"agent (Haiku).\n" +
	"- Pulling API reference for a library at the version this project uses: spawn the `docs` " +
	"agent (Haiku).\n" +
	"- Give every spawn a short, specific `description` (\"grep: findPremium callers\", " +
	"\"docs: Inertia useForm\"). The dashboard's agent tree labels agents by it.\n" +
	"- Spawn independent lookups in one message so they run together.\n" +
	"- Consult the advisor before locking a plan, after the same error twice, and before " +
	"calling the task done.\n"

// rules are what the build session starts with. key is a checked ticket key, so it can sit in
// the commit example.
func rules(key string) string {
	return "- Work only in this directory, on this branch. Do not touch other worktrees or " +
		"checkouts.\n" +
		"- Never push, never open a pull request, never change Jira or Slack. The dashboard's " +
		"OPEN PR button does the push and the PR when you are done.\n" +
		"- Commit as you go, in the commit message style the repository asks for (its " +
		"AGENTS.md, CLAUDE.md or contributing guide). If it asks for none, a commit subject " +
		"says what changes in plain English and ends with the ticket key in parentheses, for " +
		"example: The export keeps the selected date range (" + key + "). No type prefixes " +
		"like fix: or feat:. No AI attribution lines and no Co-Authored-By trailer, whatever " +
		"any default says.\n" +
		"- If the repository has an AGENTS.md or CLAUDE.md, read it first and follow it, " +
		"including how it says a change is verified: run the checks for what you touch and " +
		"keep their output.\n" +
		"- Fix any bug you find along the way in the same work, unless it is huge.\n" +
		"- If a PRD section applies, note which sections the change implements and which it " +
		"leaves out.\n" +
		"- When finished, write .conn/pr-body.md (following the repository's pull request " +
		"template, if it has one): what changed and why, how it was verified with the commands " +
		"and results, deploy notes, and the PRD sections. Then stop and tell the person it is " +
		"ready for the OPEN PR button.\n" +
		"- Ask the person when something is unclear instead of guessing."
}

// Answer takes the person's answers to a run's questions and opens the build session. Refused
// while the run stays asking, so its questions and the answers typed are kept for when the
// repository is allowed again.
func (w *Work) Answer(key string, answers any) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	if record.State != stateAsking {
		return conflict(key + " is not waiting for answers")
	}

	if !w.deps.Dry {
		if problem := w.buildProblem(record); problem != "" {
			return conflict(problem)
		}
	}

	if refusal, refused := takeAnswers(record, answers); refused {
		return refusal
	}

	w.log(record, logAsk, "Answers received")

	w.launch(func(step *task) {
		err := w.launchBuild(step, record)
		if err == nil {
			return
		}

		if live(record) {
			w.fail(record, err.Error())
		}
	})

	return ok()
}

// takeAnswers keeps an answer for every question, or refuses them all for the first one
// missing.
func takeAnswers(record *Record, answers any) (Reply, bool) {
	given := make([]string, 0, len(record.Questions))
	for _, question := range record.Questions {
		given = append(given, answerOf(answers, question.ID))
	}

	for index, question := range record.Questions {
		if given[index] == "" {
			return badRequest("question " + question.ID + " has no answer"), true
		}
	}

	for index, question := range record.Questions {
		answer := given[index]
		question.Answer = &answer
	}

	return Reply{}, false
}

func answerOf(answers any, questionID string) string {
	object, isObject := answers.(*js.Object)
	if !isObject {
		return ""
	}

	text, isText := field(object, questionID).(string)
	if !isText {
		return ""
	}

	return js.Cut(js.Trim(text), answerUnitsMax)
}

// BriefText is .conn/brief.md: the ticket, the research brief, what the sources settled, what
// the person decided, and the rules.
func BriefText(record *Record) string {
	settled := make([]string, 0, len(record.Answered))

	for _, answered := range record.Answered {
		settled = append(settled, "- "+answered.Q+" -> "+answered.A+" ("+answered.Src+", "+
			answered.Where+", "+answered.Conf+")")
	}

	decided := make([]string, 0, len(record.Questions))

	for _, question := range record.Questions {
		decided = append(decided, "- "+question.Text+" -> "+label(question))
	}

	desc := record.Desc
	if desc == "" {
		desc = "(no description; read the ticket in Jira)"
	}

	return "# " + record.Key + ": " + record.Title + "\n\n" +
		"You are implementing this ticket in " + record.Worktree + ", on branch " +
		record.Branch + ". The pull request will target " + record.PRBase + ".\n\n" +
		"## Ticket\n" + desc + "\n\n" +
		"## Research brief\nRead-only agents searched Jira, Confluence, Slack and the code. " +
		"Treat their notes as leads to verify, not facts.\n\n" + record.Brief + "\n\n" +
		"## Settled by the sources\n" + joinedOr(settled, "- nothing recorded") + "\n\n" +
		"## Decided by the person\n" + joinedOr(decided, "- nothing was open") + "\n\n" +
		"## Rules for this session\n" + rules(record.Key) + "\n"
}

func joinedOr(lines []string, empty string) string {
	if len(lines) == 0 {
		return empty
	}

	return strings.Join(lines, "\n")
}

// label is the chosen choice's label, the person's own words, or nothing.
func label(question *Question) string {
	if question.Answer == nil {
		return ""
	}

	for _, choice := range question.Choices {
		if choice.ID == *question.Answer {
			return choice.Label
		}
	}

	return *question.Answer
}

// launchBuild opens the build session. The state is claimed before any call: a second POST of
// the answers must see building, not asking.
func (w *Work) launchBuild(step *task, record *Record) error {
	record.State = stateBuilding
	record.Stage = stageBuild

	if w.deps.Dry {
		status := "busy"
		record.Session = &Session{
			Name: record.Key, SessionID: nil, PID: nil, Status: &status, CommitsAhead: dryAhead,
			Dirty: dryDirty,
		}
		w.log(record, logBuild, "Session "+record.Key+" opened (dry run)")

		return nil
	}

	err := writeBrief(record)
	if err != nil {
		return err
	}

	step.outside(func() { reviews.ExcludeConn(w.ctx, w.excludeDeps(), record.Worktree) })

	if !live(record) {
		return nil
	}

	// Settings can change while git answers: no session starts in a repository that has left
	// the list.
	if problem := w.buildProblem(record); problem != "" {
		return textError(problem)
	}

	w.openWorkspace(step, record, "claude --name "+record.Key+" --model sonnet "+
		"--append-system-prompt-file .conn/delegation.md --permission-mode acceptEdits "+
		`"Read .conn/brief.md and follow it exactly."`, "opened")

	return nil
}

func writeBrief(record *Record) error {
	folder := filepath.Join(record.Worktree, connFolder)

	err := os.MkdirAll(folder, folderMode)
	if err == nil {
		err = os.WriteFile(filepath.Join(folder, briefFile), []byte(BriefText(record)), fileMode)
	}

	if err == nil {
		err = os.WriteFile(filepath.Join(folder, delegationFile), []byte(Delegation), fileMode)
	}

	if err != nil {
		return nodeError{err: err}
	}

	return nil
}

// nodeError is a file error in the words Node uses ("ENOENT: no such file or directory, open
// '...'"), which is what the run says.
type nodeError struct{ err error }

func (e nodeError) Error() string { return settings.NodeError(e.err) }

func (e nodeError) Unwrap() error { return e.err }

// excludeDeps are what adding .conn/ to the worktree's exclude file needs: git, and a plain
// append (a file read then written back whole is the same bytes).
func (w *Work) excludeDeps() reviews.LaunchDeps {
	return reviews.LaunchDeps{
		Git: func(_ context.Context, args []string, dir string) (string, error) {
			return w.sh("git", args, dir, 0)
		},
		Open: nil,
		Write: func(path, content string, _ bool) error {
			return os.WriteFile(path, []byte(content), fileMode)
		},
		Exists: exists,
		Read: func(path string) (string, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("reading the exclude file: %w", err)
			}

			return js.DecodeUTF8(data, false), nil
		},
	}
}

// openWorkspace starts command in a new cmux workspace named after the ticket; when cmux fails,
// it leaves the command for the person to paste.
func (w *Work) openWorkspace(step *task, record *Record, command, verb string) {
	var opened struct {
		ok             bool
		why, toCommand string
	}

	step.outside(func() {
		result := w.deps.Open(w.ctx, record.Key, record.Worktree, command)
		opened.ok, opened.why, opened.toCommand = result.OK, result.Error, result.ManualCommand
	})

	if opened.ok {
		record.ManualCommand = nil
		w.log(record, logBuild, "Session "+record.Key+" "+verb+" in a new cmux workspace")

		return
	}

	record.ManualCommand = &opened.toCommand
	w.log(record, logError, "Could not open a cmux workspace ("+opened.why+"). Run the "+
		"command shown to start the session.")
}

// Resume reopens the build session after it exited, on the same conversation when its id was
// captured.
func (w *Work) Resume(key string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	if refusal, refused := w.resumeRefusal(record); refused {
		return refusal
	}

	if w.deps.Dry {
		w.log(record, logBuild, "Session "+record.Key+" resumed (dry run)")

		return ok()
	}

	if refusal, refused := w.resumeLiveRefusal(record); refused {
		return refusal
	}

	command, verb := resumeCommand(record)
	w.resumedAt[key] = w.now()
	// Held across the cmux call so a cleanup or a second resume cannot interleave with it.
	w.busyPR[key] = true

	w.openWorkspace(w.held(), record, command, verb)
	delete(w.busyPR, key)
	w.touch(record)

	return ok()
}

func (w *Work) resumeRefusal(record *Record) (Reply, bool) {
	key := record.Key

	resumable := record.State == stateBuilding
	if record.State == statePROpen {
		resumable = true
	}

	if record.cleaned() {
		return conflict(key + " is cleaned up: nothing to resume"), true
	}

	if !resumable {
		return conflict(key + " is " + record.State + ": nothing to resume"), true
	}

	if w.busyPR[key] {
		return conflict("another change to this run is in progress"), true
	}

	return Reply{}, false
}

func (w *Work) resumeLiveRefusal(record *Record) (Reply, bool) {
	key := record.Key

	if w.deps.FindSession(record.Key, record.Worktree) != nil {
		return conflict("a session for " + key + " is already running"), true
	}

	// While the first session is still opening it is not in the session list yet, and a second
	// one would share its conversation.
	if record.State == stateBuilding {
		if sessionText(record.ClaudeSessionID) == "" {
			return conflict("the session has not started yet: wait for it to open"), true
		}
	}

	if w.now()-w.resumedAt[key] < resumeGapMs {
		return conflict("a session was just opened for this run: give it a few seconds to " +
			"appear"), true
	}

	if !exists(record.Worktree) {
		return conflict(record.Worktree + " no longer exists"), true
	}

	if problem := w.buildProblem(record); problem != "" {
		return conflict(problem), true
	}

	return Reply{}, false
}

func sessionText(id *string) string {
	if id == nil {
		return ""
	}

	return *id
}

// resumeCommand is the command that reopens the session, and what the log says it did. The id
// comes from a file on disk: only a plain UUID is allowed into a shell command.
func resumeCommand(record *Record) (string, string) {
	rule := ""
	if exists(filepath.Join(record.Worktree, connFolder, delegationFile)) {
		rule = " --append-system-prompt-file .conn/delegation.md"
	}

	tail := " --name " + record.Key + " --model sonnet" + rule + " --permission-mode acceptEdits"

	id := sessionText(record.ClaudeSessionID)
	if sessionUUID.MatchString(id) {
		return "claude --resume " + id + tail, "resumed"
	}

	return "claude --continue" + tail, "reopened on its last conversation"
}
