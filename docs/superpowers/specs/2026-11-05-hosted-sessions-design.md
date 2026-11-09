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
- A screen-reader rendering of the terminal (xterm.js's screen-reader mode is off). The ask panel,
  the reply box and the session rows stay readable by assistive technology.

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
  automatically, inside Conn whatever the settings say (a resume never opens cmux or prints a
  command). A session that had already ended before the quit is not. The launch serves the page
  first, waits for the session list's first read (at most `firstTickTimeout`, 15 s; with none,
  nothing is resumed and the records are kept), then resumes. A quit during the resume waits for
  it (at most `quitResumeTimeout`, 10 s) and keeps the records it did not reach. The page says how
  many were resumed and which, with Stop all; `conn serve` also says on stderr what it resumed,
  kept or dropped.
- **`resumeOnLaunch`** (Settings, default on) off: the launch drops the record and resumes
  nothing. **Sessions set to open elsewhere** (*My terminal*, *Show me the command*): the launch
  resumes nothing and keeps the record for when sessions open inside Conn again, except the
  reviews (Reviews closes a review whose session has not come back, and a closed one is never
  resumed). **A dry run** (`CONN_*_DRY`) resumes nothing: every owner refuses, since a resumed
  session runs the real `claude`.
- **The hold after a launch**: every launch, even one that resumes nothing, holds Reviews' and My
  PRs' "the session is gone" judgements until the resume is done and for `appearMs` (5 min)
  after: no review is closed or forgotten, no round is read as over, REVIEW is refused for a PR
  with an open launch, and a My PRs press is refused beside an open round in a folder the record
  named. A review that really ended may close up to 5 minutes late.
- **A crash or `kill -9`** ends the sessions (the kernel hangs up each terminal). The record is
  kept up to date as sessions start and end, so the next launch resumes them the same way.
- The record is `$CONN_DATA/hosted.json`, mode 0600, written to a temporary file and renamed:
  for each session the host's id, the owner (`openwork`, `reviews`, `myprs` or `history`), the
  name, the folder and Claude's session id. **It never holds a command line.** It is read only as
  a regular file, not a link, owned by the person, of mode exactly 0600, at most `fileBytesMax`
  (about 373 KiB), with no unknown field and exactly the bytes Conn would write; otherwise none of
  it is used, it is moved to `hosted.json.refused`, and nothing starts. On launch the owner's own code rebuilds the command from
  those fields, re-checking everything it checks today (repository still allowed, folder still
  exists and is a worktree Conn made, session id is a UUID), so editing the file cannot start
  anything the app would not start itself. At most `sessionsMax` (12) are resumed. The checks per
  owner: **Open work**, the leg's worktree exists and its repository is allowed, and the session id
  is a UUID (a record without one is not resumed at launch; the RESUME SESSION button keeps its
  `--continue` fallback); **Reviews and My PRs**, GitHub on, the worktree is exactly a folder under
  `$CONN_DATA/reviews` or `$CONN_DATA/myprs` (each folder on the way the person's own and not a
  link, with a `.git`, in an allowed repository), the session id is a UUID, the name has the
  kind's shape, the module's store holds an open launch with that worktree, name and
  conversation (resumed on the launch's model, a self-review with its deny list), and no session
  of that name or in that worktree runs; **History**, the archive holds the session, it is not
  running or just resumed, its transcript and folder exist, the folder is the archive's, and it
  belongs to an allowed repository. "In progress" means the session's
  process was alive at the quit, in any state.

## Security

Threat model: a web page the person visits; another process or user on the machine; text from
Jira, Slack, a repository or Claude's output (attacker-influenced); a tampered file in `$CONN_DATA`;
a malicious dependency. The terminal is code execution as the person, so each of these is handled
explicitly.

1. **No client-chosen execution.** The only things a client can do to a session are attach to an id
   the server issued, send input, resize and detach. Creating a session is a Go call from the
   modules, never an HTTP route. One input needs no attached terminal: `POST
   /api/term/<id>/interrupt` (no body, behind the secret, Origin and `X-Conn`, under the session's
   input rate cap) types the single byte a keyboard's Ctrl-C does (ETX) through the host's one
   writer, so a program flooding output faster than the page can draw it, whose viewer was
   dropped, can still be interrupted. It is input, never a signal. A second interrupt within
   3 seconds is refused, since two Ctrl-C close together make `claude` quit.
2. **The network surface.** The server listens on `127.0.0.1` only (as now). **Every `/api` route**,
   not only the terminal ones (the snapshot, the event stream, the agent pages, every POST and PUT),
   requires a Host of this machine, an `Origin` that is absent only for non-browser clients and
   otherwise equals the server's own origin exactly (the terminal's WebSocket handshake must carry
   it: a browser always does), and a **per-launch secret** a second local user
   cannot obtain from the port. The HTML shell and static assets need none (they hold nothing
   sensitive). The secret is 256 bits from `crypto/rand`, compared in constant time, and travels in
   an `Authorization` header from the page's memory (kept in `sessionStorage` so a reload keeps it),
   never in a cookie (cookies are not scoped by port), never in a query string. The WebSocket proves
   it in the `Sec-WebSocket-Protocol` header, as the subprotocol `conn.key.<secret>` beside
   `conn.term.v1` (the server echoes only `conn.term.v1`), checked before any session is touched. The event stream, which `EventSource` cannot give a header, becomes a `fetch` stream.
   Handing it over: the desktop app builds the page URL itself with the secret in the fragment;
   `conn serve` prints the URL with the secret in its fragment **only when stdout is a terminal**
   (otherwise it prints the URL without it and names the lock file); the page reads the fragment
   once and removes it from the address bar with `history.replaceState`. The secret is also written
   to the data folder's lock file (mode 0600) so a second app or `conn serve` that attaches to a
   running Conn can open a working page. It is never in a log line, the child's environment or
   `ps`. Processes running as the same user can read `$CONN_DATA` and are out of scope: they can
   already run `claude`.
