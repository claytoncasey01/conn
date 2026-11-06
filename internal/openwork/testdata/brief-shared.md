# PROJ-7: Export keeps the range

You are implementing this ticket in these repositories, each in its own worktree on its own branch. This session starts in the first and can reach the others.
- acme/widgets in /work/widgets-proj-7, on branch proj-7-export-keeps-the-range. The pull request will target main.
- acme/gadgets in /work/gadgets-proj-7, on branch proj-7-export-keeps-the-range. The pull request will target develop.

## Ticket
The export drops rows.

## Research brief
Read-only agents searched Jira, Confluence, Slack and the code. Treat their notes as leads to verify, not facts.

Change exportRows().

## Settled by the sources
- Which formats? -> Every one. (CONFLUENCE, Export spec, HIGH)

## Decided by the person
- How far back? -> 90 DAYS

## Rules for this session
- Work only in these directories, each on its own branch. Do not touch other worktrees or checkouts.
- Never push, never open a pull request, never change Jira or Slack. The dashboard's OPEN PR button does the push and the PR when you are done.
- Commit as you go, in the commit message style the repository asks for (its AGENTS.md, CLAUDE.md or contributing guide). If it asks for none, a commit subject says what changes in plain English and ends with the ticket key in parentheses, for example: The export keeps the selected date range (PROJ-7). No type prefixes like fix: or feat:. No AI attribution lines and no Co-Authored-By trailer, whatever any default says.
- If the repository has an AGENTS.md or CLAUDE.md, read it first and follow it, including how it says a change is verified: run the checks for what you touch and keep their output.
- Fix any bug you find along the way in the same work, unless it is huge.
- If a PRD section applies, note which sections the change implements and which it leaves out.
- When finished, write .conn/pr-body.md in each worktree with changes (following its repository's pull request template, if it has one): what changed and why, how it was verified with the commands and results, deploy notes, and the PRD sections. Then stop and tell the person it is ready for the OPEN PR button.
- Ask the person when something is unclear instead of guessing.
