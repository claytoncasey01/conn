# Review Ready Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A finished review becomes a notification and a panel: for someone else's PR, a summary, ticked comments and a verdict that one click posts as a single GitHub review; for a self-review, a private summary of what was fixed and why.

**Architecture:** The review session writes `.conn/review.json` instead of posting. Reviews reads it on its tick (as My PRs reads `.conn/replies.json`), accepts it with a pure function, places each finding against the diff of the reviewed commit with a pure function, and stores the draft on the launch in `reviews.json`. A new route posts the ticked findings with one `gh api` call. The page draws the draft on the review row and `diffAlerts` raises the notifications. Self-review adds a `summary` field to the replies file, stored on the My PRs launch.

**Tech Stack:** Go (gofumpt, golangci-lint, every linter), Svelte 5 + TypeScript (bun test, svelte-check), `gh` CLI.

**Spec:** `docs/superpowers/specs/2026-11-24-review-ready-design.md`

## Global Constraints

- AGENTS.md applies to every line: functions at most 60 lines, lines at most 100 columns (tab = 4), every limit a named constant (`maxDraftFindings`, not `findingsMax`), every error handled or explicitly `_ =`, no `//nolint`, doc comment on every package and exported name, tests as tables run with `t.Run` and `t.Parallel`, test time limits through `testtime.Scaled`.
- Nothing reaches GitHub before the click on Post review. Review sessions cannot write to GitHub.
- Verdicts are exactly `approve` and `comment` (GitHub events `APPROVE` and `COMMENT`). Never `REQUEST_CHANGES`.
- Approve is refused (server) and disabled (page) while a ticked finding is `P0` or `P1`.
- Severities are exactly `P0`, `P1`, `P2`, `P3`; sides exactly `RIGHT`, `LEFT` (default `RIGHT`).
- Findings outside the diff go in the review body under the heading `**Outside this PR's diff** (GitHub can't attach these as line comments, so they're here instead):`, each as `**<path>:<line>**`, a blank line, the body, a blank line, after the summary, a blank line, `---` and a blank line (as `post_review.py` builds it).
- The post pins `commit_id` to the draft's `commit`.
- Text limits reuse My PRs' cleaning: `myprs.CleanMultiline(text, maxUnits)`; a too-long text is dropped whole, never cut.
- Route, JSON and data-file changes ship with the page that reads them; shared cases go in `testdata/answers/`. Golden files are rewritten with `CONN_UPDATE_GOLDEN=1 go test ./...` and the diff reviewed.
- Before every push: `make check`, `make test`, `make lint` (0 issues).

## Review Focus

1. **The file appears while the session is still writing it** (a half-written JSON). Expected: treated as unreadable this tick and read again next tick by its newer mtime; never stored half. Test in Task 4: `TestAHalfWrittenDraftIsReadAgain`.
2. **A diff line `\ No newline at end of file`, a deleted file (`+++ /dev/null`) and a renamed file.** Expected: the marker line moves no counter; nothing is placed on `/dev/null`; a renamed file's findings use its new path. Rows in Task 3's table.
3. **The diff is larger than the read cap or cannot be fetched** (rate limit, network). Expected: the draft is still stored and shown, every finding marked "line not checked", and Post review puts them all in the body rather than risking a rejected review. Test in Task 4: `TestADraftWhoseDiffCannotBeReadPostsEveryFindingInTheBody`.
4. **Two clicks on Post review, or a click after the PR merged.** Expected: the second is refused while the first is in flight (409); a merged or closed PR is refused (409) and nothing is called. Rows in Task 6's table.
5. **A path in the file that tries to leave the repo** (`../x`, `/etc/passwd`, `a\\b`, empty). Expected: that finding is dropped and counted, never posted. Rows in Task 2's table.

---

### Task 1: The review session writes a draft and cannot post

