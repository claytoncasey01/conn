# Conn as a desktop app

## Intent

Today you run `conn serve` in a terminal and open the address in a browser. That is a developer's
tool, and it defeats the point of Conn being the place you work from. The wish: open Conn the way
you open Slack. A splash while it starts, then the UI in its own window, with an icon in the Dock.
The browser and the terminal command are not part of the day-to-day.

## Goals

- A macOS app, `Conn.app`, that opens like any other: a splash, then the UI in its own window.
- Closing the window leaves Conn running in the Dock. Clicking the Dock icon brings the window
  back. Cmd-Q or Quit from the app menu stops it.
- The app contains the server. Nobody runs `conn serve` for it.
- `install.sh` installs the app (see "Release and install").
- Everything the server promises today still holds: loopback only, the `Host` and `Origin`
  checks, the `X-Conn` header on writes, one Conn per data folder.

## Non-goals

- Linux and Windows apps. The CLI keeps working on Linux and macOS as it does today.
- Menu-bar or tray icon, notifications, launch at login, auto-update, a custom title bar.
- Replacing `conn serve`, `conn stop` or any other command. The CLI is unchanged.
- Apple signing and notarization now. The owner has no Developer account yet; it is added later
  and must not need a redesign (see "Release and install").
- Rewriting the UI or the server. The page in `src/` is not changed for the app.

## Anti-goals (a failure even if it technically works)

- A weaker server: no change to `local()`, `ownWrite` or `fromOwnPage` to make the app work.
- A second copy of the server's wiring, one for `serve` and one for the app.
- Quitting the app stops a Conn the app did not start.
- `make check` or the release of the CLI starts needing cgo, or stops running on Linux.

## Decided (by the owner)

- The window technology is **Wails** (Go, the system WebKit view). Not Electron, not Tauri.
- Closing the window keeps the app running in the Dock; only an explicit quit stops it.
- Installing is by `install.sh` now; Apple signing comes later.
- If a Conn is already running on the data folder when the app opens, the app **opens a window on
  that running server** instead of refusing.

## Decided (by the assistant, to be checked)

The spike has run. "Spike result (Task 1)" at the end of this file settles the Wails version
(v3.1.0), the minimum macOS (13), the universal bundle and SIGTERM handling, and overrides any
older wording below.

- **The window loads the server's own loopback address**, `http://127.0.0.1:PORT`, not Wails's
  own asset scheme. The page makes relative `/api` calls and the server accepts only loopback
  `Host` and `Origin`, so this works with every guard unchanged. Serving the page from Wails
  assets would mean loosening `local()`, which SECURITY.md promises.
- **The app uses the same port as `conn serve`** (`CONN_PORT`, default 7777), not a random one.
  The web view keeps its saved state per origin, so an address that changes every launch would
  lose it. If another program holds the port, the app says so in a dialog.
- **Quit stops the server only if the app started it.** When the app attached to an
  already-running server, quitting the app closes the window and leaves that server running.
- **The wiring moves out of `package main`.** `newHandler` and its helpers (`cmd/conn/wiring.go`)
  can't be imported, so they move to an internal package (suggested `internal/app`) that
  `conn serve` and the app both call. This is a move with no behavior change; the existing tests
  are the proof.
- **The app is its own entry point**, `cmd/conn-app`, in the same module, built only for darwin
  (a build constraint), so Linux `make check`, `make test` and the CLI release stay pure Go.
- **`install.sh` also installs the app on macOS**, to `~/Applications/Conn.app` (no `sudo`), by
  default, with `--cli-only` to skip it. Nothing is released yet, so there are no existing
  installs to surprise. If `Conn.app` is running, the installer stops with a message to quit it
  and try again, rather than replacing a bundle under a running program. The app carries its own
  server, built from the same release as the CLI archive beside it, so there is no version
  mismatch to handle.
- **The app runs with the whole environment your shell gives `conn serve`.** An app opened from
  the Dock gets a bare environment: no `PATH` to find `claude`, `gh`, `git` and `cmux`, no
  `CONN_*` settings, and none of the credentials and sockets the tools read (`GH_TOKEN`,
  `SSH_AUTH_SOCK`, API keys, and so on). Nothing would work. At start the app runs the user's
  login shell (`$SHELL`) once and takes **every** variable from it, with no filtering by name:
  it starts from the app's own environment and each variable the shell reports replaces or adds
  to it. The shell prints them with `env -0`, so values with newlines survive. The shell may be
  fish, zsh or bash, so the builder picks the flags per shell, and the interactive startup files
  must be read the way a terminal reads them, because that is where many people set theirs.
  The run has a timeout and a limit on the output size, both named constants. If the shell fails
  or times out, the app starts with what it has and says so in the splash, not silently. The
  environment is built once and handed to the in-process server and to everything it starts,
  never written to a log or a file. This keeps attach mode and own-server mode on the same data
  folder as the CLI, so there is no second configuration source.
