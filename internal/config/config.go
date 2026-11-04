// Package config holds Conn's fixed values and tuning: models, budgets, timeouts, intervals,
// prompts. What differs per person (repos, clones, Slack, Jira) is in Settings
// (internal/settings).
package config

import (
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
)

// Summary models by name.
const (
	Haiku  = "claude-haiku-5-5"
	Sonnet = "claude-sonnet-5-5"
)

// dataFolderName is the folder Conn keeps its data in, under the user's config folder.
const dataFolderName = "conn"

// How long a finished review session may sit idle before it is closed, in minutes.
const (
	closeMinutesDefault = 10
	closeMinutesMin     = 1
	closeMinutesMax     = 60
)

const (
	// greptileBotDefault is the login of the Greptile app, whose review of the head a My PRs
	// round waits for.
	greptileBotDefault = "greptile-apps"

	// themeIndexDefault is the repo the Themes gallery reads its index from; anyone can point
	// CONN_THEME_INDEX at their own.
	themeIndexDefault = "claytoncasey01/conn-themes"
)

// The fixed tuning Config carries; Config's fields say what each is for.
const (
	summaryCostUSDMax         = 1.0
	summaryTimeout            = 60 * time.Second
	reviewPoll                = 180 * time.Second
	reviewFirstWindowDays     = 2
	reviewScanCostUSDMax      = 0.5
	reviewScanTimeout         = 90 * time.Second
	reviewDMScanInterval      = 600 * time.Second
	reviewChannelScanInterval = 600 * time.Second
)

var (
	themeRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	dots      = regexp.MustCompile(`^\.+$`)
)

// LookupFunc reads one environment variable and whether it is set: os.LookupEnv, or a map in a
// test.
type LookupFunc func(key string) (string, bool)

// Config is what the server runs with.
type Config struct {
	// Home is the user's home folder ($HOME).
	Home string
	// ClaudeDir is Claude Code's own folder ($HOME/.claude): its transcripts, and the agents and
	// skills it finds there.
	ClaudeDir string
	// DataDir is where the dashboard keeps its own data (history.db, runs, caches).
	DataDir string
	// DataDirDefault is the data folder when CONN_DATA is unset: the real one. Dry runs refuse to
	// write into it.
	DataDirDefault string
	// ClaudeBin is the claude program helper runs start (CONN_CLAUDE): a test points it at a
	// fake.
	ClaudeBin string
	// CmuxBin is the cmux program sessions are read and typed into with (CONN_CMUX).
	CmuxBin string
	// ThemesUser is the user's own themes (and gallery installs), untrusted input.
	ThemesUser string
	// ThemeIndex is the Themes gallery's index repo, or "" when CONN_THEME_INDEX is off.
	ThemeIndex string
	// TranscriptsRoot is where Claude Code keeps its transcripts.
	TranscriptsRoot string
	// SummaryModel writes the At a Glance summary, unless a regenerate picks another.
	SummaryModel string
	// Per summary run: a cap on cost and time against a runaway, not an estimate. Haiku was
	// measured on three real sessions: $0.14 for the first run (cold prompt cache, ~140k tokens
	// of inherited context) and $0.02-0.03 after, 4-9 s each. A Sonnet regenerate was not
	// measured: from list prices a cold one is about $0.56, so the cap leaves room for it. Runs
	// are one at a time, so the timeout only has to kill a stuck run.
	SummaryCostUSDMax float64
	SummaryTimeout    time.Duration
	// ReviewPrompt is the prompt a review session starts with; {pr} is the PR number and {repo}
	// its owner/repo, each written once. Plain instructions that need no skill of the person's
	// own (one installed is used). A re-review appends ReReviewLine, where {sha} is the commit
	// of the last review.
	ReviewPrompt string
	ReReviewLine string
	// ReviewPoll is how often the PR list is refreshed, and ReviewFirstWindowDays how far back
	// the first Slack scan looks.
	ReviewPoll            time.Duration
	ReviewFirstWindowDays int
	// The Slack scan: model, and a cap on cost and time per scan against a runaway (one cold
	// scan measured $0.016, 20 s).
	ReviewScanModel      string
	ReviewScanCostUSDMax float64
	ReviewScanTimeout    time.Duration
	// ReviewDMScanInterval is how often at most the DM scan runs (review requests sent to you
	// in a 1:1 Slack DM; Settings says whether it runs). It shares the scan model, budget,
	// timeout and first window.
	ReviewDMScanInterval time.Duration
	// ReviewChannelScanInterval is how often at most the channel scan looks for new posts (PRs
	// nobody has asked GitHub to request me on), while a dashboard is open.
	ReviewChannelScanInterval time.Duration
	// A finished review session is closed after ReviewCloseAfter idle (1 to 60 minutes), and
	// never later than ReviewCloseAfterMax.
	ReviewCloseAfter    time.Duration
	ReviewCloseAfterMax time.Duration
	// ReviewNamePattern matches the name of a review session working under $CONN_DATA/reviews
	// (pr-<number>[.<k>]-<owner>--<repo>, or pr-<number>[-<k>] from before Reviews read several
	// repos). Purely syntactic: the launch record says which PR a session is for.
	ReviewNamePattern *regexp.Regexp
	// FindingBots are the logins of the bots whose open review threads are findings on My PRs.
	FindingBots []string
	// ProtectedHeads are PR branches a Self-Review refuses besides the repo's default branch (a
	// trailing * matches by prefix).
	ProtectedHeads []string
	// GreptileTrigger is the comment that asks Greptile for a review.
	GreptileTrigger string
	// GreptileBot is the login whose review (of the head, or a comment after the ask) makes a
	// My PRs round reviewed.
	GreptileBot string
	// SelfNamePattern matches the name of a self-review session (self-<repo>-<number>, or
	// self-<repo>-<number>-<round>).
	SelfNamePattern *regexp.Regexp
	// SelfReviewPrompt is the prompt a self-review session starts with; {repo}, {pr}, {round},
	// {worktree}, {headRef} (the PR branch) and {branch} (the local branch, conn-self-<pr>) are
	// filled in.
	SelfReviewPrompt string
	// ReviewMe is the GitHub login Reviews reads as the reviewer (CONN_REVIEW_ME, trimmed); ""
	// asks gh for the signed-in one.
	ReviewMe string
	// Dry are the dry switches (CONN_MYPRS_DRY, CONN_REVIEWS_DRY, CONN_WORK_DRY): a module that
	// runs dry shows fixed rows and calls nothing. A seeded dry run turns all three on.
	Dry Dry
	// DrySeed is CONN_DRY_SEED as set: "0" turns the dry seed off (a test's own boot with no
	// settings file).
	DrySeed string
	// DataEnv is CONN_DATA as set ("" when unset: the data folder is then the real one).
	DataEnv string
}

