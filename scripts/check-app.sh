#!/bin/sh
# Check a built Conn.app: its signature is intact, its binary holds both architectures, and the
# binary starts. ci.yml and release.yml both run this, so a release never carries a bundle CI
# would have failed.
#
#   scripts/check-app.sh <path-to-Conn.app>
#
# Run it on a Mac, on the bundle as a user would get it: unpacked from the zip that
# scripts/build-app.sh writes, so that a seal broken by the zip itself shows. The path of the
# program inside the bundle (Conn.app/Contents/MacOS/conn-app) is the one build-app.sh makes.
set -eu

app=${1:?usage: scripts/check-app.sh <path-to-Conn.app>}
program=$app/Contents/MacOS/conn-app

codesign --verify --deep --strict "$app"

archs=$(lipo -archs "$program")
echo "archs: $archs"
case "$archs" in
*x86_64*) ;;
*)
	echo "no x86_64 slice" >&2
	exit 1
	;;
esac
case "$archs" in
*arm64*) ;;
*)
	echo "no arm64 slice" >&2
	exit 1
	;;
esac

# --print-env answers before any window code runs, so this starts the binary without a display.
# The markers begin with a NUL byte, so only the end marker's visible text is searched for: it is
# written after the last entry.
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

"$program" --print-env >"$scratch/env.out"
tr -d '\0' <"$scratch/env.out" | grep -q CONN-ENV-END