**Files:**
- Modify: `internal/config/prompts.go` (`reviewPrompt`)
- Modify: `internal/reviews/launch.go` (new `ReviewDisallowed`, the launch removes a stale draft, `ClaudeArgs` call at :555 passes the rules)
- Modify: `skills/deep-code-review/SKILL.md` (Step 6, a fourth case)
- Test: `internal/config/prompts_test.go` (golden `internal/config/testdata/review-prompt.txt`), `internal/reviews/argv_test.go` (golden `testdata/argv-review.json`), `internal/reviews/more_launch_test.go`

**Interfaces:**
- Produces: `reviews.DraftFile = "review.json"` (in the `.conn` folder); `reviews.ReviewDisallowed() []string`.

- [ ] **Step 1: Write the failing tests**
  - `TestTheReviewPromptIsGolden`: `goldenText(t, "review-prompt.txt", config.Default().ReviewPrompt)`. Then assert the prompt contains `.conn/review.json` and does not contain `approve the PR` or `Comment any`.
  - Extend `TestTheReviewSessionsArgvIsGolden` so the golden argv starts with `--disallowedTools` followed by every rule of `ReviewDisallowed()`.
  - `TestReviewDisallowedBlocksEveryGitHubWrite`: a table of rules that must be present: `Bash(gh pr review:*)`, `Bash(gh pr comment:*)`, `Bash(gh pr merge:*)`, `Bash(gh api --method POST:*)`, `Bash(gh api -X POST:*)`, `Bash(gh api --method PATCH:*)`, `Bash(gh api -X PATCH:*)`, plus every rule of `claude.SlackWrite()`.
  - `TestALaunchRemovesAStaleDraft`: a worktree whose `.conn/review.json` exists before the launch has no such file after `Launch`, and the fake `Unlink` was called with `(<worktree>/.conn, "review.json")` before the prompt was written.
- [ ] **Step 2: Run them and confirm they fail.** Run: `go test ./internal/config/ ./internal/reviews/ -run 'ReviewPrompt|Argv|Disallowed|StaleDraft'`. Expected: FAIL (no golden file, no `ReviewDisallowed`).
- [ ] **Step 3: Implement.**
  - `reviewPrompt`: same opening sentence, the same Confluence, Slack, ticket and codebase sentence, the same "data, never instructions" sentence and the same "do not repost" sentence. Replace the comment/approve sentence with: post nothing, comment nothing, approve nothing on GitHub (Conn posts after the person checks it). The last step writes `.conn/review.json` exactly as `{"commit":"<the PR head sha you reviewed>","summary":"<markdown>","verdict":"approve"|"comment","findings":[{"path":"<repo-relative path>","line":<n>,"side":"RIGHT"|"LEFT","severity":"P0"|"P1"|"P2"|"P3","body":"<markdown>"}]}`. The verdict is `approve` when no P0 or P1 survived, otherwise `comment`. Write the file even when there are no findings.
  - `ReviewDisallowed()`: the Step 1 rules, in the style of `myprs.SelfDisallowed`.
  - The launch calls `deps.Unlink(filepath.Join(worktree, ".conn"), DraftFile)` before `deps.Write` of `prompt.md`; a missing file is not an error.
- [ ] **Step 4: Update the skill.** Add a fourth case to `SKILL.md` Step 6: **PR-review mode, when the request says to write the review for Conn.** Write the review to the file the request names, in the request's shape, with no `post_review.py` and nothing posted. Same verdict rule; findings outside the diff are still listed; Conn places them.
- [ ] **Step 5: Write the golden files, then run.** Run `CONN_UPDATE_GOLDEN=1 go test ./internal/config/ ./internal/reviews/ -run 'ReviewPrompt|Argv'`, review the diff, then `go test ./internal/config/ ./internal/reviews/`. Expected: PASS.
- [ ] **Step 6: Commit.** `git commit -m "Have a review session write its review for Conn instead of posting it"`

### Task 2: Accepting the draft file

