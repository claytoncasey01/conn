# Conn Desktop App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A macOS `Conn.app` (Wails) that opens like Slack: a splash, then the UI in its own window, living in the Dock until quit, installed by `install.sh`.

**Architecture:** The server's wiring moves out of `package main` into `internal/app` so `conn serve` and the app share one copy. A thin darwin-only `cmd/conn-app` loads the login shell's whole environment, then hands a pure state machine (`internal/desk`) a window to open on either its own in-process server or an already-running one. The window loads the server's own loopback URL, so no server guard changes.

**Tech Stack:** Go 1.27, Wails (v2 or v3, chosen by the spike), cgo on macOS only, POSIX shell for the bundle script and installer.

**Spec:** `docs/superpowers/specs/2026-11-04-desktop-app-design.md`

## Global Constraints

- AGENTS.md in full: no `&&` or `||` (`internal/guard`), functions at most 60 lines, lines at most 100 columns (tab = 4), no package-level state beyond sentinel errors and compiled regexps, no `//nolint`, doc comments on every package and exported name, limits as named constants, tests as tables with `t.Run` and `t.Parallel`, time limits through `testtime.Scaled`. `make lint` reports 0 issues, `make check` and `make test` pass before every push.
- No change to `local()`, `ownWrite` or `fromOwnPage` (`internal/server`).
- The CLI (`cmd/conn`, `scripts/build-release.sh`) stays pure Go (`CGO_ENABLED=0`) and keeps building and testing on Linux. Only `cmd/conn-app` needs cgo, and only on darwin.
- The port is `CONN_PORT`, default 7777, the same as `conn serve`. A held port is a dialog that names the port and `CONN_PORT`.
- Quit stops the server only if the app started it. Closing the window never stops anything.
- The whole login-shell environment is taken, with no filtering by name; it is built once, never logged or written to a file. The shell run has a timeout and an output limit, both named constants.
- Nothing names an organization (`internal/guard`). Commit only when the owner asks: the commit steps below are for when execution is approved.

## Review Focus

Failure modes the spec implies that no task would otherwise test (each has its test in the task named):

1. The login shell prints text before the environment (a banner, a prompt hook), or a value holds a newline or an `=` — Task 2.
2. The login shell hangs or fails — the app must start with the environment it has and say so — Task 2 and Task 4.
3. A second app (or the binary run twice) starts on a folder the first owns — it attaches, and quitting it must leave the first's server running — Task 3.
4. The server the window is on goes away and nothing can take over — bounded retries, then a dialog, never a loop — Task 3.
5. `install.sh` runs while `Conn.app` is running — it stops and changes nothing — Task 6.

---

## File Structure

- `internal/app/` (moved from `cmd/conn`): `wiring.go`, `history.go`, `myprs.go`, `reviews.go`, `sessions.go`, `work.go`, `provided.go`, their `_test.go` files, plus new `server.go` (`Start`, `Server`) and `holder.go` (`WaitForHolder`, `Port`). One responsibility: build and run the Conn server on a data folder.
- `cmd/conn/`: `main.go`, `stop.go`, `theme.go` and the tests of the CLI itself. Its `serve` becomes a call to `app.Start`.
- `internal/shellenv/`: reading, framing and merging a login shell's environment.
- `internal/desk/`: the startup and recovery rules (own, attach, restart), with I/O passed in.
- `cmd/conn-app/main_darwin.go`, `window_darwin.go`, `splash.html`: the Wails glue; nothing here holds a rule.
- `scripts/build-app.sh`: assemble, ad-hoc sign and zip `Conn.app`.
- `install.sh`, `.github/workflows/{ci,release}.yml`, `README.md`, `SECURITY.md`, `docs/releasing.md`, `CONTRIBUTING.md`, `.golangci.yml`, `go.mod`.

---

### Task 1: Spike — Wails on an external loopback page (throwaway)