- **When the server the window is on goes away**, the app runs the startup steps again: lock free
  means it starts its own server and reloads; lock held means it attaches to the new holder;
  neither working in a few seconds is a dialog. There is one rule for "which server", not a
  separate watcher with its own states. The app notices by the page's event stream closing or a
  periodic request to `/api` failing; the builder picks.
- **The port stays fixed** (see above). Falling back to another port would silently drop the
  page's saved state, and a held 7777 is rare; it is a dialog that names the port and the
  `CONN_PORT` variable.
- **Both architectures are built**, as one universal bundle (settled by the spike: `lipo` joins
  the arm64 and x86_64 builds). The minimum macOS version is 13 (Ventura), set by Go 1.27 and
  written in the README. That ad-hoc signing and a `curl` install open without a
  warning is checked once by hand on a clean Apple-silicon Mac before the first release, not in
  CI.

## First step of the plan: a spike

Wails normally serves its own assets and injects a bridge into its page. Pointing the window at
an external loopback address may lose that bridge on some Wails versions. The design needs only
native pieces, not the bridge: the Dock, the app menu, quit, hide-on-close and dialogs are all
set in Go. So the first piece of work is a throwaway program that opens a window on
`http://127.0.0.1:PORT`, closes to the Dock, reopens from the Dock click and quits from the menu.
If that fails, the fallback is to let Wails serve the page and proxy `/api` to the server with
the `Host` and `Origin` it already accepts, still without loosening `local()`. Anything that
needs `local()` loosened comes back to the owner first.

## How it behaves

1. Open the app. A splash shows at once.
2. The app takes the data folder's lock (`internal/instance`, as `serve` does).
   - **Lock free:** it starts the server in-process, publishes `{pid, url}` in the lock as
     `serve` does, and loads the URL in the window when the server answers.
   - **Lock held:** it reads the holder's URL and opens the window on it. This app does not own
     the server.
3. Closing the window hides it; the app and server keep running. Clicking the Dock icon opens
   the window again. Opening the app a second time does not start a second one.
4. Quit (Cmd-Q, app menu): if the app owns the server, it shuts it down (the 2 s
   `shutdownTimeout`) and releases the lock. Otherwise it only closes.
5. `conn stop` works on an app that owns the server: it sends SIGTERM to the PID in the lock, and
   the app quits as if told to. A terminal `conn serve` while the app owns the server says
   "already running", as today.
6. A failure before the window shows (data folder unreadable, port taken, another program on the
   lock) is a dialog that says what to do, not a silent exit.

## Release and install

- The CLI archives (`conn_<os>_<arch>.tar.gz`) are built as today: pure Go, on Ubuntu.
- The app is built in its own job on a macOS runner, because Wails needs cgo there. One
  universal bundle (see "Spike result (Task 1)"), zipped as an asset beside the CLI archives,
  in `checksums.txt`.
- On Apple silicon a program must carry at least an ad-hoc signature to run, so the bundle is
  ad-hoc signed in the build. Developer ID signing and notarization is a later step on the same
  job (`codesign` with the identity, then `notarytool`), not a redesign.
- `install.sh` downloads with `curl`, which sets no quarantine flag, so the unsigned app opens
  without a Gatekeeper warning. Someone who downloads the zip in a browser would get one; the
  README says so until the app is notarized.
- Update `docs/releasing.md` and the README (install, what the app is, the Dock behavior) and
  SECURITY.md (the app is the same server, same guards, same data folder).

## Constraints

- AGENTS.md applies in full: no `&&` or `||`, 60-line functions, 100 columns, named limits,
  no package-level state, doc comments, table tests, `make lint` with 0 issues.
- Decisions that are pure (lock free or held, which URL to open, whether quit stops the server)
  are functions of plain values and are tested without a window.
- The wiring move must keep `make test` green with no test edits beyond imports.

## Left to the builder

- The `.golangci.yml` allowlist entry for Wails. The version is settled: v3.1.0, pinned (see
  "Spike result (Task 1)").