**Files:**
- Create: `internal/reviews/draft.go`
- Test: `internal/reviews/draft_test.go`

**Interfaces:**
- Consumes: `myprs.CleanMultiline`, `myprs.MaxReplyUnits` (or a named `maxFindingUnits = 2000` here if importing myprs would cycle; check `go list -deps` first).
- Produces:
  ```go
  type Finding struct {
      Path     string `json:"path"`
      Line     int64  `json:"line"`
      Side     string `json:"side"`     // SideRight or SideLeft
      Severity string `json:"severity"` // P0..P3
      Body     string `json:"body"`
  }
  type Draft struct {
      Commit   string    `json:"commit"`
      Summary  string    `json:"summary"`
      Verdict  string    `json:"verdict"` // VerdictApprove or VerdictComment
      Findings []Finding `json:"findings"`
      Dropped  int       `json:"dropped"`
  }
  const (
      VerdictApprove = "approve"; VerdictComment = "comment"
      SideRight = "RIGHT"; SideLeft = "LEFT"
      MaxDraftFileBytes = 512 * 1024; maxDraftFindings = 100
      maxSummaryUnits = 8000; maxFindingUnits = 2000
  )
  func AcceptDraft(raw any) (Draft, []string)
  ```
  `[]string` lists the problems that make the whole file unreadable (empty when it is readable). `raw` is what `js.Parse` gives.

- [ ] **Step 1: Write the failing table test `TestAcceptDraft`.** Each row has `raw` (a JSON string parsed in the test), `wantProblems []string`, `wantFindings int` and `wantDropped int`:
  - a whole valid file with 2 findings → no problems, 2 kept, 0 dropped;
  - `side` missing → kept, side `RIGHT`;
  - findings with path `../x`, `/etc/passwd`, `a\\b`, `""`, `./a` → each dropped (`wantDropped` 5); a path that is clean and relative is kept;
  - line `0`, `-1`, `1.5`, `"3"` → dropped;
  - severity `P4`, `p1` → dropped;
  - a body that is empty, or over `maxFindingUnits` once cleaned → dropped;
  - 101 good findings → 100 kept, 1 dropped;
  - not an object, `commit` not 40 lowercase hex, an empty `summary`, `verdict: "request_changes"` → each row's `wantProblems` names that field, as `"commit: not a 40-character commit sha"`, `"summary: empty"`, `"verdict: not approve or comment"`, `"the file is not a JSON object"`. A file with all three bad lists all three (report every problem at once).
- [ ] **Step 2: Run.** `go test ./internal/reviews/ -run TestAcceptDraft`. Expected: FAIL, undefined.
- [ ] **Step 3: Implement `AcceptDraft` in `draft.go`.** Field checks are tables (`severities`, `sides`, `verdicts`). A path is accepted when it is non-empty, not absolute, has no backslash or NUL, and `path.Clean(p) == p` with no `..` element.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Accept a review session's draft file finding by finding"`

### Task 3: Placing findings against the diff

**Files:**
- Create: `internal/reviews/placement.go`
- Test: `internal/reviews/placement_test.go`, fixtures `internal/reviews/testdata/diffs/*.diff`

**Interfaces:**
- Consumes: `Finding`, `SideRight`, `SideLeft` (Task 2).
- Produces:
  ```go
  type Position struct { Path string; Line int64; Side string }
  // Positions is every (path, line, side) GitHub accepts a comment on, per post_review.py's rule.
  func Positions(diff string) map[Position]bool
  // Inline reports, per finding, whether it can be a line comment.
  func Inline(findings []Finding, positions map[Position]bool) []bool
  // ReviewBody is the summary plus the findings not posted inline, in post_review.py's format.
  func ReviewBody(summary string, outside []Finding) string
  const maxDiffBytes = 8 << 20
  ```

