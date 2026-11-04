#!/bin/sh
# Install conn: download the release for this machine, check it against the release's checksums,
# and put the binary in a folder. It does nothing else: no sudo, no settings, no skills (those
# are installed from conn's Settings page).
#
#   curl -fsSL https://github.com/claytoncasey01/conn/releases/latest/download/install.sh | sh
#
# Environment:
#   CONN_INSTALL_VERSION  the release tag to install, such as v1.2.3 (default: the latest)
#   CONN_INSTALL_DIR      where the binary goes (default: $HOME/.local/bin)
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

main() {
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

	case ":$PATH:" in
	*":$dir:"*) ;;
	*) printf '  %s is not on your PATH: add it, or run %s directly\n' "$dir" "$dir/conn" ;;
	esac

	printf 'Start it with: conn serve\n'
}

main "$@"