// Dry says which modules run dry.
type Dry struct {
	MyPrs   bool
	Reviews bool
	Work    bool
}

// Any is whether any module runs dry.
func (d Dry) Any() bool {
	if d.MyPrs {
		return true
	}

	if d.Reviews {
		return true
	}

	return d.Work
}

// DataDirDefault is the folder Conn keeps everything it saves in when CONN_DATA is unset:
// $XDG_CONFIG_HOME/conn, or $HOME/.config/conn when XDG_CONFIG_HOME is unset, empty or not an
// absolute path (the XDG rule for a relative value is to ignore it).
func DataDirDefault(lookup LookupFunc) string {
	base := valueOf(lookup, "XDG_CONFIG_HOME")
	if filepath.IsAbs(base) {
		return filepath.Join(base, dataFolderName)
	}

	return filepath.Join(valueOf(lookup, "HOME"), ".config", dataFolderName)
}

// FromEnv builds the configuration from lookup. A variable that is set but empty is used as it
// is where a path or model is read (only an unset one gives the default).
func FromEnv(lookup LookupFunc) Config {
	home := valueOf(lookup, "HOME")
	claudeDir := filepath.Join(home, ".claude")
	transcripts := filepath.Join(claudeDir, "projects")
	dataDefault := DataDirDefault(lookup)
	data := valueOr(lookup, "CONN_DATA", dataDefault)
	closeMinutes := parseCloseMinutes(valueOf(lookup, "CONN_REVIEW_CLOSE_MIN"))

	return Config{
		Home:                      home,
		ClaudeDir:                 claudeDir,
		DataDir:                   data,
		DataDirDefault:            dataDefault,
		ClaudeBin:                 valueOr(lookup, "CONN_CLAUDE", "claude"),
		CmuxBin:                   valueOr(lookup, "CONN_CMUX", "cmux"),
		ThemesUser:                filepath.Join(data, "themes"),
		ThemeIndex:                parseThemeIndex(valueOf(lookup, "CONN_THEME_INDEX")),
		TranscriptsRoot:           valueOr(lookup, "CONN_TRANSCRIPTS", transcripts),
		SummaryModel:              valueOr(lookup, "CONN_SUMMARY_MODEL", Haiku),
		SummaryCostUSDMax:         summaryCostUSDMax,
		SummaryTimeout:            summaryTimeout,
		ReviewPrompt:              reviewPrompt,
		ReReviewLine:              reReviewLine,
		ReviewPoll:                reviewPoll,
		ReviewFirstWindowDays:     reviewFirstWindowDays,
		ReviewScanModel:           Haiku,
		ReviewScanCostUSDMax:      reviewScanCostUSDMax,
		ReviewScanTimeout:         reviewScanTimeout,
		ReviewDMScanInterval:      reviewDMScanInterval,
		ReviewChannelScanInterval: reviewChannelScanInterval,
		ReviewCloseAfter:          time.Duration(closeMinutes) * time.Minute,
		ReviewCloseAfterMax:       closeMinutesMax * time.Minute,
		ReviewNamePattern: regexp.MustCompile(
			`^pr-\d+(?:\.\d+)?(?:-[a-z0-9._-]{1,160})?$`),
		FindingBots:      parseBots(valueOf(lookup, "CONN_FINDING_BOTS")),
		ProtectedHeads:   parseHeads(valueOf(lookup, "CONN_PROTECTED_HEADS")),
		GreptileTrigger:  "@greptile review",
		GreptileBot:      parseGreptileBot(valueOf(lookup, "CONN_GREPTILE_BOT")),
		SelfNamePattern:  regexp.MustCompile(`^self-[a-z0-9._-]{1,60}-\d+(-\d+)?$`),
		SelfReviewPrompt: selfReviewPrompt,
		ReviewMe:         js.Trim(valueOf(lookup, "CONN_REVIEW_ME")),
		Dry: Dry{
			MyPrs:   valueOf(lookup, "CONN_MYPRS_DRY") == "1",
			Reviews: valueOf(lookup, "CONN_REVIEWS_DRY") == "1",
			Work:    valueOf(lookup, "CONN_WORK_DRY") == "1",
		},
		DrySeed: valueOf(lookup, "CONN_DRY_SEED"),
		DataEnv: valueOf(lookup, "CONN_DATA"),
	}
}