3. **The WebSocket.** `github.com/coder/websocket` (already in the module graph through Wails;
   promote it to a direct, allowlisted dependency). The handshake is rejected on a bad Host, a
   missing or foreign Origin, or a missing or wrong secret. Frames are bounded: `inputFrameBytesMax` 64 KiB, a per-session input rate cap, one
   outbound queue of `viewerQueueBytesMax` per viewer (a viewer that falls behind is dropped, the
   terminal's reader is never blocked), `viewersMax` 4 per session, a handshake deadline, pings. The
   message parser is fuzzed.
4. **The child process.** Its own session and process group (so a stop reaches everything it
   started), close-on-exec descriptors, the person's login environment (from `shellenv`) with
   `CONN_*` variables, the secret and every `CMUX_*` variable removed (a hosted session must never
   look like a cmux pane, or a reply could be sent to Conn's own terminal), the claude binary from `CONN_CLAUDE`. A stop signals
   the group.
5. **Terminal output is untrusted.** The page's terminal (xterm.js, bundled and pinned by the
   lockfile, no CDN) has: clipboard writes (OSC 52) off; window-manipulation reports off (they echo
   attacker text into the input); the program's title never used (the panel is labelled with the
   session's name); no image protocols; no addon that reads the clipboard. A terminal hyperlink
   opens only for `http` or `https` of another origin than Conn's, on Cmd-click or Ctrl-click,
   after a confirmation naming its host, and always in the default browser, never in the app
   window. A paste is cleaned of control characters and paste markers, one larger than an input
   frame is refused whole, and one whose line breaks the program would take as Enters waits for a
   confirmation. Exactly one side answers a terminal's
   queries (device attributes and the background colour Claude Code asks for), from a fixed
   table, and never with text taken from the program's output; the other side is silent. The
   server answers; the page answers nothing. The cursor position is not answered at all: two
   numbers the program chooses could spell a key.
6. **A reply is checked, then typed.** The existing rule is kept: the screen is read first and
   nothing is typed if the prompt is not what the page showed. For a hosted session the screen
   comes from Conn's own model of the terminal, so the check and the typing see the same process.
7. **Nothing sensitive at rest.** Scrollback (`scrollbackBytesMax` 4 MiB per session) is memory
   only. No keystroke or screen content is logged, including on error. `hosted.json` holds ids,
   names and folders, never a command line.
8. **The page.** A Content-Security-Policy: `default-src 'self'`, no inline script (the bootstrap
   script moves to a file), `style-src 'self' 'unsafe-inline'` (Svelte and xterm.js set styles),
   the font hosts the page already uses named explicitly, a `connect-src` for this origin and its
   WebSocket, `frame-ancestors 'none'`.
9. **Supply chain.** Two new dependencies on top of Wails: a Go pseudo-terminal/screen-model pair
   chosen by the builder, and xterm.js with its fit addon. Each is justified in the PR, pinned, and
   allowlisted.
10. **Documentation.** `SECURITY.md` gets a section on what the terminal can do and how it is
    protected, and the new weakness classes it counts as in scope.

Also stated plainly in `SECURITY.md`: Claude's output can draw something that looks like a
permission prompt, and the ask panel is built from the screen. The panel never answers by itself:
an answer is always a click, and the parser reads the bottom of the live screen, not the scrollback.
Resuming on launch restarts sessions with no one at the keyboard, in repositories whose files may
have changed since; this is accepted, shown to the person (the page lists what was resumed, with a
Stop all), and limited by the per-owner checks in Lifecycle. Also accepted: Conn checks `.conn` is
the person's own folder before writing a brief or prompt, but `claude` reads `.conn/*.md` by path
afterwards; and a descendant that calls `setsid` or `setpgid` leaves the group a stop reaches
(`claude` itself ends its background commands, which run in a group of their own, when it is
stopped politely).

Tests that must exist: an `/api` request with no secret, a wrong secret, a wrong Host or a wrong
Origin is refused on every route (a table over the route list, so a new route cannot skip it;
as built in Task 1, the server has no route registry, so the list is written by hand in the
test, and the gate covers every `/api` path structurally: one prefix check on the escaped and
the decoded path in front of all dispatch, so an unlisted or future `/api` route is behind it
too); wrong Host, wrong or missing Origin, missing or wrong secret, an oversize
frame, a flood of input and a slow viewer are each refused or bounded; no terminal route creates, names or configures a
session (the existing start routes are protected by the secret like every other); a stop kills a child that ignores SIGTERM and its own children; the secret is absent from
the child's environment, argv and every log line; a tampered `hosted.json` (a command line, a
folder outside the allowed worktrees, a session id that is not a UUID) starts nothing.

