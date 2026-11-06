# Sessions hosted inside Conn

## Why

Conn starts Claude sessions (Open work builds, Reviews, My PRs, History resume) by asking a
terminal app, cmux, to open one, and talks to a running session by reading and typing into that
terminal. That makes Conn depend on one terminal, and makes "answer from the page" work only where
a terminal driver exists. Conn is open source and is also a desktop app: it should be
self-contained and not care which terminal anyone uses.

So Conn **hosts the session itself**: the server starts `claude` on a pseudo-terminal it owns,
the page shows that terminal, and a reply or an answer is typing into a process Conn holds.

## Goals

- A session Conn starts runs inside Conn, in the browser page and in the desktop app, with the real
  Claude Code screen, on macOS and Linux, with no terminal app installed.
- Answering a prompt, typing a reply and FIX FAILING CHECKS work for every hosted session.
- Quitting and reopening the app brings back what was open (see Lifecycle).
- The terminal is as safe as the rest of the app: a web page, another local user or a hostile file
  cannot run anything through it (see Security, which is the first constraint, not the last).
- Themeable: the terminal takes its colours from the current theme.
- Several sessions at once, including the several sessions of a multi-repository run.

## Non-goals

- Windows (it needs ConPTY).
- Taking over a `claude` the person started by hand in their own terminal. Those stay as today:
  listed, and replyable only where the cmux code already handles it. That code stays as it is and
  grows no new terminals.
- Keeping sessions alive after the app is fully quit, or a detached per-session process.
- Replacing the Claude Code interface with a custom chat view.
- Recording or exporting terminal content.

## Anti-goals

- Any route, message or setting through which a client can name a command, an argument, an
  environment variable or a folder to run. Hosted sessions are created by Conn's own code from
  records it already validates, never from a request.
- Terminal content on disk or in a log.
- A terminal feature enabled "for convenience" whose abuse is a clipboard write, a reply typed into
  the shell, or a link opened without the app's link policy.

## Lifecycle

- **Close the window** (the X, Cmd-W): the window hides, the app stays in the Dock and every
  session keeps running. This is what the desktop app does today. Opening the window again, from
  the Dock or `conn serve`'s page, re-attaches to each session with its screen as it was.
- **Quit the app** (Cmd-Q, the Quit menu, SIGINT or SIGTERM to `conn serve`): every hosted session
  is stopped (a polite stop, then a kill after `stopGrace`, all bounded). Before that, the
  sessions open or in progress at that moment are recorded.
- **Open the app again**: every recorded session is resumed on its own conversation,
  automatically. A session that had already ended before the quit is not. `resumeOnLaunch`
  (Settings, default on) turns this off for people who do not want processes started at launch.
  The page says how many were resumed.
- **A crash or `kill -9`** ends the sessions (the kernel hangs up each terminal). The record is
  kept up to date as sessions start and end, so the next launch resumes them the same way.
- The record is `$CONN_DATA/hosted.json`, mode 0600: for each session an id, the owner (Open work
  run and leg, review, My PRs entry or History session), the name, the folder and Claude's session
  id. **It never holds a command line.** On launch the owner's own code rebuilds the command from
  those fields, re-checking everything it checks today (repository still allowed, folder still
  exists and is a worktree Conn made, session id is a UUID), so editing the file cannot start
  anything the app would not start itself. At most `sessionsMax` (12) are resumed.

## Security

Threat model: a web page the person visits; another process or user on the machine; text from
Jira, Slack, a repository or Claude's output (attacker-influenced); a tampered file in `$CONN_DATA`;
a malicious dependency. The terminal is code execution as the person, so each of these is handled
explicitly.

1. **No client-chosen execution.** The only things a client can do to a session are attach to an id
   the server issued, send input, resize and detach. Creating a session is a Go call from the
   modules, never an HTTP route.
2. **The network surface.** The server listens on `127.0.0.1` only (as now). Terminal endpoints
   additionally require: a Host of this machine and, on the WebSocket handshake, an `Origin` equal
   to the server's own origin exactly (the existing `local()` check, tightened to equality); and a
   **per-launch secret** a second local user cannot obtain from the port. The secret is 256 bits from
   `crypto/rand`, kept in memory, compared in constant time. `conn serve` prints the URL with the
   secret in its fragment (never sent to the server, never logged); the page exchanges it once for a
   `HttpOnly; SameSite=Strict` cookie scoped to the terminal paths; the desktop app does the same
   with a URL it builds itself. The secret is never in a query string, a log line, a process
   argument visible to `ps` after start-up, or the child's environment.
3. **The WebSocket.** `github.com/coder/websocket` (already in the module graph through Wails;
   promote it to a direct, allowlisted dependency). The handshake is rejected on a bad Host, Origin
   or cookie. Frames are bounded: `inputFrameBytesMax` 64 KiB, a per-session input rate cap, one
   outbound queue of `viewerQueueBytesMax` per viewer (a viewer that falls behind is dropped, the
   terminal's reader is never blocked), `viewersMax` 4 per session, a handshake deadline, pings. The
   message parser is fuzzed.
4. **The child process.** Its own session and process group (so a stop reaches everything it
   started), close-on-exec descriptors, the person's login environment (from `shellenv`) with
   `CONN_*` variables and the secret removed, the claude binary from `CONN_CLAUDE`. A stop signals
   the group.
5. **Terminal output is untrusted.** The page's terminal (xterm.js, bundled and pinned by the
   lockfile, no CDN) has: clipboard writes (OSC 52) off; window-manipulation reports off (they echo
   attacker text into the input); the title shown only as a sanitised label; hyperlinks passed to
   Conn's existing link policy (default browser, no navigation of the app window); no image
   protocols; no addon that reads the clipboard. Conn's server-side screen model answers only the
   queries a TUI needs (cursor position, device attributes) and never one that echoes text.
