#!/bin/sh
# Check a built Conn.app: its signature is intact, its binary and its Dock tile plug-in hold both
# architectures, and the binary starts. ci.yml and release.yml both run this, so a release never
# carries a bundle CI would have failed.
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

# The Dock tile plug-in is a second binary, which must hold both architectures too.
plugin_archs=$(lipo -archs "$app/Contents/PlugIns/ConnDockTile.docktileplugin/Contents/MacOS/ConnDockTile")
echo "Dock tile plug-in archs: $plugin_archs"
case "$plugin_archs" in
*x86_64*arm64* | *arm64*x86_64*) ;;
*)
	echo "the Dock tile plug-in does not hold both x86_64 and arm64" >&2
	exit 1
	;;
esac

# The Dock loads the class the plug-in's Info.plist names, so the binary must define that class.
plugin_contents=$app/Contents/PlugIns/ConnDockTile.docktileplugin/Contents
plugin_class=$(plutil -extract NSPrincipalClass raw "$plugin_contents/Info.plist")
echo "Dock tile plug-in class: $plugin_class"
if ! nm -gU "$plugin_contents/MacOS/ConnDockTile" | grep -q "_OBJC_CLASS_\$_$plugin_class\$"; then
	echo "the Dock tile plug-in does not define its principal class $plugin_class" >&2
	exit 1
fi

# --print-env answers before any window code runs, so this starts the binary without a display.
# The markers begin with a NUL byte, so only the end marker's visible text is searched for: it is
# written after the last entry.
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

"$program" --print-env >"$scratch/env.out"
tr -d '\0' <"$scratch/env.out" | grep -q CONN-ENV-END
