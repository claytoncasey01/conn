# Review ready: a summary to read, and one click to post

## Intent

A review session should end in something the person can act on in seconds, without reading a
terminal. When a review finishes, Conn tells them, shows what the review found or did, and, for
someone else's PR, posts the review and approves only when they press a button. Almost always
they post every comment and approve, so that path is one click.

## Goals

- A notification when a review session's result is ready, for a review of someone else's PR
  (Reviews) and for a self-review round of one's own (My PRs). Clicking it opens that PR's row.
- **Review of someone else's PR:** Claude posts nothing to GitHub. Conn shows the summary, every
  inline comment (all ticked, with Select all) and a verdict (Approve, preselected from Claude's
  verdict, or Comment). **Post review** posts the ticked comments and the summary as one GitHub
  review with that verdict.
- **Self-review:** Conn shows a private summary of what was fixed and why each fix was needed.
  Nothing is posted.

## Non-goals

- Editing a comment's text in Conn before posting (untick it, or post and edit on GitHub).
- Requesting changes (see Calls made).
- Any change to the self-review round itself: its fixes, pushes, `replies.json` and the existing
  per-thread "Post and resolve" buttons stay as they are.
- Posting a self-review's summary anywhere.

## Anti-goals

- Anything reaching GitHub before the click. A review session that still posts or approves on
  its own is a failure of this feature, even if the panel works.
- A review lost because its session closed, its worktree was swept, or Conn restarted before the
  click.
- A post that partly lands, or that GitHub refuses whole because one comment's line is not in
  the diff.
- Text from the PR, the model's file or Slack treated as anything but data.

## Constraints

- AGENTS.md rules: limits on every file read and list, all problems reported at once, I/O at the
  edges with pure deciding functions, tables for many-case tests, golden files for contracts
  (the prompt, the file format).
- The server's routes, JSON and data files are a contract with the page: change them together,
  and put shared cases in `testdata/answers/`.
- GitHub rejects a whole review when any inline comment names a path, line or side outside the
  diff's hunks. GitHub does not let anyone approve their own PR.
- `docs/reviews.md` promises that Conn itself never posts or approves for Reviews. That promise
  changes to "only when you press Post review", and the docs say so.

## Approaches

1. **Draft file, then one GitHub call (chosen).** The session writes `.conn/review.json` as its
   last step, Conn checks it and stores the draft in `reviews.json`, and Post review is one
   `gh api` call that creates the review with its comments and event together. It mirrors how
   My PRs already reads `.conn/replies.json`, Claude never writes to GitHub, and Select all is a
   filter Conn applies.
2. **A pending review on GitHub.** Claude creates an unsubmitted review and Conn submits it.
   GitHub would check each line as Claude goes, but there is one pending review per person per
   PR (it collides with one started on GitHub's page), unticking means deleting on GitHub, and
   Claude keeps write access to GitHub. Rejected.
3. **Reading the summary from the terminal transcript.** Fragile, and gives no structured
   comments. Rejected.

## Design

### The review session (someone else's PR)

- `reviewPrompt` (`internal/config/prompts.go`) changes from "comment ... inline, and ...
  approve" to: review the PR (with `/deep-code-review` when installed), post nothing, comment
  nothing and approve nothing on GitHub, and as the last step write `.conn/review.json`. The
  re-review line stays. The prompt has a golden file.
- The file's shape is the skill's own draft shape, plus the commit, a verdict and a severity:
  `{"commit": "<40-hex sha>", "summary": "<markdown>", "verdict": "approve"|"comment",
  "findings": [{"path", "line", "side": "RIGHT"|"LEFT", "severity": "P0".."P3", "body"}]}`.
  `commit` is the PR head the session reviewed (its worktree is at the base branch, so it reads
  the PR through `gh`, whose head may have moved since REVIEW was pressed): the line numbers
  belong to it, so the diff check and the post use it, not `Launch.HeadSha`.
- A launch removes any `.conn/review.json` left in a reused worktree before the session starts,
  as My PRs does for `replies.json`, and the review session is denied the GitHub write commands
  (`gh pr review`, `gh api` with a write method), following `myprs.SelfDisallowed`.
- `skills/deep-code-review/SKILL.md` gains this as a fourth PR-review case: when the request
  says to write the review for Conn, write it to `.conn/review.json` in that shape and post
  nothing. Its verdict rule is the posting rule it already has: approve when no P0 or P1
  survived, otherwise comment.

### Reading and storing the draft

- On the same tick that already follows launches (`autoClose`'s walk), Conn looks for
  `<worktree>/.conn/review.json` of each open launch: a regular file, not a link, at most a fixed
  byte limit, read through the same guarded read as My PRs' replies file.
- A pure function accepts the file, finding by finding, as `AcceptReplies` does: a finding
  whose path is not a relative path in the repo, whose line is not a positive integer, whose
  side or severity is not in its table, or whose body is empty or too long once cleaned (in
  UTF-16 units) is dropped whole and counted; at most a fixed number are kept. The row shows how
  many were dropped. The file as a whole is unreadable only when it is not JSON, too large, or
  lacks a valid `commit`, `summary` or `verdict`: then nothing is stored as a draft, the row says
  "review file unreadable" with every problem listed, no alert fires, and it does not count as
  a landed review, so the session stays open (to the one-hour cap) for the person to ask Claude
  to write it again. A later, readable write is picked up by its newer mtime.
- The accepted draft is stored on the launch record in `reviews.json` (the head sha the session
  reviewed, the file's mtime, the draft, and later `postedAt` and the review's URL), so it
  survives the session closing, the worktree sweep and a restart. A newer mtime replaces an
  unposted draft; a posted one is never replaced.

### Checking comments against the diff

- When the draft is stored, Conn reads the PR's diff for that head (`gh api` with the diff media
  type, capped in bytes) and a pure function, ported from `post_review.py`'s hunk rule, marks each
  finding inline or summary-only. Summary-only findings are shown on the row with that label and
  go into the review body under an "Outside this PR's diff" heading, as the script does.
- If the PR's head has moved past the draft's `commit`, the row says so and Post review still
  posts against that commit (`commit_id`), so line positions stay right.

### Posting

- `POST /api/reviews/post` with the PR key, the launch, the ticked finding indexes and the verdict.
  The server checks the PR is open and allowed, the draft is unposted, the indexes are in range
  and the verdict is in the table, then makes one `gh api` call:
  `POST repos/{owner}/{repo}/pulls/{n}/reviews` with `commit_id`, `event` (`APPROVE` or
  `COMMENT`), `body` (summary plus summary-only and any unplaceable findings) and `comments`.
- On success the draft records `postedAt` and the review's URL; the row shows "Posted" with a
  link. A failure shows GitHub's message and leaves the draft unposted, so the click can be tried
  again. A second click while one is in flight is refused.
- An APPROVE refused because the PR is one's own cannot happen here (Reviews never lists one's
  own PRs); it is still handled by reporting the error, not by silently posting a comment.

