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
- **Both architectures are built**, as one universal bundle if the build allows and otherwise one
  bundle per architecture. The minimum macOS version is whatever the chosen Wails release
  requires, written in the README. That ad-hoc signing and a `curl` install open without a
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
- The app is built in its own job on a macOS runner, because Wails needs cgo there. One bundle
  per architecture (or one universal bundle: the builder chooses), zipped as an asset beside the
  CLI archives, in `checksums.txt`.
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

- Wails v2 or v3, whichever is the stable release when this is built, and its allowlist entry in
  `.golangci.yml`.
- The splash content (a plain page or the app's own mark), the window's size and minimum size,
  and its remembered position.
- The app icon (a placeholder is fine; the owner supplies the final one).
- The app menu (at least Quit, Hide, and the standard Edit items so copy and paste work).
- How the app receives SIGTERM from `conn stop` and quits cleanly.
- Universal bundle or one per architecture, and the bundle's identifier and version stamp.
- Whether CI can run the app's pure tests on macOS only, and a smoke test that the bundle starts
  and answers on its port.
