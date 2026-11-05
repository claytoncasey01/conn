# Update notice for the desktop app: design

Status: built on `update-notice-spec`, stacked on `desktop-app`, and not yet seen on a real Mac.
Written 2026-11-05 as a proposal and corrected the same day to match what was built: where the two
differed, this text now says what the code does, and "What was built differently" lists the changes.

## Why

Conn.app has no way to learn that a newer release exists. A user finds out only by re-running the
install command, so most will stay on the version they first installed. This adds the smallest thing
that fixes that: the app notices a newer release and says so, once, in plain words, with the command
that updates it. It does not install anything.

Updating itself (the app downloading and replacing itself, as Slack and Discord do) is a later step
and is not part of this: see issue #15 on signing and Sparkle, and "Not in this" below.

## What the user sees

- The app menu (the one named Conn, beside the Apple menu) has an item, **Check for Updates…**,
  directly under About, unless the check is off (below). Choosing it checks at once and answers in a
  dialog: "Conn is up to date (v1.2.3)", the update dialog below, or, for a failed check, "Could
  not check for updates" with a fixed reason ("Could not reach GitHub."). A build with no release
  version (`dev`) answers that it is a development build and does not check. A newer release whose
  app is not attached yet (below) answers "A new release is being published" and "Try again in a
  few minutes.", and offers nothing.
- A check also runs by itself, about 10 seconds after the app has started and then once a day
  while the app stays open. When it finds a newer release, the menu item reads **Update
  Available: v1.2.4…** from then until the app quits, and a dialog is shown once for that version
  in this run of the app. A version the user has dismissed is not shown again until the next
  launch (choosing the menu item does show it again, because the person asked), and nothing is
  written to disk. Only the highest version told is remembered: a release withdrawn after a newer
  one was told, which leaves a lower one as the latest, is never told again.