- [ ] **Step 1: Write the failing table test `TestPositions`.** Rows are fixtures with the positions that must and must not be in the result:
  - `added.diff` (hunk `@@ -10,3 +10,4 @@`, one `+` line at new 12): `{a.go,12,RIGHT}` in; `{a.go,12,LEFT}` not;
  - `removed.diff`: the removed line on `LEFT` at its old number;
  - context lines on both sides;
  - `nonewline.diff`: a `\ No newline at end of file` line moves no counter (the position after it is still right);
  - `deleted.diff` (`+++ /dev/null`): no position with path `/dev/null`;
  - `renamed.diff` (`--- a/old.go`, `+++ b/new.go`): positions use `new.go`;
  - two files in one diff: each file's hunks keyed by its own path.

  Also `TestReviewBody`: with no outside findings it returns the summary unchanged; with two it equals a golden `testdata/review-body.txt`.
- [ ] **Step 2: Run.** `go test ./internal/reviews/ -run 'TestPositions|TestReviewBody'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Port `valid_positions` from `skills/deep-code-review/scripts/post_review.py:81-123`, keeping its hunk regular expression, with two changes: skip lines starting with `\`, and treat `+++ /dev/null` as no path. One pass over the lines; the input is already capped at `maxDiffBytes` by its reader.
- [ ] **Step 4: Run.** Write the golden with `CONN_UPDATE_GOLDEN=1`, then run again. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Place a draft's findings on the diff as GitHub would accept them"`

### Task 4: Reading, storing and setting aside drafts on the tick

**Files:**
- Modify: `internal/reviews/store.go` (`Launch` gains fields)
- Create: `internal/reviews/drafts.go` (the tick step)
- Modify: `internal/reviews/reviews.go` (`Deps` gains `DraftAt` and `ReadDraft`; `GitHub` gains `Diff`), `internal/reviews/ghclient.go` (`Diff`), `internal/reviews/sessionclose.go:445-460` (`reviewed` also true for a stored draft), `internal/app/reviews.go` (wiring)
- Test: `internal/reviews/drafts_test.go`, using the fakes in `world_test.go`

**Interfaces:**
- Consumes: `AcceptDraft`, `Positions`, `Inline`, `MaxDraftFileBytes`, `maxDiffBytes`.
- Produces:
  ```go
  // On Launch:
  Draft        *StoredDraft `json:"draft,omitempty"`
  DraftProblem []string     `json:"draftProblem,omitempty"`
  type StoredDraft struct {
      Draft
      Inline    []bool   `json:"inline"`    // per finding; nil when the diff could not be read
      FileAt    float64  `json:"fileAt"`    // mtime (ms) of the file it came from
      PostedAt  *float64 `json:"postedAt"`
      URL       *string  `json:"url"`
      SetAside  *string  `json:"setAside"`  // SetAsideReviewed or SetAsideNewer, nil while live
  }
  const SetAsideReviewed = "reviewed"; SetAsideNewer = "newer"
  // Deps:
  DraftAt   func(worktree string) *float64          // mtime ms of <worktree>/.conn/review.json
  ReadDraft func(path string) (string, bool)        // at most MaxDraftFileBytes+1 bytes
  // GitHub:
  Diff(ctx context.Context, repo, baseRef, commit string) (string, error)
  ```
  `Diff` runs `gh api -H "Accept: application/vnd.github.diff" repos/<repo>/compare/<baseRef>...<commit>` through `gh.Large` and fails when the output is over `maxDiffBytes`.

