# Open work across several repositories: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A run holds one or more repositories ("legs"), chosen per ticket when it starts, each with its own worktree, branch and PR, worked in one shared session or one session per leg.

**Architecture:** Per-repository facts move from the top of `Record` into `Record.Repos []*Leg`; old files read as one leg. Server functions that used `record.Worktree` and friends take a `*Leg`; run-level actions loop over legs. The pages change in the same PR as the routes they read.

**Tech Stack:** Go (`internal/openwork`, `internal/settings`, `internal/sessions`, `internal/app`), Svelte 5 + TypeScript (`src/`), shared fixtures in `testdata/answers/`.

**Spec:** `docs/superpowers/specs/2026-11-05-multi-repo-open-work-design.md` (read it first; the plan only adds what the spec leaves open).

## Global Constraints

- AGENTS.md and CLAUDE.md rules apply to every Go change: no `&&`/`||` (internal/guard fails the tests), functions at most 60 lines, lines at most 100 columns (tab = 4), no abbreviations, units last in names, named constants for every limit (`legsMax = 6`), every error handled, no package-level state, doc comments on every package and exported name, tables for multi-case tests with `t.Parallel`.
- Never `//nolint`. `make check`, `make test` and `make lint` (0 issues) pass before every commit that ends a task.
- Single-repository behaviour must not change: `BriefText`, log lines, commands, polling and the dry walk are byte-identical for a run with one leg (golden files prove it).
- JSON keys of a record keep the order `ordered()` gives; legacy top-level keys are dropped on write, unknown keys are kept.
- `legsMax = 6`. Branch name is `<key lowercase>-<slug>` in every leg. Sessions are all named after the ticket key and found by folder.
- Defaults: `reviewMove` `"all"`, `sessionLayout` `"shared"`. `PUT /api/settings/atlassian` leaving a key out keeps it; `baseBranches` replaces the whole map.
- Commit after each task. Do not push; the controller pushes once at the end.

## Review Focus

- Retry after a failure in the second leg: the first leg's worktree is reused, not re-made, and the failure names the repository.
- A run file written by the current version (top-level `worktree`, `pr`, `session`, `claudeSessionId`, `cleaned`) opens, displays, polls its PR and cleans up exactly as before.
- OPEN PR with one leg that has no commits and one that has: one PR opens, the log says which was skipped, the review move follows `reviewMove`.
- OPEN PR where leg two's push fails: leg one's PR stays recorded and the run is `pr_open`; pressing again retries only leg two.
- Two chosen repositories that are the same name in different case, or the same clone folder: refused with one message.
- A repository leaves the allowlist after start: every push, resume, session launch and cleanup for that leg refuses, naming it; the other legs are unaffected.
- Split layout: FIX FAILING CHECKS types each red leg's instruction into that leg's session, never another's.

---

### Task 1: Settings keys and the Atlassian card

**Files:**
- Modify: `internal/settings/settings.go`, `read.go`, `store.go`, their tests, `internal/dryseed/dryseed.go` (+test), `testdata/answers/settings.json`
- Modify: `src/lib/settingsModel.ts`, `settingsApi.ts`, `src/pages/SettingsServices.svelte`, their tests, `docs/settings.md`

**Interfaces:**
- Produces (Go): `settings.Jira` gains `BaseBranches map[string]string` (repo lowercase to branch), `ReviewMove string`, `SessionLayout string`; exported constants `ReviewFirst = "first"`, `ReviewAll = "all"`, `ReviewNever = "never"`, `LayoutShared = "shared"`, `LayoutSplit = "split"`. `func (j Jira) BaseFor(repo string) *string` returns the per-repo entry, else `BaseBranch`, else nil.
- Produces (TS): `Atlassian.jira` has `baseBranches: Record<string,string>`, `reviewMove`, `sessionLayout`.

