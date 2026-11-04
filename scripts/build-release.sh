#!/bin/sh
# Build the release archives of conn into a folder: one conn_<os>_<arch>.tar.gz for each of the
# four targets, and checksums.txt over them. install.sh downloads these.
#
#   scripts/build-release.sh <out-folder>
#
# The page bundle (dist/) must be built first (bun run build), because the binary embeds it. The
# build is pure Go (no cgo), so every target builds on any machine.
set -eu

out=${1:?usage: scripts/build-release.sh <out-folder>}

targets="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"

if [ ! -f dist/index.html ]; then
	echo "build-release: dist/index.html is missing: run bun run build first" >&2
	exit 1
fi

mkdir -p "$out"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

for target in $targets; do
	os=${target%/*}
	arch=${target#*/}

	mkdir -p "$scratch/$os-$arch"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
		go build -trimpath -ldflags='-s -w' -o "$scratch/$os-$arch/conn" ./cmd/conn
	tar -czf "$out/conn_${os}_${arch}.tar.gz" -C "$scratch/$os-$arch" conn
done

cd "$out"

if command -v sha256sum >/dev/null 2>&1; then
	sha256sum conn_*.tar.gz >checksums.txt
else
	shasum -a 256 conn_*.tar.gz >checksums.txt
fi