**Files:** Create `spike/main.go` outside the module's tracked tree (scratchpad), not committed. Modify: the spec's "Decided" section with the result.

**Produces:** a written answer, in the spec, to: Wails v2 or v3; the exact build command that yields a runnable binary; whether the window shows `http://127.0.0.1:PORT`; whether a Dock click after closing brings it back; whether Cmd-Q, the app menu and a native dialog work on that page.

- [ ] **Step 1:** Build a program with the chosen Wails release that opens a window on `http://127.0.0.1:7777` (run `bin/conn serve` beside it) with hide-on-close, an app menu (Quit, Hide, Edit items) and one dialog call.
- [ ] **Step 2:** Check by hand: UI loads and a write (change a setting) succeeds; close the window, click the Dock icon, window returns; Cmd-Q exits; `kill -TERM <pid>` exits.
- [ ] **Step 3:** Record the result in the spec under "Decided (by the assistant)". If any check fails, stop and take the fallback in the spec's "First step of the plan" (Wails serves the page and proxies `/api`, `local()` untouched) to the owner before Task 3 onward.

Everything after this assumes it passed.

---

### Task 2: `internal/shellenv` — the login shell's environment

**Files:** Create `internal/shellenv/shellenv.go`, `internal/shellenv/shellenv_test.go`.

**Produces:**
```go
const (
	timeout        = 10 * time.Second // the whole shell run
	outputBytesMax = 1 << 20
	marker         = "\x00CONN-ENV\x00"
)

// Print writes marker then each entry of environ followed by NUL: what `conn-app --print-env` runs.
func Print(w io.Writer, environ []string) error
// Parse is the entries between the first marker and the end of output. Anything before the marker
// (a banner) is ignored; an entry with no '=' or an empty name is dropped.
func Parse(output []byte) ([]string, error) // error: no marker in output
// Merge is base with each of fromShell replacing or adding by name; order of base kept.
func Merge(base, fromShell []string) []string
// Args are the arguments that make shell (by its base name: fish, zsh, bash, else sh) run script
// as a login, interactive shell: ["-l", "-i", "-c", script].
func Args(shell string) []string
// Load runs shell with Args(shell) in "$CONN_ENV_EXE" --print-env (exe passed in the shell's
// environment, never spliced into the script, so quoting is the same in fish, zsh and bash) and
// returns Merge(base, Parse(output)). On a timeout, an exit error or an unparsable output it
// returns base unchanged and an error saying why.
func Load(ctx context.Context, shell, exe string, base []string) ([]string, error)
```
Use `internal/run.Cmd` for the process (Timeout, OutputBytesMax).

- [ ] **Step 1: Failing tests** (`TestParse`, `TestMerge`, `TestArgs`, `TestLoad`, each a table):
  - `TestParse`: output `"welcome\n" + marker + "A=1\x00B=two words\x00C=line1\nline2\x00D=x=y\x00junk\x00=novalue\x00"` returns `["A=1","B=two words","C=line1\nline2","D=x=y"]`; output with no marker returns an error (`ErrNoEnvironment`).
  - `TestMerge`: base `["A=1","B=2"]` with shell `["B=9","C=3"]` is `["A=1","B=9","C=3"]`.
  - `TestArgs`: `/opt/homebrew/bin/fish`, `/bin/zsh`, `/bin/bash`, `/bin/sh` and `""` each return `["-l","-i","-c", <script>]` where script is exactly `"$CONN_ENV_EXE" --print-env`.
  - `TestLoad` uses a fake shell script in `t.TempDir()` (executable) that: prints a banner then calls `$CONN_ENV_EXE`; sleeps past `testtime.Scaled(...)` limit (timeout case, returns base + error); exits 1 (error case). The fake `exe` is a script that calls `Print` through a tiny helper or `printf`.
