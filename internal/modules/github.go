package modules

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
)

const (
	// authTimeout stops `gh auth status`.
	authTimeout = 15 * time.Second
	// authBytesMax caps what `gh auth status` may print.
	authBytesMax = 256 << 10
)

var (
	// login is a GitHub login (an Enterprise Managed User's has an underscore).
	login = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,38}$`)
	// accountHead is an account's first line: a check mark or a cross, then what it says.
	accountHead = regexp.MustCompile(
		`^(\x{2713}|\x{2714}|X|\x{2717}|x)[` + js.SpaceClass + `]+([^\n\r\x{2028}\x{2029}]*)$`)
	// accountName is the login in "account jane" or, in older versions, "as jane".
	accountName = regexp.MustCompile(`(?:account|as) ([^` + js.SpaceClass + `]+)`)
	// activeLine is an account's "Active account: true" line.
	activeLine = regexp.MustCompile(
		`(?i)^-?[` + js.SpaceClass + `]*Active account:[` + js.SpaceClass + `]*(true|false)`)
	// factLine is a line under an account that is a fact about it, not a reason.
	factLine = regexp.MustCompile(`(?i)^-?[` + js.SpaceClass + `]*(?:` +
		`(?:Active account|Git operations protocol|Token(?: scopes)?):|` +
		`To re-authenticate|To (?:log in|authenticate))`)
	// leadingDash is the "- " that starts a line under an account.
	leadingDash = regexp.MustCompile(`^-[` + js.SpaceClass + `]*`)
	// hostLine is a line that is only a host name: the header gh prints above its accounts.
	hostLine = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}$`)
	// symbolsOnly is a line with no letter, digit or underscore.
	symbolsOnly = regexp.MustCompile(`^\W*$`)
	// notSignedIn is gh saying no account is signed in.
	notSignedIn = regexp.MustCompile(`(?i)not logged in|not logged into`)
	// tokenWord is a token-shaped word: it never reaches the card, whatever gh printed around it.
	tokenWord = regexp.MustCompile(
		`\b(?:gh[opusr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,})\b`)
)

// AuthRun is how one `gh auth status` ended. Some gh versions print to stdout and some to stderr,
// so both are kept.
type AuthRun struct {
	// Exited is false when the process was killed or never started; Code is its exit code.
	Exited bool
	Code   int
	Out    string
	Err    string
	// SpawnError is set when the process never ran: "ENOENT" for a missing gh.
	SpawnError string
}

// AuthRunner runs `gh auth status`.
type AuthRunner func(ctx context.Context) AuthRun

// GhAuthStatus runs the real `gh auth status`, with a time limit and a cap on its output.
func GhAuthStatus(ctx context.Context) AuthRun {
	result := run.Cmd(ctx, "gh", []string{"auth", "status"}, run.Opts{
		Dir: "", Timeout: authTimeout, OutputBytesMax: authBytesMax, Stdin: "", Env: nil,
	})

	ended := AuthRun{
		Exited: true, Code: result.Code, Out: result.Stdout, Err: result.Stderr, SpawnError: "",
	}
	if result.Code >= 0 {
		return ended
	}

	ended.Exited = false

	ended.SpawnError = result.Spawn
	if ended.SpawnError == "" {
		ended.SpawnError = "killed"
	}

	return ended
}

// active is what an account's "Active account" line said.
type active int

const (
	// activeUnknown is a gh version that prints no such line.
	activeUnknown active = iota
	activeYes
	activeNo
)

// account is one account `gh auth status` lists.
type account struct {
	ok bool
	// login is "" when the line names none, or one that is not a valid login.
	login  string
	active active
	// why is the first line under it that says why it does not work.
	why string
}

// accounts are the accounts `gh auth status` lists, in both layouts: "✓ Logged in to HOST
// account NAME (store)" with an "Active account" line (multi-account gh), and "✓ Logged in to
// HOST as NAME (store)" or "X HOST: authentication failed" (older gh).
func accounts(text string) []account {
	found := []account{}

	for raw := range strings.SplitSeq(text, "\n") {
		line := js.Trim(raw)
		if line == "" {
			continue
		}

		head := accountHead.FindStringSubmatch(line)
		if head != nil {
			found = append(found, newAccount(head[1], head[2]))

			continue
		}

		if len(found) == 0 {
			continue
		}

		describe(&found[len(found)-1], line)
	}

	return found
}

