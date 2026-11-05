# Update Notice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Conn.app finds out that a newer release exists and says so: a "Check for Updates…" menu item and a once-per-version dialog with the install command. It installs nothing.

**Architecture:** A pure package `internal/update` (version rules, redirect reading, one HEAD request, a `Run` loop with injected clock, request and reporter) with no Wails in it; `cmd/conn-app` (darwin only) adds the menu item and dialogs and starts `update.Run` next to `desk.Run`. No server route, no `src/` change.

**Tech Stack:** Go 1.27 standard library, Wails v3.1.0 (already pinned, menu and dialog APIs), `net/http/httptest` for tests.

**Spec:** `docs/superpowers/specs/2026-11-05-update-notice-design.md` (the binding authority; where this plan and the spec disagree, the spec wins and the ledger records a ruling).

## Global Constraints

- Follow `AGENTS.md`: gofumpt; TigerStyle adapted for Go (a named constant for every limit; no `&&`/`||`, which `internal/guard` enforces; functions at most 60 lines, lines at most 100 columns; names with units last); data-oriented (pure rules, I/O at the edges, no package-level state except sentinel errors and compiled regexps); tests are tables with `t.Parallel`; every package and exported name has a doc comment; time limits in tests go through `testtime.Scaled`. `make lint` must report 0 issues; `make check` and `make test` pass. Never `//nolint`.
- `internal/update` imports only the standard library (so it is tested and linted on Linux). Only `cmd/conn-app/*_darwin.go` touches Wails. `cmd/conn-app` keeps only `_darwin` files. `GOOS=linux go vet ./...` and `CGO_ENABLED=0 go build ./cmd/conn` must keep passing.
- Constants (exact values): `StartDelay = 10 * time.Second`, `CheckEvery = 24 * time.Hour`, `checkTimeout = 5 * time.Second`, `locationBytesMax = 512`, `versionDigitsMax = 9`.
- The releases address is one constant, `ReleasesURL = "https://github.com/claytoncasey01/conn/releases"`; the install command is one constant, `Command = "curl -fsSL " + ReleasesURL + "/latest/download/install.sh | sh"`.
- The environment: `CONN_UPDATE_CHECK` (off values: `off`, `false`, `0`, `no`, `none`, `disabled`, any case, trimmed) and `CONN_UPDATE_BASE` (the releases address, default `ReleasesURL`; for tests and for trying a build against a fake release server, as `CONN_INSTALL_BASE` does for the installer).
- No git push. One commit per task, plain-English subjects.
- The network request carries nothing of the user's: no version, no identifier; the client name is `conn-app`.

## Review Focus

1. A hostile or odd answer: a redirect to another host, a `Location` that is not a release tag, an over-long `Location`, a 200 with no redirect, a 404 or 5xx. Each gives "could not check", never an offer, never a URL built from the text. (Task 2)
2. Version comparison: `v1.10.0` newer than `v1.9.0`; a version is not newer than itself; `v1.2.3-rc1` latest is not newer than `v1.2.3`; a current `v1.2.3-rc1` is offered `v1.2.3`; `dev` never checks. (Task 1)
3. No loop and no pile-up: a failing network gives one request per interval; menu asks while a check runs do not start a second; cancelling the context stops `Run` mid-wait and mid-request. (Task 3)
4. Proxy: the check goes through `HTTPS_PROXY` from the login-shell environment (a work computer), so it must start after the environment is loaded. (Task 4)
5. Off switch: `CONN_UPDATE_CHECK=off` makes no request at all and hides the menu item, even when the variable comes only from the login shell. (Tasks 3 and 4)

---

### Task 1: Version rules, tag reading, off switch

**Files:**
- Create: `internal/update/update.go`, `internal/update/update_test.go`

**Interfaces:**
- Produces:
  - `const ReleasesURL`, `const Command` (values above), `const StartDelay`, `const CheckEvery`.
  - `type Kind int` with `Unchecked`, `UpToDate`, `Available`, `Failed` (in that order, documented).
  - `type Result struct { Kind Kind; Latest string; Err error }`, plain data.
  - `func Newer(current, latest string) bool`.
  - `func Evaluate(current, latest string, fetchErr error) Result`: `current` not a version (e.g. `dev`) gives `Unchecked`; `fetchErr != nil` gives `Failed` with the error; an unparseable `latest` gives `Failed`; `Newer` gives `Available`; else `UpToDate` (both with `Latest` set).
  - `func Enabled(raw string) bool`: false for the off values, true otherwise (unset, blank, anything else).
  - `func TagFrom(location, requestURL, basePath string) (string, bool)` is **not** here; it is Task 2.