- The update dialog is titled "Conn v1.2.4 is available". Its message says "Conn v1.2.4 is
  available (you have v1.2.3).", that Conn must be quit first because the installer refuses to
  replace a running app, and the one-line install command. It has three buttons: **Copy Command**
  (the default), **Release Notes** (opens the release's page in the default browser), **Later**.
- Nothing else changes: no badge, no notification, no window of its own, no change to the page.

The command in the dialog is the one the README gives:
`curl -fsSL https://github.com/claytoncasey01/conn/releases/latest/download/install.sh | sh`.

## How it works

A package `internal/update` holds the rules and the requests. It has no Wails in it, so it is
tested on Linux too (the same split as `internal/desk`).

- **Finding the latest release.** One HEAD request to
  `https://github.com/claytoncasey01/conn/releases/latest`, the address `install.sh` uses, with
  redirects switched off. GitHub answers with a redirect whose `Location` ends in `/tag/<tag>`; the
  tag is read from that. No GitHub API, so no rate limit and no token, and it works the same as the
  installer (which only counts published releases that are not pre-releases).
- **Is it newer?** `newer(current, latest string) bool`: both must be `vMAJOR.MINOR.PATCH` (an
  optional `-rc1`-style suffix and `+build` are allowed; the numbers, of at most 9 digits, are
  compared as numbers, not text, and a suffix is compared only by being there or not). A current
  version that is not of that shape (`dev`, as in the repository's `VERSION` file) never checks at
  all, and a latest that is not of that shape is ignored. An equal or older latest is never
  offered, so a downgrade is never suggested.
- **Is its app attached yet?** GitHub names a release as the latest the moment it is published,
  but the release workflow's `app` job attaches `Conn_darwin_universal.zip` and
  `checksums-app.txt` up to about half an hour later, or never when it fails. In that window the
  install command updates only the command line and says "this release has no app", so offering
  it would be wrong. So when the latest tag is newer than this build, and only then, the check
  makes one more HEAD request, to `<base>/download/<tag>/checksums-app.txt` (`AppAttached`), the
  file `install.sh` asks for too. It uses the same client, limits and discipline: redirects off,
  the address built only from the base and the validated tag, and only the status read. A
  redirect (GitHub sends a present file to its storage host) or a 200 (a test server serving the
  file itself) means attached; a 404 means not yet, and the result is `Publishing`; any other
  status is a failed check with a fixed reason.
- **When.** `Run(ctx, deps)` takes its clock, requests, asks and reporter as plain fields (`Fetch`,
  `AppAttached`, `After`, `Asked`, `Report`, with `Current`, the build's version), like `desk.Deps`,
  and returns when the context ends. It waits `startDelay` (10 s), checks, then waits `checkEvery`
  (24 h) and checks again. A failed check is retried at the next interval, not sooner: there is no
  retry loop. One goroutine does all of it, so there is never a second request in flight.
- **Limits, named as constants.** Timeout for each request `checkTimeout` (5 s), with shorter limits
  on connecting, the TLS handshake and the wait for headers; no body is read; the response is used
  only for its status and `Location` header. Only a redirect status is read, with exactly one
  `Location`, capped at `locationBytesMax` (512), on the same scheme and host as the request, with
  no user, query or fragment, and a path that is the releases path plus `/tag/` plus a tag of at
  most `tagBytesMax` (64) in the version shape. The second request reads only the status. Each check
  makes one request, or two when it finds a newer release, at most once per `checkEvery`, plus one
  check each time the user chooses the menu item; asks made while a check runs become at most one
  more check (the ask channel holds one).
- **In the app.** `cmd/conn-app` starts `update.Run` (`update_darwin.go`) after the login shell's
  environment is loaded and before `desk.Run`: the check reads the proxy variables once, at its
  first request, and `CONN_UPDATE_CHECK` may come only from the shell. The app menu is built by
  hand from roles (`menu_darwin.go`), because Wails cannot add an item inside its stock app menu.
  **Check for Updates…** sits directly under About, with no separator, because a separator cannot
  be hidden with the item. The result is told on one goroutine, in order, so `Run` never waits on
  the screen. "Release Notes" opens `ReleasesURL` plus `/tag/<tag>` through `desk.LinkTarget`.
  `LinkTarget` accepts any `http` or `https` address with a host; what keeps the page on GitHub is
  that the URL is built only from that constant and a validated tag, never from the address the
  check asked. The tag must match the version shape (and be at most 64 bytes) before it is put in
  any URL or text, because it came from the network. Failure reasons are fixed sentences chosen by
  error kind, so no text from the server reaches the screen or stderr.
- **Turned off with `CONN_UPDATE_CHECK=off`** (also `false`, `0`, `no`, `none`, `disabled`, in any
  case). The check then makes no request and the menu item is hidden, not disabled. It is only
  known after the login shell's environment loads, so the item starts visible and is hidden then.
- **`CONN_UPDATE_BASE`** replaces the releases address that is asked (default `ReleasesURL`), for
  trying a build against a fake release server. It is for developers, so it is in
  `docs/releasing.md` and not in the README's table. Release Notes still opens the real page.

## What it touches

- **Network.** One unauthenticated HEAD request to `github.com` about 10 seconds after the app has
  started, and daily, and a second only when the first names a newer release, to learn whether its
  app is attached yet. They send no version of Conn and nothing of the user's; GitHub sees the
  address it came from and a plain client name (`conn-app`). The README's "What Conn touches"
  section and SECURITY.md say so, and that `CONN_UPDATE_CHECK=off` turns it off.
- **Files.** None written.
- **Server and page.** Unchanged: no route, no `src/` change, so every guard in `internal/server`
  is untouched. This is the reason the notice is a native menu item and not a banner in the page.
- **Private repository.** While `claytoncasey01/conn` is private an unauthenticated request gets
  a 404, so the check finds no tag and says it could not check. It works as soon as the repository
  is public, which is the plan.

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
- The app menu is built by hand, because Wails cannot add an item inside its stock app menu. It
  holds the same roles in the same order (About, Services, Hide, Hide Others, Show All, Quit), so
  the keys and Services should behave as before; only a run on a Mac shows it.
- The item is hidden when the check is off, not disabled, and sits directly under About with no
  separator, because a separator cannot be hidden and would be left doubled.
- The check starts after the login shell's environment is loaded, so it uses the person's proxy
  and `CONN_UPDATE_CHECK` can be set in the shell. The cost is that the item shows for a moment
  before it is hidden; an ask in that moment waits and is answered once the check starts, or is
  dropped if the check turns out to be off.
- A dev build starts `Run` too but never requests; an ask gets the development-build answer.
- Failure reasons say "GitHub" even when `CONN_UPDATE_BASE` points elsewhere; only a bad
  `CONN_UPDATE_BASE` is named.
- Each found release is also noted on stderr, and each failure, at most once a day plus asks.
- A newer release whose app is not attached yet is its own `Kind`, `Publishing`, not a field on
  `Result` and not `UpToDate`: the dialog it needs on an ask differs from both "up to date" and
  "available", and a `Kind` keeps `Result` three plain fields that `Tell` switches on. It is
  neither news nor a failure: no offer, no menu label, no warning, and the highest tag told is not
  advanced by it, so the release is offered once its app is attached. It is noted on stderr.
- The app question is a second `Deps` function (`AppAttached`), called by `Run` only for an
  `Available` result, and `withApp` turns its answer into the result; `Latest` stays one request.
- A 200 for `checksums-app.txt` counts as attached, as a redirect does, because a test server
  serving the file itself answers 200, and `install.sh` (which follows redirects) reads a final
  200 as present. The brief this was built from said any status other than a redirect or a 404 is
  a failure; this one exception is the owner's to overrule.

## What was built differently

Where the proposal guessed, the build found otherwise, and this text above was corrected:

- Release Notes: `LinkTarget` does not limit the host; the URL's construction does.
- The dependency field is `Fetch`, not `Check`, and `Run` also takes `Current` and `Asked`.
- The app menu is hand-built, the item sits under About with no separator, and is hidden when off.
- `Run` starts after the environment loads, not beside `desk.Run` at once.
- `CONN_UPDATE_BASE` was added.
- Asks made during a check become at most one more, not none; the offer is shown again on an ask.
- A withdrawn lower release is never told again (the highest tag told is kept).
- The tag pattern is the version shape with at most 9 digits a number, and at most 64 bytes.
- The dialog helper is in `update_darwin.go`, not `window_darwin.go`.
- A `CONN_UPDATE_BASE` that is not an `http` or `https` address with a host is a bad base, and a
  latest tag over 64 bytes is a failed check, not a panic in the text.
- A second request asks whether the newer release's app is attached, and a release without it is
  `Publishing`, not offered: the proposal offered a release as soon as `/latest` named it, which
  could send the person to a command that cannot update the app yet.

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
   the menu item while a check runs does not start a second at once (at most one more follows);
   cancelling the context stops `Run` mid-wait and mid-request.
4. **Quit.** Quitting the app while a check is in flight does not wait for the check: the quit
   ends the context, which stops the check on its own goroutine (`runDesk`).
5. **Off switch.** `CONN_UPDATE_CHECK=off` makes no request at all (a test server that fails on any
   request), and hides the menu item.

## What needs the owner's eyes (cannot be done headless)

- The menu item and both dialogs look right on a real Mac, including dark mode, and the item sits
  under About. Cmd-Q, Cmd-H, Cmd-Opt-H and Services still work in the hand-built menu, and the
  item is gone with `CONN_UPDATE_CHECK=off`.
- The proxy over HTTPS (a tunnel to the real `github.com`) was not exercised; a plain proxy was.
- **Copy Command** really puts the command on the clipboard; **Release Notes** opens the release.
- With the repository public and a newer release published, a launched older build offers it.
