package openwork

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/drysession"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
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

// sessionUUID is Claude's session id shape: only that goes into an argument.
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
		w.dryBuild(step, record)

		return nil
	}

	slots, err := w.prepareSlots(record)
	if err != nil {
		return err
	}

	// .conn/ is excluded in every worktree a session works in: the shared session writes its PR
	// body in each one. A split slot left unlaunched is excluded when it is launched.
	worktrees := worktreesOf(record)
	if record.Layout == layoutSplit {
		worktrees = legWorktrees(slots)
	}

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
		w.openWorkspace(step, record, slot, LaunchSpec(w.deps.Claude, record, slot), "opened")
	}

	return nil
}

// prepareSlots writes the brief of each slot that may launch now, and are those slots. The
// shared session's brief is all or nothing. A split slot whose repository may not build, or
// whose brief cannot be written, is said in the log and left unlaunched, with no file written:
// RESUME SESSION launches it later. Only when no split slot is left is it an error.
func (w *Work) prepareSlots(record *Record) ([]*Leg, error) {
	if record.Layout != layoutSplit {
		lead := record.lead()

		err := writeBrief(record, lead, nil)
		if err != nil {
			return nil, err
		}

		return []*Leg{lead}, nil
	}

	ready := make([]*Leg, 0, len(record.Repos))

	var failed error

	for _, slot := range record.Repos {
		if problem := w.buildProblem(slot); problem != "" {
			w.log(record, logError, w.sessionNamed(record, slot)+" not opened: "+problem)

			continue
		}

		err := writeBrief(record, slot, slot)
		if err != nil {
			failed = err
			w.log(record, logError, w.sessionNamed(record, slot)+" not opened: "+err.Error())

			continue
		}

		ready = append(ready, slot)
	}

	if len(ready) > 0 {
		return ready, nil
	}

	if problem := w.launchProblem(record); problem != "" {
		return nil, textError(problem)
	}

	return nil, failed
}

// legWorktrees are the legs' worktrees, in order.
func legWorktrees(legs []*Leg) []string {
	worktrees := make([]string, 0, len(legs))
	for _, leg := range legs {
		worktrees = append(worktrees, leg.Worktree)
	}

	return worktrees
}

// LaunchSpec is what starts a slot's session for the first time: claude in the slot's worktree,
// named for the ticket, reaching the other legs' worktrees when it is the shared session, told to
// read the brief. The ticket's title, description and answers are in the brief, never in an
// argument; the key is a checked ticket key and each folder is one argument.
func LaunchSpec(claude string, record *Record, slot *Leg) pty.Spec {
	argv := []string{
		claude, "--name", record.Key, "--model", "sonnet",
		"--append-system-prompt-file", ".conn/delegation.md",
	}
	argv = append(argv, reachArgs(record, slot)...)
	// -- ends the options: nothing after it is read as one, whatever a later change puts there.
	argv = append(argv, "--permission-mode", "acceptEdits",
		"--", "Read .conn/brief.md and follow it exactly.")

	return pty.Spec{Argv: argv, Dir: slot.Worktree, Env: nil, Name: record.Key}
}

// dryBuild opens the dry run's sessions: each slot's is busy, and every leg has commits. Inside
// Conn, each slot also gets the dry run's stand-in session on the host (openDry).
func (w *Work) dryBuild(step *task, record *Record) {
	status := "busy"

	for _, leg := range record.Repos {
		leg.Ahead, leg.Dirty = dryAhead, dryDirty
	}

	slots := w.sessionSlots(record)

	for _, slot := range slots {
		markLaunched(record, slot)
		slot.Session = &Session{Name: record.Key, SessionID: nil, PID: nil, Status: &status}

		w.log(record, logBuild, w.sessionNamed(record, slot)+" opened (dry run)")
	}

	// The record says every slot's session first, so no one sees half of them while the host
	// is asked.
	for _, slot := range slots {
		w.openDry(step, record, slot)
	}
}

// openDry starts the dry run's stand-in session for a slot inside Conn, when sessions open
// there: a script in this process (drysession), in the slot's worktree, so the terminal can be
// tried with nothing run. Set to open elsewhere, the dry run opens nothing, as before.
func (w *Work) openDry(step *task, record *Record, slot *Leg) {
	if w.deps.Settings().Sessions.Where != settings.WhereInside {
		return
	}

	var opened launch.Opened

	spec := drysession.Spec(slot.Worktree, record.Key)

	step.outside(func() { opened = w.deps.Open(w.ctx, spec) })

	if !opened.OK {
		w.log(record, logError, w.sessionNamed(record, slot)+": could not open the dry run's "+
			"session ("+opened.Error+")")
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

// writeBrief writes a slot's .conn/brief.md (for leg, or the whole run) and .conn/delegation.md,
// private, through reviews.WriteLaunchFile: only into a .conn folder of the person's own (not a
// link a repository committed), and never through a link at either file.
func writeBrief(record *Record, slot, leg *Leg) error {
	folder := filepath.Join(slot.Worktree, connFolder)

	err := reviews.WriteLaunchFile(filepath.Join(folder, briefFile), BriefText(record, leg), false)
	if err == nil {
		err = reviews.WriteLaunchFile(filepath.Join(folder, delegationFile), Delegation, false)
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
