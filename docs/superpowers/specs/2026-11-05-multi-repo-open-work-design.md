# Open work across several repositories

## Why

Open work builds a Jira ticket in exactly one repository, the one named by
`atlassian.jira.buildRepo` in Settings. Nobody works in a single repository for every ticket, and
some tickets span several. A person who opens Settings on day one is asked to pick the one repo
every ticket will ever be built in, and the dropdown lets them pick one.

Conn is open source. Teams differ in how a cross-repo ticket should be worked and when Jira should
say it is in review, so those behaviours are choices with defaults, not rules.

## What changes, in one paragraph

The repository (or repositories) is chosen **per ticket, when the run is started**, not in
Settings. A run holds one or more **legs**; a leg is one repository's clone, worktree, branch and
pull request. A run is worked in **one shared session** across its legs or **one session per leg**.
Each leg gets its own pull request. When the ticket moves to review is a setting. A repository's
base branch is optional per repository. The old `buildRepo` stays as an optional default for the
picker. Runs and settings files written by the current version keep working.

## Goals

- Start a run for a ticket in any allowed repository, or in several.
- Remember the person's last choice so the common case is one click.
- One run per ticket still: one research stage, one brief, one Jira ticket.
- Every safety rule that holds for one repository holds for each leg: allowlist, push guards,
  branch naming, the `gh` call naming its repository.
- Both session layouts and all three review-move policies work and are tested.
- A run made by the current version reads, displays and finishes exactly as before.

## Non-goals

- Repositories that must merge in a set order, or PR-to-PR links beyond the PR body.
- Different branch names per leg (every leg uses `<key lowercase>-<slug>`).
- Starting several runs for one ticket. A ticket has one run.
- Adding or dropping a repository from a run that has started. Cancel and start again.
- Cloning repositories. As today, a clone must already exist.
- Per-workflow Jira rules beyond the review-move choice.

## Anti-goals (a failure even if it works)

- A Settings page that again forces a repository choice before Open work is usable.
- Two representations of one fact: the repository's worktree, branch and PR are stored once, in
  the leg, not also at the top of the run.
- A change to the single-repository experience beyond what the picker needs: its brief text,
  commands, log lines and polling must not change.

## Behaviour

### Settings (`atlassian.jira`)

| Key | Meaning | Default |
|---|---|---|
| `buildRepo` | Optional. The repository preselected in the picker when the browser has no earlier choice. Cleared when it leaves the allowlist (as now). | none |
| `baseBranch` | Optional. A base branch for every repository without its own. | none |
| `baseBranches` | Optional map, repository to branch. Entries for repositories that leave the allowlist are dropped on save. | `{}` |
| `reviewMove` | `"first"`, `"all"` or `"never"`: move the ticket to review after the first PR opens, after every leg with changes has a PR, or never. | `"all"` |
| `sessionLayout` | `"shared"` or `"split"`: the layout the picker starts on. | `"shared"` |

The base branch of a leg is `baseBranches[repo]`, else `baseBranch`, else the clone's default
branch read from disk as now. A branch name is checked by `settings.ParseBranch`. As for the
existing keys, a `PUT /api/settings/atlassian` that leaves a key out keeps it, and the card sends
its explicit Save. Unknown values of `reviewMove` or `sessionLayout` are rejected with a message
naming the allowed values, and every problem in a body is reported at once.

The Atlassian card: the "Open work builds in" dropdown becomes **Default repository (optional)**,
with None as a real choice; **Base branch** is labelled as applying to repositories without their
own, followed by an optional base branch input per allowed repository; two small selects for
**Move the ticket to review** and **Sessions across repositories**, each with a one-line help text.

### Starting a run

`GET /api/work` carries `build`, replacing the single-repository object:

```
build: {
  repos: [{ repo, base, ready, reason }],  // every allowed repository, in allowlist order
  default: "owner/name" | null,            // atlassian.jira.buildRepo, when still allowed
  layout: "shared" | "split",              // atlassian.jira.sessionLayout
}
```

`base` and `reason` are `null` when absent; `ready` is false with a reason such as "No local clone
of acme/api: set a folder in Settings." The page disables a repository that is not ready and says
why next to it.

`POST /api/work/<KEY>/start` takes `{ model, force?, repos?, layout? }`. `repos` is a list of 1 to
`legsMax` (6) repository names, in order; the first is the **lead**. Absent or empty, the request
means `[buildRepo]`; with no `buildRepo` it is refused with "Choose at least one repository." A
duplicate, an unknown name or too many is a 400. Every chosen repository must be allowed and ready;
all problems are returned together, one sentence per repository. `layout` defaults to
`sessionLayout`.