- [ ] **Step 1: Write the failing tests** (`update_test.go`, package `update_test`, tables). `TestNewer` rows (current, latest, want): `v1.9.0`/`v1.10.0` true; `v1.2.3`/`v1.2.3` false; `v1.2.3`/`v1.2.2` false; `v1.2.3`/`v2.0.0` true; `v1.2.3`/`v1.2.3-rc1` false; `v1.2.3-rc1`/`v1.2.3` true; `v1.2.3-rc1`/`v1.2.3-rc2` false; `v1.2.3+7`/`v1.2.3` false; `dev`/`v1.0.0` false; `v1.0.0`/`dev` false; `v1.0.0`/`` false; `v1.0.0`/`1.0.1` false (no leading `v`); `v1.0.0`/`v1.0` false; `v1.0.0`/`v1.0.1.2` false; `v1.0.0`/`v1.0.9999999999` false (more than `versionDigitsMax` digits); `v1.0.0`/`v1.0.1 ` false. `TestEvaluate` rows: dev current gives `Unchecked` with no `Latest`; fetch error gives `Failed` and `errors.Is` finds it; garbage latest gives `Failed`; newer gives `Available` with `Latest`; equal gives `UpToDate`. `TestEnabled` rows: `""`, `"on"`, `"yes"`, `"1"`, `"garbage"` true; `"off"`, `" OFF "`, `"false"`, `"0"`, `"no"`, `"none"`, `"Disabled"` false.
- [ ] **Step 2: Run them** (`go test ./internal/update`), expect FAIL (package does not compile).
- [ ] **Step 3: Implement** in `update.go`: a compiled regexp for `vMAJOR.MINOR.PATCH` with optional `-pre` and `+build`, numbers of at most `versionDigitsMax` digits; `Newer` parses both, compares numbers as numbers, on equal numbers is true only when current has a pre-release and latest has none. The off values are one table the code reads.
- [ ] **Step 4: Run** `go test -race ./internal/update` and `make lint`; expect PASS and 0 issues.
- [ ] **Step 5: Commit** `Add the rules for telling whether a newer release exists`.

### Task 2: Finding the latest release

**Files:**
- Modify: `internal/update/update.go` (or a new `internal/update/fetch.go`), `internal/update/update_test.go` (or `fetch_test.go`)

