package reviews

// What Conn itself remembers about reviews, in one small file: the Slack matches and the cursor
// of each channel, per-PR scan schedules, one record per launch, and the PRs the scans found that
// wait to be checked. Everything else is derived from GitHub, so the file can be rebuilt: losing
// it must never hurt, and a bad one is read as empty, never an error.
//
// Version 2 keys every PR as `owner/repo#number` (lowercase), since a number alone is not unique
// across repos. A version 1 file (keyed by number, from when Reviews read one repo) is migrated
// once, when there is an allowlist to place its records against: the old file is copied to
// `reviews.json.v1.bak` first (an older Conn reads any other version as empty, and its next save
// would wipe the file), each record is re-keyed from its own evidence, and a record whose repo
// cannot be known is kept in `legacy`, never shown and never deleted.

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// fileVersion is the version this Conn writes.
const fileVersion = 2

// Limits on what the file keeps.
const (
	// keepMs is how long a PR not open and untouched is kept; GitHub still knows it.
	keepMs = 14 * 86_400_000
	// foundKeepMs is how long a discovery not fetched is kept (the next scan finds it again if
	// it is still asked for).
	foundKeepMs = 86_400_000
	// MaxFound is the most discoveries kept waiting.
	MaxFound = 300
	// maxSafeInteger is Number.MAX_SAFE_INTEGER.
	maxSafeInteger = 1<<53 - 1
)

// The launch models.
const (
	ModelSonnet = "sonnet"
	ModelOpus   = "opus"
)

var (
	slackTS     = regexp.MustCompile(`^\d{9,11}\.\d{1,9}$`)
	channelName = regexp.MustCompile(`^#[a-z0-9._-]{1,80}$`)
	numberKey   = regexp.MustCompile(`^[1-9]\d{0,15}$`)
	keyShape    = regexp.MustCompile(`^([^#` + js.SpaceClass + `]+)#([1-9]\d{0,15})$`)

	errNotRepo   = errors.New("not a repo name")
	errNotNumber = errors.New("not a PR number")
)

// SlackPost is the first post about a PR in a chosen channel; Channel is nil for a post kept
// from before channels were chosen (it counts for any).
type SlackPost struct {
	TS        string  `json:"ts"`
	Permalink string  `json:"permalink"`
	PostedAt  float64 `json:"postedAt"`
	Channel   *string `json:"channel"`
}

// DMPost is the first Slack DM that asked for a review of a PR (1:1, from a person).
type DMPost struct {
	TS        string  `json:"ts"`
	Permalink string  `json:"permalink"`
	PostedAt  float64 `json:"postedAt"`
}

// Launch is one REVIEW press.
type Launch struct {
	At       float64 `json:"at"`
	Model    string  `json:"model"`
	Name     string  `json:"name"`
	HeadSha  string  `json:"headSha"`
	Worktree string  `json:"worktree"`
	// SessionID is Claude's session id, pinned once the session is seen.
	SessionID *string  `json:"sessionId"`
	ClosedAt  *float64 `json:"closedAt"`
	// Clone is the clone the worktree was made from ("" for a launch from before this was
	// recorded: its worktree's .git pointer says).
	Clone string `json:"clone,omitempty"`
}

// Scan is the back-off bookkeeping of one scan of one PR.
type Scan struct {
	Tries  float64 `json:"tries"`
	LastAt float64 `json:"lastAt"`
}

// ThreadScan is the thread scan's bookkeeping, with the head it was started for.
type ThreadScan struct {
	Tries   float64 `json:"tries"`
	LastAt  float64 `json:"lastAt"`
	HeadSha string  `json:"headSha"`
}

// Ended is how a PR stopped being open, from its own fetch: merged, closed, or gone (GitHub no
// longer finds it).
type Ended struct {
	How string  `json:"how"`
	At  float64 `json:"at"`
}

// Entry is what the file keeps per PR: what Conn tracked, and when Slack was last asked about
// it.
type Entry struct {
	Slack *SlackPost `json:"slack"`
	// SlackAskAt is when the author last asked again in the Slack thread.
	SlackAskAt *float64 `json:"slackAskAt"`
	DM         *DMPost  `json:"dm"`
	// DMLastAt is when the last DM asking for a review was sent.
	DMLastAt    *float64   `json:"dmLastAt"`
	FirstSeenAt float64    `json:"firstSeenAt"`
	Launches    []*Launch  `json:"launches"`
	Scan        Scan       `json:"scan"`
	ThreadScan  ThreadScan `json:"threadScan"`
	// Requested is that the "Requests me" search has listed it (it stays so once GitHub drops
	// the request after a review).
	Requested bool `json:"requested"`
	// Ended is set once the PR's own fetch says it is no longer open; cleared when a search or
	// fetch finds it open again.
	Ended *Ended `json:"ended"`
	// SeenAt is when a search or a fetch last found it open.
	SeenAt float64 `json:"seenAt"`
	// PlacedAt is when the person placed this old record under a repo (Settings).
	PlacedAt *float64 `json:"placedAt,omitempty"`
}

