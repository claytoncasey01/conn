# Sessions hosted inside Conn: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Conn starts and hosts `claude` sessions on its own pseudo-terminals, shows them in the page, resumes them after a full quit, and does so with the app's security tightened first.

**Architecture:** Security first: a per-launch secret on every `/api` route (Task 1). Then a bounded process host (Task 2) and a passive screen model (Task 3); a WebSocket endpoint over them (Task 4); hosted sessions in the session list and its reply/answer/stop routes (Task 5); the page's terminal panel (Task 6); modules start hosted sessions through argv specs (Task 7); lifecycle and resume (Task 8); docs and real verification (Task 9).

**Tech Stack:** Go, `github.com/coder/websocket` (promoted to direct), a Go pseudo-terminal and VT screen-model choice made in Tasks 2 and 3 (each justified in the report and the PR), xterm.js + fit addon (bundled, pinned).

**Spec:** `docs/superpowers/specs/2026-11-05-hosted-sessions-design.md`. Read it first, especially Security and Lifecycle; this plan only adds sequencing and test names.

## Global Constraints

- AGENTS.md and CLAUDE.md for all Go: Data Oriented Design (plain-data types, logic in functions, I/O at the edges), TigerStyle adapted for Go (a named constant for every limit; no `&&`/`||`; functions at most 60 lines; lines at most 100 columns; no abbreviations; every error handled; panics only for program bugs, never for input), Terraform practices (small packages, no package-level state, `context.Context` first, errors with `%w`, table tests with `t.Parallel`, doc comments). No `//nolint`. `make check`, `make test`, `make lint` (0 issues) and the UI tests pass before every commit that ends a task.
- Limits (exact): `sessionsMax = 12`, `scrollbackBytesMax = 4 << 20`, `viewersMax = 4`, `inputFrameBytesMax = 64 << 10`, `stopGrace` 5 s, handshake deadline 5 s.
- The secret is 256 bits from `crypto/rand`, compared with `subtle.ConstantTimeCompare`, held in memory (and the 0600 lock file), and never appears in a log line, error text, the child's environment, `ps`, a URL query, a cookie.
- No route, message or setting lets a client name a command, argv, environment or folder. Terminal content and keystrokes are never logged or written to disk.
- Single-repo Open work keeps its behaviour apart from where the session opens. Contract changes ship with the page and `testdata/answers/`.
- Commit after each task. Do not push.

## Review Focus

- Every `/api` route refuses a request without the secret: the test iterates the server's real route list, so a new route cannot skip it.
- A second local user running `curl -H 'X-Conn: 1' http://127.0.0.1:<port>/api/...` gets nothing, including the event stream and the agent pages.
- A page on another origin or port, or a rebound hostname, cannot open the WebSocket.
- A child that ignores SIGTERM and spawns its own children is gone after a stop; no zombie, no leaked descriptor.
- A hosted child never inherits `CONN_*`, the secret or `CMUX_*`; a reply to a hosted session never reaches the cmux code.
- A tampered `hosted.json` (a command line, a folder outside an allowed worktree, a session id that is not a UUID, 500 entries) starts nothing beyond the per-owner checks and `sessionsMax`.
- Output containing OSC 52, OSC 8 to a Conn path, title and window-report sequences, and a forged permission prompt in scrollback does not write the clipboard, navigate the app, echo text into the input, or produce an ask.
- The window closing leaves sessions running; Quit stops them and the next launch resumes them; the attached (second) app's Quit leaves the owner's sessions alone.

---

### Task 1: The secret on every `/api` route

**Files:**
- Create: `internal/secret/secret.go` (+ tests)
- Modify: `internal/server/*` (route gate, event stream), `internal/desk/*` (lock file, attach, URL), `cmd/conn/*` (print rule), `cmd/conn-app/*`, `index.html` and the bootstrap script (moved to a file), `src/lib/live.svelte.ts` and every `fetch` helper (header; `EventSource` replaced by a `fetch` stream), CSP header, `docs/`, `SECURITY.md`

**Interfaces:**
- Produces: `secret.New() (Secret, error)`, `(Secret).String() string`, `(Secret).Matches(presented string) bool` (constant time), `secret.Header = "Authorization"` with scheme `Bearer`; `server` option `Secret`; the lock file gains `secret`; `desk` builds `URL#k=<secret>`.
- Page: `src/lib/apiKey.ts` exports `takeFromFragment()`, `header()`; all API calls go through one `api()` helper.

- [ ] **Step 1:** Failing table tests in `internal/server`: for every route the server registers (iterate the real mux/route table), a request with no secret, a wrong secret, a wrong Host, and a wrong Origin gets 401/403 and no handler runs; with the secret it reaches the handler; the HTML shell and static assets need no secret; a missing Origin is accepted only with the secret.
- [ ] **Step 2:** Run `go test ./internal/server/...`; expect FAIL.
- [ ] **Step 3:** Implement the gate in one place (middleware over the mux), `secret` package, lock-file field (0600, written atomically), `desk` attach reading it, `conn serve` printing the URL with the fragment only when stdout is a terminal (otherwise the URL without it and a line naming the lock file), CSP and the bootstrap script move.
- [ ] **Step 4:** Page: read the fragment once, store in `sessionStorage`, remove it with `history.replaceState`, add the header to every request; replace `EventSource`. Update source tests.
- [ ] **Step 5:** Run the app (`scripts/build-app.sh` or `conn serve`) and confirm in a browser and the desktop app that the page loads and live updates work; `curl` without the secret to three routes is refused. `make check test lint`, UI tests. Commit.

