# Hosted sessions: verification on a Mac

What was run by hand against a real `conn serve` and a real `claude`, on 2026-11-07, at the
`hosted-sessions` branch (base 9997886). The app's window was not driven: what needs a person at
the Mac is listed at the end.

Setup:
- macOS on Apple silicon, Go 1.27.3, `claude` 2.1.319, signed in with a claude.ai subscription.
- `make build`, then `bin/conn serve` in the background, each run with its own temporary
  `CONN_DATA`, `CONN_PORT` and `CONN_THEME_INDEX=off`. `$S` below is a scratch folder.
- The secret was read from `conn.lock` by a helper and never printed.
- A small Bun script was the WebSocket client. It sends the page's two subprotocols
  (`conn.term.v1`, `conn.key.<secret>`) and an `Origin` header of our choice.

Two runs were needed. A dry run resumes nothing, by design: every owner's check refuses under
`CONN_*_DRY`, because a resumed session runs the real `claude`.
- **Run A** (dry). It covers the gate, the Open work stand-in session and Interrupt.
- **Run B** (not dry). Its allowlist is a scratch repository whose `origin` names
  `claytoncasey01/conn-verify-scratch`, and GitHub is off. One real `claude -p "Reply with the
  single word ok" --model haiku` in that folder made a conversation. `CONN_TRANSCRIPTS` held only
  its transcript, and History imported it. History's RESUME then started a real
  `claude --resume <id>` inside Conn. That session carries the lifecycle checks.

## The gate (run A)

```
no secret                                        401
wrong secret                                     401
right secret                                     200
right secret, Host evil.example                  403
right secret, Origin http://evil.example         403
right secret, Origin other port                  403
right secret, Origin localhost (Host 127.0.0.1)  403
right secret, own Origin                         200
secret in query string                           401
POST stop, secret, no x-conn                     403
events stream no secret                          401
/%61pi/snapshot (encoded /api), no secret        401
page shell, no secret                            200
terminal WS handshake, no secret                 401
```

The terminal's WebSocket handshake:

| Origin | Key | Result |
|---|---|---|
| none | right | 403 |
| `http://evil.example` | right | 403 |
| own | wrong | 401 |
| own | right | 101, protocol `conn.term.v1` (the key is not echoed) |

`conn serve` printed `conn: open http://127.0.0.1:7791/#k=<secret>, with the "secret" in
…/conn.lock`, since its stdout was not a terminal.

Seen in passing: an unknown `/api` path that passes the gate (`GET` or `POST /api/nonexistent`)
answers 200 with the page's HTML, not 404. `main` does the same. It is behind the gate, so it is
noted, not counted as a finding of this branch.

## Open work, dry (run A)

1. START `PROJ-1` and answer its two questions. The run is then `building`, and a hosted session
   `PROJ-1` (kind `build`, `hosted: true`, pid above 2^30) shows in the snapshot.
2. Over the WebSocket: resize 100x30, type `zq9verify hello`, Enter, Ctrl-C:

   ```
   [text] {"t":"replay-end"}
   Dry run: a stand-in for claude, "PROJ-1". Nothing runs here.
   Type something and press Enter.
   > zq9verify hello
   (dry run) Noted: nothing was sent.
   > ^C
   ```
3. `POST /api/sessions/<id>/reply` on the stand-in was refused: `could not see the session's input
   box on screen, so nothing was sent`. The stand-in draws no Claude input box.
4. Interrupt:

   ```
   POST /api/term/<id>/interrupt   {"ok":true}
   POST /api/term/<id>/interrupt   {"error":"an interrupt was just sent or is being sent: a second Ctrl-C so soon could make the program quit"}
   (3.2 s later)                   {"ok":true}
   ```
5. Reviews, dry: `POST /api/reviews/start` for `acme/widgets#4110` answered
   `{"ok":true,"name":"pr-4110-acme--widgets"}` and started nothing, as documented.
6. SIGTERM. Conn exited in about 1 s. `hosted.json` was kept (mode 0600, 5 fields:
   `id, owner, name, dir, claudeSessionId`), and the dry settings folder was removed. On restart
   it said `conn: resumed 0 hosted sessions; 1 not resumed` and wrote `{"version":1,"sessions":[]}`.

## A real claude inside Conn (run B)

- **Started.** History RESUME ran `claude --resume 7397f204-…` on Conn's terminal, in its own
  process group (pgid = its pid). Its only child was `caffeinate`, in the same group.
- **The trust dialog.** The folder was new to `claude`, so it opened on its folder-trust dialog.
  That was answered over the WebSocket with Down, then Enter. Claude then registered, and the row
  linked by pid (`sessionId` set).
- **Typing.** Shift-Tab over the WebSocket moved claude from the account's default auto mode to
  manual mode, so it would ask for permission.
- **A typed reply.** `POST /api/sessions/<id>/reply` with "Use the Bash tool to run exactly: echo
  zq9verify > marker.txt" answered `{"ok":true}`.
- **The permission prompt.** With an `/api/events` stream open (the ask is read only while a page
  watches), the snapshot showed:

  ```
  {"kind":"permission","id":"a53a9aff88299aae.9","title":"Bash command",
   "text":"echo zq9verify > marker.txt","options":[{"key":"1","label":"Yes","role":"allow"},
   {"key":"4","label":"No","role":"deny"}]}
  ```

  The answers:
  - a stale id: 409, `the prompt on screen is not the one that was shown, so nothing was sent`;
  - the shown id, choice 1: `{"ok":true}`, and `marker.txt` held `zq9verify`;
  - the same id again: 409.
