package conn

import (
	_ "embed" // for the version file below
	"strings"
)

// The version this binary was built as. VERSION says "dev" in the repository; a release build
// writes the release's tag into it first (see scripts/build-release.sh), so the version is a fact
// of the build, not a variable the linker sets.
//
//go:embed VERSION
var version string

// Version is the version of this build: a release tag such as "v1.2.3", or "dev".
func Version() string { return strings.TrimSpace(version) }