**Interfaces:**
- Consumes: Task 1's version regexp for the tag shape.
- Produces:
  - `func NewClient() *http.Client`: `Timeout: checkTimeout`; transport with `Proxy: http.ProxyFromEnvironment`, a dial timeout, `TLSHandshakeTimeout`, `ResponseHeaderTimeout`, `MaxResponseHeaderBytes`, `DisableKeepAlives: true`, every limit named explicitly (AGENTS.md); `CheckRedirect` returns `http.ErrUseLastResponse`.
  - `func Latest(ctx context.Context, client *http.Client, base string) (string, error)`: `HEAD base+"/latest"` with `User-Agent: conn-app`; the status must be 301, 302, 303, 307 or 308; the `Location` header must be at most `locationBytesMax` bytes; it is resolved against the request URL; the resolved scheme and host must equal the request's; its path must be exactly the base's path plus `/tag/` plus a tag that matches the version shape and is at most 64 bytes; returns the tag. Anything else is an error that says what was wrong in short words (never echoing the header's text). The body is never read.
  - sentinel errors for the cases (`ErrNoRedirect`, `ErrBadLocation`, …), compared with `errors.Is` in tests.

- [ ] **Step 1: Write the failing tests** with `httptest.NewServer` handlers (the server's URL plus `/claytoncasey01/conn/releases` is `base`): a redirect to the same server's `/claytoncasey01/conn/releases/tag/v1.2.3` gives `v1.2.3`; the same with a relative `Location`; a redirect to another host gives the bad-location error; `Location` of `/claytoncasey01/conn/releases/tag/not-a-version`, `/…/tag/v1.2.3/extra`, `/…/tag/v1.2.3?x=1`, `/…/releases/latest`, `/other/repo/releases/tag/v1.2.3`, a 600-byte one: each the bad-location error (the 600-byte one for its length); a 200 gives the no-redirect error; 404 and 500 give a status error; a handler that hangs ends at the context's deadline (use a short context) without hanging the test; the test server records that the method was `HEAD`, the `User-Agent` was `conn-app`, and no cookie/authorization/`Referer` header was sent.
- [ ] **Step 2: Run them**, expect FAIL.
- [ ] **Step 3: Implement** `NewClient`, `Latest` and the tag reading as a pure helper `tagFrom(location *url.URL-or-string, request *url.URL, basePath string) (string, bool)` so the rule is tested without a server too (a short table).
- [ ] **Step 4: Run** `go test -race ./internal/update`, `make lint`; PASS, 0 issues.
- [ ] **Step 5: Commit** `Find the latest release from GitHub's redirect, and reject any other answer`.

### Task 3: The check loop

**Files:**
- Create: `internal/update/run.go`, `internal/update/run_test.go`

**Interfaces:**
- Consumes: Tasks 1 and 2 (`Evaluate`, `Result`, `Latest`).
- Produces:
  - `type Event struct { Result Result; Manual bool; Fresh bool }`: `Manual` is a check the user asked for; `Fresh` is true the first time this run reports that `Latest` as `Available` (always true for `Manual`).
  - `type Deps struct { Current string; Fetch func(ctx context.Context) (string, error); After func(d time.Duration) <-chan time.Time; Asked <-chan struct{}; Report func(Event) }`.
  - `func Run(ctx context.Context, deps Deps)`: returns when `ctx` ends. One goroutine does everything, so there is never a second request at once. It waits `StartDelay` (via `After`), checks, then waits `CheckEvery`, and so on. A value on `Asked` makes a `Manual` check at once (a pending ask while a check runs is not queued beyond the channel's own buffer: the app uses a buffer of 1). With `Current` not a version: no automatic check is ever made, and an ask reports `Unchecked` without a request. Every result, including `Failed`, is reported; a failed automatic check is not retried before the next interval.

- [ ] **Step 1: Write the failing tests** (fake `After` that hands the test the durations asked for and fires on command, fake `Fetch` that counts calls and can block until released, a `Report` that records events; `runBounded` helper with `testtime.Scaled` so a hang fails instead of hanging): first check only after `StartDelay`, then after `CheckEvery`; an available release reports `Fresh` once per version and not again at the next interval, but a newer one later is `Fresh`; a manual ask reports `Manual` and `Fresh`, also for a version already told; two asks while a fetch is blocked make one more fetch, not two; a failing fetch reports `Failed` once per interval, with no extra fetch; `ctx` cancelled during the start wait, during the interval wait and during a blocked `Fetch` (the fake `Fetch` honours the context) all return `Run` promptly; `Current: "dev"` makes zero fetches over several intervals and an ask reports `Unchecked`; a break-and-restore proof for the pile-up row (remove the single-goroutine guarantee in a scratch edit, show the row fails, restore).
- [ ] **Step 2: Run them**, expect FAIL.
- [ ] **Step 3: Implement** `Run` as a loop over a `select` on `ctx.Done()`, the current timer channel and `Asked`; keep `Run` under 60 lines by splitting the check-and-report step.
- [ ] **Step 4: Run** `go test -race -shuffle=on -count=3 ./internal/update`, `make lint`; PASS, 0 issues.
- [ ] **Step 5: Commit** `Run the update check after launch and once a day, and when asked`.

### Task 4: The menu item, the dialogs and the wiring

**Files:**
- Modify: `cmd/conn-app/menu_darwin.go`, `cmd/conn-app/window_darwin.go`
- Create: `cmd/conn-app/update_darwin.go`

**Interfaces:**
- Consumes: `update.Run`, `update.Deps`, `update.Event`, `update.NewClient`, `update.Latest`, `update.Enabled`, `update.ReleasesURL`, `update.Command`, `update.Result` kinds; `conn.Version()` for `Current` (the app's embedded version; check how `cmd/conn` imports it); `s.openInBrowser`, `s.app.Dialog`, `s.app.Clipboard` (confirm each Wails v3.1.0 API in the module cache: `go env GOMODCACHE`).
- Produces: an app-menu item **Check for Updates…** that sends on a buffered(1) channel without blocking (a full buffer drops the ask); after an `Available` event its label reads **Update Available: <tag>…** until quit; the update dialog (title `Conn <tag> is available`; message says what the user has and has not, that Conn must be quit first, and shows `update.Command`; buttons **Copy Command** (puts `update.Command` on the clipboard), **Release Notes** (opens `<base>/tag/<tag>` through `openInBrowser`; the URL is built only from a tag that passed Task 2 and the base constant) and **Later**); the manual answers "Conn is up to date (<current>)", "Could not check for updates" with a short reason (no response text), and "This is a development build, which does not check for updates"; a one-line stderr note when an update is found or a check fails (short reason only).

- [ ] **Step 1: Build the menu.** The Wails `AppMenu` role may not be extendable: find out (read the v3.1.0 source). If an item cannot be added to it, build the app menu by hand from roles (About, separator, Check for Updates…, separator, Hide, Hide Others, Show All, separator, Quit) keeping every key (Cmd-Q, Cmd-H, Cmd-Opt-H) and the Edit and Window menus as they are. `appMenu` returns the menu and the update item (a small struct), so `run` can keep both.
- [ ] **Step 2: Wire `update.Run`.** Start it from `runDesk` right after `loadEnvironment` returns and before `desk.Run` (a goroutine on the same context): the HTTP proxy variables from the login shell are read once, at the first request, so the check must not run before they are set. Read `CONN_UPDATE_CHECK` through `lookup` after the environment load; when `update.Enabled` is false, start nothing and hide the menu item on the main thread (`application.InvokeSync`; if the item cannot be hidden, disable it and say so in the report: that is a ruling). `CONN_UPDATE_BASE` through `lookup`, default `update.ReleasesURL`. The context is the one `desk.Run` runs on, so a quit ends it; the quit does not wait for it.
- [ ] **Step 3: The dialogs and label.** Report runs on `Run`'s goroutine: set the label with `application.InvokeSync` (menu changes belong on the main thread) and show each dialog from its own goroutine, because `Show` waits for the button (see `tellEnvironmentFailed`). Show the dialog for `Available` only when `Fresh`; always for a `Manual` event.
- [ ] **Step 4: A test for the text.** Put the message and title builders in `internal/update` as pure functions (`func Message(current, latest string) string` and friends, a table test that the text names both versions, the command and says to quit first, and that nothing from outside the tag is in it) so the darwin glue holds no wording.
- [ ] **Step 5: Verify.** `make lint` (including the darwin package), `make check`, `make test`, `GOOS=linux go vet ./...`, `CGO_ENABLED=0 go build ./cmd/conn`. Then the headless smoke test: build the app (see `scripts/build-app.sh`, which needs `bun run build` first; write `v0.0.1` into `VERSION` for the build and restore it afterwards), start a tiny fake release server (a few lines of Go or `python3 -m http.server` cannot redirect, so write a Go `httptest`-style program in the scratchpad) that redirects `/x/releases/latest` to `/x/releases/tag/v0.0.2`, start the app with `CONN_UPDATE_BASE` pointing at it and `CONN_DATA` in a temp folder, wait past `StartDelay`, and show the stderr note naming `v0.0.2`; run it again with `CONN_UPDATE_CHECK=off` and show the fake server saw no request. Say plainly what could not be seen (the menu, the dialogs, the buttons).
- [ ] **Step 6: Commit** `Tell the user in the app when a newer release exists`.

### Task 5: Documentation

**Files:**
- Modify: `README.md`, `SECURITY.md`, `docs/releasing.md`, `docs/superpowers/specs/2026-11-05-update-notice-design.md`

- [ ] **Step 1: README.** Add `CONN_UPDATE_CHECK` (default on; `off` turns it off) to the Configuration table, and say in "What Conn touches" → Network that the macOS app asks `github.com` once at launch and daily whether a newer release exists, sending nothing of yours; in the app section, what the menu item and dialog do and that updating is re-running the install command. Do not touch the Status line or the "Linux untried" line (the owner's). `CONN_UPDATE_BASE` is for developers: a short line in `CONTRIBUTING.md` or `docs/releasing.md`, not the table.
- [ ] **Step 2: SECURITY.md.** One sentence: the app's update check is one unauthenticated request that only reads a redirect, installs nothing, and trusts only an answer that is a release tag on the same host.
- [ ] **Step 3: `docs/releasing.md`.** Add to the manual checklist: with the repository public and a newer release published, an older build shows the item and the dialog; **Copy Command** copies; **Release Notes** opens the release; **Check for Updates…** says up to date on the newest; with `CONN_UPDATE_CHECK=off` the item is gone.
- [ ] **Step 4: The spec.** Mark it built, record `CONN_UPDATE_BASE`, the start-after-environment rule and any ruling the tasks made (the menu built by hand; hidden or disabled item).
- [ ] **Step 5: Verify and commit.** `make check`, `make lint`; commit `Document the update notice`.
