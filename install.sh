#!/bin/sh
# Install conn: download the release for this machine, check it against the release's checksums,
# and put the binary in a folder. On macOS it also installs the Conn app (Conn.app), checked the
# same way, unless the release has no app. It does nothing else: no sudo, no settings, no skills
# (those are installed from conn's Settings page).
#
#   curl -fsSL https://github.com/claytoncasey01/conn/releases/latest/download/install.sh | sh
#
# Option, passed after sh -s --, as in "... | sh -s -- --cli-only":
#   --cli-only            install the command-line tool only, not the app
#
# Environment:
#   CONN_INSTALL_VERSION  the release tag to install, such as v1.2.3 (default: the latest)
#   CONN_INSTALL_DIR      where the binary goes (default: $HOME/.local/bin)
#   CONN_INSTALL_APP_DIR  where Conn.app goes, on macOS (default: $HOME/Applications)
#   CONN_INSTALL_BASE     the releases address (default: https://github.com/claytoncasey01/conn/releases)
#
# Everything is inside main, which runs on the last line: a download cut short never runs half a
# script.
set -eu

fail() {
	printf 'install: %s\n' "$1" >&2
	exit 1
}

# platform prints "<os> <arch>" as the release files name them.
platform() {
	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) fail "$(uname -s) is not supported: conn is built for macOS and Linux" ;;
	esac

	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "$(uname -m) is not supported: conn is built for amd64 and arm64" ;;
	esac

	printf '%s %s\n' "$os" "$arch"
}

# latest_tag prints the tag the latest release has: the address /latest redirects to ends in it.
latest_tag() {
	final=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$1/latest") ||
		fail "could not find the latest release at $1/latest"
	tag=${final##*/}

	case $tag in
	v[0-9]*) printf '%s\n' "$tag" ;;
	*) fail "$1/latest did not lead to a release (it ended in '$tag')" ;;
	esac
}

# sha256_of prints the SHA-256 of a file.
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		fail "neither sha256sum nor shasum is installed, so the download cannot be checked"
	fi
}

# app_is_running stops the install when Conn.app is running: replacing a bundle under a running
# program is not safe, and nothing has been changed yet at this point. Without pgrep there is
# nothing to ask, so the check is skipped. The first letter is in brackets so that the pattern
# does not match the command line of a process that merely holds it, such as this script run as
# "sh -c" with the text in it.
app_is_running() {
	command -v pgrep >/dev/null 2>&1 || return 0

	if pgrep -f '[C]onn\.app/Contents/MacOS/conn-app' >/dev/null 2>&1; then
		fail "Conn is running: quit Conn first (Cmd-Q, or conn stop) and run the installer again. Use --cli-only to update just the command-line tool. Nothing was installed."
	fi
}

# fetch_app downloads the app's zip and checks it against the release's checksums-app.txt, then
# unpacks it into $scratch/app. It sets app_ready to "no" when the release has no app, which only
# a 404 for checksums-app.txt means: any other failure (a network fault, a 5xx) is an error, so a
# passing fault is not reported as a statement about the release. curl -f cannot tell these
# apart, so the status is read from -w instead.
fetch_app() {
	app_zip=Conn_darwin_universal.zip
	app_ready=no

	sums_url=$base/download/$tag/checksums-app.txt
	tool_note="the command-line tool is installed; the app was not"
	code=$(curl -sSL -o "$scratch/checksums-app.txt" -w '%{http_code}' "$sums_url") ||
		fail "could not download $sums_url ($tool_note)"

	case $code in
	200) ;;
	404)
		rm -f "$scratch/checksums-app.txt"
		printf 'this release has no app; installed the command-line tool only\n'
		return 0
		;;
	*) fail "$sums_url answered HTTP $code ($tool_note)" ;;
	esac

	command -v unzip >/dev/null 2>&1 ||
		fail "unzip is required to install the app ($tool_note; use --cli-only to skip the app)"

	curl -fsSL -o "$scratch/$app_zip" "$base/download/$tag/$app_zip" ||
		fail "could not download $base/download/$tag/$app_zip"

	want=$(awk -v name="$app_zip" '$2 == name { print $1 }' "$scratch/checksums-app.txt")
	[ -n "$want" ] || fail "checksums-app.txt has no line for $app_zip"

	got=$(sha256_of "$scratch/$app_zip")
	[ "$got" = "$want" ] || fail "the app download does not match its checksum (got $got, want $want): the app was not installed"

	mkdir "$scratch/app"
	unzip -q "$scratch/$app_zip" -d "$scratch/app" || fail "could not unpack $app_zip"
	[ -x "$scratch/app/Conn.app/Contents/MacOS/conn-app" ] ||
		fail "Conn.app/Contents/MacOS/conn-app in $app_zip is not an executable program"

	app_ready=yes
}