// FoundSlack is a post a scan found.
type FoundSlack struct {
	TS        string `json:"ts"`
	Permalink string `json:"permalink"`
	Channel   string `json:"channel"`
}

// FoundDM is the DMs a scan found about one PR: the earliest one's ts and link, the latest one's
// ts.
type FoundDM struct {
	FirstTS   string `json:"firstTs"`
	Permalink string `json:"permalink"`
	LastTS    string `json:"lastTs"`
}

// Found is a PR a scan named that is not known yet: it is fetched ("exists, is open, repo
// allowed") before it counts.
type Found struct {
	At    float64     `json:"at"`
	Slack *FoundSlack `json:"slack"`
	DM    *FoundDM    `json:"dm"`
}

// File is reviews.json.
type File struct {
	Version int `json:"version"`
	// Cursors are by channel (`#name`): the Slack ts of the newest post already read there.
	Cursors *js.Map[string] `json:"cursors"`
	// DMCursorTS is the Slack ts the DM scan has read up to (less its overlap); nil before the
	// first one.
	DMCursorTS *string `json:"dmCursorTs"`
	// PRs are by key, `owner/repo#n` lowercase.
	PRs *js.Map[*Entry] `json:"prs"`
	// Found are by key: what the scans found and the fetches have not checked yet (at most
	// MaxFound).
	Found *js.Map[*Found] `json:"found"`
	// Legacy are by old PR number: version 1 records whose repo could not be worked out. Kept,
	// never shown, never fetched.
	Legacy *js.Map[*Entry] `json:"legacy"`
}

// EmptyFile is a file with nothing in it.
func EmptyFile() *File {
	return &File{
		Version: fileVersion, Cursors: js.NewMap[string](), DMCursorTS: nil,
		PRs: js.NewMap[*Entry](), Found: js.NewMap[*Found](), Legacy: js.NewMap[*Entry](),
	}
}

// ---- the key ----

// PRKey is the key of a PR: `owner/repo#n`, lowercase. An error for a repo that is not
// `owner/repo` or a number that is not a positive safe integer.
func PRKey(repo string, number int64) (string, error) {
	if !repos.IsName(repo) {
		return "", errNotRepo
	}

	if !safeNumber(number) {
		return "", errNotNumber
	}

	return keyOf(repo, number), nil
}

// KeyParts is a key's repo and number.
type KeyParts struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
}

// ParseKey is a key's repo and number, and false when it is not one (wrong shape, a bad repo, a
// number that is not plain digits).
func ParseKey(key string) (KeyParts, bool) {
	match := keyShape.FindStringSubmatch(key)
	if match == nil {
		return KeyParts{Repo: "", Number: 0}, false
	}

	if !repos.IsName(match[1]) {
		return KeyParts{Repo: "", Number: 0}, false
	}

	if match[1] != strings.ToLower(match[1]) {
		return KeyParts{Repo: "", Number: 0}, false
	}

	number, ok := digitsNumber(match[2])
	if !ok {
		return KeyParts{Repo: "", Number: 0}, false
	}

	return KeyParts{Repo: match[1], Number: number}, true
}

// digitsNumber is Number(text) of plain digits, when that is a safe integer.
func digitsNumber(text string) (int64, bool) {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}

	if value > maxSafeInteger {
		return 0, false
	}

	return int64(value), true
}

func safeNumber(number int64) bool {
	if number <= 0 {
		return false
	}

	return number <= maxSafeInteger
}

// ---- reading ----

func object(value any) *js.Object {
	found, _ := value.(*js.Object)

	return found
}

func member(value *js.Object, key string) (any, bool) {
	return value.Get(key)
}

func memberOf(value *js.Object, key string) any {
	found, _ := value.Get(key)

	return found
}

// number is a finite number.
func number(value any) (float64, bool) {
	found, isNumber := value.(float64)
	if !isNumber {
		return 0, false
	}

	if math.IsNaN(found) {
		return 0, false
	}

	if math.IsInf(found, 0) {
		return 0, false
	}

	return found, true
}