### Task 2: The process host

**Files:**
- Create: `internal/pty/*.go` (name may change; one concept: hosting processes on terminals), tests, a tiny scripted fake program under `testdata/` or a `Spawner` fake

**Interfaces:**
- Produces: `type Spec struct{ Argv []string; Dir string; Env []string; Name string }`; `type Host struct` created by `pty.NewHost(Config) *Host` with `Start(ctx, Spec) (ID, error)`, `Write(ID, []byte) error`, `Resize(ID, cols, rows uint16) error`, `Stop(ctx, ID) error`, `StopAll(ctx)`, `Subscribe(ID) (Viewer, error)`, `Live() []Info`; `Info{ID, Name, Dir, PID, StartedAt, ExitCode *int}`; `Config{Spawner Spawner; Limits Limits}`; `type Spawner func(Spec) (Process, error)` with the real one over the chosen PTY library; `Viewer{Replay []byte; Output <-chan []byte; Close()}`.

- [ ] **Step 1:** Failing tests with a fake `Spawner`: sessionsMax is enforced with an error; scrollback is a bounded ring (oldest bytes dropped past 4 MiB); a slow viewer is dropped and the reader never blocks; viewersMax; Write after exit errors; `Stop` escalates polite stop to kill after `stopGrace`; IDs are random and unguessable.
- [ ] **Step 2:** Run; expect FAIL. Implement.
- [ ] **Step 3:** Real spawner tests (skipped only on unsupported OS): `Setsid`, process group stop kills a child that ignores SIGTERM and its own children; the child's environment contains none of `CONN_*`, the secret or `CMUX_*` (a Spec builder function `ChildEnv(parent []string) []string` is pure and table-tested); descriptors are close-on-exec; resize changes `stty size` seen by a scripted child.
- [ ] **Step 4:** `make check test lint`. Commit with the PTY library justification in the message body.

### Task 3: The screen model

**Files:**
- Create: `internal/pty/screen.go` (or `internal/screen`), tests with captured byte streams in `testdata/`; Modify: `internal/ask` only to add an input adapter

**Interfaces:**
- Produces: `type Screen struct` (passive, fed with `Feed([]byte)`, `Resize(cols, rows)`, `Lines(n int) []string` returning the bottom `n` lines as the cmux `read-screen` text would look, `Cursor() (row, col int)`); a fixed `Answers` table type naming which queries the host answers.

- [ ] **Step 1:** Failing tests: feeding the bytes of an alternate-screen TUI frame yields the expected bottom lines; wrapped lines; cursor addressing; a captured Claude Code permission prompt (take real bytes from a `claude` run on this machine if available, else from `testdata/answers`) parses through `ask.Parse` to the same ask as the cmux screen text did; a forged prompt printed in scrollback above the live region is not returned by `Lines`.
- [ ] **Step 2:** Choose and justify the VT library (or small own code); implement; the model never writes to the PTY itself.
- [ ] **Step 3:** Decide, by running a real `claude` start-up under the host, whether the server or the page answers terminal queries (DA, CPR, OSC 11); record the result and implement the one side with the fixed table. Test that no answer contains text taken from output.
- [ ] **Step 4:** `make check test lint`. Commit.

### Task 4: The WebSocket

**Files:**
- Create: `internal/server/term.go` (+ tests, a fuzz test), message types in `internal/pty` or `internal/server`

**Interfaces:**
- Consumes: Tasks 1-3. Produces: `GET /api/term/<id>` upgrading to a WebSocket. Client frames: `{t:"input",d}`, `{t:"resize",cols,rows}`; first frame or subprotocol carries the secret. Server frames: binary output; `{t:"replay-end"}`, `{t:"exit",code}`.

- [ ] **Step 1:** Failing tests (real `httptest` server, `coder/websocket` client): wrong Host, wrong or missing Origin, no secret, wrong secret each fail the handshake before any session is touched; an unknown id fails; a frame over `inputFrameBytesMax` closes the connection; an input flood is rate-limited; `viewersMax+1` is refused; a slow viewer is closed; replay arrives before live output; resize reaches the host; exit is reported. Fuzz the frame parser (`FuzzFrame`) with a corpus; it never panics.
- [ ] **Step 2:** Run; expect FAIL; implement; promote `coder/websocket` to a direct dependency and allowlist it in `.golangci.yml` with a comment.
- [ ] **Step 3:** `make check test lint`. Commit.

### Task 5: Hosted sessions in the session list

**Files:**
- Modify: `internal/sessions/*`, `internal/server/*`, `internal/app/*`, `internal/ask`, `src/lib/types.ts` (+ tests, `testdata/answers/`)

