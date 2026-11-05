# Update notice for the desktop app: design

Status: proposed. Written 2026-11-05 on `update-notice-spec`, stacked on `desktop-app`. Nothing in it
is built.

## Why

Conn.app has no way to learn that a newer release exists. A user finds out only by re-running the
install command, so most will stay on the version they first installed. This adds the smallest thing
that fixes that: the app notices a newer release and says so, once, in plain words, with the command
that updates it. It does not install anything.

Updating itself (the app downloading and replacing itself, as Slack and Discord do) is a later step
and is not part of this: see issue #15 on signing and Sparkle, and "Not in this" below.

## What the user sees

- The app menu (the one named Conn, beside the Apple menu) has an item, **Check for Updates…**,
  always. Choosing it checks at once and answers in a dialog: either "Conn is up to date (v1.2.3)" or
  the update dialog below. A failed check says so ("Could not reach GitHub").
- A check also runs by itself, a few seconds after launch and then once a day while the app stays
  open. When it finds a newer release, the menu item reads **Update Available: v1.2.4…** from then
  until the app quits, and a dialog is shown once for that version in this run of the app. A
  version the user has dismissed is not shown again until the next launch, and nothing is written to
  disk.
- The update dialog says: "Conn v1.2.4 is available (you have v1.2.3)." with the one-line install
  command in the message, and three buttons: **Copy Command**, **Release Notes** (opens the
  release's page in the default browser), **Later**. The message says to quit Conn first, because
  the installer refuses to replace a running app.
- Nothing else changes: no badge, no notification, no window of its own, no change to the page.

The command in the dialog is the one the README gives:
`curl -fsSL https://github.com/claytoncasey01/conn/releases/latest/download/install.sh | sh`.

## How it works

A package `internal/update` holds the rules and the one request. It has no Wails in it, so it is
tested on Linux too (the same split as `internal/desk`).

- **Finding the latest release.** One HEAD request to
  `https://github.com/claytoncasey01/conn/releases/latest`, the address `install.sh` uses, with
  redirects switched off. GitHub answers with a redirect whose `Location` ends in `/tag/<tag>`; the
  tag is read from that. No GitHub API, so no rate limit and no token, and it works the same as the
  installer (which only counts published releases that are not pre-releases).
- **Is it newer?** `Newer(current, latest string) bool`: both must be `vMAJOR.MINOR.PATCH` (an
  optional `-rc1`-style suffix and `+build` are ignored for the comparison; the numbers are
  compared as numbers, not text). A current version that is not of that shape (`dev`, as in the
  repository's `VERSION` file) never checks at all, and a latest that is not of that shape is
  ignored. An equal or older latest is never offered, so a downgrade is never suggested.
- **When.** `Run(ctx, deps)` takes its clock, request and reporter as plain function fields
  (`Check`, `After`, `Report`), like `desk.Deps`, and returns when the context ends. It waits
  `startDelay` (10 s), checks, then waits `checkEvery` (24 h) and checks again. A failed check
  is retried at the next interval, not sooner: there is no retry loop.
- **Limits, named as constants.** Request timeout `checkTimeout` (5 s); no body is read; the
  response is used only for its status and `Location` header, which is capped at `locationBytesMax`
  (512); at most one request per `checkEvery`, plus one each time the user chooses the menu item
  (and those are ignored while one is already running).
- **In the app.** `cmd/conn-app` starts `update.Run` next to `desk.Run`, adds the menu item
  (`menu_darwin.go`), and shows the dialog (`window_darwin.go`'s dialog helper). "Release Notes"
  goes through `LinkTarget`'s browser path, so only an `https` URL on `github.com` is opened. The
  tag is checked against `^v[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*$` before it is put in any URL or
  text, because it came from the network.
- **Turned off with `CONN_UPDATE_CHECK=off`**, in the same style as `CONN_THEME_INDEX=off`. The
  menu item then does not appear. It is read where the app reads its other `CONN_*` settings.

## What it touches

- **Network.** One unauthenticated HEAD request to `github.com` at launch and daily. It sends
  nothing of the user's, no version and no identifier; GitHub sees the address it came from and a
  plain client name (`conn-app`). The README's "What Conn touches" section and SECURITY.md gain a
  line saying so, and that `CONN_UPDATE_CHECK=off` turns it off.
- **Files.** None written.
- **Server and page.** Unchanged: no route, no `src/` change, so every guard in `internal/server`
  is untouched. This is the reason the notice is a native menu item and not a banner in the page.
- **Private repository.** While `claytoncasey01/conn` is private the redirect needs a login, so the
  check gets a login page, finds no tag and says it could not check. It works as soon as the
  repository is public, which is the plan.

## Decisions I made (the owner can overrule)

- Native menu item and dialog, not a banner in the page. A banner would also reach people who run
  `conn serve` in a browser, but it needs a server route and a page change, and widens what the
  guards see.
- A dialog once per version per run, not a persistent badge. There is no new state to keep.
- Copy the install command; do not run it. The installer needs the app quit, and the app cannot
  quit, replace itself and relaunch without the helper issue #15 is for.
- HEAD on the `/latest` redirect, not the API: no limit, no token, one source of truth with
  `install.sh`.
- A daily interval and a 10 s launch delay, so the check never competes with the splash and the
  server starting.

## Not in this

- Downloading or installing an update, relaunching, delta updates, release channels (a pre-release
  is never offered), release notes shown in the app, a "skip this version" setting, a version shown
  on the Settings page.
- Linux and the browser use. The command line has no check; `conn version` shows what is installed.

## Review focus

Inputs the spec implies and the tests must pin:

1. **A hostile or odd answer.** A redirect to another host, a `Location` that is not a release tag,
   an absurdly long header, a 200 with no redirect, a 404 or 5xx: each says "could not check", never
   offers an update, and never builds a URL from the text.
2. **Version comparison.** `v1.10.0` is newer than `v1.9.0`; `v1.2.3` is not newer than itself; an
   `-rc` latest of the same numbers is not newer than the release; `dev` never checks.
3. **No loop and no pile-up.** A slow or failing network gives one request per interval; choosing
   the menu item while a check runs does not start a second; cancelling the context stops
   `Run` mid-wait and mid-request.
4. **Quit.** Quitting the app while a check is in flight ends within the existing stop timeout.
5. **Off switch.** `CONN_UPDATE_CHECK=off` makes no request at all (a test server that fails on any
   request), and removes the menu item.

## What needs the owner's eyes (cannot be done headless)

- The menu item and both dialogs look right on a real Mac, including dark mode.
- **Copy Command** really puts the command on the clipboard; **Release Notes** opens the release.
- With the repository public and a newer release published, a launched older build offers it.