// valueOf is the variable key, or "" when it is unset.
func valueOf(lookup LookupFunc, key string) string {
	value, _ := lookup(key)

	return value
}

// valueOr is the variable key, or fallback when it is unset; a set but empty value is kept.
func valueOr(lookup LookupFunc, key, fallback string) string {
	value, ok := lookup(key)
	if !ok {
		return fallback
	}

	return value
}

// parseCloseMinutes reads CONN_REVIEW_CLOSE_MIN: whole minutes clamped to 1..60; unset, empty or
// not a finite number gives 10.
func parseCloseMinutes(raw string) int {
	if js.Trim(raw) == "" {
		return closeMinutesDefault
	}

	minutes := js.ToNumber(raw)
	if math.IsNaN(minutes) {
		return closeMinutesDefault
	}

	if math.IsInf(minutes, 0) {
		return closeMinutesDefault
	}

	return int(min(closeMinutesMax, max(closeMinutesMin, math.Floor(minutes))))
}

// split is a comma-separated list, trimmed, with empty entries dropped.
func split(list string) []string {
	var entries []string

	for entry := range strings.SplitSeq(list, ",") {
		entry = js.Trim(entry)
		if entry != "" {
			entries = append(entries, entry)
		}
	}

	return entries
}

// findingBotsDefault are the bots whose open review threads count as findings.
func findingBotsDefault() []string { return []string{"greptile-apps", "devin-ai-integration"} }

// parseBots reads CONN_FINDING_BOTS: logins separated by commas; unset or nothing left gives the
// defaults.
func parseBots(list string) []string {
	bots := split(list)
	if len(bots) == 0 {
		return findingBotsDefault()
	}

	return bots
}

// parseGreptileBot reads CONN_GREPTILE_BOT: the login, trimmed; unset or blank gives the default.
func parseGreptileBot(raw string) string {
	login := js.Trim(raw)
	if login == "" {
		return greptileBotDefault
	}

	return login
}

// protectedHeadsDefault are branches a Self-Review never runs on: its session pushes its fixes
// straight onto the PR's branch. A trailing * matches by prefix.
func protectedHeadsDefault() []string {
	return []string{"main", "master", "develop", "staging", "release/*"}
}

// parseHeads reads CONN_PROTECTED_HEADS: names (or prefixes ending in *) separated by commas;
// unset or nothing left gives the defaults.
func parseHeads(list string) []string {
	heads := split(list)
	if len(heads) == 0 {
		return protectedHeadsDefault()
	}

	return heads
}

// IsRepoName reports whether repo is `owner/repo` of plain GitHub name characters: it goes into a
// URL, so nothing else (a ?, a .. segment) may pass.
func IsRepoName(repo string) bool {
	if !themeRepo.MatchString(repo) {
		return false
	}

	return !slices.ContainsFunc(strings.Split(repo, "/"), dots.MatchString)
}

// ThemeIndexOff are the values that turn the gallery off: someone who types any of them wants it
// off, so none may leave it on (docs/using-themes.md and docs/themes.md state exactly these, held
// by testdata/theme-index-off.json).
func ThemeIndexOff() []string { return []string{"off", "false", "0", "none", "no", "disabled"} }

// parseThemeIndex reads CONN_THEME_INDEX: one of ThemeIndexOff (any case, trimmed) turns the
// gallery off (""); owner/repo is kept; unset, blank or anything else gives the default.
func parseThemeIndex(raw string) string {
	value := js.Trim(raw)
	if slices.Contains(ThemeIndexOff(), strings.ToLower(value)) {
		return ""
	}

	if IsRepoName(value) {
		return value
	}

	return themeIndexDefault
}