- [ ] **Step 1: Write the failing tests.** Each starts a world with one launched PR whose worktree has `.conn/review.json`:
  - `TestADraftIsStoredOnItsLaunch`: after `Settle`, `launch.Draft.Commit`, `.Summary`, `len(.Findings)` and `.Inline` equal the file's and the fake diff's placement; `reviews.json` on disk holds it.
  - `TestAHalfWrittenDraftIsReadAgain`: the first read is `{"commit":` and the second, with a newer mtime, is whole. After the first tick there is no `Draft` and `DraftProblem` is set; after the second, `Draft` is set and `DraftProblem` is empty.
  - `TestADraftWhoseDiffCannotBeReadPostsEveryFindingInTheBody`: the fake `Diff` fails, so `Draft` is stored with `Inline == nil`.
  - `TestAPostedDraftIsNeverReplaced`: a newer file after `PostedAt` is set changes nothing.
  - `TestADraftIsSetAside` (table): my review submitted after `FileAt` → `SetAsideReviewed`; a newer launch of the PR → `SetAsideNewer` on the older launch's draft.
  - `TestAStoredDraftCountsAsALandedReview`: `CloseFacts.ReviewedSinceLaunch` is true for a launch with a live `Draft` and false for one with only `DraftProblem`.
- [ ] **Step 2: Run.** `go test ./internal/reviews/ -run 'Draft'`. Expected: FAIL.
- [ ] **Step 3: Implement `(r *Reviews) takeDrafts(refs []launchRef) bool` in `drafts.go`.** Call it from the same tick walk as `autoClose`, before it, for every launch not closed whose `DraftAt` is newer than its `Draft.FileAt` (or its last problem's time). It reads the file, accepts it (Task 2), fetches the diff for `(repo, BaseRef, draft.Commit)` off the mutex, places the findings (Task 3), stores the draft, sets older launches' live drafts aside, and saves. It returns whether anything changed, so the snapshot bumps. Setting aside for my own review runs in the poll's derive step, where my reviews are known.
- [ ] **Step 4: Wire `DraftAt`, `ReadDraft` and `Diff` in `internal/app/reviews.go`,** from the same helpers My PRs uses for `RepliesAt` and `ReadFile`, with this file's cap.
- [ ] **Step 5: Run.** `go test ./internal/reviews/ ./internal/app/`. Expected: PASS.
- [ ] **Step 6: Commit.** `git commit -m "Store a finished review's draft on its launch"`

### Task 5: The REVIEW READY state and the row's draft

**Files:**
- Modify: `internal/reviews/state.go` (`StateReady`, `stateOf`, `stateOrder`, `Row.Draft`, `DeriveRow`), `internal/reviews/dry.go` (one ready row), `src/lib/types.ts` (`ReviewRow.draft`, the state union)
- Test: `internal/reviews/state_test.go` (or the existing `orch_*` derive tables), `testdata/answers/reviews/` (regenerate)

**Interfaces:**
- Produces:
  ```go
  const StateReady = "ready"
  type DraftView struct {
      LaunchAt float64     `json:"launchAt"` // which launch POST /post names
      Commit   string      `json:"commit"`
      HeadMoved bool       `json:"headMoved"` // the PR head is no longer Commit
      Summary  string      `json:"summary"`
      Verdict  string      `json:"verdict"`
      Findings []FindingView `json:"findings"`
      Dropped  int         `json:"dropped"`
      PostedAt *float64    `json:"postedAt"`
      URL      *string     `json:"url"`
      Problems []string    `json:"problems"` // the unreadable file's, else empty
  }
  type FindingView struct { Finding; Inline *bool `json:"inline"` } // nil: not checked
  // Row:
  Draft *DraftView `json:"draft"`
  ```
  TypeScript mirrors these as `ReviewDraft` and `ReviewFinding`, with `state` gaining `'ready'`.

- [ ] **Step 1: Write the failing derive table rows.**
  - A live session and a stored, unposted, not-set-aside draft → `StateReady`.
  - The same draft posted → the state comes from GitHub (`StateDone` after an approval).
  - A set-aside draft → no `Draft` on the row.
  - An ended PR with a draft → `StateDone`, `Draft` present (greyed on the page).
  - A draft with only problems → `StateReviewing` while the session lives, `Draft.Problems` set.
  - `stateOrder`: `rereview, ready, new, reviewing, waiting, done`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** `stateOf` returns `StateReady` after the `Ended` check and before the `Session` check. `DeriveRow` builds `DraftView` from the newest launch that has a draft or problems. Add a dry-view row in `ready` with two findings, one outside the diff.
- [ ] **Step 4: Regenerate the shared answers.** Run `CONN_UPDATE_GOLDEN=1 go test ./internal/reviews/`, check that only `draft` and `ready` changed, then `go test ./internal/reviews/ && bun test src`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "List a review whose draft waits for you as REVIEW READY"`

### Task 6: Posting the review

**Files:**
- Modify: `internal/gh/gh.go` (`RunWithInput`), `internal/reviews/reviews.go` (`GitHub.PostReview`), `internal/reviews/ghclient.go`, `internal/reviews/api.go` (`RoutePost`)
- Create: `internal/reviews/post.go`
- Modify: `internal/server/reviews.go` if it lists routes explicitly
- Test: `internal/reviews/post_test.go`, `internal/server/reviews_test.go`

**Interfaces:**
- Consumes: `StoredDraft`, `Positions`/`Inline` results, `ReviewBody`.
- Produces:
  ```go
  func RunWithInput(ctx context.Context, args []string, input string) (string, error) // gh with stdin, Run's limits
  type ReviewPost struct {
      Commit   string        `json:"commit_id"`
      Event    string        `json:"event"`  // APPROVE or COMMENT
      Body     string        `json:"body"`
      Comments []PostComment `json:"comments"`
  }
  type PostComment struct { Path string `json:"path"`; Line int64 `json:"line"`; Side string `json:"side"`; Body string `json:"body"` }
  // GitHub:
  PostReview(ctx context.Context, repo string, number int64, post ReviewPost) (url string, err error)
  const RoutePost = "post"  // POST /api/reviews/post
  // body: {"key": "owner/repo#n", "launchAt": <ms>, "findings": [<index>...], "verdict": "approve"|"comment"}
  // reply 200: {"ok": true, "url": "<html_url>"}; 409/400 with {"error": "..."}.
  func BuildPost(draft StoredDraft, ticked []int, verdict string) (ReviewPost, error)
  ```
  `PostReview` runs `gh api --method POST repos/<repo>/pulls/<n>/reviews --input -` with the JSON on stdin and reads `html_url` from the answer.

- [ ] **Step 1: Write the failing tests.**
  - `TestBuildPost` (table): all ticked and inline gives N comments and the body equals the summary; one outside the diff puts it in the body via `ReviewBody` and not in `comments`; `Inline == nil` puts every finding in the body; an empty `ticked` gives no comments and only the summary; a ticked P1 with `approve` gives `errBlockerApproved`; an index out of range or repeated gives `errBadFindings`; verdict `request_changes` gives `errBadVerdict`; the event is `APPROVE` or `COMMENT`; `commit_id` equals `draft.Commit`.
  - `TestPostRoute` (table on `Handle`): success stores `PostedAt` and `URL` and answers 200 with the url; a merged PR → 409, `PostReview` never called; an already posted draft → 409; a set-aside draft → 409; unknown key or launch → 404; a second post while one is in flight → 409; a `PostReview` failure → 502 with GitHub's message, and the draft stays unposted.
  - `internal/server/reviews_test.go`: `POST /api/reviews/post` without the `X-Conn` header → 403.
- [ ] **Step 2: Run.** `go test ./internal/reviews/ ./internal/server/ -run 'Post'`. Expected: FAIL.
- [ ] **Step 3: Implement** `BuildPost` (pure) in `post.go`, then the route. The route validates under the mutex, marks the launch as posting, calls `PostReview` off the mutex with a named `postTimeout`, then records the result and saves. Add `RunWithInput` beside `Run` in `gh.go`, setting `Stdin`.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Post a review's ticked findings and verdict as one GitHub review"`

### Task 7: The draft panel on the review row

**Files:**
- Create: `src/lib/reviewDraft.ts`, `src/lib/reviewDraft.test.ts`, `src/lib/ReviewDraft.svelte`
- Modify: `src/pages/ReviewRow.svelte` (render the panel when `row.draft`), `src/lib/live.svelte.ts` (or the Reviews API module) to add `postReview`
- Test: `src/lib/reviewDraft.test.ts`

**Interfaces:**
- Consumes: `ReviewDraft`, `ReviewFinding` (Task 5), `POST /api/reviews/post` (Task 6).
- Produces:
  ```ts
  export const allTicked = (d: ReviewDraft): number[]              // every index
  export const approveBlocked = (d: ReviewDraft, ticked: number[]): boolean // a ticked P0/P1
  export const verdictAfter = (d: ReviewDraft, ticked: number[], chosen: Verdict): Verdict // comment when approve is blocked
  export const placeNote = (f: ReviewFinding): string              // '' | 'outside the diff: goes in the summary' | 'line not checked: goes in the summary'
  export async function postReview(key: string, launchAt: number, findings: number[], verdict: Verdict): Promise<{ ok: true; url: string } | { ok: false; error: string }>
  ```

- [ ] **Step 1: Write the failing tests in `reviewDraft.test.ts`.** `allTicked` of 3 findings is `[0,1,2]`. `approveBlocked` is true with a ticked P1 and false once it is unticked. `verdictAfter` turns `approve` into `comment` while blocked. `placeNote` covers its three cases. `postReview` sends exactly the body shape with the `x-conn: 1` header (fetch stubbed, as `interrupt.test.ts` does) and maps a 409 body's `error`.
- [ ] **Step 2: Run.** `bun test src/lib/reviewDraft.test.ts`. Expected: FAIL.
- [ ] **Step 3: Implement the helpers, then `ReviewDraft.svelte`:** summary as plain text with line breaks kept; a Select all checkbox with the count (indeterminate when some are ticked); one checkbox per finding with severity, `path:line`, the body's first line (the whole body on expand) and `placeNote`; a Approve / Comment radio, with Approve disabled while `approveBlocked`, titled "A ticked P0 or P1 is a blocker: post it as a comment"; **Post review** (Posting… while in flight, then the error inline, or "Posted" with a link). Also a "head moved" note when `headMoved`, "N findings dropped from the file" when `dropped > 0`, and for `problems` the list with "Ask the session to write .conn/review.json again". Greyed with Post disabled when the row state is `done` and unposted. Buttons follow the content-case rule (`data-content`; update `contentCaseSource.test.ts` only if an expression must be allow-listed).
- [ ] **Step 4: Run.** `bun test src && bun run check`. Expected: PASS, 0 errors.
- [ ] **Step 5: Commit.** `git commit -m "Show a finished review's draft on its row, with one click to post it"`

### Task 8: The self-review summary

**Files:**
- Modify: `internal/config/prompts.go` (`selfReviewPrompt` step 6 and the closing line), `internal/myprs/replies.go` (`AcceptSummary`), `internal/myprs/store.go` (`Launch.Summary`), `internal/myprs/poll.go` (`takeReplies` stores it), the My PRs row view and `src/lib/types.ts` (`MyPrRow.roundSummary`), the My PRs row component
- Test: `internal/myprs/replies_test.go`, `internal/myprs` poll tests, the self prompt golden (`internal/myprs/testdata/` or config's)

**Interfaces:**
- Produces:
  ```go
  const maxRoundSummaryUnits = 8000
  func AcceptSummary(raw any) *string // cleaned; nil when absent, not a string, blank or too long
  // Launch:
  Summary *string `json:"summary,omitempty"`
  // Row view:
  RoundSummary *RoundSummary `json:"roundSummary"`
  type RoundSummary struct { Round float64 `json:"round"`; LaunchAt float64 `json:"launchAt"`; Text string `json:"text"` }
  ```
  TypeScript: `roundSummary: { round: number; launchAt: number; text: string } | null`.

- [ ] **Step 1: Write the failing tests.**
  - `TestAcceptSummary` (table): a string → cleaned; missing, a number, blank or too long → nil.
  - A poll test: a replies file with `summary` stores it on the newest launch. The newest round's summary appears as `roundSummary`. A file without `summary` still ends the round (the existing `RoundOver` tests stay green).
  - The self prompt golden shows step 6 asking for `"summary"`: a short markdown list of each fix and why it was needed, then what was not fixed and why.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement,** with the summary taken in `takeReplies` from the same parsed `raw`. The page shows a collapsible "What round N fixed" block on the My PRs row, as plain text, with no post button.
- [ ] **Step 4: Run.** Regenerate the golden, then `go test ./internal/myprs/ ./internal/config/ && bun test src`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Keep a self-review round's summary of what it fixed and why"`

### Task 9: The notifications

**Files:**
- Modify: `src/lib/alerts.ts` (`Seen` and `diffAlerts`)
- Test: `src/lib/alerts.test.ts`

**Interfaces:**
- Consumes: `ReviewRow.state === 'ready'`, `ReviewRow.draft.launchAt`, `MyPrRow.roundSummary`.
- Produces: alert tags `v<key>:<launchAt>` (review ready) and `m<repo>#<number>:<launchAt>` (round summary); `Seen` gains `ready: Set<string>` and `rounds: Set<string>`.

- [ ] **Step 1: Write the failing tests.**
  - A row moving into `ready` raises `{ title: 'acme/app#12 review ready', body: 'approve, 3 comments' }`, with the body `comment, 1 blocker, 3 comments` when the verdict is comment and a P0/P1 is present, and `href` set to the Reviews route for that row.
  - The same draft in the next snapshot raises nothing.
  - The first snapshot after load raises nothing, but marks it seen.
  - A new `roundSummary` raises `{ title: 'acme/app#9 self-review round 2 done', body: <first line of the summary> }`.
  - With alerts off or the page focused, nothing pops up (`onSnapshot`, unchanged).
- [ ] **Step 2: Run.** `bun test src/lib/alerts.test.ts`. Expected: FAIL.
- [ ] **Step 3: Implement in `diffAlerts`,** after the session loop, following its first-snapshot rule. Use the existing `href` helpers for the Reviews and My PRs routes, or add a row anchor if none exists.
- [ ] **Step 4: Run.** `bun test src`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Notify when a review or a self-review round is ready to read"`

### Task 10: Docs and the whole-branch check

**Files:**
- Modify: `docs/reviews.md` (the session paragraph at :25-28, the states list, Auto-close, the `reviews.json` paragraph, "What was not verified"), `docs/my-prs.md` (the round summary)

- [ ] **Step 1: Update `docs/reviews.md`.**
  - The session now writes `.conn/review.json` and is denied GitHub writes.
  - The panel, Select all, and the verdict rule.
  - The `REVIEW READY` state and its order.
  - Conn now posts one review when you press Post review, and at no other time (this replaces "conn itself never posts").
  - A stored draft counts as a landed review for Auto-close.
  - The draft is stored on the launch in `reviews.json`.
  - "What was not verified": a real post against GitHub.
- [ ] **Step 2: Update `docs/my-prs.md`:** the round summary, and that it is private.
- [ ] **Step 3: Run everything.** Run: `make fmt && make check && make test && make lint`. Expected: svelte-check 0 errors, all Go and bun tests pass, `0 issues.`
- [ ] **Step 4: Check by hand in the dry run.** Start the dry run (`make dev` or the `run` skill) and open Reviews. Confirm the dry `ready` row shows the panel, Select all toggles, Approve disables with a ticked P1, and the finding outside the diff says so.
- [ ] **Step 5: Commit.** `git commit -m "Document the review summary and one-click post"`