- **The child's environment** (`ps -E`). No `CONN_*`, no `CMUX_*`, and the secret in neither the
  environment nor the arguments.

## Quit, crash and launch (run B)

| Step | Seen |
|---|---|
| SIGTERM to `conn serve` with the session running | Conn exited within a second. `claude` and its `caffeinate` were gone. `hosted.json` (0600) held one record: `{"id":"b6ca…","owner":"history","name":"Reply-with-the-single-word-ok","dir":"$S/repo","claudeSessionId":"7397f204-…"}`. |
| Start again | stderr: `conn: resumed 1 hosted sessions; 0 not resumed`. The snapshot had `resumed: {sessions:[{id, name:"repo-42"}]}`. The process was `claude --resume 7397f204-… --name repo-42`, and its screen over the WebSocket showed the earlier conversation, the `marker.txt` turn included. |
| `where` set to `terminal`, quit, start | `conn: 1 hosted sessions kept; they resume when sessions open inside Conn again`. Nothing was started and the record was unchanged. |
| `where` set back to `inside`, quit, start | `conn: resumed 1 hosted sessions; 0 not resumed`. |
| `resumeOnLaunch` set to false, quit, start | `conn: 1 hosted sessions dropped: resume on launch is off`. The record was `{"version":1,"sessions":[]}` and nothing ran. |
| RESUME, then `POST /api/sessions/<id>/stop {"force":true}` (what Stop all sends) | `{"ok":true,"name":"repo-42"}`. The group was gone, exit code 143, and the record was empty within a second. |
| A record with an extra `"command":"touch /tmp/zq9-pwned"` field | `no hosted session is resumed: … json: unknown field "command"`. The file was moved to `hosted.json.refused`, nothing ran, and `/tmp/zq9-pwned` did not exist. |
| A valid record at mode 0644 | `… its mode is -rw-r--r--, not -rw-------`. Nothing ran. |
| A hand-written record without the trailing newline | `… it is not written as Conn writes it`. Nothing ran. |
| SIGINT with a session running | Conn exited, the group was gone, and the record held the session. |
| `kill -9` of `conn serve` with a session running | `claude` was gone within 3 seconds, because the terminal hung up, and the record still held it. The next start said `resumed 1 hosted sessions` and the banner listed it. |

The name changed from `Reply-with-the-single-word-ok` to `repo-42` between the first RESUME and
the launch's resume. By then claude had named the session, and History's row holds that name, so
the launch passes `--name repo-42`, exactly as RESUME now would.

## Processes after a quit

- After every quit above, `ps` showed no `claude --resume` and no `conn serve` left.
- **A real background command.** Real claude was asked for a background command with Bash's
  `run_in_background`, `sleep 613`. It ran under a `zsh` in a process group of its own (pgid
  57666, not claude's 57520), so it had left the session's group. After SIGTERM to Conn it was
  gone too: claude ended it on the polite stop.
- **A command that calls `setsid`.** A stand-in `CONN_CLAUDE` (a shell script, not claude)
  started `sleep 611` in its group and `sleep 612` after `setsid`. After SIGTERM, `sleep 611` was
  gone and `sleep 612` was still running, reparented to launchd. This is the documented limit: a
  process that leaves the group is out of a stop's reach. It was killed by hand.

## Logs

- A unique marker, `zq9verify`, was typed through the WebSocket and in a reply. It was then
  searched for in every run's stdout and stderr and in every file under both data folders.
  It was found nowhere.
- The live secret was in none of the logs.
- What `conn serve` wrote in all, besides the two start lines: the `resumed / kept / dropped / no
  hosted session is resumed` lines quoted above.

## Linux

- `GOOS=linux` builds of `cmd/conn` (amd64 and arm64), `go vet ./internal/pty` and the pty test
  binary all compile.
- `docker run golang:1.27.3 go test -race -count=3 ./internal/pty/...` **failed**:
  `TestRealStopKillsTheWholeGroup` (both rows) failed with "the program did not end after it was
  killed", and `TestRealGroupOfALeaderThatExitsIsGone` with "did not end".
- The same command with `docker run --init` passed: `pty`, `pty/costtest` and `pty/leaktest`,
  three times each.

The difference is the container's PID 1. Without `--init` it is `go test`, which never reaps the
orphaned children that are reparented to it. Their zombies stay in the process group, so the
group never looks empty to the stop. See the report for the finding.

## Not verified here: needs the person at the Mac

- **The Wails window.**
  - Does WKWebView send an `Origin` on the terminal's WebSocket? The handshake refuses one
    without it. Open a session's Terminal panel in `Conn.app` and type into it.
  - Does a Cmd-click on a link in the terminal reach the default browser after its confirmation,
    with the window staying on Conn?
- **Window close.** Closing the window (the X, Cmd-W) keeps the sessions running, and reopening
  it from the Dock shows each terminal as it was.
- **Cmd-Q, then open the app again.** The sessions resume and the banner lists them. The Quit
  menu should do the same.
- **The banner and Stop all in each theme**, and the Terminal panel's colours in each theme
  (light and dark).
- **A real Open work build** (it needs Jira), **a real review or self-review** (it posts to a
  real PR on GitHub), and their resume at launch with the model and deny list. Only their checks
  are covered, by the Go tests.
- **Linux at runtime** outside a container: a real `claude` on Linux, `/dev/pts`, and the
  `TCGETS` and environ paths. Above, these were compiled and run only in Docker.
- **A clean-machine install.** The install script and Gatekeeper on a Mac that has never run
  Conn, then a hosted session from the installed app.