6. **A reply is checked, then typed.** The existing rule is kept: the screen is read first and
   nothing is typed if the prompt is not what the page showed. For a hosted session the screen
   comes from Conn's own model of the terminal, so the check and the typing see the same process.
7. **Nothing sensitive at rest.** Scrollback (`scrollbackBytesMax` 4 MiB per session) is memory
   only. No keystroke or screen content is logged, including on error. `hosted.json` holds ids
   only.
8. **The page.** A Content-Security-Policy of `default-src 'self'` with no inline script and a
   `connect-src` for this origin's WebSocket; `frame-ancestors 'none'`.
9. **Supply chain.** Two new dependencies on top of Wails: a Go pseudo-terminal/screen-model pair
   chosen by the builder, and xterm.js with its fit addon. Each is justified in the PR, pinned, and
   allowlisted.
10. **Documentation.** `SECURITY.md` gets a section on what the terminal can do and how it is
    protected, and the new weakness classes it counts as in scope.

Tests that must exist: wrong Host, wrong or missing Origin, missing or wrong cookie, an oversize
frame, a flood of input and a slow viewer are each refused or bounded; no request creates a
session; a stop kills a child that ignores SIGTERM and its own children; the secret is absent from
the child's environment, argv and every log line; a tampered `hosted.json` (a command line, a
folder outside the allowed worktrees, a session id that is not a UUID) starts nothing.

## Behaviour

- **Where sessions open** (Settings, Atlassian-independent "Sessions" card): *Inside Conn*
  (default), *My terminal* (the cmux code as it is, plus an optional custom open command with
  `{name}`, `{dir}` and `{command}` placeholders, no shell), or *Show me the command* (what
  happens today when cmux fails). Each module reads it when it starts a session.
- **The page.** A hosted session has a Terminal panel on the Sessions page and on the Open work run
  page: the live screen, keyboard input, resize, a Detach/collapse control, and the existing ask
  panel above it. Opening the panel late shows the scrollback, then live output. Several viewers
  of one session see the same screen.
- **The server.** `internal/pty`-style package: start, write, resize, stop, exit status, a bounded
  output ring, and a screen model (a Go VT emulator) from which `ask.Parse` and the reply check
  read. A `Spawner` seam lets the dry run (`CONN_WORK_DRY=1`) and the tests start a scripted fake
  instead of `claude`.
- **Open work.** `openWorkspace` becomes "start a hosted session for this leg" (shared layout: one
  session in the lead's worktree with `--add-dir` for the others; split layout: one per leg), and
  `ManualCommand` stays as the fallback for the other two settings. `Resume` reopens a leg's
  session the same way.
- **Reviews, My PRs, History resume** start their sessions the same way.
- **Themes.** The terminal's 16 ANSI colours, foreground, background, cursor and selection are
  derived from the theme's existing tokens, so every theme works with no change to the theme
  files or the gallery.

## Constraints

- AGENTS.md applies: Data Oriented Design (plain-data `Spec`, `Record` and `Screen` types, logic in
  functions, I/O at the edges), TigerStyle adapted for Go (limits as named constants, no compound
  conditions, small functions, assertions as checked errors or panics for program bugs), and
  Terraform's practices (small packages, no package-level state, errors with context,
  `context.Context` first, table tests, `realMain`).
- No `//nolint`. `make check`, `make test`, `make lint` clean; the UI tests pass.
- Contract changes (new `/api` routes, the sessions JSON, `settings.json`, `hosted.json`) are made
  together with the page, with `testdata/answers/` updated.
- Single-repository Open work behaves as before apart from where the session opens.

## Left to the builder

- Which Go pseudo-terminal and screen-model libraries (or small own code over `x/sys/unix`), with
  the dependency justification.
- Package names and layout; the WebSocket message encoding; the cookie exchange mechanics, within
  the Security section.
- How the Wails app and `conn serve` each hand the secret to the page.
- How `ask.Parse` is fed from the screen model without changing its behaviour.
- The Terminal panel's layout, within the existing visual language.

## Decisions made without the person (my calls, to check)

- The real Claude Code interface in a terminal panel, not a custom view over its JSON stream.
- WebSocket (not SSE plus POST) for the byte stream.
- `sessionsMax` 12, `scrollbackBytesMax` 4 MiB, `viewersMax` 4, `inputFrameBytesMax` 64 KiB.
- Auto-resume on launch is on by default with a setting to turn it off.
- The cmux code stays as the *My terminal* option and for adopting sessions; nothing new for other
  terminals.
- A separate PR from multi-repository Open work, based on `main`; whichever lands second rebases.
