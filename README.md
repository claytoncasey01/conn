# Conn

A local dashboard for Claude Code. It shows your live sessions and the agents inside each one, keeps an archive of finished sessions, and can list the pull requests that need your review, track your own, and start Claude Code sessions for both.

Conn runs on your machine and listens on 127.0.0.1 only. It reads the files Claude Code already writes under `~/.claude`, and it has no account or service of its own.

## What it does

- Shows every live session with its status, cost estimate and repository, and a tree of the agents and subagents inside it. A session waiting on a prompt is marked and sorted first. See [sessions](docs/sessions.md).
- For sessions running in cmux, lets you type a reply, or allow once or deny a permission prompt, from the browser. The prompt is read again right before your answer is sent, and nothing is sent if it changed.
- Keeps a searchable archive of finished sessions, each with a short summary. See [history](docs/history.md).
- Lists your open pull requests with their unresolved review threads. One click starts a Claude Code session, in its own git worktree, that fixes what is open. See [my PRs](docs/my-prs.md).
- Lists the pull requests that ask for your review, from GitHub and optionally Slack. One click starts a review session in a worktree. See [reviews](docs/reviews.md).
- Lists the Jira tickets in your current sprint. Conn researches a ticket, asks you what it cannot settle, starts a build session, and opens the PR when you confirm. See [open work](docs/open-work.md).
- Ships four built-in themes and a gallery. A theme is a JSON file. See [using themes](docs/using-themes.md) and [writing a theme](docs/themes.md).

Choosing a repository is the only setup. My PRs and Reviews then work through `gh`. The Slack scans and Open work stay off until you turn on the Slack and Atlassian modules in Settings.

## Requirements

- [Go](https://go.dev) 1.27 or later, to build Conn.
- [Bun](https://bun.sh) 1.4 or later, to build the UI.
- macOS. The server calls `ps`. Other platforms are untried.
- Claude Code, with `claude` on your PATH. History summaries, Reviews scans and Open work run `claude -p`.

Optional:

- `gh`, signed in. Needed for My PRs, Reviews and the PR links on sessions.
- cmux. Needed to reply from the browser, answer prompts, and launch review and build sessions.
- The claude.ai Slack and Atlassian connectors. Needed for the Slack scans in Reviews and for Open work.

## Quick start

```sh
git clone https://github.com/claytoncasey01/conn
cd conn
make serve
```

`make serve` builds the UI and the `conn` binary (`bin/conn`, with the UI built in), then runs `conn serve`, which listens on http://127.0.0.1:7777. Open that address in your browser. Ctrl-C stops it.

One Conn runs per data folder. A second `conn serve` on the same folder prints the address of the one already running and exits, and `conn stop` stops it. A port that another program holds is an error that says so: set `CONN_PORT` to a free one.

On first start every page except Themes sends you to Settings. Choose the repositories Conn may read, as patterns such as `acme/widgets` or `acme/*`. Nothing is fetched until you do. The allowlist limits what Conn reads, not what a Claude Code session can reach on its own. See [settings](docs/settings.md).

## What Conn touches

- **Network.** The server makes one kind of web request of its own: the theme gallery reads `raw.githubusercontent.com` with plain GETs that carry no credentials. `CONN_THEME_INDEX=off` turns it off. GitHub goes through your `gh`. Slack, Jira and Confluence go through short headless `claude -p` runs that use your claude.ai connectors. Those runs cost tokens, and each is capped in cost and time. There is no telemetry.
- **Fonts.** Themes name Google Fonts families, and the page loads the active theme's from `fonts.googleapis.com`, so your browser contacts Google. The glyph font for Matrix's rain is bundled.
- **Files.** Conn keeps its data in `~/.conn` (`CONN_DATA` changes it): the History archive, `settings.json`, `reviews.json`, `myprs.json` and `conn.lock`, which names the server running on the folder. They are readable only by you. `history.db` holds the text of your prompts and results, so treat it like `~/.claude`.
- **Git.** Review and self-review sessions work in their own git worktrees, never in your working copy. Each page's doc lists everything Conn writes and calls for that feature.
- **Writes.** Every request that changes something needs the `x-conn: 1` header and a local `Origin`.

Review and self-review sessions are ordinary Claude Code sessions running as you, with your own settings and `gh` login. Their prompts and deny lists narrow what they do, and they do not make it impossible for a session to act on GitHub. The limits are described in [my PRs](docs/my-prs.md).

## Configuration

Most settings are on the Settings page. A few come from the environment:

| Variable | Default | Purpose |
|---|---|---|
| `CONN_DATA` | `~/.conn` | Where Conn keeps its data |
| `CONN_PORT` | `7777` | Dashboard port |
| `CONN_TRANSCRIPTS` | `~/.claude/projects` | Where Claude Code's transcripts are read from |
| `CONN_CLAUDE`, `CONN_CMUX` | `claude`, `cmux` | Binaries to run |
| `CONN_SUMMARY_MODEL` | Haiku | Model for History summaries |
| `CONN_THEME_INDEX` | `claytoncasey01/conn-themes` | Gallery repository, or `off` |
| `CONN_REVIEW_ME` | from `gh` | Your GitHub login |
| `CONN_REVIEW_CLOSE_MIN` | `10` | Minutes idle before a finished review session is closed (1 to 60) |

The variables that tune My PRs are listed in [my PRs](docs/my-prs.md).

## Development

```sh
make dev      # hot reload: UI on :5173, API on :7778 (restarts when a Go file changes), data in ~/.conn-dev
make test     # bun test for the UI and themes; go test with the race detector, in random order
make lint     # golangci-lint, every linter on (.golangci.yml)
make fmt      # gofumpt and goimports
make check    # make lint, then svelte-check
make build    # bin/conn
```

[AGENTS.md](AGENTS.md) has the rules for writing Go here, for people and coding agents alike.

`make dev` has its own data folder, so it never shares `history.db` or `reviews.json` with the dashboard on :7777. To work without real accounts, set `CONN_MYPRS_DRY=1`, `CONN_REVIEWS_DRY=1` or `CONN_WORK_DRY=1`. They fake My PRs, Reviews and Open work with invented data and make no GitHub, Slack or Jira call. See [dry runs](docs/settings.md).

Layout:

| Path | Contents |
|---|---|
| `cmd/conn/` | The `conn` command |
| `internal/` | The server and everything it reads and runs |
| `src/pages/`, `src/lib/` | The Svelte 5 pages and shared code |
| `src/effects/` | Theme effects such as the Matrix rain and the CRT tube |
| `themes/` | Built-in themes, and `_template/` to copy |
| `gallery-seed/` | Example gallery themes |
| `agents/` | Read-only helper agents that Open work build sessions use. Copy them to `~/.claude/agents/` |
| `cmd/dev/` | The development loop behind `make dev` |
| `testdata/` | Cases the UI's tests and the server's tests share |
| `docs/` | Reference for each feature |

Run `bun run theme:check` after editing a theme.

To keep names out of the repository, such as your employer's, list them in `.company-terms` at the repository root. The file is git-ignored. Put one term per line, use `#` for a comment, and start a line with `re:` for a regular expression. `go test ./...` then fails, naming the file and line, if a tracked file or file name contains a term. `CONN_GUARD_TERMS=<path>` points at a list kept elsewhere. Without a list the check is skipped.

## Status

Version 0.1.0. Parts of Reviews have not been run against real Slack or a real GitHub organization. [Reviews](docs/reviews.md) lists which.
