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
request to `github.com` that only reads a redirect: it installs nothing, sends no version or
identifier, and trusts only an answer that is a release tag on the same host, and
`CONN_UPDATE_CHECK=off` turns it off.

These are in scope, because Conn promises them:

- **The local server.** A page on another site, or another user of the machine, making Conn read
  or change something. Every write needs the `x-conn: 1` header, and a request is refused unless
  its `Host` is a loopback name and its `Origin`, when it has one, is a page served here.
- **Session control.** Typing a reply or answering a permission prompt in a session you did not
  choose, or an answer sent after the prompt changed.
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
- Someone who already runs code as your user, or can already read your home folder.
- Exposing the port beyond `127.0.0.1` yourself, with a proxy or a tunnel.
- A theme of yours that you wrote, or a `theme.css` that you approved when asked.
- Weaknesses in `gh`, `git`, `claude`, cmux or GitHub, Slack and Atlassian themselves.

If you are not sure which side a problem falls on, report it privately anyway.
