# Releasing

A release is made by publishing one on GitHub. Nothing is built or published from a branch, a push or a pull request.

1. Make sure `main` is what you want to ship and `make check` and `make test` pass.
2. On GitHub, create a release: **Releases > Draft a new release**, choose **Create new tag**, and name it `v1.2.3` (a `v`, three numbers, and optionally a suffix such as `-rc.1`). Target `main`. Write the notes, then **Publish release**.
3. The `release` workflow (`.github/workflows/release.yml`) starts on the publish. It checks out the tag, builds the page bundle, runs `make test`, writes the tag into `VERSION`, builds `conn` for macOS and Linux on amd64 and arm64 (`scripts/build-release.sh`), and attaches to the release:
   - `conn_darwin_amd64.tar.gz`, `conn_darwin_arm64.tar.gz`, `conn_linux_amd64.tar.gz`, `conn_linux_arm64.tar.gz`, each holding one file, `conn`;
   - `checksums.txt`, the SHA-256 of each archive;
   - `install.sh`, as it is at the tag.
4. Watch the run under **Actions**. If it fails, the release page has no files and the install URL finds nothing: fix the cause, then re-run the workflow (it uploads with `--clobber`, so a re-run replaces files). A tag that is wrong is deleted with its release and made again.

`https://github.com/claytoncasey01/conn/releases/latest/download/install.sh` always names the newest release that is not marked a pre-release. A pre-release is installed with `CONN_INSTALL_VERSION=v1.2.3-rc.1`.

## Trying it without publishing

```sh
bun run build
printf 'v0.0.1\n' > VERSION            # put it back to "dev" afterwards
scripts/build-release.sh /tmp/conn-release
```

`internal/installscript` runs `install.sh` against a fake release server in the tests, so a change to the script is checked by `make test`.

## macOS

The binaries are not signed with a Developer ID or notarized. A file fetched by `curl` does not carry the quarantine flag that makes Gatekeeper check it, and Go signs the arm64 binary ad hoc, which is all Apple Silicon asks for, so `install.sh` installs a `conn` that runs. A binary downloaded through a browser from the release page does carry the flag, and macOS will refuse it until it is cleared (`xattr -d com.apple.quarantine conn`). Signing and notarizing is the fix for that, if browser downloads ever matter; it needs an Apple Developer account.