The start panel gets a multi-select of the allowed repositories (a checkbox list, each row showing
its resolved base branch), a two-way choice for the layout that appears only with two or more
repositories chosen, and START. The selection and layout are remembered in the browser
(`localStorage`, wrapped in try/catch; the page works without it). With no remembered choice the
selection is `default`; with neither, nothing is selected and START says "Choose a repository".

### The run

The run record keeps what is about the ticket (key, title, model, state, stage, Jira status, the
research agents, what was answered, the questions, the log, the brief, the error). Everything about
a repository moves into `repos`, an ordered list of legs:

```
leg: { repo, clone, base, prBase, branch, worktree, worktreeReady,
       session, claudeSessionId, manualCommand, ahead, dirty, pr, cleaned }
```

and the run gains `layout`. `clone` stays server-only, as now; `repo` is shown, because each row of the page names its repository. The run the page reads
has `repos` and `layout` and none of the old top-level `base`, `prBase`, `branch`, `worktree`,
`pr`, `session`, `claudeSessionId`, `manualCommand`, `cleaned`. `ahead` and `dirty` leave
`session` and live on the leg, so the page can say which legs have work.

**Old records.** A record with none of `repos` is read as a run with one leg made from its
top-level fields, layout `"shared"`. It is written back in the new shape, and its legacy keys are
dropped from the file. Keys the code does not know are still kept where they were. Runs from before
Settings (no `repo`) keep working: the repository is read from the worktree's origin as now.

### Pipeline

1. **Worktrees.** For each leg in order: `git fetch origin <base>` in its clone, then add the
   worktree beside the clone and branch, exactly as today. A leg whose `worktreeReady` is true is
   skipped, so Retry resumes where it stopped. A failure in any leg fails the run with the leg's
   repository in the message; legs already made stay.
2. **Jira in progress.** Once, after the first leg is ready, as today.
3. **Research.** One research stage for the run. The code mapper's prompt names every leg's
   repository and worktree and it may read all of them. A single-leg run's prompt is unchanged.
4. **Brief.** One brief text for the run, written to `.conn/brief.md` in the lead's worktree (shared
   layout) or in every worktree (split layout). A single-leg run's brief is byte-identical to today's. With
   several legs the line naming the worktree and branch becomes a list, one line per leg (repository,
   worktree, branch, PR target), the rule "Work only in this directory" becomes "Work only in these
   directories, each on its own branch", and the PR-body rule asks for `.conn/pr-body.md` in each
   worktree with changes. In split layout each brief also names the other repositories and says other
   sessions work there, and to ask the person if a change is needed outside this repository.
5. **Sessions.** Shared: one cmux workspace and one `claude` session, started in the lead's
   worktree with each other worktree added with `--add-dir`. Split: one workspace and session per
   leg, each in its own worktree. Every session is named after the ticket key (so the Sessions page
   shows them as build sessions) and is found by its folder. Only a leg that has a session has a
   `session`, `claudeSessionId` and `manualCommand`; in shared layout that is the lead only.

### Pull requests

- **OPEN PR** (`POST /api/runs/<KEY>/pr`) takes an optional `{ repo }`. Absent, it opens the PR of
  every leg that has commits and no PR yet; present, only that leg's. It runs in `building` and in
  `pr_open` (while some leg with commits has no PR). A leg with no commits ahead is skipped with a
  log line; if no named or selected leg has commits the answer is today's "the branch has no commits
  yet". Each leg is checked separately by the checks that exist today (plain branch names, not
  protected, key-prefixed branch, repository allowed, GitHub on) and one leg's refusal does not stop
  the others. The legs are pushed and opened one after another. A failure leaves the legs that
  succeeded in place and is reported with its repository; pressing OPEN PR again retries the legs
  that have no PR.