- The splash content (a plain page or the app's own mark), the window's size and minimum size,
  and its remembered position.
- The app icon (a placeholder is fine; the owner supplies the final one).
- The app menu (at least Quit, Hide, and the standard Edit items so copy and paste work).
- The bundle's identifier and version stamp. (Settled by the spike: SIGTERM and SIGINT already
  quit cleanly through Wails's default signal handler, and the bundle is universal.)
- Whether CI can run the app's pure tests on macOS only, and a smoke test that the bundle starts
  and answers on its port.

## Spike result (Task 1)

**Verdict: the external loopback page works.** A Wails window opened on `http://127.0.0.1:7799`
loaded the whole UI, made a write that the server accepted (a script-injected request from the
proxied origin `127.0.0.1:7798`, not the UI's own save: Tasks 4 and 5 must check a real UI save
through the actual app), hid on close, came back on the
reopen event, and quit from the menu, from an Apple event and from `kill -TERM`. No guard in the
server was touched. The fallback in "First step of the plan" is not needed. The spike program was
throwaway and is not in the repo.

### Which Wails, and why

**Wails v3.1.0** (`github.com/wailsapp/wails/v3 v3.1.0`), pinned to that exact version. The module
proxy and GitHub releases on 2026-11-04 show two stable lines: v3.0.0 (26 October 2026, the first
stable v3) with v3.1.0 (1 November 2026, a weekly release), and v2.17.1 (1 November 2026). v3 is
chosen because the three things this design needs are plain options there:

- `WebviewWindowOptions.URL` loads an external address. v2.17.1 has no such option (it serves its
  own assets), so it would need a workaround.
- A `WindowClosing` hook that calls `Hide()` and `Cancel()` is hide-on-close.
- `events.Mac.ApplicationShouldHandleReopen` is the Dock-click event.

The cost: v3 has been stable for only nine days and ships weekly, so the API may move. Pin the
version and read the release notes before bumping it. v3.1.0's `go.mod` says `go 1.26.0`, so it
needs Go 1.26 or newer (the repo is on 1.27.1). v2 was not built.

### Build

No Wails CLI is needed, and no build tags. A plain `go build` yields a runnable binary; the
`.app` bundle is assembled by hand (a folder, one `Info.plist`, the binary, then an ad-hoc
`codesign`). The command that was used is the form for the host architecture (arm64); the
x86_64 build changes only `GOARCH`:

```
MACOSX_DEPLOYMENT_TARGET=13.0 CGO_ENABLED=1 \
CGO_CFLAGS=-mmacosx-version-min=13.0 CGO_LDFLAGS=-mmacosx-version-min=13.0 \
GOARCH=arm64 go build -o ConnSpike.app/Contents/MacOS/conn-spike .
```

- **cgo and Xcode.** Wails v3 needs `CGO_ENABLED=1` and a C compiler. The Command Line Tools
  were enough here (`xcode-select -p` is `/Library/Developer/CommandLineTools`; full Xcode is not
  installed). With `CGO_ENABLED=0` and no tag the build stops at Wails's own build guard.
- **Set `CGO_ENABLED=1` for the other architecture.** Go switches cgo off when `GOARCH` differs
  from the host, so the `GOARCH=amd64` build failed on that guard until it was set explicitly.
  Built that way, an arm64 and an x86_64 binary joined with `lipo -create` gave a universal
  binary (both slices `minos 13.0`). The x86_64 slice was linked but not run.
- **Without the version flags** the link prints a `built for newer 'macOS' version` warning per
  object and the header says `minos 11.0`, which is wrong. The three variables above fix both.
- **Minimum macOS is 13 (Ventura).** The floor comes from Go 1.27, not Wails: the Go 1.27 release
  notes say it requires macOS 13. Wails's own build template targets 12.0, and its newest APIs
  (macOS 14 and 26) are behind runtime `@available` checks. Write 13 in the README and in
  `LSMinimumSystemVersion`.
- **A CGO-free build exists but is experimental.** v3.1.0 added a macOS backend that builds with
  `CGO_ENABLED=0 go build -tags purego`. The spike binary built that way (16 MB, no Cocoa
  libraries linked) loaded the page, hid on close and quit on SIGTERM. Not adopted: its release
  note calls it experimental, and it was not tried from Linux. Worth a look later if cgo on the
  macOS runner becomes a burden.
- **The CLI is unaffected.** `CGO_ENABLED=0 go build ./cmd/conn` in the repo still succeeds, and
  the repo's `go.mod` and `go.sum` were not touched.
- **Linux checks with a darwin-only app package.** In a scratch module, `GOOS=linux go vet ./...`
  and `go build ./...` skipped a `cmd/app` whose only file had `//go:build darwin` and exited 0.
  Naming that package explicitly fails with `build constraints exclude all Go files`. So `./...`
  is safe, but golangci-lint on Linux will not see the app's files; they need a macOS lint run
  in CI. Every file in that package, tests included, needs the constraint, or must live in an
  untagged package so the pure logic still runs on Linux.

### Verified by running it

Server on `CONN_PORT=7799` with a temporary `CONN_DATA`; the real Conn was never touched.

- **The UI loaded.** Through a logging reverse proxy (port 7798 to 7799, same guards) the webview
  requested `/`, `/assets/*.js`, `/assets/*.css`, `/api/themes`, `/api/events` and
  `/api/settings`. On a direct run on 7799 the server's `/api/health` went from `clients:0` to
  `clients:1` (the page's event stream connected). Edit > Select All then Copy put the page text
  on the clipboard.
- **A write from the webview succeeded.** A `PUT /api/settings/repos` with `X-Conn: 1` and
  `Origin: http://127.0.0.1:7798`, sent by script from inside the loaded page, returned 200 and
  `GET /api/settings` showed the new value. That script was injected by the logging proxy, not by
  the UI: the UI's own save buttons were not clicked.
- **Hide on close.** Clicking the window's close button (System Events works here without extra
  permission) logged the close hook, left the process running and the window count at 0. The
  event stream stayed connected (`clients:1`) while hidden.
- **Reopen event.** `tell application id "..." to reopen` and `open Bundle.app` on the running
  bundle both delivered `ShouldHandleReopen`, showed the window, and started no second process.
  This is the event a Dock click sends; a real Dock click was not made.
- **Menu.** The app menu has About, Services, Hide, Hide Others, Show All and Quit; the Edit menu
  has Undo, Redo, Cut, Copy, Paste, Paste and Match Style, Delete, Select All. The Hide
  item hid the app (`visible` false, process alive) and the reopen event brought it back,
  frontmost.
- **Native dialog.** `app.Dialog.Info()` showed an `AXDialog` with the title, the message and an
  OK button, both over the open window and with the main window hidden. Clicking OK returned
  control to Go.
- **Quit.** The Quit menu item, `tell application id "..." to quit` and `kill -TERM` each ended
  the process in under 500 ms, ran `ShouldQuit` and `OnShutdown`, and the server's client count
  fell to 0. `SIGTERM` worked with the window hidden too. `kill -INT` quit it the same way, with
  the window visible and hidden, logging "Quitting application...", so Wails's default handler
  covers both signals and routes them through the same quit path.

### Surprises

- **The Wails JS bridge is absent on the external page.** From inside the page,
  `window._wails` and `window.wails` are `undefined`. Go-to-page calls do not work either:
  `WebviewWindow.ExecJS` queues the script until the page reports `wails:runtime:ready`, which an
  external page never does, so it silently never runs. This does not matter for this design,
  because everything native is set from Go. Do not use `ExecJS`, frontend events or bindings.
- **`App.Show()` crashes off the main thread.** Calling `app.Show()` from a goroutine (the
  reopen handler, and again from a bare goroutine, twice) raised `SIGTRAP` every time. Wrap it:
  `application.InvokeSync(app.Show)`. `app.Hide()` from a goroutine did not crash in the test,
  and `window.Hide()` and `window.Show()` from a goroutine worked, but the source calls the
  native function directly for `App.Hide()` too, so wrap both to be safe.
- **Reopen needs more than `window.Show()` to come forward.** With only `window.Show()` the app
  was visible but not frontmost when reopened from the background. The handler that worked is
  `InvokeSync(app.Show)`, then `window.Show()`, then `window.Focus()`.
- **There is no `HideWindowOnClose` option in v3.** It is the close hook shown above.
- **Wails logs `INF` lines to stderr** (build and platform info) unless a logger is set.

### Not verified: needs the owner's eyes

- A real Dock click after closing the window (only the equivalent reopen event was sent).
- Cmd-Q and Cmd-H from the keyboard, and Cmd-V paste. The menu items and the Apple event were
  used, and copy was checked.
- How the native dialog looks, and how the window looks: screen capture is blocked here, so no
  screenshot was taken.
- The splash flash: the window shows blank until the first response arrives. Not measured.
- What the window shows if the server goes away while it is open.
- Running the x86_64 slice, and running a bundle on macOS 13 itself (tested on 27.0.1 only).
