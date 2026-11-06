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

// The rules that change when one session works in several worktrees: where it works, and where
// it writes the PR body.
const (
	workHere = "- Work only in this directory, on this branch. Do not touch other worktrees or " +
		"checkouts.\n"
	workInEach = "- Work only in these directories, each on its own branch. Do not touch other " +
		"worktrees or checkouts.\n"
	bodyHere   = "- When finished, write .conn/pr-body.md (following the repository's pull request "
	bodyInEach = "- When finished, write .conn/pr-body.md in each worktree with changes " +
		"(following its repository's pull request "
)

// rules are what the build session starts with: a session in one worktree's. key is a checked
// ticket key, so it can sit in the commit example.
func rules(key string) string { return rulesFor(key, workHere, bodyHere) }

// rulesFor are the rules with where the session works and where it writes the PR body.
func rulesFor(key, work, body string) string {
	return work +
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
		body + "template, if it has one): what changed and why, how it was verified with the " +
		"commands and results, deploy notes, and the PRD sections. Then stop and tell the " +
		"person it is ready for the OPEN PR button.\n" +
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
		if problem := w.launchProblem(record); problem != "" {
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

// BriefText is .conn/brief.md: where the session works, the ticket, the research brief, what the
// sources settled, what the person decided, and the rules. leg is the leg a split layout's
// session works in; nil is the shared session, which works in every leg. A run in one
// repository has one brief, whatever leg is.
func BriefText(record *Record, leg *Leg) string {
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

	return "# " + record.Key + ": " + record.Title + "\n\n" + briefWhere(record, leg) + "\n\n" +
		"## Ticket\n" + desc + "\n\n" +
		"## Research brief\nRead-only agents searched Jira, Confluence, Slack and the code. " +
		"Treat their notes as leads to verify, not facts.\n\n" + record.Brief + "\n\n" +
		"## Settled by the sources\n" + joinedOr(settled, "- nothing recorded") + "\n\n" +
		"## Decided by the person\n" + joinedOr(decided, "- nothing was open") + "\n\n" +
		"## Rules for this session\n" + briefRules(record, leg) + "\n"
}

// briefWhere says where the session works: the one worktree; every leg, one line each, for the
// shared session; or its own leg, then the others and that other sessions work there.
func briefWhere(record *Record, leg *Leg) string {
	if len(record.Repos) == 1 {
		lead := record.lead()

		return "You are implementing this ticket in " + lead.Worktree + ", on branch " +
			lead.Branch + ". The pull request will target " + lead.PRBase + "."
	}

	if leg == nil {
		lines := make([]string, 0, len(record.Repos))
		for _, each := range record.Repos {
			lines = append(lines, legLine(each))
		}

		return "You are implementing this ticket in these repositories, each in its own " +
			"worktree on its own branch. This session starts in the first and can reach the " +
			"others.\n" + strings.Join(lines, "\n")
	}

	others := make([]string, 0, len(record.Repos))

	for _, each := range record.Repos {
		if each != leg {
			others = append(others, legLine(each))
		}
	}

	working := "Other sessions work there"
	if len(others) == 1 {
		working = "Another session works there"
	}

	return "You are implementing this ticket in " + leg.Repo + " at " + leg.Worktree +
		", on branch " + leg.Branch + ". The pull request will target " + leg.PRBase + ".\n\n" +
		"This run also builds in these repositories:\n" + strings.Join(others, "\n") +
		"\n" + working + ": do not change them. If a change is needed outside " + leg.Repo +
		", ask the person."
}

// legLine is one leg in a brief: its repository, worktree, branch and PR target.
func legLine(leg *Leg) string {
	return "- " + leg.Repo + " in " + leg.Worktree + ", on branch " + leg.Branch +
		". The pull request will target " + leg.PRBase + "."
}

// briefRules are the session's rules: one worktree's, unless the session is the shared one of a
// run in several repositories.
func briefRules(record *Record, leg *Leg) string {
	if len(record.Repos) == 1 {
		return rules(record.Key)
	}

	if leg != nil {
		return rules(record.Key)
	}

	return rulesFor(record.Key, workInEach, bodyInEach)
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

// launchBuild opens the build sessions: one in the lead's worktree that reaches every other
// (shared), or one in each worktree (split). The state is claimed before any call: a second POST
// of the answers must see building, not asking.
func (w *Work) launchBuild(step *task, record *Record) error {
	record.State = stateBuilding
	record.Stage = stageBuild

	if w.deps.Dry {
		w.dryBuild(record)

		return nil
	}

	slots := w.sessionSlots(record)

	for _, slot := range slots {
		err := writeBrief(record, slot, briefFor(record, slot))
		if err != nil {
			return err
		}
	}

	// Every worktree excludes .conn/: the shared session writes its PR body in each one.
	worktrees := worktreesOf(record)

	step.outside(func() {
		for _, worktree := range worktrees {
			reviews.ExcludeConn(w.ctx, w.excludeDeps(), worktree)
		}
	})

	if !live(record) {
		return nil
	}

	// Settings can change while git answers: no session starts in a repository that has left
	// the list, nor reaches one. A split session whose own repository may not build is left
	// unlaunched while the others open.
	if problem := w.launchProblem(record); problem != "" {
		return textError(problem)
	}

	for _, slot := range slots {
		if !live(record) {
			return nil
		}

		if problem := w.slotProblem(record, slot); problem != "" {
			w.log(record, logError, w.sessionNamed(record, slot)+" not opened: "+problem)

			continue
		}

		markLaunched(record, slot)
		w.openWorkspace(step, record, slot, launchCommand(record, slot), "opened")
	}

	return nil
}

// launchCommand is the command that starts a slot's session for the first time.
func launchCommand(record *Record, slot *Leg) string {
	return "claude --name " + record.Key + " --model sonnet --append-system-prompt-file " +
		".conn/delegation.md" + reachFlags(record, slot) + " --permission-mode acceptEdits " +
		`"Read .conn/brief.md and follow it exactly."`
}

// dryBuild opens the dry run's sessions: each slot's is busy, and every leg has commits.
func (w *Work) dryBuild(record *Record) {
	status := "busy"

	for _, leg := range record.Repos {
		leg.Ahead, leg.Dirty = dryAhead, dryDirty
	}

	for _, slot := range w.sessionSlots(record) {
		markLaunched(record, slot)
		slot.Session = &Session{Name: record.Key, SessionID: nil, PID: nil, Status: &status}

		w.log(record, logBuild, w.sessionNamed(record, slot)+" opened (dry run)")
	}
}

// briefFor is the brief a slot's session is given: its own leg's in the split layout, the run's
// in the shared one.
func briefFor(record *Record, slot *Leg) *Leg {
	if record.Layout == layoutSplit {
		return slot
	}

	return nil
}

func writeBrief(record *Record, slot, leg *Leg) error {
	folder := filepath.Join(slot.Worktree, connFolder)

	err := os.MkdirAll(folder, folderMode)
	if err == nil {
		err = os.WriteFile(filepath.Join(folder, briefFile), []byte(BriefText(record, leg)),
			fileMode)
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