func newAccount(mark, rest string) account {
	works := mark == "✓"
	if mark == "\u2714" {
		works = true
	}

	named := ""

	match := accountName.FindStringSubmatch(rest)
	if match != nil {
		if login.MatchString(match[1]) {
			named = match[1]
		}
	}

	why := rest
	if works {
		why = ""
	}

	return account{ok: works, login: named, active: activeUnknown, why: why}
}

// describe reads one line under an account: its "Active account" line, or the first reason it
// does not work.
func describe(current *account, line string) {
	if match := activeLine.FindStringSubmatch(line); match != nil {
		current.active = activeNo
		if strings.EqualFold(match[1], "true") {
			current.active = activeYes
		}

		return
	}

	if current.ok {
		return
	}

	if factLine.MatchString(line) {
		return
	}

	if !strings.HasPrefix(line, "-") {
		return
	}

	if strings.Contains(current.why, ":") {
		return
	}

	current.why += ": " + leadingDash.ReplaceAllLiteralString(line, "")
}

// LoginFrom is the signed-in login `gh auth status` reports ("Logged in to github.com account
// jane", or "as jane" in older versions), only when it is a valid login; "" otherwise.
func LoginFrom(text string) string {
	for _, found := range accounts(text) {
		if !found.ok {
			continue
		}

		if found.login != "" {
			return found.login
		}
	}

	return ""
}

// GitHubProbe is `gh auth status`. The active account decides: exit 0 is signed in, and so is a
// nonzero exit while the active account has its check mark (a second, stale account is what
// makes gh exit 1). Only a parsed login, a fixed sentence or one cleaned line about the failing
// account is shown, never the raw output: it holds the host header and token lines.
func GitHubProbe(runner AuthRunner) Probe {
	return func(ctx context.Context) (ProbeResult, error) {
		ended := runner(ctx)
		text := ended.Out + "\n" + ended.Err
		found := accounts(text)

		live, isLive := liveAccount(found)
		if signedIn(ended, isLive) {
			return ProbeResult{OK: true, Detail: signedInDetail(found, live, isLive, text)}, nil
		}

		return ProbeResult{OK: false, Detail: failedDetail(ended, found, text)}, nil
	}
}

// liveAccount is the first working account that is not known to be inactive.
func liveAccount(found []account) (account, bool) {
	for _, candidate := range found {
		if !candidate.ok {
			continue
		}

		if candidate.active == activeNo {
			continue
		}

		return candidate, true
	}

	return account{}, false
}

func signedIn(ended AuthRun, isLive bool) bool {
	if !ended.Exited {
		return false
	}

	if ended.Code == 0 {
		return true
	}

	return isLive
}

func signedInDetail(found []account, live account, isLive bool, text string) string {
	name := ""
	if isLive {
		name = live.login
	}

	if name == "" {
		name = LoginFrom(text)
	}

	detail := "Signed in to GitHub"
	if name != "" {
		detail = "Signed in as " + name
	}

	for _, candidate := range found {
		if !candidate.ok {
			return detail + " (another account needs gh auth login)"
		}
	}

	return detail
}

func failedDetail(ended AuthRun, found []account, text string) string {
	if ended.SpawnError == "ENOENT" {
		return "The gh CLI was not found: install it, then run gh auth login"
	}

	if !ended.Exited {
		return "gh did not answer in time"
	}

	if notSignedIn.MatchString(text) {
		return "Not signed in: run gh auth login"
	}

	line := failingAccount(found).why
	if line == "" {
		line = firstMeaningfulLine(text)
	}

	detail := CleanDetail(tokenWord.ReplaceAllLiteralString(line, "***"), detailMax)
	if detail == "" {
		return "gh auth status exited with " + strconv.Itoa(ended.Code)
	}

	return detail
}

// failingAccount is the account that should have worked: the active one that failed, else the
// first that failed.
func failingAccount(found []account) account {
	for _, candidate := range found {
		if candidate.ok {
			continue
		}

		if candidate.active == activeYes {
			return candidate
		}
	}

	for _, candidate := range found {
		if !candidate.ok {
			return candidate
		}
	}

	return account{}
}

// firstMeaningfulLine is the first line with a letter or digit that is not just a host name.
func firstMeaningfulLine(text string) string {
	for raw := range strings.SplitSeq(text, "\n") {
		line := js.Trim(raw)
		if line == "" {
			continue
		}

		if symbolsOnly.MatchString(line) {
			continue
		}

		if hostLine.MatchString(line) {
			continue
		}

		return line
	}

	return ""
}
