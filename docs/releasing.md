# Releasing

A release is made by publishing one on GitHub. Nothing is built or published from a branch, a push or a pull request.

1. Make sure `main` is what you want to ship and `make check` and `make test` pass.
2. On GitHub, create a release: **Releases > Draft a new release**, choose **Create new tag**, and name it `v1.2.3` (a `v`, three numbers, and optionally a suffix such as `-rc.1`). Target `main`. Write the notes, then **Publish release**.
3. The `release` workflow (`.github/workflows/release.yml`) starts on the publish. It has two jobs. The `build` job runs on Linux: it checks out the tag, builds the page bundle, runs `make test`, writes the tag into `VERSION`, builds `conn` for macOS and Linux on amd64 and arm64 (`scripts/build-release.sh`), and attaches to the release:
   - `conn_darwin_amd64.tar.gz`, `conn_darwin_arm64.tar.gz`, `conn_linux_amd64.tar.gz`, `conn_linux_arm64.tar.gz`, each holding one file, `conn`;
   - `checksums.txt`, the SHA-256 of each archive;
   - `install.sh`, as it is at the tag.

   The `app` job runs on macOS once `build` has passed. It checks out the tag, builds the page bundle, writes the tag into `VERSION`, builds the app (`scripts/build-app.sh`), and attaches to the release:
   - `Conn_darwin_universal.zip`, holding `Conn.app`, one bundle with an Apple silicon and an Intel binary, signed ad hoc;
   - `checksums-app.txt`, the SHA-256 of the zip.

   The CLI archives and `checksums.txt` come from the `build` job, and the zip and `checksums-app.txt` from the `app` job. Each job uploads only its own files, so a re-run of one does not touch the other's.
4. Watch the run under **Actions**. If `build` fails, the release page has no files and the install URL finds nothing: fix the cause, then re-run the workflow (it uploads with `--clobber`, so a re-run replaces files). If only `app` fails, the release has the CLI and no app: `install.sh` then installs the command-line tool only and says "this release has no app", and re-running the `app` job adds the app. That message from `install.sh` means the `app` job failed or has not finished yet; a failed `app` job also says so in its run's summary. Before it uploads, the `app` job runs the same checks as CI's `app bundle` job: the signature, both architectures, and `--print-env`. A tag that is wrong is deleted with its release and made again.

`https://github.com/claytoncasey01/conn/releases/latest/download/install.sh` always names the newest release that is not marked a pre-release. A pre-release is installed with `CONN_INSTALL_VERSION=v1.2.3-rc.1`.

## Trying it without publishing

```sh
bun run build
printf 'v0.0.1\n' > VERSION            # put it back to "dev" afterwards
scripts/build-release.sh /tmp/conn-release
```

To try the app's update notice without a published release, build the app with a release version in `VERSION` (a `dev` build never checks), serve an address where `/x/releases/latest` redirects to `/x/releases/tag/v9.9.9`, and start the app with `CONN_UPDATE_BASE=<address>` set to the part before `/latest`, such as `http://127.0.0.1:8000/x/releases`. `CONN_UPDATE_BASE` replaces the address the check asks and is for this only; **Release Notes** still opens the real release page. The check starts about 10 seconds after the app has started, or at once from **Check for Updates…**.

`internal/installscript` runs `install.sh` against a fake release server in the tests, so a change to the script is checked by `make test`.

## Before the first release with the app

These steps need a Mac and a person, so the build cannot do them. Do them once, before the first release that carries the app.

1. Push the branch and watch CI. Both legs of `lint and type check`, the `app bundle` job and the `test` legs must pass.
2. On a clean Apple-silicon Mac, run `install.sh` and open `~/Applications/Conn.app`. It must open without a Gatekeeper warning. `install.sh` needs a release that carries the app: publish a pre-release (`v1.2.3-rc.1`) and install it with `CONN_INSTALL_VERSION=v1.2.3-rc.1`, or serve the files `scripts/build-release.sh` and `scripts/build-app.sh` write under `<address>/download/<tag>/` and run `install.sh` with `CONN_INSTALL_BASE=<address>` and `CONN_INSTALL_VERSION=<tag>`.
3. Download the zip in a browser (from that pre-release), unzip it and open the app. Note the warning. It is cleared by `xattr -dr com.apple.quarantine Conn.app`.
4. Look at the window by eye: the splash, the error dialog, and the real UI with a click-through (change a setting and save it in the app). Click a PR link (My PRs or Reviews) and see it open in the default browser while the window stays on Conn; click a link to a page of Conn and see it stay in the window. Click the Dock icon after closing the window. Press Cmd-Q, Cmd-H, Cmd-C and Cmd-V on the keyboard.
5. Quit with `kill -TERM` while the error dialog is open. Start the app on a port that another program holds, with an isolated `CONN_DATA`, so the real data folder is not touched.
6. Kill the server under an attached window and watch the app recover.
7. Run the x86_64 slice (under Rosetta, or on an Intel Mac) and the bundle on macOS 13.
8. The update notice, with the repository public and a newer release published. Check each of these:
   - An older build: put a version below the published one in `VERSION` (such as `v0.0.1`), run `bun run build` and `scripts/build-app.sh <out-folder>`, restore `VERSION` to `dev`, unzip `Conn_darwin_universal.zip` and open the app. About 10 seconds after it has started, the menu item reads **Update Available: <version>…** and a dialog appears. Check that it appears only once per run of the app.
   - In the dialog, check that **Copy Command** puts the install command on the clipboard, **Release Notes** opens the release in the browser, and **Later** closes it.
   - Check that **Check for Updates…** on the newest release says Conn is up to date.
   - Check that with `CONN_UPDATE_CHECK=off` the item is gone. An app opened from the Dock or Finder does not inherit the terminal's environment: it reads the environment of your login shell, so set the variable in the shell's profile (for zsh, `~/.zprofile`), quit the app, open it again, and remove the line afterwards. The item is hidden once that environment has loaded, so look about 10 seconds after the app has started.
   - Check that Cmd-Q, Cmd-H, Cmd-Opt-H and the Services menu work, because the app menu is built by hand.
9. Set the branch protection's required checks to the new names: `lint and type check (ubuntu-latest)`, `lint and type check (macos-latest)` and `app bundle (macos-latest)`.

## macOS

The binaries are not signed with a Developer ID or notarized. A file fetched by `curl` does not carry the quarantine flag that makes Gatekeeper check it, and Go signs the arm64 binary ad hoc, which is all Apple Silicon asks for, so `install.sh` installs a `conn` that runs. A binary downloaded through a browser from the release page does carry the flag, and macOS will refuse it until it is cleared (`xattr -d com.apple.quarantine conn`). Signing and notarizing is the fix for that, if browser downloads ever matter; it needs an Apple Developer account.

`Conn.app` is signed ad hoc and not notarized, for the same reason. `install.sh` installs it without a warning because `curl` sets no quarantine flag. The zip downloaded through a browser does carry it, and `xattr -dr com.apple.quarantine Conn.app` clears it. Once an Apple Developer account exists, Developer ID signing and notarization is added to the `app` job: `codesign` with the identity, then `notarytool`. It is a step in that job, not a redesign.