### The row

```
acme/app #12  Add retry to the uploader               [REVIEW READY]
  Summary: Retry logic is sound; one P2 on the backoff cap, two P3 nits.
  [x] Select all (3)
    [x] P2 uploader.go:88  The cap is never applied after the 5th retry…
    [x] P3 uploader.go:12  Name the constant maxRetries…
    [x] P3 docs/README.md:4 (outside the diff: goes in the summary)
  Verdict: (•) Approve  ( ) Comment                   [ Post review ]
```

- Every finding starts ticked; Select all toggles them together and shows the count.
- The verdict starts at Claude's; Approve is disabled with a note while a ticked finding is P0
  or P1 (a blocker is a comment, as the skill says), and enabled again when those are unticked.
- A new state, `REVIEW READY`: a draft is stored, not posted and not set aside. In `stateOf` it
  comes before `reviewing`, so it shows (and alerts) as soon as the draft is stored, even while
  the session is still open and idle. Once posted, the state comes from GitHub as today (`DONE`
  after an approval).
- An unposted draft is set aside (kept in the record, no longer shown or postable) when a review
  of mine lands on GitHub after the draft was stored (I reviewed by hand), or when a newer launch
  of the PR starts (its draft replaces it). While the PR is merged or closed the draft shows
  greyed, with Post review disabled. A re-review request does not set it aside: the row shows
  the draft with its "head moved" note, and Post review still posts against its commit.

### Self-review summary

- The self-review prompt's last step asks for one more field in `.conn/replies.json`:
  `"summary"`, a short markdown list of each fix and why it was needed (and what was not fixed,
  and why). My PRs reads it with the same checks as the replies (capped, cleaned, optional:
  a file without it still ends the round) and stores it on the round.
- The My PRs row shows it in a collapsible "What this round fixed" block. Nothing is posted.

### Notifications

- `diffAlerts` (`src/lib/alerts.ts`) gains two cases, from data the snapshot already carries:
  a Reviews row entering `REVIEW READY` ("acme/app#12 review ready: approve, 3 comments"), and a
  My PRs round gaining a summary ("acme/app#9 self-review round 2 done"). Each fires once per
  draft or round (keyed by its launch or round), not on the first snapshot after a restart, and
  opens that row. They follow the existing alert switch and focus rule.

### Auto-close

- A launch whose draft is stored counts as "the review landed" in `CloseFacts`, so the session
  closes after the usual idle time instead of waiting for a review on GitHub. A posted review
  still counts as today.

### Docs

- `docs/reviews.md`: what the session does now, the panel, the post, the new state, the file and
  its limits, and the changed promise about posting. `docs/my-prs.md`: the round summary.

## Calls made (for the person to check)

- The verdict is Approve or Comment, never Request changes, following the shipped skill's rule.
- Approve is blocked while a ticked P0 or P1 is in the review.
- Comments cannot be edited in Conn; they can be unticked.
- A finding outside the diff is posted in the review body, not dropped.
- The post pins the reviewed commit, so a PR that moved since still gets correct line positions.
- The draft lives on the launch in `reviews.json`, so nothing is lost when the session closes.
- The session reports the commit it reviewed (`commit`), and that commit, not the one seen at
  REVIEW, anchors the diff check and the post.
- A bad finding is dropped and counted; only a file with no usable commit, summary or verdict is
  unreadable, and that leaves the session open so it can be rewritten.
- REVIEW READY wins over REVIEWING; a draft is set aside by my own review on GitHub or a newer
  launch, and greyed once the PR has ended.
- Review sessions are denied GitHub write commands, so the prompt is not the only guard.

## Left to the builder

- Exact byte, finding and text limits (named constants, in line with the replies file's).
- Where the diff is fetched and cached, and how its size cap is reported.
- The panel's styling within the existing Reviews row.
- The test tables and golden files' names.
