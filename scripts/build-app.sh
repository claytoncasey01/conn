#!/bin/sh
# Build the macOS app of conn into a folder: Conn_darwin_universal.zip, a Conn.app bundle that
# holds one binary for Apple silicon and Intel, and checksums-app.txt over the zip. install.sh
# downloads these.
#
# This script is where those names are decided: Conn_darwin_universal.zip, checksums-app.txt and
# Conn.app/Contents/MacOS/conn-app. install.sh, .github/workflows/ci.yml and release.yml repeat
# them, and internal/installscript fails when install.sh and this script disagree.
#
#   scripts/build-app.sh <out-folder>
#
# Run it on a Mac: the app is built with cgo, which needs the Xcode command line tools, and the
# bundle is joined, signed and zipped with lipo, codesign and ditto. The page bundle (dist/)
# must be built first (bun run build), because the binary embeds it. The bundle is signed ad hoc
# (no developer identity), which is all Apple silicon needs to run it.
set -eu

# The bundle's identifier. Info.plist.tmpl takes it from here, so it is written once.
bundle_identifier=io.github.claytoncasey01.conn

# The oldest macOS the app runs on. Go 1.27 sets this floor. The build flags and Info.plist.tmpl
# both take it from here.
macos_minimum=13.0

out=${1:?usage: scripts/build-app.sh <out-folder>}

if [ "$(uname -s)" != Darwin ]; then
	echo "build-app: this builds a macOS app and needs a Mac (uname -s is $(uname -s))" >&2
	exit 1
fi

if [ ! -f dist/index.html ]; then
	echo "build-app: dist/index.html is missing: run bun run build first" >&2
	exit 1
fi

# bundle_version prints the numeric core of the VERSION file for CFBundleVersion: v1.2.3,
# v1.2.3-rc.1 and v1.2.3+build.5 give 1.2.3, and dev (or anything else that is not x.y.z) gives
# 0.0.0.
bundle_version() {
	core=$(tr -d '[:space:]' <VERSION)
	core=${core#v}
	core=${core%%+*}
	core=${core%%-*}
	if printf '%s\n' "$core" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
		echo "$core"
	else
		echo 0.0.0
	fi
}

# build_arch builds the app for one architecture. cgo is switched on explicitly because Go
# switches it off when GOARCH differs from the host's, and the version flags keep the Mach-O
# header at the floor above rather than the SDK's default.
build_arch() {
	arch=$1
	MACOSX_DEPLOYMENT_TARGET=$macos_minimum CGO_ENABLED=1 \
		CGO_CFLAGS=-mmacosx-version-min=$macos_minimum \
		CGO_LDFLAGS=-mmacosx-version-min=$macos_minimum \
		GOOS=darwin GOARCH=$arch \
		go build -trimpath -ldflags='-s -w' -o "$scratch/conn-app-$arch" ./cmd/conn-app
}

mkdir -p "$out"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

build_arch arm64
build_arch amd64

app=$scratch/Conn.app
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
lipo -create -output "$app/Contents/MacOS/conn-app" \
	"$scratch/conn-app-arm64" "$scratch/conn-app-amd64"
sed -e "s|@IDENTIFIER@|$bundle_identifier|" \
	-e "s|@MACOS_MINIMUM@|$macos_minimum|" \
	-e "s|@VERSION@|$(bundle_version)|" \
	scripts/Info.plist.tmpl >"$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist" >/dev/null
# The icon is drawn by scripts/make-app-icon.swift, which rewrites this file.
cp assets/app/Conn.icns "$app/Contents/Resources/Conn.icns"

# Extended attributes (macOS adds some to files it considers downloaded or copied) would make
# ditto store a "._" companion file for each, which unzip then extracts as a real file and breaks
# the signature's seal. So they are cleared before signing and left out of the zip.
xattr -cr "$app"
codesign --force --deep --sign - "$app"

# The zip is made in the scratch folder first, so a failed run leaves no half-written zip in the
# out folder.
ditto -c -k --keepParent --norsrc --noextattr --noqtn "$app" "$scratch/Conn_darwin_universal.zip"
cp "$scratch/Conn_darwin_universal.zip" "$out/Conn_darwin_universal.zip"

cd "$out"
shasum -a 256 Conn_darwin_universal.zip >checksums-app.txt