- [ ] **Step 2:** `go test ./internal/shellenv/ -v` → FAIL (package missing).
- [ ] **Step 3:** Implement the signatures above. `timeout` stays a constant; tests reach the bound through an unexported `loadWith(ctx, shell, exe, base, timeout)` in `export_test.go`.
- [ ] **Step 4:** `go test -race ./internal/shellenv/` → PASS; `make lint` 0 issues.
- [ ] **Step 5:** Commit `Add the login shell's environment, for an app the Dock starts`.

---

### Task 3: `internal/app` (the move) and `internal/desk` (the rules)

This task has two parts that land in order: the move changes no behavior; the rules are new.

**Files:** `git mv` the wiring files listed in File Structure from `cmd/conn` to `internal/app` (package `app`), with the `_test.go` files that test them (`dryseed_test.go`, `provided_internal_test.go`, `myprs_test.go`, `sessions_test.go`, and any helper they alone use); create `internal/app/server.go`, `internal/app/holder.go`, `internal/app/server_test.go`, `internal/desk/desk.go`, `internal/desk/desk_test.go`; modify `cmd/conn/main.go`, `cmd/conn/wiring` callers.

**Produces (`internal/app`):**
```go
// Server is a Conn serving on a data folder, started by Start.
type Server struct { URL string /* http://127.0.0.1:PORT */ }
func (s *Server) Done() <-chan struct{} // closed after shutdown and lock release
func (s *Server) Err() error            // why it ended; nil after a clean shutdown; valid after Done

// Start takes the data folder's lock, loads settings, listens on Port(lookup), publishes
// {PID, URL} in the lock and serves until ctx ends, then shuts down within shutdownTimeout.
// It returns instance.ErrHeld (wrapped) when another Conn holds the folder, and a PortTakenError
// when another program holds the port.
func Start(ctx context.Context, lookup config.LookupFunc, stderr io.Writer) (*Server, error)

type PortTakenError struct{ Port int } // message names the port and CONN_PORT, as today
func Port(lookup config.LookupFunc) (int, error)                                   // moved, same rules
func WaitForHolder(ctx context.Context, dataDir string) (instance.Holder, error) // moved
var ErrStoppedMeanwhile, ErrNoAddress error                                       // moved sentinels
```
`cmd/conn`'s `serve` becomes: `app.Start`; on `instance.ErrHeld` → `alreadyRunning` (unchanged output); print `conn listening on <URL>`; wait for `Done()` or ctx; map `Err()` to the exit codes it has today.

**Produces (`internal/desk`):**
```go
type Action int
const (Own Action = iota; Attach; WaitForAddress)
// Decide: lock free → Own; held with a URL → Attach; held with none yet → WaitForAddress.
func Decide(running bool, holderURL string) Action

type Deps struct {
	Start   func(ctx context.Context) (*app.Server, error) // app.Start with the process lookup
	Holder  func() (instance.Holder, bool, error)          // instance.Running on the data folder
	Probe   func(ctx context.Context, url string) error    // one cheap GET of an existing /api route
	Open    func(url string)                               // load url in the window
	Fail    func(message string)                           // native dialog
}
const (probeEvery = 2 * time.Second; probeFailuresMax = 3; restartsMax = 5; restartWindow = time.Minute)
// Run opens the window and keeps it on a live server until ctx ends. Owning: Start, Open, and
// on ctx end cancel the server and wait for Done (bounded by app's shutdown timeout). Attached:
// Open, then Probe every probeEvery; probeFailuresMax in a row (or an owned server's Done)
// starts over from Decide. More than restartsMax starts over inside restartWindow → Fail(...) and
// return an error. An attached app never stops the server it attached to.
func Run(ctx context.Context, deps Deps) error
```