# place_app puts $scratch/app/Conn.app at $1/Conn.app. The new bundle is copied next to its
# place first, so that the last step is a rename inside one folder: a failure never leaves a
# half-written Conn.app, and the old one is put back if the new one cannot be moved in.
place_app() {
	app_dir=$1
	staged=$app_dir/.Conn.app.new.$$
	old=$app_dir/.Conn.app.old.$$

	mkdir -p "$app_dir" || fail "could not make $app_dir"
	cp -pR "$scratch/app/Conn.app" "$staged" || {
		rm -rf "$staged"
		fail "could not copy the app into $app_dir"
	}

	if [ -e "$app_dir/Conn.app" ] || [ -L "$app_dir/Conn.app" ]; then
		mv "$app_dir/Conn.app" "$old" || {
			rm -rf "$staged"
			fail "could not move the existing $app_dir/Conn.app aside"
		}
	fi

	if ! mv "$staged" "$app_dir/Conn.app"; then
		rm -rf "$staged"
		if [ -e "$old" ]; then
			mv "$old" "$app_dir/Conn.app"
		fi
		fail "could not move the app into $app_dir: the old one was put back"
	fi

	rm -rf "$old"
}

main() {
	cli_only=no
	for arg in "$@"; do
		case $arg in
		--cli-only) cli_only=yes ;;
		-*) fail "unknown option '$arg': the only option is --cli-only" ;;
		esac
	done

	command -v curl >/dev/null 2>&1 || fail "curl is required"
	command -v tar >/dev/null 2>&1 || fail "tar is required"

	base=${CONN_INSTALL_BASE:-https://github.com/claytoncasey01/conn/releases}
	dir=${CONN_INSTALL_DIR:-${HOME:?HOME is not set: set CONN_INSTALL_DIR}/.local/bin}
	# A failure inside $(...) leaves only that subshell, so each is checked here.
	target=$(platform) || exit 1
	os=${target% *}
	arch=${target#* }

	tag=${CONN_INSTALL_VERSION:-}
	if [ -z "$tag" ]; then
		tag=$(latest_tag "$base") || exit 1
	fi

	case $tag in
	v[0-9]*) ;;
	*) fail "'$tag' is not a release tag (they look like v1.2.3)" ;;
	esac

	# The app is for macOS only, and it is asked for unless --cli-only.
	want_app=no
	if [ "$os" = darwin ]; then
		if [ "$cli_only" = no ]; then
			want_app=yes
		fi
	fi

	if [ "$want_app" = yes ]; then
		# HOME is needed for the app's folder only when the app is installed.
		app_dir=${CONN_INSTALL_APP_DIR:-${HOME:?HOME is not set: set CONN_INSTALL_APP_DIR}/Applications}
		app_is_running
	fi

	archive=conn_${os}_${arch}.tar.gz
	scratch=$(mktemp -d)
	trap 'rm -rf "$scratch"' EXIT

	printf 'Installing conn %s for %s/%s\n' "$tag" "$os" "$arch"

	curl -fsSL -o "$scratch/$archive" "$base/download/$tag/$archive" ||
		fail "could not download $base/download/$tag/$archive"
	curl -fsSL -o "$scratch/checksums.txt" "$base/download/$tag/checksums.txt" ||
		fail "could not download $base/download/$tag/checksums.txt"

	want=$(awk -v name="$archive" '$2 == name { print $1 }' "$scratch/checksums.txt")
	[ -n "$want" ] || fail "checksums.txt has no line for $archive"

	got=$(sha256_of "$scratch/$archive")
	[ "$got" = "$want" ] || fail "the download does not match its checksum (got $got, want $want): nothing was installed"

	tar -xzf "$scratch/$archive" -C "$scratch" conn || fail "could not unpack $archive"

	mkdir -p "$dir"
	cp "$scratch/conn" "$dir/.conn.new.$$"
	chmod 755 "$dir/.conn.new.$$"
	mv -f "$dir/.conn.new.$$" "$dir/conn"

	printf 'Installed %s\n' "$("$dir/conn" version)"
	printf '  binary: %s\n' "$dir/conn"

	app_installed=no
	if [ "$want_app" = yes ]; then
		fetch_app
		if [ "$app_ready" = yes ]; then
			place_app "$app_dir"
			printf '  app:    %s\n' "$app_dir/Conn.app"
			app_installed=yes
		fi
	fi

	case ":$PATH:" in
	*":$dir:"*) ;;
	*) printf '  %s is not on your PATH: add it, or run %s directly\n' "$dir" "$dir/conn" ;;
	esac

	if [ "$app_installed" = yes ]; then
		printf 'Open Conn from %s, or start the server with: conn serve\n' "$app_dir"
	else
		printf 'Start it with: conn serve\n'
	fi
}

main "$@"