// numberOrNull reads a field that must be null or a finite number (absent is neither).
func numberOrNull(value any, present bool) (*float64, bool) {
	if !present {
		return nil, false
	}

	if value == nil {
		return nil, true
	}

	found, ok := number(value)
	if !ok {
		return nil, false
	}

	return &found, true
}

func text(value any) (string, bool) {
	found, isText := value.(string)

	return found, isText
}

func asLaunch(value any) *Launch {
	raw := object(value)
	if raw == nil {
		return nil
	}

	when, atOK := number(memberOf(raw, "at"))
	model, _ := text(memberOf(raw, "model"))
	name, nameOK := text(memberOf(raw, "name"))
	headSha, headOK := text(memberOf(raw, "headSha"))
	worktree, worktreeOK := text(memberOf(raw, "worktree"))

	if !all(atOK, isModel(model), nameOK, headOK, worktreeOK) {
		return nil
	}

	sessionID, sessionOK := sessionOf(raw)
	closedValue, closedPresent := member(raw, "closedAt")
	closedAt, closedOK := numberOrNull(closedValue, closedPresent)

	if !sessionOK {
		return nil
	}

	if !closedOK {
		return nil
	}

	clone, _ := text(memberOf(raw, "clone"))
	if !strings.HasPrefix(clone, "/") {
		clone = ""
	}

	if strings.Contains(clone, "\x00") {
		clone = ""
	}

	return &Launch{
		At: when, Model: model, Name: name, HeadSha: headSha, Worktree: worktree,
		SessionID: sessionID, ClosedAt: closedAt, Clone: clone,
	}
}

func sessionOf(raw *js.Object) (*string, bool) {
	value, present := member(raw, "sessionId")
	if !present {
		return nil, false
	}

	if value == nil {
		return nil, true
	}

	found, isText := text(value)
	if !isText {
		return nil, false
	}

	return &found, true
}

func isModel(model string) bool {
	if model == ModelSonnet {
		return true
	}

	return model == ModelOpus
}

// all is whether every check passed.
func all(checks ...bool) bool {
	for _, check := range checks {
		if !check {
			return false
		}
	}

	return true
}

func asScan(value any) (Scan, bool) {
	raw := object(value)
	if raw == nil {
		return Scan{Tries: 0, LastAt: 0}, false
	}

	tries, triesOK := number(memberOf(raw, "tries"))
	lastAt, lastOK := number(memberOf(raw, "lastAt"))

	if !all(triesOK, lastOK) {
		return Scan{Tries: 0, LastAt: 0}, false
	}

	return Scan{Tries: tries, LastAt: lastAt}, true
}

func asEnded(value any) *Ended {
	raw := object(value)
	if raw == nil {
		return nil
	}

	when, atOK := number(memberOf(raw, "at"))
	if !atOK {
		return nil
	}

	how, _ := text(memberOf(raw, "how"))

	switch how {
	case EndedMerged, EndedClosed, endedGone:
		return &Ended{How: how, At: when}
	}

	return nil
}

// asSlack reads an entry's post: nil for null, false for a bad one (or none at all).
func asSlack(value any, present bool) (*SlackPost, bool) {
	if !present {
		return nil, false
	}

	if value == nil {
		return nil, true
	}

	raw := object(value)
	if raw == nil {
		return nil, false
	}

	stamp, tsOK := text(memberOf(raw, "ts"))
	permalink, linkOK := text(memberOf(raw, "permalink"))
	postedAt, postedOK := number(memberOf(raw, "postedAt"))

	if !all(tsOK, linkOK, postedOK) {
		return nil, false
	}

	// A version 1 post has no channel: it came from the one channel Conn read then, so it counts
	// for whichever channel is chosen.
	var channel *string

	name, _ := text(memberOf(raw, "channel"))
	if channelName.MatchString(name) {
		channel = &name
	}

	return &SlackPost{TS: stamp, Permalink: permalink, PostedAt: postedAt, Channel: channel}, true
}

// asDM reads an entry's DM: nil for null or absent (a file from before the DM scan), false for a
// bad one.
func asDM(value any) (*DMPost, bool) {
	if value == nil {
		return nil, true
	}

	raw := object(value)
	if raw == nil {
		return nil, false
	}

	stamp, tsOK := text(memberOf(raw, "ts"))
	permalink, linkOK := text(memberOf(raw, "permalink"))
	postedAt, postedOK := number(memberOf(raw, "postedAt"))

	if !all(tsOK, linkOK, postedOK) {
		return nil, false
	}

	return &DMPost{TS: stamp, Permalink: permalink, PostedAt: postedAt}, true
}
