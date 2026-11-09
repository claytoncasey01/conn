# AGENTS.md

How code is written in Conn, for anyone working on it, human or coding agent. `CLAUDE.md` imports
this file, so Claude Code reads the same rules. The [README](README.md) has the commands and the
layout.

Run `make check` and `make test` before every push. `make lint` must report 0 issues.

## Go

The Go follows three sources, and gofumpt formats it:

- [TigerStyle](https://tigerstyle.dev), TigerBeetle's coding style, adapted for Go.
- Data Oriented Design.
- The practices of [Terraform](https://github.com/hashicorp/terraform)'s Go codebase. It is a
  very large Go project that has stayed readable, so it is the example to follow where these
  rules say nothing.

Write new code this way, and bring code you touch up to it. Where the sources disagree, the
order is: safety, then performance, then the developer's experience.

### TigerStyle, adapted for Go

- **Safety first, then performance, then the developer's experience.** A shortcut that trades the
  first for the others is not taken.
- **Put a limit on everything.** Every loop, queue, buffer, file read, request body, retry and
  wait has a fixed upper bound, named as a constant: a `for` walking up folders stops after
  `maxFoldersUp`, a file larger than `maxFileBytes` is not read, a process past `Timeout` is
  stopped. Nothing grows without bound because its input did.
- **No unbounded recursion.** Where input decides the depth (nested JSON, a folder tree), the
  depth is capped and the cap is checked.
- **Check what must be true, and fail loudly when it is not.** Go has no asserts: check
  preconditions, postconditions and invariants in code. A broken invariant (a program bug) panics
  with a message saying which; bad input or a failing environment (a file, a process, the
  network) is an error returned to the caller. Never panic on input.
- **Handle every error.** An error is returned, wrapped or acted on. Discarding one is explicit
  (`_ =`) and only where nothing useful can be done, such as closing a file already read.
- **Simple, explicit control flow.** Guard clauses return early and keep the happy path
  unindented. A compound condition (`&&`, `||`) is fine while it reads clearly; when one grows
  past that, name it with a variable or split it. No compound condition has a side effect. Prefer
  stating a condition positively.
- **Small functions, short lines.** At most 60 lines per function and 100 columns per line, a
  tab counting as 4. A function that grows past that is split where its steps are.
- **Smallest scope.** Declare a variable where it is used, compute a value right before it is
  needed, and keep the number of names in scope small.
- **Names say everything.** No abbreviations (`index`, not `i`, outside the shortest loops).
  Limits and defaults are named the way the standard library names them: a limit that feeds a
  library option takes that option's name (`maxHeaderBytes` for `MaxHeaderBytes`), and any other
  puts the qualifier first (`maxBodyBytes`, `maxPatterns`, `defaultCloseMinutes`), never
  `bodyBytesMax`. A `time.Duration` already carries its unit, so it is named for what it bounds
  (`summaryTimeout`).
- **Comments are sentences that say why.** A capital letter and a full stop; the code says what.
- **Set library options explicitly.** When building an `http.Server`, an `exec.Cmd` or similar,
  name every limit even when the value is the default, so each limit is a decision made where it
  is used.
- **Few dependencies.** Each one is a deliberate choice, allowlisted in `.golangci.yml`.
- **No technical debt.** Do it properly the first time; a known shortcut is not merged.

### Data Oriented Design

- **Start from the data.** Know what it is, how much of it there is, where it comes from and
  where it goes, before writing the code that moves it. The code is a transformation of data.
- **Plain data, plain functions.** Types are data: structs of values with no hidden behaviour.
  Logic lives in functions that take data in and return data out.
- **I/O at the edges.** A handler reads (files, processes, requests), decides (pure functions),
  then writes. The deciding part is tested with plain values, no fakes and no disk.
- **Few indirections.** No interfaces, hierarchies or wrappers unless a second implementation or
  a test needs one. Prefer a slice of values to a graph of pointers, keep ordered data in order,
  and use a map only for lookup.
- **One representation per fact.** A path, limit or rule is defined once and referenced
  everywhere it is used, and two types that track the same state become one. Derive the rest.
- **Lay data out for how it is used.** Group what is read together, and pass a small struct of
  what a step needs rather than a long list of parameters.
- **Tables are data too.** A rule with many cases (error names, recognised values) is a table
  the code reads, and a test with many cases is a table of rows.

### Terraform's practices

- **`main` only touches the process.** It hands its arguments, environment and output streams to
  `realMain` and exits with what that returns, so everything past the process boundary is tested
  through `realMain`. An unknown command prints usage and exits 2.
- **No package-level state.** Build everything in `main` and pass it in, with no DI library.
  Sentinel errors and compiled regular expressions are the only package-level variables. A cache
  is a value its owner creates, never a global.
- **Small packages under `internal/`, one per concept.** A package is named for what it does
  (`settings`, `origin`), never `util` or `common`.
- **Small interfaces, where they are used**, and only when a test or a second implementation
  needs one. Fakes are hand-written structs.
- **Export only what another package uses.** Tests that need an unexported name use
  `export_test.go` or an `_internal_test.go` file; other tests live in the `_test` package.
- **Report every problem at once.** Validation checks every input and returns all the problems
  (`errors.Join`, or a list the page shows), each saying what to set it to, rather than stopping at
  the first.
- **Errors carry their context.** Wrap with `fmt.Errorf("doing what: %w", err)` so the chain says
  where it failed, and compare with `errors.Is` and `errors.As`, never by message.
- **`context.Context` first** for anything that can block or be cancelled.
- **A test that covers more than one case is a table**, so the next case is one more row, run
  with `t.Run` and `t.Parallel`. A row states one expected error (`wantErr` is `nil` or a
  sentinel). Tests assert one fact per check, so a failure names the one that broke. Time limits
  in tests go through `testtime.Scaled`, which `CONN_TEST_TIME_SCALE` stretches.
- **Fixtures live in `testdata/`.** A contract (a prompt, a file format) has a golden file there,
  and a deliberate change rewrites it with `CONN_UPDATE_GOLDEN=1 go test ./...`, so the review
  shows its diff.
- **Every package and exported name has a doc comment.**

### Enforced by tooling

- `make lint`: golangci-lint with every linter on (`.golangci.yml`), opting out of a few with a
  reason for each. Never add `//nolint` to get past a finding: fix the code. If a linter is wrong
  for the whole repo, disable it in `.golangci.yml` with a comment saying why.
- `make fmt`: gofumpt and goimports.
- `make test`: the race detector, in random order, and `internal/guard` (no organization names).

## What the server must keep

The server answers the pages in `src/` over `/api`. Its routes, JSON shapes, event stream, data
files under `~/.config/conn` and `CONN_*` variables are a contract with the UI: change one only
together with the page that reads it. `testdata/answers/` holds cases both the UI's tests and the Go tests
check, so the two sides cannot drift apart.

## Commits and pull requests

- A commit subject says what changes, in plain English.
- A pull request says what changed and why, and how it was verified.
- Nothing names an organization a checkout is used at: see `.company-terms` in the README.