- [ ] **Step 1:** Write failing table tests in `settings_test.go`: defaults (`reviewMove` all, `sessionLayout` shared, `baseBranches` empty); a bad `reviewMove` and a bad `sessionLayout` and a bad branch in `baseBranches` are all reported in one result; an entry for a repo not on the allowlist is dropped on save; a PUT without the keys keeps them; `BaseFor` precedence per-repo, global, nil.
- [ ] **Step 2:** Run `go test ./internal/settings/...`; expect FAIL.
- [ ] **Step 3:** Implement in `read.go`/`store.go`/`settings.go` following `readBaseBranch`/`readBuildRepo`/`hasBaseBranch`; update `testdata/answers/settings.json` and the dry seed (allowlist must hold at least two repositories with clones, `buildRepo` unchanged).
- [ ] **Step 4:** Write failing TS tests in `settingsServices.test.ts` and `settingsApi.test.ts`: the model round-trips the three keys; the saved-text in `settingsModel.ts:372` reads "default repository <repo>" or nothing when none.
- [ ] **Step 5:** Change `SettingsServices.svelte` (~300-325): the `jira-build` select is labelled "Default repository (optional)" with "None" as a real option; the Base branch input is labelled "Base branch for every repository without its own", followed by one optional input per allowed repository; two selects, "Move the ticket to review" (After the first PR / After every PR / Never, I'll do it myself) and "Sessions across repositories" (One shared session / One session per repository), each with a one-line help.
- [ ] **Step 6:** Update the Atlassian paragraph of `docs/settings.md`. Run `make check test lint` and `npm test`; expect PASS. Commit.

### Task 2: Legs in the record, no behaviour change

**Files:**
- Modify: `internal/openwork/record.go`, `start.go`, `build.go`, `pr.go`, `tick.go`, `runs.go`, `research.go`, `dry.go`, every `*_test.go` and `export_test.go` in the package, `src/lib/types.ts`, `src/pages/TicketRun.svelte`, `OpenWork.svelte`, `testdata/answers/*` that hold a run

**Interfaces:**
- Produces: `type Leg struct { Repo string; Clone string (server-only); Base, PRBase, Branch, Worktree string; WorktreeReady bool; Session *Session; ClaudeSessionID *string; ManualCommand *string; Ahead, Dirty float64; PR *PR; Cleaned *bool }` with JSON tags `repo, clone, base, prBase, branch, worktree, worktreeReady, session, claudeSessionId, manualCommand, ahead, dirty, pr, cleaned`; `Session` loses `CommitsAhead`/`Dirty`.
- Produces: `Record.Repos []*Leg`, `Record.Layout string`; `func (r *Record) lead() *Leg`; `func (r *Record) legFor(repo string) (*Leg, bool)`; `const legsMax = 6`; `func (l *Leg) cleaned() bool`.
- Produces: `Run` (page view) has `Repos []*LegRun` (the leg without `clone` and `worktreeReady`) and `Layout`; no top-level `base, prBase, branch, worktree, pr, session, claudeSessionId, manualCommand, cleaned`.
- Consumes: nothing. Later tasks call `legFor`, `lead`, and take `*Leg`.

- [ ] **Step 1:** Write failing tests in `record_test.go`: (a) a legacy record file (use an existing fixture shape: top-level `worktree`, `branch`, `base`, `prBase`, `repo`, `clone`, `pr`, `session` with `commitsAhead`/`dirty`, `claudeSessionId`, `cleaned`) reads as one leg with every field mapped and `Layout` `"shared"`; (b) writing it back drops the legacy keys, keeps an unknown key and the order of the keys that remain; (c) a legacy run from before Settings (no `repo`) keeps `Repo` empty and `repoOf` still asks the worktree's origin.
- [ ] **Step 2:** Run it; expect FAIL.
- [ ] **Step 3:** Implement the types, the legacy mapping in `readRecord`/`settleRead`, `writeRecord` skipping the legacy keys (the `skip` argument of `ordered`), `viewJSON` building `Run`. Then convert every function that used `record.Worktree/Branch/Base/PRBase/PR/Session/ClaudeSessionID/ManualCommand/Cleaned/WorktreeReady/Repo/Clone` to take `leg *Leg` (operating on `record.lead()` for now) and fix the tests mechanically (`export_test.go` builders make one-leg records). Counts of commits and dirty files go to `leg.Ahead`/`leg.Dirty`.
- [ ] **Step 4:** Update `types.ts` (`Run.repos: RunRepo[]`, `layout`), and make `TicketRun.svelte` and `OpenWork.svelte` read `run.repos[0]` wherever they read the removed fields, plus `sessionLive`'s `cwd` check over all legs. Regenerate golden files and `testdata/answers` with `CONN_UPDATE_GOLDEN=1 go test ./...`; read the diff: only the run shape may change.
- [ ] **Step 5:** `make check test lint`, `npm test`; expect PASS with the brief and log goldens unchanged. Commit.

### Task 3: Choosing repositories and making their worktrees

**Files:**
- Modify: `internal/openwork/target.go`, `start.go`, `runs.go`, `list.go`, `api.go` (+tests), `src/lib/types.ts`, `src/pages/OpenWork.svelte`, `src/lib/openWorkSource.test.ts`, `docs/open-work.md`

**Interfaces:**
- Consumes: `Settings.Atlassian.Jira.BaseFor`, `Leg`, `legsMax`.
- Produces: `func BuildTargets(current settings.Settings, find CloneFinder, defaultOf func(clone string) (string, bool)) []Target` (one per allowed repo, allowlist order, `Reason` set when not ready; `BuildTarget` is deleted); `func ChooseTargets(all []Target, chosen []string, fallback *string) ([]Target, []string)` returning the chosen targets in order and every problem sentence ("Choose at least one repository.", duplicates compared case-insensitively and by clone folder, not allowed, not ready, more than `legsMax`); `Build{Repos []BuildRepo; Default *string; Layout string}` and `BuildRepo{Repo string; Base, Reason *string; Ready bool}` in `GET /api/work`.
- `Work.Start(key, body)` reads `repos` (list of strings) and `layout`; problems are one 409 (400 for a malformed list) message.

- [ ] **Step 1:** Write failing table tests for `BuildTargets` and `ChooseTargets` (pure values, no disk): ready, no clone, no default branch and no base, per-repo base beats global beats default, empty choice with and without `buildRepo`, duplicates by case, same clone twice, seven repositories, all problems in one result.
- [ ] **Step 2:** Run; expect FAIL. Implement `target.go`. 
- [ ] **Step 3:** Failing tests in `gates_test.go`/`git_test.go` (using the package's world builders): START with two repos makes two legs, two worktrees named `<clone>-<key>`, branch `<key>-<slug>` in both; Retry after leg two's `git worktree add` fails reuses leg one and the failure names leg two's repository; starting a ticket again with an overlapping set reuses the shared worktree and refuses per shared repository when the old branch was cut from another base, and the log names a dropped leg's worktree.
- [ ] **Step 4:** Implement: `startTicket` resolves targets, `newRecord` builds legs and `Layout`; `makeWorktree` loops over legs not `WorktreeReady`; `buildProblem` takes a leg (repo allowed, clone found again, the old "Open work builds in X now" check is deleted); `Tickets()` fills `Build`.
- [ ] **Step 5:** `OpenWork.svelte`: a checkbox list of `build.repos` (disabled and explained when not ready, showing the base), a layout choice shown when two or more are ticked, START disabled with "Choose a repository" when none; selection and layout stored in `localStorage` under keys of your choosing (try/catch, works without), preselect = remembered ∩ ready, else `build.default`. Add the source test. Update `docs/open-work.md` (start section).
- [ ] **Step 6:** `make check test lint`, `npm test`; expect PASS. Commit.

### Task 4: Research, brief and sessions across legs

**Files:**
- Modify: `internal/openwork/research.go`, `build.go`, `tick.go`, `runs.go`, `dry.go` (+tests, `testdata/` golden briefs), `internal/sessions/store.go` and `internal/app/work.go` if the worktree match needs it

**Interfaces:**
- Consumes: Task 2 and 3 types.
- Produces: `func BriefText(record *Record, leg *Leg) string` (leg nil: the shared brief; a leg: that leg's brief in split layout); `func (w *Work) sessionSlots(record *Record) []*Leg` (shared: `[lead]`; split: every leg); `Work.Resume(key, repo string)`.

- [ ] **Step 1:** Failing golden tests (`testdata/` via `CONN_UPDATE_GOLDEN`): the one-leg brief equals today's file byte for byte; a two-leg shared brief lists both legs and says "Work only in these directories, each on its own branch" and asks for `.conn/pr-body.md` in each worktree with changes; a split brief names the other repository and says another session works there.
- [ ] **Step 2:** Run; expect FAIL. Implement `BriefText` and the research prompt (names every leg; unchanged text for one leg; extra folders passed if the helper allows it, otherwise listed in the prompt).
- [ ] **Step 3:** Failing tests: shared layout opens one workspace in the lead worktree whose command carries `--add-dir <other worktree>` per other leg; split opens one per leg, each with its own brief and `ClaudeSessionID`; a failed cmux call sets that slot's `ManualCommand` only; `followSession` and `followGit` keep every slot and every leg current (`Ahead`/`Dirty` on each leg); `Resume` without `repo` reopens every dead slot, with `repo` one; refusals (live session, gap, missing worktree, repository not allowed) are per slot and name the repository.
- [ ] **Step 4:** Implement; make the Sessions page match a session in any leg's worktree to its run (`Worktree()` stays the lead's; add what the store needs, e.g. `Worktrees() []string`, and use it in `hidden`). The route regexp and `handlers()` pass `repo` from the body to `Resume`.
- [ ] **Step 5:** `make check test lint`. Expect PASS. Commit.

### Task 5: Pull requests, review move, polling, fixes, merge, cleanup

**Files:**
- Modify: `internal/openwork/pr.go` (split into `pr_open.go`, `pr_fix.go`, `pr_cleanup.go` if it passes ~600 lines), `tick.go`, `dry.go`, `api.go` (+tests), `internal/dryseed`

**Interfaces:**
- Produces: `Work.OpenPR(key, repo string) Reply`, `Work.FixChecks(key, repo string) (Reply, error)`, `Work.Cleanup(key, repo string) Reply`; `Record.ReviewMoved bool` (`json:"reviewMoved,omitempty"`, server-only); `func reviewDue(policy string, opened, withCommits, withPR int, moved bool) bool` (pure); `func merged(record *Record) bool` per the spec (settled = merged or closed, at least one merged).
- Poll bookkeeping maps (`lastPoll`, `polling`, `pollError`, `lastGit`, `busyPR`) are keyed by `legKey(key, repo string) string`.

- [ ] **Step 1:** Failing table test for `reviewDue` (first, all, never; moved already; no commits legs ignored; zero PRs). Failing tests for `merged` (closed-unmerged settled, none merged, leg with commits and no PR does not block).
- [ ] **Step 2:** Run; expect FAIL; implement both.
- [ ] **Step 3:** Failing tests, one per Review Focus line on PRs: no-commit leg skipped with a log line; leg two's push fails and leg one's PR stays, state `pr_open`, second press retries leg two only; per-leg refusals do not stop the others; `repo` selects one leg; disallowed repository refuses that leg naming it; the review move runs once and is retried after a failed move; split-layout FixChecks types into the right session; Cleanup per leg with `repo` and without; polling reads each leg's PR on its own schedule and logs a repeating error once per leg.
- [ ] **Step 4:** Implement. OPEN PR may run in `building` and `pr_open`; the loop is sequential and holds no lock across outside calls; the move uses the existing `moveJira(MoveReview())` and sets `ReviewMoved` when Jira answered. The dry walk covers each leg (PR numbers derived from key and leg index; the single-leg numbers are unchanged).
- [ ] **Step 5:** `make check test lint`. Expect PASS. Commit.

### Task 6: The run page

**Files:**
- Modify: `src/pages/TicketRun.svelte`, `src/lib/types.ts`, new helper `src/lib/runLegs.ts` (+ tests), the source tests that read the page

**Interfaces:**
- Consumes: `Run.repos`, the routes of Tasks 4 and 5 with `{ repo }` bodies.
- Produces: `runLegs.ts` exports pure helpers `legsWithCommits(run)`, `legsWithoutPr(run)`, `canOpenAll(run)`.

- [ ] **Step 1:** Failing tests for the helpers and for a source test that the page posts `repo` for the per-leg buttons.
- [ ] **Step 2:** Implement: with one leg the page is as it was; with more, one row per leg (repository, branch and base, commits and changed files, PR with checks and review, its OPEN PR, FIX FAILING CHECKS, RESUME SESSION (shared: lead row only), REMOVE WORKTREE), OPEN ALL PRS when `canOpenAll`, and a note on a leg with commits and no PR after the others merged.
- [ ] **Step 3:** `npm test`, `make check test lint`. Commit.

### Task 7: Documentation and an end-to-end dry run

**Files:**
- Modify: `docs/open-work.md`, `README.md` (Open work line), `CHANGELOG`/release notes file if the repo has one

- [ ] **Step 1:** Rewrite the Open work doc for the new flow (choose repositories per ticket, layouts, review move, per-leg PRs, what is written where, the one-way file note). Remove every mention of `buildRepo` as required.
- [ ] **Step 2:** Run `CONN_WORK_DRY=1` with the dry seed; in the app start a ticket with two repositories in each layout and walk to merged; record what was seen.
- [ ] **Step 3:** `make check test lint`, `npm test`, `npm run build` if the repo has it. Commit.