## Behaviour

- **Where sessions open** (Settings, Atlassian-independent "Sessions" card): *Inside Conn*
  (default), *My terminal* (the cmux code as it is; no setting names a program, per the
  anti-goals, so there is no custom open command), or *Show me the command* (what happens today
  when cmux fails; the line is written for sh, bash, zsh and fish, not csh or tcsh). Each module reads it when it starts a session.
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
- Package names and layout; the WebSocket message encoding; how the secret is handed over and carried, within
  the Security section.
- How the Wails app and `conn serve` each hand the secret to the page.
- How `ask.Parse` is fed from the screen model without changing its behaviour.
- The Terminal panel's layout, within the existing visual language.

## Decisions made without the person (my calls, to check)

- The real Claude Code interface in a terminal panel, not a custom view over its JSON stream.
- WebSocket (not SSE plus POST) for the byte stream.
- `sessionsMax` 12, `scrollbackBytesMax` 4 MiB, `viewersMax` 4, `inputFrameBytesMax` 64 KiB.
- Auto-resume on launch is on by default with a setting to turn it off.
- A hosted session is a row in the same session list as any other, with a server-issued opaque id
  instead of a pid; `reply`, `answer`, `stop` and the terminal routes dispatch on that id and
  reach hosted sessions before any cmux lookup.
- Owners return an argv (`Spec{Argv, Dir, Name}`), not a shell string; the one function that
  quotes an argv into a command for the *My terminal* and *Show me the command* paths is the only
  place quoting happens.
- When Conn attaches to an already-running Conn, the attached app does not own the sessions;
  quitting it does not stop them.
- The cmux code stays as the *My terminal* option and for adopting sessions; nothing new for other
  terminals.
- A separate PR from multi-repository Open work, based on `main`; whichever lands second rebases.

## Since this spec

- **New session** (issue #21) starts a plain `claude` session from the Sessions page, through the
  same opener, so it follows *Where sessions open*. The page sends only an allowlisted repository
  and an optional name; the server resolves the clone (as Open work does, a real folder of the
  person's own, run in its real path), chooses the conversation id (`--session-id <uuid>`) and
  builds the argv. No new owner: inside Conn it is recorded under `history` with that id, since
  `hosted.Recorder.Opener` now reads the conversation from `--session-id` as well as `--resume`,
  and History's check resumes it at launch once the archive holds its transcript. The route,
  its guard and its limits are in SECURITY.md, *New session*.