- [ ] **Step 1 (the move):** `git mv` the files, change `package main` to `package app`, export only what `cmd/conn` still calls. Run `make test` — expected: green with only package and import edits in the moved tests. Commit `Move the server's wiring into internal/app, with no change in behavior`.
- [ ] **Step 2: Failing tests for `Start`** in `internal/app/server_test.go` (table, `CONN_DATA` and `CONN_PORT=0` in a map lookup): free folder → `Server.URL` answers `GET /` with 200 and the lock file holds `{pid,url}`; second `Start` on the same folder → `errors.Is(err, instance.ErrHeld)`; a port held by a `net.Listen` in the test → `errors.As` to `PortTakenError` with that port; cancel ctx → `Done()` closes within `testtime.Scaled(3*time.Second)`, lock free after (`instance.Running` false), `Err()` nil.
- [ ] **Step 3:** Implement `Start`, `Server`, `PortTakenError`; reduce `cmd/conn.serve` to the shape above. `make test` green, including `cmd/conn`'s existing child-process tests untouched.
- [ ] **Step 4: Failing tests for `desk`** (`TestDecide` table of the three cases; `TestRun` table using hand-written fakes, no window, no disk):
  - free lock: `Open` called once with the started server's URL; cancel ctx → server ctx cancelled and `Run` returns nil.
  - held with URL: `Open` called with the holder URL; `Start` never called; cancel ctx → returns nil and the fake holder's server was not stopped (**Review Focus 3**; also run once with two real `app.Start`ed folders: second `Run` attaches).
  - attached, `Probe` fails `probeFailuresMax` times then the lock is free: `Start` called, `Open` called a second time with the new URL.
  - attached, `Probe` always fails and the lock stays held with a dead URL: after `restartsMax` restarts inside `restartWindow`, `Fail` is called once with a message naming the URL and `Run` returns an error; assert it returns (**Review Focus 4**).
  - `Start` returns `PortTakenError{7777}`: `Fail` message contains `7777` and `CONN_PORT`; `Run` returns an error.
- [ ] **Step 5:** Implement `Decide` and `Run`. Time through an injectable `After func(time.Duration) <-chan time.Time` field in `Deps` so tests do not sleep.
- [ ] **Step 6:** `make lint` (0 issues), `make test`. Commit `Add the rules for opening a window on a Conn: own it, attach to it, start over`.

---

### Task 4: `cmd/conn-app` — the Wails shell (darwin only)

**Files:** Create `cmd/conn-app/main_darwin.go`, `cmd/conn-app/window_darwin.go`, `cmd/conn-app/splash.html` (embedded), `cmd/conn-app/doc.go` (package comment, no build constraint). Modify `go.mod` and `go.sum` (Wails), `.golangci.yml` (depguard allow entry for the Wails module; the comment about "conn builds without cgo" says the CLI does, and the app is the one cgo exception).

**Interfaces:** Consumes `shellenv.Load/Print`, `desk.Run/Deps`, `app.Start`. Produces the `conn-app` binary.

- [ ] **Step 1:** `main_darwin.go` `func main()` only touches the process: if `os.Args[1]` is `--print-env`, call `shellenv.Print(os.Stdout, os.Environ())` and exit 0 before any GUI code. Otherwise: take `os.Environ()`, `shellenv.Load(ctx, os.Getenv("SHELL"), executablePath, environ)`, `os.Setenv` each entry (the one place this happens, so the server and everything it starts inherit it), keep the load error for the splash. If `$SHELL` is empty use `/bin/zsh`.
- [ ] **Step 2:** `window_darwin.go`: the Wails options per the spike result — splash HTML as the first page, hide-on-close, app menu (Quit, Hide, Edit items), `signal.NotifyContext` on `SIGTERM` and `SIGINT` that quits the app (so `conn stop` works). `desk.Deps.Open` navigates the window to the URL; `Fail` shows a native error dialog then quits. A shell load error is shown on the splash as one line ("could not read your shell's environment: …; using the app's own").
- [ ] **Step 3:** Pure helper `splashText(loadErr error) string` in a non-darwin-tagged file `cmd/conn-app/splash.go` with `TestSplashText` (nil error → no warning line; an error → the line above) so it runs on Linux too.
- [ ] **Step 4:** `go build -tags <from spike> -o /tmp/conn-app ./cmd/conn-app` on macOS; `/tmp/conn-app --print-env | head -c 40` prints the marker and entries; `GOOS=linux go vet ./...` still passes (the package is darwin-only).
- [ ] **Step 5:** Run by hand against Task 1's checklist, now through `desk`: the app owns a fresh folder (`CONN_DATA=$(mktemp -d)`); start `bin/conn serve` first on another folder and the app on that one (attach); `bin/conn stop` ends an owned app.
- [ ] **Step 6:** `make lint` on macOS (0 issues). Commit `Add the Conn app: a window on the server, kept in the Dock`.

