# Security

## Reporting a problem

Report a security problem privately, not in a public issue or pull request:

1. Open the repository's **Security** tab.
2. Choose **Report a vulnerability** (GitHub's private vulnerability reporting).

Say what you found, the version (`conn version`), and the steps that show it. A report is read by
the maintainer, who will reply on the report, say whether it is a vulnerability, and keep you
informed until it is fixed. The fix is published with a release, and you are named in the notes
unless you would rather not be.

## Supported versions

Only the latest release is supported. A fix is made on `main` and released as a new version.

## What Conn is, for deciding what counts

Conn is a local program. It listens on `127.0.0.1` only, has no account or service of its own, and
runs as you. The [README](README.md#what-conn-touches) lists what it reads, writes and calls.

The macOS app, `Conn.app`, is the same server in a window. It opens no listener of its own, changes
no check, and keeps its data in the same folder, so everything below holds for it as for
`conn serve`. At start it runs your login shell once and reads the shell's environment into its own
process, so that `claude`, `gh` and `git` are found. Every program the server starts (`claude`,
`gh`, `git`) inherits that whole environment, tokens included, as it would in a terminal. Conn
itself never writes that environment to a file or a log, and when the shell fails it shows only a
short reason, never what the shell printed. The app's update check is one unauthenticated `HEAD`
request to `github.com` that only reads a redirect, and trusts only an answer that is a release tag
on the same host. Only when that release is newer does it make a second `HEAD` request, for the
release's `checksums-app.txt`, which reads only the status and follows nothing, to learn whether
the app is attached yet. It installs nothing, sends no version of Conn and nothing of yours, and
`CONN_UPDATE_CHECK=off` turns it off.

These are in scope, because Conn promises them:

- **The local server.** A page on another site or port, a rebound host name, or another user of
  the machine, making Conn read or change something. Every `/api` route, reads and the event
  stream included, is behind one gate: the `Host` must be a loopback name, the `Origin` must be
  absent or exactly the server's own (`http://` and the `Host`), and the request must carry this
  launch's secret in `Authorization: Bearer`. The secret is 256 bits from `crypto/rand`, made at
  each start, compared in constant time, and kept in memory and in the data folder's `conn.lock`
  (mode 0600), so another user who can reach the port cannot read it. It is never in a query
  string, a cookie, a log line, an error, a command line or a child's environment. The app hands
  it to its window in the address's fragment, which is never sent to a server; `conn serve`
  prints it only to a terminal. The page keeps it in `sessionStorage` and removes it from the
  address bar. A browser records the address with `#k=` in its history before the page removes
  it, keeps it in your browser profile's history (and may offer it again as you type an
  address), and may sync that history with a browser account; this is accepted because the secret
  lasts one launch of Conn and the port answers on `127.0.0.1` only. Every write also needs the
  `x-conn: 1` header. The page and its files need no secret: they hold nothing of yours. The
  page's Content-Security-Policy allows scripts only from Conn (no inline script), requests only
  to Conn, and no framing. A request that gets past the gate without the secret, or a way to read
  the secret without being your user, is in scope.
- **Session control.** Typing a reply or answering a permission prompt in a session you did not
  choose, or an answer sent after the prompt changed.
- **Sessions inside Conn.** Anything in [Sessions inside Conn](#sessions-inside-conn) that does
  not hold: a request that starts a session or names a command, an argument, a variable or a
  folder to run; a page or another user that attaches to a terminal or types into it; terminal
  output that writes the clipboard, types into the program, opens a link past the link policy
  or moves the window; an edited `hosted.json` that starts something Conn would not start
  itself; a keystroke or a screen's text written to disk or a log; the secret in a child's
  environment or arguments; a stop that leaves a process in the session's process group.
- **Paths and links.** Reading or writing outside the data folder, a clone, a worktree or a
  theme folder by way of `..`, a symlink or a crafted name.
- **The theme gallery.** Installing a file that does not match the hash and commit in the index,
  following a redirect, sending a token or cookie, or serving a stylesheet from a gallery theme.
- **The installer.** `install.sh` installing something that is not the release's checksummed
  archive or, for the app, its checksummed zip, or changing anything besides the binary and
  `Conn.app`.
- **Leaks.** Secrets or the text of your sessions written where other users can read them. Conn
  creates the data folder private to you (mode 0700), and that is what keeps the files in it from
  other users.

These are not, because they are how Conn works or need access an attacker would not have:

- What a Claude Code session started by Conn does with your own permissions. Review and
  self-review sessions are ordinary sessions running as you, and their prompts and deny lists
  narrow what they do without making anything impossible. The README says so.
- Someone who already runs code as your user, or can already read your home folder. Such a
  process can read the secret from `conn.lock` and change `hosted.json`, and could run `claude`
  itself anyway.
- The risks listed as accepted under [Sessions inside Conn](#sessions-inside-conn).
- Exposing the port beyond `127.0.0.1` yourself, with a proxy or a tunnel.
- A theme of yours that you wrote, or a `theme.css` that you approved when asked.
- Weaknesses in `gh`, `git`, `claude`, cmux or GitHub, Slack and Atlassian themselves.

If you are not sure which side a problem falls on, report it privately anyway.

## Sessions inside Conn

By default Conn runs the Claude Code sessions it starts (an Open work build, a review, a
self-review, a History resume, a new session from the Sessions page) on a terminal of its own, and the page shows that terminal. The
terminal is code execution as you: whatever is typed into it, the program runs. So these are the
rules, and a way around any of them is in scope.

**What a client can do.** Attach to a session by the id the server issued, type, resize, and
detach. Nothing else. No route, message or setting names a command, an argument, a variable or a
folder: sessions are started by Conn's own code, from records it has already checked (New
session, below, names only an allowed repository, and Conn resolves its folder). One more
input needs no attached terminal: **Interrupt** (`POST /api/term/<id>/interrupt`, no body) types
the one byte Ctrl-C types (ETX). It is input, never a signal. A second press within 3 seconds is
refused, because two Ctrl-C close together make `claude` quit.

**New session.** `POST /api/sessions/new` starts a plain `claude` session. Its body is a JSON
object with a repository name (`owner/name`) and, optionally, a session name; any other field, a
value that is not text, or a body over 64 KiB is refused. The page never sends a folder, a
program, an argument or a variable, and the server reads none: the repository is only a key.
- The repository must be on the allowlist now and have a local clone, found as Open work finds
  one. The clone's path is resolved first (a clone set in Settings may be a link), and the
  session runs in that real path, which must be a safe folder (below). Its `.git` must be a
  safe folder too: not a link, and not a file pointing at a git folder elsewhere (a worktree or a
  `--separate-git-dir` clone), since either would let the folder borrow another repository's
  origin, and so its place on the allowlist.
- **A safe folder.** The folder must be yours, a real folder (not a link) that only you may
  write, and every folder above it up to `/` a real folder of yours or root's that no one else may
  write, except a sticky one such as `/tmp`, where no one may rename or remove what is not theirs.
  Then no one but you and root can change what the path names, so starting the program in it by
  its path (a chdir) reaches the folder that was checked. The check runs when the repository is
  chosen and again right before the start. A folder that fails says which part and what to do:
  use the folder a link points to, or `chmod go-w` a folder others may write.
- The name passes the same checks as every session's name (no control, bidi or invisible
  character, valid UTF-8, at most 256 bytes) and is then reduced to letters, digits, `.`, `_` and
  `-`, so it cannot start with `-` and read as a flag. A name with nothing left is refused.
- Conn chooses the conversation id itself (a random UUID from `crypto/rand`), so the command is
  always exactly `claude --session-id <uuid>`, plus `--name <name>` when a name was given, run as
  a list of arguments with no shell. It opens where Settings say, like every other session.
- The route needs the gate below, `x-conn: 1` and the page's own `Origin`: unlike the other
  writes, a request with no `Origin` is refused, since a start runs a program. A second start in
  the same repository within 10 seconds is refused, and so is a session inside Conn while 12 run
  or are starting. A dry run starts nothing.
- Inside Conn the session is recorded as a History session on the id Conn chose. At the next
  launch it comes back only through History's check below, so a session that never wrote a
  transcript is not resumed.

Why the page cannot choose a folder: a folder from the request would let any caller that gets
past the gate run `claude` (and whatever a repository's own Claude settings run) anywhere on the
disk. Tying the folder to the allowlist and to a clone Conn resolves itself keeps a start to the
same places Open work, Reviews and History already run sessions.

**The gate.** Every terminal route is under `/api`, so it needs the same Host, Origin and secret
as the rest (see the local server, above), and a write needs `x-conn: 1`. A browser cannot put a
header on a WebSocket, so the terminal's WebSocket carries the secret as one of its subprotocols
(`conn.key.<secret>`, never echoed back), and its handshake must also carry exactly the server's
own `Origin`: a handshake with no Origin is refused.

**Limits.** Each one is a named constant in the code.

| What | Limit |
|---|---|
| Sessions running at once | 12 |
| Output kept for a late viewer (memory only) | 4 MiB per session |
| Viewers of one session | 4 |
| One input frame | 64 KiB of input; a larger paste is refused whole |
| Input rate per session | 1 MiB a second, 250 frames a second, 10 resizes a second |
| Terminal connections over all sessions | 64 |
| Handshake | 5 seconds |
| Pings | every 15 seconds; no answer in 20 and the viewer is dropped |
| A stop | SIGTERM to the process group, SIGKILL 5 seconds later |

A viewer that falls behind is dropped; the program's output is never held up for it. Input the
program does not take is dropped, and the terminal stays open.

**The program.** It runs in a new session and process group, with only its terminal as
descriptors: each spawn first marks every other descriptor of Conn's process close-on-exec, those
the app's WebKit opens included. It is the program `CONN_CLAUDE` names, with your environment less
Conn's own variables (`CONN_*`, the secret among them), cmux's (`CMUX_*`, so a hosted session
never looks like a cmux pane), your terminal's and multiplexer's, and those of a Claude Code
session Conn may have been started from. A stop signals the whole group.

**Output is untrusted.** The page's terminal is xterm.js, bundled with Conn, with no CDN.
- It answers no query. The server answers device attributes and the background colour from a
  fixed table, never with text from the program's output. It does not answer a query for the
  cursor position: two numbers the program chooses could spell a key.
- It never writes the clipboard (OSC 52), sends no window reports, and never shows the title.
- A focus report is never typed, and a mouse report only while the terminal has the focus.
- A link opens only on Cmd-click or Ctrl-click, only for `http` or `https`, and only after you
  confirm a dialog that names its host. It opens in your default browser, never in the app's
  window.
- A paste is cleaned of control characters and paste markers. A paste with line breaks that the
  program would read as Enters waits for you to confirm it.

**Replies and answers.** A reply from the page is typed only into an input box the screen shows
empty. It is refused when it starts with `!` or `/`, or holds a control character other than a
line break. An answer is
one digit key. Before it is typed, the server reads the screen again from its own model of the
terminal and checks that the prompt is still the one the page showed. When the key's turn to be
written comes, it checks again. The check and the typing see the same process.

**Nothing at rest.** No keystroke and no screen text is logged, or written to disk by Conn, error
paths included. History's `history.db` still holds the session's transcript, as for any session.

**The record, `hosted.json`.** Conn keeps the list of the sessions it runs in
`$CONN_DATA/hosted.json`, so that it can resume them at the next launch. For each session it holds
the host's id, the owner (`openwork`, `reviews`, `myprs` or `history`), the session's name, its
folder and Claude's session id. It holds no command line, argument or variable. It is written to a
temporary file and renamed, mode 0600. It is read only when:
- it is a regular file, not a link, owned by you, of mode exactly 0600;
- it is at most about 373 KiB and holds at most 12 sessions;
- it has no field Conn does not write, and its bytes are exactly what Conn would write.

Otherwise none of it is used, the file is moved to `hosted.json.refused` (a newer refusal replaces
an older one), and nothing is started. Each session in a file that passes is then checked again by
its owner, which rebuilds the command from its own state, as its own button would:
- **Open work.** Not a dry run, and Open work on. The run exists and is building or has its PR
  open. The folder is one of the run's session worktrees, launched and not cleaned up. The
  worktree exists, its repository is still allowed and cloned, and no session runs there. Claude's
  id is a UUID.
- **Reviews and My PRs.** GitHub on, not a dry run. Claude's id is a UUID. The name has the
  kind's shape. The worktree is exactly a folder under `$CONN_DATA/reviews` or `$CONN_DATA/myprs`,
  each folder on the way yours and not a link, with a `.git`, in an allowed repository.
  `reviews.json` or `myprs.json` holds an open launch with that worktree and name, and the session
  resumes on that launch's model. No session of that name, and none in that worktree, already runs.
- **History** (a session resumed from History, or started with New session). Not a dry run.
  Claude's id is a UUID. The archive has the session, it is not running, its transcript and
  folder exist, and it was not just resumed. The folder is the one the archive holds, it is a
  safe folder (see New session, above), and its repository is allowed. History's RESUME button
  makes the same folder check, so a session in a folder reached through a link, or under a folder
  others may write, is no longer resumed, by the button or at launch; the refusal says which
  part to fix.

**Quit, close and launch.**
- Closing the app's window hides it. Every session keeps running, and opening the window again
  shows each one as it was.
- Quitting (Cmd-Q, the Quit menu, SIGINT or SIGTERM to `conn serve`) writes the record, then
  stops every session. A session you were stopping at that moment is not recorded. A second
  SIGINT or SIGTERM to `conn serve` during the quit ends it at once, once the record is written;
  the sessions still running end as their terminals hang up.
- A crash or `kill -9` ends the sessions too: the system hangs up each terminal. The record is
  kept up to date every second, so the next launch resumes them the same way.
- At launch Conn waits for the first read of the session list (at most 15 seconds), then
  resumes what the record names, inside Conn, each as its owner's check rebuilds it. The page
  says how many it resumed and which, and offers **Stop all**. A quit during the resume waits for
  it (at most 10 seconds) and keeps the records it did not reach.
- After every start of Conn, even one that resumes nothing, Reviews closes no review and My PRs
  counts no round as over until the resume is done and for 5 minutes after, since a session may
  still be coming back. So a review that really ended may close up to 5 minutes late, and REVIEW
  is refused meanwhile for a PR that has an open review launch.
- With *Resume on launch* off (Settings), the record is dropped at launch and nothing is
  resumed. With sessions set to open in your terminal or as a command, nothing is resumed and the
  record is kept for when they open inside Conn again, except reviews: Reviews closes a review
  whose session has not come back, and a closed review is never resumed. `conn serve` says on
  stderr what it resumed, kept or dropped.
- An app that attached to a Conn already running does not own its sessions. Quitting that app
  stops nothing.

**Accepted risks.** These are known and are not treated as vulnerabilities.
- **Forged prompts.** Claude's output can draw something that looks like a permission prompt,
  and the page's ask panel is read from the screen. The panel never answers by itself: an answer
  is always your click, and it reads the bottom of the live screen, not the scrollback.
- **Resuming with no one present.** A launch restarts sessions with no one at the keyboard, in
  repositories whose files may have changed since. The owners' checks above limit what comes
  back, the page lists it with Stop all, and *Resume on launch* turns it off.
- **Processes running as you.** They can read the secret and edit `hosted.json`. An edit can at
  most resume a session the owners' checks accept, which Conn would start itself.
- **Files read by path.** Conn checks `.conn` is a folder of yours before it writes the brief or
  the prompt, but the session reads `.conn/brief.md` or `.conn/prompt.md` by path afterwards. A
  change made between Conn's write and Claude's read is read as it is.
- **Processes that leave the group.** A program that calls `setsid` or `setpgid` leaves the
  session's process group, and a stop does not reach it. Claude Code runs a background command in
  a group of its own and ends it when it is stopped politely; if it is killed instead, such a
  command may outlive it.
- **Zombies nobody reaps.** A session ends only once nothing is left in its process group. A
  child that outlives the program is reparented to PID 1, which normally reaps it at once. Under
  a PID 1 that never reaps (a container started without `--init`), it stays in the group as a
  zombie for good. That session then never ends and keeps one of the 12 places until Conn
  restarts. Nothing is signalled wrongly. On Linux, run Conn under an init that reaps orphans:
  `docker run --init`, a systemd service, or a normal login session.
- **Two identical prompts.** Two prompts that look exactly alike, queued with no change of status
  between them, get the same id, so a click meant for the first can answer the second. What is
  approved is exactly what was shown, once.