- **State.** The run is `pr_open` once any leg has a PR.
- **Review move.** After a PR opens, with `reviewMove` `"first"`: move the ticket if no PR was open
  before. `"all"`: move it when no leg that has commits lacks a PR. `"never"`: never. The move is
  the existing one (a status of Jira's review category named so, exactly one match) and happens at
  most once per run.
- **Polling** follows each leg's open PR on its own schedule, with the same intervals and the same
  once-only error logging, keyed by ticket and repository.
- **FIX FAILING CHECKS** (`fix-checks`) takes an optional `{ repo }`. Absent, it covers every leg
  whose checks are failing, one instruction per leg naming that leg's checks and repository, typed
  into the session that works there. Its refusals are per leg as today.
- **Merged.** The run counts as merged when every leg with a PR has merged and there is at least
  one. **MOVE JIRA TO** is available then, as now.
- **REMOVE WORKTREE** (`cleanup`) takes an optional `{ repo }`; absent, every leg whose PR merged or
  closed and is not yet cleaned. Its safety rules apply to each worktree exactly as now (never
  forced, only a linked worktree git lists, refused while a session in it runs or files are
  uncommitted).

### The run page

The branch line, PR chip, RESUME SESSION, OPEN PR, and REMOVE WORKTREE become per-leg rows when the
run has more than one leg: one row per repository showing its branch and base, its commits and
changed files, its PR with checks and review, and the buttons that apply to that leg. A run with one
leg looks as it does today. A footer button OPEN ALL PRS appears when more than one leg has commits
and no PR. The ticket-level buttons (CANCEL RUN, MOVE JIRA TO) stay at the top.

## Constraints

- AGENTS.md applies: no compound conditions, 60 lines per function, 100 columns, limits as named
  constants (`legsMax`), every error handled, `make check`, `make test` and `make lint` clean.
- Routes, JSON shapes and data files are a contract with the UI: the Go and the pages change
  together, and `testdata/answers/` is updated for both sides.
- No new dependency. No package-level state.
- `GET /api/work`, `POST .../start`, `pr`, `fix-checks`, `cleanup` and the run JSON are the only
  contract changes. Run files written by the new version are not readable by the old one; that is
  stated in the release notes.
- The dry run (`CONN_WORK_DRY=1`) supports several legs: its seed lists at least two allowed
  repositories, and it walks every leg through its pending, red or green, approved and merged PR.

## Left to the builder

- Package layout: where the leg type, its helpers and the per-leg operations go; splitting
  `pr.go` and `record.go` as they grow.
- The names of the new constants and functions, within the naming rules.
- The exact wording of messages, within the examples given.
- How the code mapper receives several folders, and how the shared session is launched with
  `--add-dir`, within the behaviour above.
- How `internal/sessions` learns a run has several worktrees, so a session in a non-lead
  worktree is matched to its run (the Sessions page's hidden-repository rule uses the run's
  worktree today).
- The `localStorage` key names.

## Decisions made without the person (my calls, to check)

- Default `reviewMove` is `"all"`, default `sessionLayout` is `"shared"`.
- Branches and PR titles are the same in every leg.
- A leg with no commits is skipped at OPEN PR rather than refused.
- `ahead` and `dirty` move from `session` to the leg.
- The global `baseBranch` stays as a fallback beside the per-repository map instead of being
  migrated into it.
- Run files are written in the new shape on the first write, with no separate migration step.
- At most 6 repositories per run.

## Settled after the builder check (my calls, to check)

- **RESUME SESSION** (`resume`) takes an optional `{ repo }`, a fifth contract change beside
  `start`, `pr`, `fix-checks` and `cleanup`. Absent, it reopens every session slot (a leg that has
  a session) whose session is not live; present, that leg's only. In shared layout only the lead
  has a slot, so only its row shows the button. Its refusals and the one-per-30-seconds gap are per
  slot.
- **Starting a ticket again** replaces its run record, as today. A repository the old and new runs
  share reuses its worktree when it is on the right branch, and the "cut from another base" refusal
  is per shared repository. A leg the new run drops stays on disk untouched, and the start log
  line names its worktree path. Nothing is cleaned up for the person.
- **Merged.** A closed-unmerged PR is settled, not blocking. MOVE JIRA TO is offered when every leg
  with a PR is merged or closed and at least one merged. The run stays `pr_open` until then. A
  leg with commits and no PR does not block it, and the page says so in that leg's row.
- **Frozen and live values.** A leg's `base` and `branch` are frozen at start (the branch was cut
  from it). Its clone is looked up again from Settings on every Retry, resume, session launch and
  push, and the allowlist is re-checked each time, as today. A leg whose repository left the list
  or has no clone stops the action with that repository named.
- **A failed review move** does not count as the one attempt. The run records `reviewMoved` once
  Jira answered (moved, or already there, or no single review status); each later qualifying PR
  open tries again until then. Under `"all"`, commits that appear in a leg after the move do not
  take the ticket out of review.
- `baseBranches` in a PUT replaces the whole map. A remembered selection drops repositories that
  are no longer ready. A one-repository run stores `layout` `"shared"`. Sequential pushes hold no
  lock across the outside calls, as the single push does not today.