**Interfaces:**
- Consumes: `pty.Host`, `Screen`. Produces: hosted sessions appear in the sessions snapshot with `hosted: true`, the issued id, `canReply: true`; `reply`, `answer`, `stop`, `terminal` routes dispatch on the id and reach hosted sessions before any cmux lookup; asks come from `Screen.Lines`.

- [ ] **Step 1:** Failing tests: a hosted session row with the right flags; reply types into the process only after the screen check passes and refuses when the prompt changed; answer sends the choice; stop calls `Host.Stop`; a hosted id never reaches `cmuxTarget`; a person-started session still goes the old way.
- [ ] **Step 2:** Implement; update the page types and the Sessions page to treat hosted rows like any other, with the ask panel unchanged. `make check test lint`, UI tests. Commit.

### Task 6: The terminal panel

**Files:**
- Create: `src/lib/Terminal.svelte`, `src/lib/terminalTheme.ts` (+ tests); Modify: `src/pages/Sessions.svelte`, `src/pages/TicketRun.svelte`, `package.json`/lockfile (xterm.js, fit addon, pinned), `index.html`/CSP if needed

**Interfaces:**
- Produces: `<Terminal sessionId />`; `terminalTheme(tokens: CssTokens): XtermTheme` (pure: foreground, background, cursor, selection and the 16 ANSI colours derived from the theme's tokens).

- [ ] **Step 1:** Failing tests: `terminalTheme` for every built-in theme gives contrast of at least 4.5:1 for foreground on background; source tests assert OSC 52 / clipboard addon absent, window-report options off, links routed through the app's link policy (never the app window), and the secret sent per Task 4.
- [ ] **Step 2:** Implement the panel: attach, replay, live output, input, fit/resize (debounced), detach, exit banner. `npm run build` and the UI tests pass. Commit.

### Task 7: Modules start hosted sessions

**Files:**
- Modify: `internal/openwork/build.go` (`openWorkspace`, `Resume`), `internal/reviews`, `internal/myprs`, `internal/historyapi`, `internal/settings` (a `sessions` section: `where` = `inside` | `terminal` | `command`, `terminalCommand`, `resumeOnLaunch`), `internal/app`, the Settings page, `internal/dryseed`, tests

**Interfaces:**
- Produces: owners return `pty.Spec` (argv, never a shell string); one function `ShellLine(argv []string) string` is the only place quoting happens (for the cmux and paste paths); `claude` comes from `CONN_CLAUDE`; `sessions.where` default `inside`.

- [ ] **Step 1:** Failing tests per owner: the argv for build, resume, review, self-review and history-resume sessions (golden); a hostile ticket title, branch or folder stays inside one argv element; `where=command` shows the pasted line; `where=terminal` still uses the cmux opener; dry run starts a scripted fake session.
- [ ] **Step 2:** Implement; Settings card "Where sessions open" with one-line help for each choice. `make check test lint`, UI tests. Commit.

### Task 8: Lifecycle and resume

**Files:**
- Create: `internal/hosted/*.go` (record read/write, per-owner re-check), tests; Modify: `cmd/conn-app/*` (shutdown hook), `cmd/conn/*` (signals), `internal/app/*`, the page (a "resumed N sessions" banner with Stop all)

**Interfaces:**
- Produces: `hosted.Record{ID, Owner, Name, Dir, ClaudeSessionID}` (ids and names only), `hosted.Write(dir, []Record)` (0600, atomic), `hosted.Read(dir) ([]Record, error)` (bounded size, bounded count); `type Owner interface`-free per-owner check functions returning `(pty.Spec, bool)`; `Resume(ctx, Records, Checks, Host)` limited to `sessionsMax`.

- [ ] **Step 1:** Failing tests: records are written as sessions start and end; Quit stops every session and leaves the record of those that were live; the next launch resumes them; a session that ended before the quit is not resumed; a tampered file (command line field, outside folder, non-UUID id, 500 entries, a symlink as the file) starts nothing beyond the per-owner checks; `resumeOnLaunch=false` resumes nothing; closing the window changes nothing; an attached second app's Quit stops nothing.
- [ ] **Step 2:** Implement the per-owner checks as in the spec's Lifecycle; Wails `OnShutdown`/quit hook, SIGINT/SIGTERM in `conn serve`; the banner. `make check test lint`, UI tests. Commit.

### Task 9: Documentation and real verification

**Files:**
- Modify: `SECURITY.md`, `README.md`, `docs/open-work.md`, `docs/settings.md`, `docs/reviews.md`, any "cmux" wording that means a terminal in general

- [ ] **Step 1:** `SECURITY.md`: what the terminal can do, the threat model, the secret, the in-scope weakness classes, the accepted risks (forged prompts, resume with no one present, same-user processes). README: the cmux requirement line becomes optional.
- [ ] **Step 2:** On this Mac: build the app, start a real Open work session (dry or real) and a Reviews session; type a reply, answer a permission prompt, close the window (sessions keep running), reopen it (screen restored), Cmd-Q, reopen (sessions resumed); in a second terminal `curl` the API without the secret and see it refused. Record what was seen.
- [ ] **Step 3:** `make check test lint`, UI tests. Commit.
