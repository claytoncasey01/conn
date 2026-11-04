# Contributing

Thank you for wanting to help. This page is how to build Conn, check a change and send it. The
rules for the code itself are in [AGENTS.md](AGENTS.md), which people and coding agents follow
alike; read it before your first change, because it is stricter than most Go projects.

## Before you start

- **A bug or a small fix:** open a pull request, or an issue first if you are unsure.
- **A feature or a change in behaviour:** open an issue first and say what you want and why. Conn
  keeps its routes, JSON shapes, event stream, data files and `CONN_*` variables as a contract with
  the page that reads them, so a change to one is made together with the other.
- **A theme for the gallery:** it goes to [conn-themes](https://github.com/claytoncasey01/conn-themes),
  not here. [Writing a theme](docs/themes.md) lists every token.
- **A security problem:** do not open an issue. Follow [SECURITY.md](SECURITY.md).

## Build and run

You need [Go](https://go.dev) 1.27 or later and [Bun](https://bun.sh) 1.4 or later, and
`golangci-lint` v2 for the lint. The [README](README.md#build-from-source) has the rest.

```sh
make dev     # hot reload: UI on :5173, API on :7778, data in ~/.config/conn-dev
make serve   # build, then run on :7777
```

You do not need real GitHub, Slack or Jira accounts to work on a page. `CONN_MYPRS_DRY=1`,
`CONN_REVIEWS_DRY=1` and `CONN_WORK_DRY=1` fake My PRs, Reviews and Open work with invented data
and make no call. Point `CONN_DATA` at a scratch folder when you use them. See
[dry runs](docs/settings.md).

## Check your change

Run these before you push. A pull request has to pass the same checks in CI, on Linux and macOS.

```sh
make fmt     # gofumpt and goimports
make check   # make lint (0 issues, every linter on), then svelte-check
make test    # bun test, then go test with the race detector, in random order
```

- Add a test with the change. A test with several cases is a table of rows. Fixtures live in
  `testdata/`; a contract with a golden file is rewritten on purpose with
  `CONN_UPDATE_GOLDEN=1 go test ./...`, so the review shows the diff.
- Never add `//nolint` to get past the linter: fix the code. If a linter is wrong for the whole
  repository, disable it in `.golangci.yml` with a comment saying why.
- A few rules are enforced by the tests in `internal/guard`, such as no `&&` or `||` in Go (split
  the condition into nested ifs or a `switch`). The failure message names the file and line.
- If your clone is used at an employer, list the employer's names in `.company-terms` (see the
  README). The file is git-ignored, and `go test` then fails if a tracked file holds one.

## Send it

- Branch from `main` and keep the change to one thing.
- A commit subject says what changes, in plain English.
- A pull request says what changed and why, and how you checked it. The template asks for these.
- Nothing in the repository names an organization a checkout is used at.

## License

Conn is under the [Apache License 2.0](LICENSE). By sending a contribution you agree it is
licensed under the same terms (section 5 of the license). There is no separate agreement to sign.
