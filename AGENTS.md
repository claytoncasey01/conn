# AGENTS.md

How code is written in Conn, for anyone working on it, human or coding agent. The
[README](README.md) has the commands and the layout.

Run `make check` and `make test` before every push. `make lint` must report 0 issues.

## Go

The Go follows Data Oriented Design, [TigerStyle](https://tigerstyle.dev) adjusted for Go, the
practices of Terraform's Go codebase, and gofumpt. Write new code this way, and leave code you
touch this way.

- **Data first.** Plain data types and pure functions that take data in and return data out. I/O
  (files, processes, HTTP) happens at the edges: read, decide, write. The deciding part is tested
  with plain values, no fakes.
- **Every linter stays on.** `.golangci.yml` turns on everything, then opts out of a few with a
  reason for each. Never add `//nolint` to get past a finding: fix the code. If a linter is wrong
  for the whole repo, disable it in `.golangci.yml` with a comment saying why.
- **Dependencies are allowlisted.** `depguard` permits the standard library, this module and the
  modules listed in `.golangci.yml`. Adding a dependency means adding it there in the same change.
- **No package-level state.** Build everything in `main` and pass it in. Sentinel errors and
  compiled regular expressions are the exceptions.
- **Small interfaces, where they are used**, and only when a test or a second implementation
  needs one. Fakes are hand-written structs.
- **`main` only touches the process.** It hands its arguments, environment and output streams to
  `realMain` and exits with what that returns, so everything past the process boundary is tested
  through `realMain`. An unknown command prints usage and exits 2.
- **Report every problem at once.** Validation checks every input and returns all the problems,
  each saying what to set it to, rather than stopping at the first.
- **No compound conditions.** Never join conditions with `&&` or `||`; split them into nested ifs
  or a `switch`, one case each. `internal/guard` fails the tests on one.
- **A test that covers more than one case is a table**, so the next case is one more row. A row
  states one expected error (`wantErr` is `nil` or a sentinel). Tests assert one fact per check,
  so a failure names the one that broke. Time limits in tests go through `testtime.Scaled`, which
  `CONN_TEST_TIME_SCALE` stretches.
- **Set library options explicitly.** When building an `http.Server`, an `exec.Cmd` or similar,
  name every limit even when the value is the default.
- **100 columns**, a tab counting as 4, and functions of at most 60 lines.
- **Limits are named with the unit and the qualifier last:** `bodyBytesMax`, `patternsMax`, never
  `maxPatterns`. A `time.Duration` already carries its unit, so it is named for what it bounds.
- **One source of truth per fact.** A path, limit or rule is defined once and referenced
  everywhere it is used.
- **Export only what another package uses.** Tests that need an unexported name use
  `export_test.go` or an `_internal_test.go` file.

## What the server must keep

The server answers the pages in `src/` over `/api`. Its routes, JSON shapes, event stream, data
files under `~/.conn` and `CONN_*` variables are a contract with the UI: change one only together
with the page that reads it. `testdata/answers/` holds cases both the UI's tests and the Go tests
check, so the two sides cannot drift apart.

## Commits and pull requests

- A commit subject says what changes, in plain English.
- A pull request says what changed and why, and how it was verified.
- Nothing names an organization a checkout is used at: see `.company-terms` in the README.