---

### Task 5: `scripts/build-app.sh` and CI

**Files:** Create `scripts/build-app.sh`, `scripts/Info.plist.tmpl`, `assets/app/Conn.icns` (placeholder). Modify `.github/workflows/ci.yml`.

**Produces:** `scripts/build-app.sh <out-folder>` writes `Conn_darwin_universal.zip` and `checksums-app.txt` (SHA-256 of the zip, same line format as `checksums.txt`). Bundle: `Conn.app/Contents/{MacOS/conn-app,Info.plist,Resources/Conn.icns}`, `CFBundleIdentifier` a constant in the script, `CFBundleShortVersionString` from `VERSION`, `LSMinimumSystemVersion` the minimum the chosen Wails release states.

- [ ] **Step 1:** Script (POSIX sh, same header comment style as `build-release.sh`): requires `dist/index.html`; builds `conn-app` for `darwin/arm64` and `darwin/amd64` with `CGO_ENABLED=1` and the spike's tags; `lipo -create` into one binary; assemble the bundle; `codesign --force --deep --sign - Conn.app`; `ditto -c -k --keepParent Conn.app <zip>`; checksum.
- [ ] **Step 2:** Run it locally on macOS. Verify: `codesign --verify --deep --strict Conn.app` exits 0; `lipo -archs …/conn-app` prints `x86_64 arm64`; `…/conn-app --print-env` prints the marker.
- [ ] **Step 3:** `ci.yml`: add `macos-latest` to the `check` job's matrix (so the darwin-only files are linted and type-checked, since Linux lint cannot see them), and a macOS job `app` (same pinned actions as the other jobs, `contents: read`) that runs `bun run build`, `scripts/build-app.sh app-files`, and the three verifications from Step 2 as `run` lines.
- [ ] **Step 4:** Push the branch and watch CI; both matrix legs of `check` and the `app` job must pass. Commit `Build the app bundle, and lint the app on macOS in CI`.

---

### Task 6: `install.sh` installs the app

**Files:** Modify `install.sh`, `internal/installscript/install_test.go` (and its fake release server fixtures).

**Interfaces:** the fake release server (in the test) gains `Conn_darwin_universal.zip` and `checksums-app.txt`. New environment: `CONN_INSTALL_APP_DIR` (default `$HOME/Applications`). New argument: `--cli-only`. Linux never installs the app.

- [ ] **Step 1: Failing tests** (extend the existing `run` helper in `install_test.go`, which already puts a fake `uname` first on `PATH` and uses a fresh `HOME`; add a fake `pgrep` the same way, answering a pid or nothing, and set `CONN_INSTALL_APP_DIR` under the temp `HOME`):
  - macOS default: the CLI is installed as before **and** `$CONN_INSTALL_APP_DIR/Conn.app/Contents/MacOS/conn-app` exists after unzip.
  - `--cli-only`: no app directory is created.
  - a bad checksum for the zip: exit non-zero, no `Conn.app`, and the CLI install is unchanged by it.
  - a release without the app asset: CLI installed, output says the release has no app, exit 0.
  - **Review Focus 5:** with the fake `pgrep` answering a pid for `Conn.app/Contents/MacOS/conn-app`, exit non-zero, message says to quit Conn first, and `Conn.app` is byte-for-byte what it was.
  - Linux: never touches `CONN_INSTALL_APP_DIR`.
