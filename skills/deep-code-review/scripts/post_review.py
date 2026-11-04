#!/usr/bin/env python3
"""
Post a drafted deep-code-review as a single GitHub PR review, via `gh`.

Why this exists (don't skip it and hand-roll `gh api` calls): GitHub's review
API rejects the *entire* review if even one inline comment names a
file/line that isn't part of the PR's diff — it doesn't partially apply the
valid ones. So every finding has to be checked against the actual diff
hunks first, and anything that doesn't land inside a hunk gets folded into
the summary body instead of dropped. That's exactly what a careful human
reviewer does by hand ("this one's in a file the PR doesn't touch, so it's
in the write-up but not as a line comment") — this script just makes it
automatic and reliable instead of something Claude has to remember to do
correctly at 2am.

Only call it once the request has said to post: the user confirmed the
drafted review, or the request itself asked for the findings to be posted
(see SKILL.md, Step 6). It posts as a plain review comment (event=COMMENT)
unless --event APPROVE is given, which it uses only when no blocker
survived. It never requests changes. GitHub does not let you approve your
own PR: an APPROVE that GitHub refuses for that reason is posted again as a
COMMENT, and the script says so.

Usage:
    python3 post_review.py --input review.json [--pr 42] [--repo owner/repo]
                           [--event COMMENT|APPROVE] [--dry-run]

Input JSON shape:
{
  "pr": 42,                       # optional if --pr given, or if run from the PR's branch
  "repo": "owner/repo",           # optional; auto-detected via `gh repo view` if omitted
  "summary": "markdown body for the top-level review comment",
  "findings": [
    {
      "path": "src/orders/refund.py",
      "line": 41,
      "side": "RIGHT",            # RIGHT = new/added file content (default), LEFT = deleted content
      "body": "markdown text for this one finding"
    },
    ...
  ]
}

Findings whose (path, line, side) isn't part of the diff are automatically
moved into the summary under an "outside this PR's diff" heading, formatted
the same way GitHub would have shown them inline (file:line + body), so
nothing is silently lost.
"""

import argparse
import json
import re
import subprocess
import sys


def run(cmd, input_data=None):
    result = subprocess.run(
        cmd, input=input_data, capture_output=True, text=True
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"command failed: {' '.join(cmd)}\n{result.stderr.strip()}"
        )
    return result.stdout


def detect_repo():
    out = run(["gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"])
    return out.strip()


def detect_pr():
    out = run(["gh", "pr", "view", "--json", "number", "-q", ".number"])
    return int(out.strip())


HUNK_RE = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")


def valid_positions(repo, pr):
    """
    Parse `gh pr diff` and return the set of (path, line, side) that
    GitHub's review-comment API will accept.

    side=RIGHT covers context lines and added ('+') lines, keyed by their
    line number in the new file. side=LEFT covers context lines and removed
    ('-') lines, keyed by their line number in the old file. Context lines
    are valid on both sides, which matches how GitHub itself treats them.
    """
    diff_text = run(["gh", "pr", "diff", str(pr), "--repo", repo, "--patch"])
    valid = set()
    path = None
    old_line = new_line = None

    for raw in diff_text.splitlines():
        if raw.startswith("+++ "):
            p = raw[4:].strip()
            path = p[2:] if p.startswith("b/") else p
            continue
        if raw.startswith("--- "):
            continue
        m = HUNK_RE.match(raw)
        if m:
            old_line = int(m.group(1))
            new_line = int(m.group(3))
            continue
        if path is None or old_line is None:
            continue
        if raw.startswith("+"):
            valid.add((path, new_line, "RIGHT"))
            new_line += 1
        elif raw.startswith("-"):
            valid.add((path, old_line, "LEFT"))
            old_line += 1
        else:
            # context line — present on both sides
            valid.add((path, new_line, "RIGHT"))
            valid.add((path, old_line, "LEFT"))
            old_line += 1
            new_line += 1

    return valid


def split_findings(findings, valid):
    inline, outside = [], []
    for f in findings:
        key = (f["path"], int(f["line"]), f.get("side", "RIGHT"))
        (inline if key in valid else outside).append(f)
    return inline, outside


def build_summary(base_summary, outside):
    if not outside:
        return base_summary
    lines = [
        base_summary.rstrip(),
        "",
        "---",
        "",
        "**Outside this PR's diff** (GitHub can't attach these as line "
        "comments, so they're here instead):",
        "",
    ]
    for f in outside:
        lines.append(f"**{f['path']}:{f['line']}**")
        lines.append("")
        lines.append(f["body"])
        lines.append("")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--input", required=True, help="Path to the review JSON file")
    ap.add_argument("--pr", type=int, help="PR number (else read from input JSON, else current branch's PR)")
    ap.add_argument("--repo", help="owner/repo (else read from input JSON, else `gh repo view`)")
    ap.add_argument("--event", choices=["COMMENT", "APPROVE"], default="COMMENT",
                    help="COMMENT (default), or APPROVE when no blocker survived")
    ap.add_argument("--dry-run", action="store_true", help="Print what would be posted, don't call the API")
    args = ap.parse_args()

    with open(args.input) as fh:
        data = json.load(fh)

    repo = args.repo or data.get("repo") or detect_repo()
    pr = args.pr or data.get("pr") or detect_pr()
    findings = data.get("findings", [])

    valid = valid_positions(repo, pr)
    inline, outside = split_findings(findings, valid)
    summary = build_summary(data["summary"], outside)

    payload = {
        "body": summary,
        "event": args.event,
        "comments": [
            {
                "path": f["path"],
                "line": int(f["line"]),
                "side": f.get("side", "RIGHT"),
                "body": f["body"],
            }
            for f in inline
        ],
    }

    print(f"Repo: {repo}  PR: #{pr}  Event: {args.event}", file=sys.stderr)
    print(f"Inline comments: {len(inline)}  Moved to summary: {len(outside)}", file=sys.stderr)
    if outside:
        for f in outside:
            print(f"  -> summary-only: {f['path']}:{f['line']}", file=sys.stderr)

    if args.dry_run:
        print(json.dumps(payload, indent=2))
        return

    try:
        out = post(repo, pr, payload)
    except RuntimeError as err:
        if args.event != "APPROVE" or OWN_PR not in str(err).lower():
            raise
        print("GitHub does not allow approving your own PR: posting it as a COMMENT instead.",
              file=sys.stderr)
        payload["event"] = "COMMENT"
        out = post(repo, pr, payload)
    posted = json.loads(out)
    print(f"Posted review ({payload['event']}): {posted.get('html_url', '(no url returned)')}")


# What GitHub says when a review approves the PR of the one posting it.
OWN_PR = "can not approve your own pull request"


def post(repo, pr, payload):
    return run(
        ["gh", "api", f"repos/{repo}/pulls/{pr}/reviews", "--input", "-"],
        input_data=json.dumps(payload),
    )


if __name__ == "__main__":
    main()
