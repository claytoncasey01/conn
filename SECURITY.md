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
  archive, or changing anything besides the binary.
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