- [ ] **Step 2:** `go test ./internal/installscript/ -v` → the new cases FAIL.
- [ ] **Step 3:** Implement in `install.sh`: parse `--cli-only` in `main`; after the CLI step, on macOS unless `--cli-only`, fetch the zip and `checksums-app.txt`, verify with the existing `sha256_of`, refuse if `pgrep -f 'Conn.app/Contents/MacOS/conn-app'` finds a process, unpack with `unzip -q` (on macOS and in the Linux tests alike; a bundle of plain files needs nothing `ditto` adds) into the scratch folder, then replace `Conn.app` by `mv` of the old one aside, the new one in, and removing the old (restore the old on failure). Update the header comment and the closing hint ("Open Conn from ~/Applications, or run conn serve").
- [ ] **Step 4:** `go test ./internal/installscript/ -v` → PASS; `sh -n install.sh`.
- [ ] **Step 5:** Commit `Install the app with install.sh on macOS, unless --cli-only`.

---

### Task 7: Release and docs

**Files:** Modify `.github/workflows/release.yml`, `docs/releasing.md`, `README.md`, `SECURITY.md`, `CONTRIBUTING.md`.

- [ ] **Step 1:** `release.yml`: new job `app` on `macos-latest`, `needs: build`, same checkout of the tag, setup-go, setup-bun, pinned SHAs as the `build` job, `bun install --frozen-lockfile`, `bun run build`, stamp `VERSION`, `scripts/build-app.sh release-files`, then `gh release upload "$TAG" release-files/Conn_darwin_universal.zip release-files/checksums-app.txt --clobber`. The `build` job is unchanged. Update the header comment.
- [ ] **Step 2:** `docs/releasing.md`: list the two new assets; a "Before the first release with the app" checklist: install on a clean Apple-silicon Mac with `install.sh` and open `~/Applications/Conn.app` (no Gatekeeper warning), then download the zip in a browser and note the warning (cleared with `xattr -dr com.apple.quarantine Conn.app`); the Developer ID signing and notarization step goes on the `app` job when the account exists.
- [ ] **Step 3:** `README.md`: what the app is, how it installs, Dock behavior (close keeps it running, Cmd-Q quits and stops it if it started the server), that `conn serve` still works, that the app takes the environment from your login shell, the minimum macOS version. `SECURITY.md`: the app is the same server with the same guards and data folder, and reads the login shell's environment into its own process only. `CONTRIBUTING.md`: building the app needs macOS, Xcode command line tools and cgo; everything else stays pure Go.
- [ ] **Step 4:** `make check` and `make test`. Commit `Release the app and document it`.

---

## Self-review

- **Spec coverage:** goals (window, Dock, server inside, install) → Tasks 3, 4, 6; no weakened guards → loopback URL in Tasks 1 and 4, nothing in `internal/server` touched; single wiring copy → Task 3; attach and quit rules → Task 3; whole-environment → Tasks 2 and 4; server-loss recovery and fixed port dialog → Task 3; universal bundle, ad-hoc signing, later notarization → Tasks 5 and 7; spike first → Task 1; `--cli-only` and refuse-while-running → Task 6; docs → Task 7. Linux `make check` unharmed → Global Constraints and Task 5's macOS lint leg.
- **Types match across tasks:** `app.Start/Server/PortTakenError/WaitForHolder/Port` (Task 3) are what Task 4 and `desk.Deps` use; `shellenv.Load/Print` (Task 2) are what Task 4 calls; `Conn_darwin_universal.zip` and `checksums-app.txt` (Task 5) are what Tasks 6 and 7 name.
- **Open to the builder:** Wails version and tags (Task 1), the `/api` route used by `Probe`, the placeholder icon, the universal-or-per-arch fallback if `lipo` of two cgo builds is not possible on the runner.
