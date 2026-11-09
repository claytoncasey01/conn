// Package update holds the rules for telling whether a newer Conn release exists: which version
// is newer, what a check found, and whether the check is switched off. It is plain data and pure
// functions, with nothing that touches the network or the screen, and it imports only the standard
// library.
package update

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// ReleasesURL is where Conn's releases are published.
	ReleasesURL = "https://github.com/claytoncasey01/conn/releases"

	// Command installs the latest release. Conn only shows it; the person runs it.
	Command = "curl -fsSL " + ReleasesURL + "/latest/download/install.sh | sh"

	// startDelay is how long the app runs before its first check, so a check never slows a start.
	startDelay = 10 * time.Second

	// checkEvery is the time between two checks.
	checkEvery = 24 * time.Hour
)

// maxVersionDigits is the most digits one number of a version may have, so it always fits an int.
const maxVersionDigits = 9

// versionParts is how many numbers a version has: major, minor and patch.
const versionParts = 3

// Kind is what a check found.
type Kind int

const (
	// Unchecked means no check was made: this build has no release version, such as a dev build.
	Unchecked Kind = iota
	// UpToDate means the latest release is not newer than this build.
	UpToDate
	// Available means a newer release exists.
	Available
	// Failed means the check could not tell, and Result.Err says why.
	Failed
	// Publishing means a newer release exists but its app is not attached to it yet, so the
	// install command would leave the app as it is. It is neither news nor a failure: the next
	// check looks again.
	Publishing
)

// Result is the outcome of one check.
type Result struct {
	// Kind is what the check found.
	Kind Kind
	// Latest is the latest release tag, set for UpToDate, Available and Publishing.
	Latest string
	// Err is why the check failed, set only for Failed.
	Err error
}

// version is a release tag taken apart. The build metadata after a plus sign is dropped, because
// it takes no part in the order.
type version struct {
	numbers    [versionParts]int
	preRelease string
}

var errNotVersion = errors.New("not a release version like v1.2.3")

// errTagTooLong means a release tag is longer than maxTagBytes, so it is too long to show.
var errTagTooLong = errors.New("release tag is too long")

// versionPattern matches vMAJOR.MINOR.PATCH with an optional -pre-release and +build.
var versionPattern = regexp.MustCompile(fmt.Sprintf(
	`^v(\d{1,%[1]d})\.(\d{1,%[1]d})\.(\d{1,%[1]d})(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`,
	maxVersionDigits,
))

// parse takes a release tag apart, or says it is not one.
func parse(tag string) (version, error) {
	var parsed version

	match := versionPattern.FindStringSubmatch(tag)
	if match == nil {
		return parsed, fmt.Errorf("%q: %w", tag, errNotVersion)
	}

	for index := range parsed.numbers {
		number, err := strconv.Atoi(match[index+1])
		if err != nil {
			return parsed, fmt.Errorf("reading a number of %q: %w", tag, err)
		}

		parsed.numbers[index] = number
	}

	parsed.preRelease = match[versionParts+1]

	return parsed, nil
}

// newer reports whether latest is a newer release than current. When either is not a version
// (a dev build, an empty tag, garbage) the answer is no, so a doubt never becomes an offer.
// Pre-releases are compared only by presence, never by order: a release follows its own
// pre-release, and one pre-release never follows another.
func newer(current, latest string) bool {
	currentVersion, err := parse(current)
	if err != nil {
		return false
	}

	latestVersion, err := parse(latest)
	if err != nil {
		return false
	}

	for index := range currentVersion.numbers {
		if latestVersion.numbers[index] > currentVersion.numbers[index] {
			return true
		}

		if latestVersion.numbers[index] < currentVersion.numbers[index] {
			return false
		}
	}

	// The numbers are equal, so only a release following its own pre-release is newer.
	if currentVersion.preRelease == "" {
		return false
	}

	return latestVersion.preRelease == ""
}

// evaluate turns what a check read into a Result. fetchErr is the error from reading the latest
// tag, nil when it was read.
func evaluate(current, latest string, fetchErr error) Result {
	_, err := parse(current)
	if err != nil {
		return Result{Kind: Unchecked, Latest: "", Err: nil}
	}

	if fetchErr != nil {
		return Result{Kind: Failed, Latest: "", Err: fetchErr}
	}

	if len(latest) > maxTagBytes {
		wrapped := fmt.Errorf("reading the latest release: %w", errTagTooLong)

		return Result{Kind: Failed, Latest: "", Err: wrapped}
	}

	_, err = parse(latest)
	if err != nil {
		wrapped := fmt.Errorf("reading the latest release: %w", err)

		return Result{Kind: Failed, Latest: "", Err: wrapped}
	}

	if newer(current, latest) {
		return Result{Kind: Available, Latest: latest, Err: nil}
	}

	return Result{Kind: UpToDate, Latest: latest, Err: nil}
}

// withApp is what an Available result becomes once the check has asked whether the release's
// app is attached: attached and appErr are that answer. A release without its app yet is
// Publishing, because the install command cannot update the app until it is attached.
func withApp(available Result, attached bool, appErr error) Result {
	if available.Kind != Available {
		panic("update.withApp: the result is not Available")
	}

	if appErr != nil {
		wrapped := fmt.Errorf("asking whether %s has its app: %w", available.Latest, appErr)

		return Result{Kind: Failed, Latest: "", Err: wrapped}
	}

	if !attached {
		return Result{Kind: Publishing, Latest: available.Latest, Err: nil}
	}

	return available
}

// Enabled reports whether the update check is on, given the value of CONN_UPDATE_CHECK. Only the
// off values switch it off; unset, blank or anything else leaves it on.
//
// The off values are the ones config.ThemeIndexOff holds, so one list of words turns off every
// switch; this package imports only the standard library, so a test holds the two lists equal.
// config trims as JavaScript does and this does as strings.TrimSpace does, which differ only for
// U+FEFF and U+0085. Neither is ever typed into a shell profile, so the difference cannot matter.
func Enabled(raw string) bool {
	return !slices.Contains(offValues(), strings.ToLower(strings.TrimSpace(raw)))
}

// offValues are the words that switch the check off.
func offValues() []string { return []string{"off", "false", "0", "no", "none", "disabled"} }
