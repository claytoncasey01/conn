# Conn

A local dashboard for Claude Code. It shows your live sessions and the agents inside each one, keeps an archive of finished sessions, and can list the pull requests that need your review, track your own, and start Claude Code sessions for both.

Conn runs on your machine and listens on 127.0.0.1 only. It reads the files Claude Code already writes under `~/.claude`, and it has no account or service of its own.

## What it does

- Shows every live session with its status, cost estimate and repository, and a tree of the agents and subagents inside it. A session waiting on a prompt is marked and sorted first. See [sessions](docs/sessions.md).
- Runs the sessions it starts inside itself, on a terminal the page shows, so no terminal app is needed. You can type into that terminal, or type a reply, or allow once or deny a permission prompt, from the page. The prompt is read again right before your answer is sent, and nothing is sent if it changed. See [sessions inside Conn](#sessions-inside-conn).
- Keeps a searchable archive of finished sessions, each with a short summary. See [history](docs/history.md).
- Lists your open pull requests with their unresolved review threads. One click starts a Claude Code session, in its own git worktree, that fixes what is open. See [my PRs](docs/my-prs.md).
- Lists the pull requests that ask for your review, from GitHub and optionally Slack. One click starts a review session in a worktree. See [reviews](docs/reviews.md).
- Lists the Jira tickets in your current sprint. You choose the repository, or several, for each ticket when you start it. Conn researches the ticket, asks you what it cannot settle, starts a build session (one across the repositories, or one in each), and opens a PR in each repository when you confirm. See [open work](docs/open-work.md).
- Ships four built-in themes and a gallery. A theme is a JSON file. See [using themes](docs/using-themes.md) and [writing a theme](docs/themes.md).

Choosing a repository is the only setup. My PRs and Reviews then work through `gh`. The Slack scans and Open work stay off until you turn on the Slack and Atlassian modules in Settings.

## Install

```sh
curl -fsSL https://github.com/claytoncasey01/conn/releases/latest/download/install.sh | sh
```

The script downloads the latest release for your Mac or Linux machine (amd64 or arm64), checks it against the checksums published with the release, and puts `conn` in `~/.local/bin`. On a Mac it also installs [the app](#the-app), `Conn.app`, into `~/Applications`, checked the same way. It asks for no password and changes nothing else: no settings, and no skills (those are installed from the Settings page). Read it first if you like: it is [`install.sh`](install.sh) in this repository, and the copy on each release is the one that release was built with.

| Variable | Default | Purpose |
|---|---|---|
| `CONN_INSTALL_VERSION` | the latest release | A release tag to install, such as `v1.2.3` |
| `CONN_INSTALL_DIR` | `~/.local/bin` | Where the binary goes |
| `CONN_INSTALL_APP_DIR` | `~/Applications` | Where `Conn.app` goes, on a Mac |

To install the command-line tool only, pass `--cli-only`: `curl -fsSL https://github.com/claytoncasey01/conn/releases/latest/download/install.sh | sh -s -- --cli-only`. The script does not replace `Conn.app` while it is running: it stops and says so before it changes anything, and you quit Conn and run it again. A release that has no app installs the command-line tool and says so. Linux gets the command-line tool only.

Run it again to update. If `~/.local/bin` is not on your `PATH`, the script says so. On a Mac, open Conn from `~/Applications`. In a browser, or on Linux, run:

```sh
conn serve
```

It listens on http://127.0.0.1:7777 and prints the page's address with a secret after `#k=` (made anew at each start): open that address in your browser. The page keeps the secret for the tab and removes it from the address bar. When `conn serve`'s output is not a terminal (a pipe, a file, a log), it prints the address without the secret and names the lock file the secret is in; add `#k=` and the lock file's `secret` to the address. `conn version` prints the release you have. How releases are made is in [releasing](docs/releasing.md).

### The app

`Conn.app` is the same server in a window of its own, with an icon in the Dock. It shows a splash while it starts, then the page. It needs macOS 13 or later, on Apple silicon or Intel.

- Closing the window hides it and leaves Conn running, with every session inside it. Click the Dock icon to bring the window back. Cmd-Q, or Quit in the app menu, quits, and stops those sessions; the next launch resumes them (see [sessions inside Conn](#sessions-inside-conn)).
- It listens on the same port as `conn serve` (7777, or `CONN_PORT`) and keeps its data in the same folder, so the app and the command line show the same Conn. If another program holds the port, or the data folder cannot be read, the app says so in a dialog.
- If a Conn is already running on the data folder, such as a `conn serve` in a terminal, the app opens its window on that server. Quitting the app then leaves that server running. Only a server the app started is stopped when it quits: to stop it, quit the app (Cmd-Q). The server runs inside the app, so `conn stop` on such a server ends the app too.
- A link to a pull request, a Slack thread or any other site opens in your default browser, and the window stays on Conn. That holds for a link you click, a form, a script and a redirect alike. Links that are not web addresses are not opened. A page inside a frame is not watched.
- If the server under the window goes away or stops answering, the app starts a server of its own, or opens on the new holder of the data folder. If it has started over 6 times in a row without an answer from Conn, it stops and shows a dialog.
- Alerts are macOS notifications. The first time you turn them on (the `Alerts` button, or the Alerts row in Settings, also shown at first launch), macOS asks whether Conn may show notifications. It asks only once: after a no, Settings opens System Settings at Notifications, where you allow Conn, and Conn notices when you come back. A notification shows only while Conn's window is hidden or not in front, and clicking one brings the window back on the page it is about. The window keeps reading Conn's updates while it is hidden, so alerts keep coming after you close it (on macOS 14 and later; macOS 13 may pause a hidden window after some minutes). macOS lets an app ask only from a usual place such as `~/Applications`: run from a temporary folder, Conn says notifications are unavailable.
- **App icon** in Settings changes Conn's Dock icon to match your theme (the default), to the classic, or to one theme's look. The Dock keeps it while the app is closed and kept in the Dock. Finder and Launchpad keep the classic icon. See [App icon](docs/settings.md).
- An app opened from the Dock gets almost no environment, so at start it runs your login shell once, as a terminal does, and takes the environment from it. That is how `claude`, `gh`, `git` and your tokens are found. The environment is kept in the app's memory and is not logged or written to a file. If the shell cannot be read, the app says why in a dialog (a short reason only, never what the shell printed) and carries on with its own environment.
- About 10 seconds after it has finished starting (once it has read your login shell's environment), and then once a day, the app asks GitHub whether a newer release exists. **Check for Updates…**, right under About in the Conn menu, asks at once and answers in a dialog. When there is a newer release whose app is ready to download, the item reads **Update Available: v1.2.4…** and a dialog says so, once for each version in each run of the app, with three buttons: **Copy Command** puts the install command on the clipboard, **Release Notes** opens the release in your browser, and **Later** closes it. The app installs nothing: to update, quit Conn and run the install command again. `CONN_UPDATE_CHECK=off` turns the check off and hides the item; the app reads it from your login shell's environment too. A development build, whose version is not a release, never checks.
- The app is signed ad hoc and not notarized, until there is an Apple Developer account for it. `install.sh` installs it without a warning, because `curl` does not mark the file as downloaded from the internet. A zip downloaded in a browser is marked, and macOS refuses it until you run `xattr -dr com.apple.quarantine Conn.app`.

What you need besides the binary:

- Claude Code, with `claude` on your PATH. History summaries, Reviews scans and Open work run `claude -p`.
- macOS is the platform Conn is used on: the server calls `ps`. The Linux builds are published and untried. On Linux, run Conn under an init that reaps orphaned processes (`docker run --init`, a systemd service, a normal login session): under a PID 1 that does not, a session whose children outlive it can keep its place until Conn restarts (see [SECURITY.md](SECURITY.md#sessions-inside-conn)).

Optional:

- `gh`, signed in. Needed for My PRs, Reviews and the PR links on sessions.
- cmux. Only if you set sessions to open in your terminal (Settings, Where sessions open), or to reply to a session you started in cmux yourself. Sessions Conn starts run inside Conn by default and need no terminal app.
- The claude.ai Slack and Atlassian connectors. Needed for the Slack scans in Reviews and for Open work.

## Build from source

You need [Go](https://go.dev) 1.27 or later and [Bun](https://bun.sh) 1.4 or later (the UI is built in), besides what is listed above.

```sh
git clone https://github.com/claytoncasey01/conn
cd conn
make serve
```

`make serve` builds the UI and the `conn` binary (`bin/conn`, with the UI built in), then runs `conn serve`, which listens on http://127.0.0.1:7777. Open the address it prints, which carries the secret. Ctrl-C stops it.

The docs run the command as `conn`: put the checkout's `bin/` on your `PATH`, or copy `bin/conn` to a folder that is on it, or type `bin/conn` from the checkout.

One Conn runs per data folder. A second `conn serve` on the same folder prints the address of the one already running (with its secret, on a terminal) and exits, and `conn stop` stops it. A port that another program holds is an error that says so: set `CONN_PORT` to a free one.

On first start every page except Themes sends you to Settings. Choose the repositories Conn may read, as patterns such as `acme/widgets` or `acme/*`. Nothing is fetched until you do. The allowlist limits what Conn reads, not what a Claude Code session can reach on its own. See [settings](docs/settings.md).

## Sessions inside Conn

Open work, Reviews, My PRs and History's RESUME start Claude Code sessions, and **New session** on the Sessions page starts a plain one in a repository you choose (see [sessions](docs/sessions.md#sessions-inside-conn)). By default each one runs inside Conn: the Sessions page and the Open work run page show its terminal, which you can type into from the browser or the app. Several tabs, or the app and a browser, see the same screen, up to 4 at once. A program that floods the terminal faster than the page can draw closes the panel; **Interrupt** then sends it Ctrl-C.

- Closing the app's window keeps every session running. Opening it again shows each one as it was.
- Quitting Conn (Cmd-Q, or Ctrl-C or SIGTERM to `conn serve`) stops every session, after writing down which ones ran. A second Ctrl-C or SIGTERM to `conn serve` while it stops them quits at once, once that list is written: the sessions still running end as their terminals hang up.
- The next launch resumes those sessions on their own conversations. The page says how many, and offers **Stop all**. *Resume on launch* in Settings turns this off.

Settings, *Where sessions open*, can send new sessions to cmux instead, or show the command to paste into any terminal. How the terminal is protected, and what is kept on disk, is in [SECURITY.md](SECURITY.md#sessions-inside-conn).

## What Conn touches

- **Network.** The server makes one kind of web request of its own: the theme gallery reads `raw.githubusercontent.com` with plain GETs that carry no credentials. `CONN_THEME_INDEX=off` turns it off. The macOS app makes one more: about 10 seconds after it has finished starting, and then daily, a `HEAD` request to `github.com` for the address of the latest release, to learn whether a newer one exists, and only when one does, a second `HEAD` request to learn whether that release's app is attached yet, so it never offers a release the install command cannot deliver. They send no version of Conn and nothing of yours, read only the status and the redirect and no body, and GitHub sees the address they came from and the client name `conn-app`. `CONN_UPDATE_CHECK=off` turns it off. GitHub goes through your `gh`. Slack, Jira and Confluence go through short headless `claude -p` runs that use your claude.ai connectors. Those runs cost tokens, and each is capped in cost and time. There is no telemetry.
- **Fonts.** Themes name Google Fonts families, and the page loads the active theme's from `fonts.googleapis.com`, so your browser contacts Google. The glyph font for Matrix's rain is bundled.
- **Files.** Conn keeps everything it saves in one folder, `~/.config/conn` (`$XDG_CONFIG_HOME/conn` when that is set to an absolute path; `CONN_DATA` changes it): the History archive, `settings.json`, `reviews.json`, `myprs.json`, `hosted.json` (the sessions running inside Conn, to resume at the next launch: ids, names and folders, no command line; mode 0600) and `conn.lock`, which names the server running on the folder and holds its secret (mode 0600). It also holds copies of the agents and skills Conn provides (`agents/`, `skills/`); the agents are linked into `~/.claude/agents` when the server starts, and a skill is linked into `~/.claude/skills` only when you press INSTALL on the Settings page. See [Agents and skills](docs/settings.md#agents-and-skills). Conn creates the folder private to you (mode 0700). A `~/.conn` folder from an earlier version is not read or moved: Conn starts fresh in the new folder, and the old one can be deleted or copied over by hand. `history.db` holds the text of your prompts and results, so treat it like `~/.claude`.
- **The app's icon.** The app writes the Dock icon you choose (Settings, App icon) to `~/Library/Application Support/<the app's bundle identifier>/DockIcon.png`, in a folder private to you, and removes it when you go back to the classic. The app's Dock tile plug-in, which the Dock runs while the app is closed, only reads that file.
- **Your shell.** The app runs your login shell once, when it starts, to read the environment that `claude`, `gh` and `git` need. It keeps that environment in memory and never logs it or writes it down. `conn serve` does not do this.
- **Git.** Review and self-review sessions work in their own git worktrees, never in your working copy. Each page's doc lists everything Conn writes and calls for that feature.
- **The API.** Every `/api` request needs the secret this launch of Conn made (256 random bits, in an `Authorization: Bearer` header), a `Host` that is a loopback name, and no `Origin` or exactly the server's own; every request that changes something also needs the `x-conn: 1` header. The page itself and its files need none. The page carries a Content-Security-Policy: scripts only from Conn itself, no inline script, requests only to Conn, and never in a frame.

Review and self-review sessions are ordinary Claude Code sessions running as you, with your own settings and `gh` login. Their prompts and deny lists narrow what they do, and they do not make it impossible for a session to act on GitHub. The limits are described in [my PRs](docs/my-prs.md).

## Configuration

Most settings are on the Settings page. A few come from the environment:

| Variable | Default | Purpose |
|---|---|---|
| `CONN_DATA` | `~/.config/conn` (`$XDG_CONFIG_HOME/conn` when set) | Where Conn keeps its data |
| `CONN_PORT` | `7777` | Dashboard port |
| `CONN_TRANSCRIPTS` | `~/.claude/projects` | Where Claude Code's transcripts are read from |
| `CONN_CLAUDE` | `claude` | The Claude Code binary. Inside Conn it must be `claude` itself or a wrapper that `exec`s it |
| `CONN_CMUX` | `cmux` | The cmux binary: for sessions set to open in your terminal, and replies to sessions you run in cmux |
| `CONN_SUMMARY_MODEL` | Haiku | Model for History summaries |
| `CONN_THEME_INDEX` | `claytoncasey01/conn-themes` | Gallery repository, or `off` |
| `CONN_UPDATE_CHECK` | on | `off` stops the app's check for a newer release |
| `CONN_REVIEW_ME` | from `gh` | Your GitHub login |
| `CONN_REVIEW_CLOSE_MIN` | `10` | Minutes idle before a finished review session is closed (1 to 60) |

The variables that tune My PRs are listed in [my PRs](docs/my-prs.md).

## Development

```sh
make dev      # hot reload: UI on :5173, API on :7778 (restarts when a Go file or a built-in theme changes), data in ~/.config/conn-dev
make test     # bun test for the UI and themes; go test with the race detector, in random order
make lint     # golangci-lint, every linter on (.golangci.yml)
make fmt      # gofumpt and goimports
make check    # make lint, then svelte-check
make build    # bin/conn
scripts/build-app.sh <out>        # Conn.app in a zip, with its checksums, in <out>
scripts/check-app.sh <Conn.app>   # the signature, both architectures (the app and its Dock tile plug-in), and that the binary starts
```

Building the app needs a Mac, the Xcode command line tools and cgo. Run `bun run build` first, because the app embeds the page. Everything else is pure Go, and `conn` builds and tests on Linux.

[AGENTS.md](AGENTS.md) has the rules for writing Go here, for people and coding agents alike.

`make dev` prints the UI's address with the API's secret each time the API starts; the secret is new at each restart, so open the new address. It has its own data folder, so it never shares `history.db` or `reviews.json` with the dashboard on :7777. To work without real accounts, set `CONN_MYPRS_DRY=1`, `CONN_REVIEWS_DRY=1` or `CONN_WORK_DRY=1`. They fake My PRs, Reviews and Open work with invented data and make no GitHub, Slack or Jira call. See [dry runs](docs/settings.md).

Layout:

| Path | Contents |
|---|---|
| `cmd/conn/` | The `conn` command |
| `cmd/conn-app/` | The macOS app (`Conn.app`), built only on a Mac |
| `internal/` | The server and everything it reads and runs |
| `src/pages/`, `src/lib/` | The Svelte 5 pages and shared code |
| `src/effects/` | Theme effects such as the Matrix rain and the CRT tube |
| `themes/` | Built-in themes, and `_template/` to copy |
| `gallery-seed/` | Example gallery themes |
| `agents/` | Read-only helper agents that Open work build sessions use. Conn links them into `~/.claude/agents/` when it starts, in the default data folder and not in a dry run |
| `skills/` | Skills Conn provides. Each is linked into `~/.claude/skills/` only when you press INSTALL on the Settings page |
| `cmd/dev/` | The development loop behind `make dev` |
| `testdata/` | Cases the UI's tests and the server's tests share |
| `docs/` | Reference for each feature |

Run `conn theme check` after editing a theme.

To keep names out of the repository, such as your employer's, list them in `.company-terms` at the repository root. The file is git-ignored. Put one term per line, use `#` for a comment, and start a line with `re:` for a regular expression. `go test ./...` then fails, naming the file and line, if a tracked file or file name contains a term. `CONN_GUARD_TERMS=<path>` points at a list kept elsewhere. Without a list the check is skipped.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) says how to build, test and send a change. Report a security problem as [SECURITY.md](SECURITY.md) describes, not in a public issue. Themes for the gallery go to [conn-themes](https://github.com/claytoncasey01/conn-themes).

## License

Apache-2.0: see [LICENSE](LICENSE) and [NOTICE](NOTICE). The Matrix rain's glyph font, `src/effects/fonts/matrix-code/`, is SIL OFL 1.1.

## Status

Version 0.1.0. Parts of Reviews have not been run against real Slack or a real GitHub organization. [Reviews](docs/reviews.md) lists which.
