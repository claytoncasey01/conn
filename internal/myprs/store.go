package myprs

// What Conn itself remembers about My PRs, in one small file: a record per PR with its
// Self-Review launches, the reply proposals and the Slack draft. Everything else is derived from
// GitHub, so the file can be rebuilt: losing it must never hurt, and a bad one is read as empty,
// never an error. Same shape and guarantees as reviews.json.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/claytoncasey01/conn/internal/js"
)

const (
	// keepMs is how long a PR that has not been listed is remembered; GitHub still knows it.
	keepMs = 14 * 86_400_000
	// maxTries bounds a saved count of tries: a larger one is a bad record.
	maxTries = 99
	// fileMode and folderMode keep myprs.json and its folder the owner's.
	fileMode   = 0o600
	folderMode = 0o700
	// randomBytes is how much randomness a temp file's name holds.
	randomBytes = 8
)

// The proposal states.
const (
	StateProposed = "proposed"
	StateReplied  = "replied"
	StatePosted   = "posted"
	StateSkipped  = "skipped"
	StateFailed   = "failed"
)

// The draft states.
const (
	DraftSaved  = "saved"
	DraftFailed = "failed"
)

var (
	// channelShape is a Slack channel as settings.json holds it.
	channelShape = regexp.MustCompile(`^#[a-z0-9._-]{1,80}$`)
	// keyShape is a record's key, `owner/repo#number`, as JavaScript's \s reads it.
	keyShape = regexp.MustCompile(`^[^/#` + js.SpaceClass + `]+/[^/#` + js.SpaceClass +
		`]+#[1-9]\d*$`)
)

// Launch is one Self-Review round started from Conn.
type Launch struct {
	At    float64 `json:"at"`
	Model string  `json:"model"`
	// Name is the cmux session name (self-<repo>-<number>, with -<round> after the first).
	Name     string `json:"name"`
	Worktree string `json:"worktree"`
	// HeadSha is the PR's head when the round started.
	HeadSha string  `json:"headSha"`
	Round   float64 `json:"round"`
	// Armed is that automation (the Greptile ask) may still run; it ends once the ask is made.
	Armed bool `json:"armed"`
	// AskedSha is the head the Greptile review was asked for.
	AskedSha *string `json:"askedSha"`
	// AskedAt is when it was asked (ms): bots are judged by what they did after this.
	AskedAt  *float64 `json:"askedAt"`
	ClosedAt *float64 `json:"closedAt"`
	// AskTry is the last Greptile ask tried for this launch, saved before the call: gh can give
	// up on a comment GitHub took, so a try is never known to have failed.
	AskTry *AskTry `json:"askTry"`
	// Manual is that cmux could not open the session, so it runs only once the command shown
	// was pasted into a terminal.
	Manual bool `json:"manual,omitempty"`
}

// AskTry is the last ask for Sha: At the last try, FirstAt the first for Sha (a comment from any
// of them settles the round), Tries how many (a new head starts again at one).
type AskTry struct {
	Sha     string  `json:"sha"`
	At      float64 `json:"at"`
	FirstAt float64 `json:"firstAt"`
	Tries   int     `json:"tries"`
}

// Proposal is a reply the session proposed for one thread, and what became of it.
type Proposal struct {
	ThreadID string  `json:"threadId"`
	Text     string  `json:"text"`
	State    string  `json:"state"`
	Error    *string `json:"error"`
	// LaunchAt is the At of the launch whose replies file proposed it; 0 for one a click made.
	LaunchAt float64 `json:"launchAt"`
	// TriedAt is when its reply was first tried and not known to have landed; nil once the
	// reply is known to be there.
	TriedAt *float64 `json:"triedAt,omitempty"`
}

// SlackDraft is the Slack draft saved for a ready head.
type SlackDraft struct {
	Sha   string  `json:"sha"`
	Text  string  `json:"text"`
	State string  `json:"state"`
	Error *string `json:"error"`
	At    float64 `json:"at"`
	// Tries are the attempts made for this sha, this one included.
	Tries int `json:"tries"`
	// Channel is where it was saved (or tried); "" in a record written before it was kept.
	Channel string `json:"channel,omitempty"`
}

// Entry is what Conn remembers of one PR.
type Entry struct {
	FirstSeenAt float64     `json:"firstSeenAt"`
	Launches    []*Launch   `json:"launches"`
	Proposals   []*Proposal `json:"proposals"`
	Draft       *SlackDraft `json:"draft"`
	SeenAt      float64     `json:"seenAt"`
}

// File is myprs.json.
type File struct {
	Version int `json:"version"`
	// PRs are by `owner/repo#number`.
	PRs *js.Map[*Entry] `json:"prs"`
}

// EntryKey is the key of a PR's record.
func EntryKey(repo string, number float64) string { return repo + "#" + js.Number(number) }

// EmptyFile is a file with no records.
func EmptyFile() *File { return &File{Version: 1, PRs: js.NewMap[*Entry]()} }

// ---- reading ----

func finiteNumber(value any) (float64, bool) {
	number, isNumber := value.(float64)
	if !isNumber {
		return 0, false
	}

	return number, isFinite(number)
}

// nullable reads a key that must be there, holding null or a value read is accepts.
func nullable[T any](record *js.Object, key string, read func(any) (T, bool)) (*T, bool) {
	value, present := record.Get(key)
	if !present {
		return nil, false
	}

	if value == nil {
		return nil, true
	}

	found, ok := read(value)
	if !ok {
		return nil, false
	}

	return &found, true
}

func textValue(value any) (string, bool) { return text(value) }

// orDefault is the value at key, or fallback when the key is not there.
func orDefault(record *js.Object, key string, fallback any) any {
	value, present := record.Get(key)
	if !present {
		return fallback
	}

	return value
}

// smallCount is a whole number of tries from 1 to maxTries.
func smallCount(value any) (int, bool) {
	number, ok := finiteNumber(value)
	if !ok {
		return 0, false
	}

	if math.Trunc(number) != number {
		return 0, false
	}

	if number < 1 {
		return 0, false
	}

	if number > maxTries {
		return 0, false
	}

	return int(number), true
}

// LoadFile reads the file at path. A missing, unreadable, malformed or other-version file is an
// empty one, and a bad record is dropped alone.
func LoadFile(path string) *File {
	data, err := os.ReadFile(path)
	if err != nil {
		return EmptyFile()
	}

	return LoadText(data)
}

// LoadText is the file data holds, as LoadFile.
func LoadText(data []byte) *File {
	raw, err := js.Parse([]byte(js.DecodeUTF8(data, false)))
	if err != nil {
		return EmptyFile()
	}

	if object(raw) == nil {
		return EmptyFile()
	}

	if field(raw, "version") != 1.0 {
		return EmptyFile()
	}

	out := EmptyFile()
	prs := object(field(raw, "prs"))

	for _, key := range prs.Keys() {
		if !keyShape.MatchString(key) {
			continue
		}

		value, _ := prs.Get(key)
		if entry := asEntry(value); entry != nil {
			out.PRs.Set(key, entry)
		}
	}

	return out
}

func asEntry(value any) *Entry {
	record := object(value)
	firstSeenAt, firstOK := finiteNumber(field(record, "firstSeenAt"))
	seenAt, seenOK := finiteNumber(field(record, "seenAt"))
	launches, launchesOK := field(record, "launches").([]any)
	proposals, proposalsOK := field(record, "proposals").([]any)

	for _, ok := range []bool{record != nil, firstOK, seenOK, launchesOK, proposalsOK} {
		if !ok {
			return nil
		}
	}

	var draft *SlackDraft

	if found := field(record, "draft"); found != nil {
		draft = asDraft(found)
		if draft == nil {
			return nil
		}
	} else if _, present := record.Get("draft"); !present {
		return nil
	}

	entry := &Entry{
		FirstSeenAt: firstSeenAt, Launches: []*Launch{}, Proposals: []*Proposal{},
		Draft: draft, SeenAt: seenAt,
	}

	for _, one := range launches {
		if launch := asLaunch(one); launch != nil {
			entry.Launches = append(entry.Launches, launch)
		}
	}

	for _, one := range proposals {
		if proposal := asProposal(one); proposal != nil {
			entry.Proposals = append(entry.Proposals, proposal)
		}
	}

	return entry
}

func asLaunch(value any) *Launch {
	record := object(value)
	if record == nil {
		return nil
	}

	launch := &Launch{
		At: 0, Model: "", Name: "", Worktree: "", HeadSha: "", Round: 0, Armed: false,
		AskedSha: nil, AskedAt: nil, ClosedAt: nil, AskTry: asAskTry(field(record, "askTry")),
		Manual: field(record, "manual") == true,
	}

	if !launchTexts(record, launch) {
		return nil
	}

	if !launchState(record, launch) {
		return nil
	}

	return launch
}

func launchTexts(record *js.Object, launch *Launch) bool {
	startedAt, atOK := finiteNumber(field(record, "at"))
	model, _ := text(field(record, "model"))
	name, nameOK := text(field(record, "name"))
	worktree, worktreeOK := text(field(record, "worktree"))
	headSha, headOK := text(field(record, "headSha"))

	for _, ok := range []bool{atOK, isModel(model), nameOK, worktreeOK, headOK} {
		if !ok {
			return false
		}
	}

	launch.At, launch.Model, launch.Name = startedAt, model, name
	launch.Worktree, launch.HeadSha = worktree, headSha

	return true
}

func launchState(record *js.Object, launch *Launch) bool {
	round, roundOK := finiteNumber(field(record, "round"))
	armed, armedOK := field(record, "armed").(bool)
	askedSha, askedShaOK := nullable(record, "askedSha", textValue)
	askedAt, askedAtOK := nullable(record, "askedAt", finiteNumber)
	closedAt, closedAtOK := nullable(record, "closedAt", finiteNumber)

	for _, ok := range []bool{roundOK, armedOK, askedShaOK, askedAtOK, closedAtOK} {
		if !ok {
			return false
		}
	}

	launch.Round, launch.Armed = round, armed
	launch.AskedSha, launch.AskedAt, launch.ClosedAt = askedSha, askedAt, closedAt

	return true
}

func isModel(model string) bool {
	switch model {
	case "sonnet", "opus":
		return true
	}

	return false
}

// asAskTry reads a launch's try: an older launch has none, and a bad one is read as none (it
// only bounds the asks, so the launch itself is kept).
func asAskTry(value any) *AskTry {
	record := object(value)
	if record == nil {
		return nil
	}

	sha, shaOK := text(field(record, "sha"))
	triedAt, atOK := finiteNumber(field(record, "at"))
	firstAt, firstOK := finiteNumber(orDefault(record, "firstAt", field(record, "at")))
	tries, triesOK := smallCount(field(record, "tries"))

	for _, ok := range []bool{shaOK, atOK, firstOK, triesOK} {
		if !ok {
			return nil
		}
	}

	return &AskTry{Sha: sha, At: triedAt, FirstAt: firstAt, Tries: tries}
}

func isProposalState(state string) bool {
	switch state {
	case StateProposed, StateReplied, StatePosted, StateSkipped, StateFailed:
		return true
	}

	return false
}

func asProposal(value any) *Proposal {
	record := object(value)
	threadID, threadOK := text(field(record, "threadId"))
	body, textOK := text(field(record, "text"))
	state, _ := text(field(record, "state"))
	why, whyOK := nullable(record, "error", textValue)
	launchAt, launchOK := finiteNumber(orDefault(record, "launchAt", 0.0))

	for _, ok := range []bool{
		record != nil, threadOK, textOK, isProposalState(state), whyOK,
		launchOK,
	} {
		if !ok {
			return nil
		}
	}

	proposal := &Proposal{
		ThreadID: threadID, Text: body, State: state, Error: why, LaunchAt: launchAt,
		TriedAt: nil,
	}

	// A bad try time only loses the wider check for a reply already on the thread: the
	// proposal is kept.
	if triedAt, ok := finiteNumber(field(record, "triedAt")); ok {
		proposal.TriedAt = &triedAt
	}

	return proposal
}

func isDraftState(state string) bool {
	switch state {
	case DraftSaved, DraftFailed:
		return true
	}

	return false
}

func asDraft(value any) *SlackDraft {
	record := object(value)
	sha, shaOK := text(field(record, "sha"))
	body, textOK := text(field(record, "text"))
	state, _ := text(field(record, "state"))
	why, whyOK := nullable(record, "error", textValue)
	madeAt, atOK := finiteNumber(field(record, "at"))
	// An older record has no count: it was one attempt.
	tries, triesOK := smallCount(orDefault(record, "tries", 1.0))
	stateOK := isDraftState(state)

	for _, ok := range []bool{record != nil, shaOK, textOK, stateOK, whyOK, atOK, triesOK} {
		if !ok {
			return nil
		}
	}

	draft := &SlackDraft{
		Sha: sha, Text: body, State: state, Error: why, At: madeAt, Tries: tries, Channel: "",
	}

	// A channel that is not one by the settings grammar is a bad hint only: the draft is kept
	// without it.
	if channel, isText := text(field(record, "channel")); isText {
		if channelShape.MatchString(channel) {
			draft.Channel = channel
		}
	}

	return draft
}

// ---- writing ----

// Marshal is the file as it is written: JSON.stringify with two spaces, and a newline.
func (f *File) Marshal() []byte {
	var out bytes.Buffer

	err := json.Indent(&out, js.Marshal(f), "", "  ")
	if err != nil {
		panic("myprs: the file's JSON did not indent: " + err.Error())
	}

	out.WriteByte('\n')

	return out.Bytes()
}

// SaveFile writes the file atomically: a temp file in the same folder, then a rename, so a
// crash or a failed write leaves the previous file whole. Owner-only (0700 folder, 0600 file).
// An error when it cannot write; the old file is then untouched and no temp file is left.
// rename is a seam for tests (nil is os.Rename).
func SaveFile(path string, file *File, rename func(from, to string) error) error {
	if rename == nil {
		rename = os.Rename
	}

	random := make([]byte, randomBytes)
	_, _ = rand.Read(random)
	temp := path + "." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(random) + ".tmp"

	err := writePrivate(filepath.Dir(path), temp, file.Marshal())
	if err == nil {
		err = rename(temp, path)
	}

	if err != nil {
		_ = os.Remove(temp)

		return err
	}

	return nil
}

func writePrivate(folder, temp string, data []byte) error {
	err := os.MkdirAll(folder, folderMode)
	if err != nil {
		return fmt.Errorf("making the folder: %w", err)
	}

	// The mode only applies to a folder this call creates; one made earlier may be looser.
	err = os.Chmod(folder, folderMode)
	if err != nil {
		return fmt.Errorf("making the folder private: %w", err)
	}

	err = os.WriteFile(temp, data, fileMode)
	if err != nil {
		return fmt.Errorf("writing the file: %w", err)
	}

	err = os.Chmod(temp, fileMode)
	if err != nil {
		return fmt.Errorf("making the file private: %w", err)
	}

	return nil
}

// TrackOf is the record for key, created (first seen now) when there is none.
func TrackOf(file *File, key string, now float64) *Entry {
	if entry, found := file.PRs.Get(key); found {
		return entry
	}

	entry := &Entry{
		FirstSeenAt: now, Launches: []*Launch{}, Proposals: []*Proposal{}, Draft: nil,
		SeenAt: now,
	}
	file.PRs.Set(key, entry)

	return entry
}

// Prune stamps the PRs on the list as seen, and forgets those that have been off it for 14
// days. covers says which entries the list speaks for: one it does not cover (a repo that is not
// on the allowlist, or that the poll did not search) is left exactly as it is, neither stamped
// nor forgotten, so a repo that leaves the list and comes back finds its record.
func Prune(file *File, open map[string]bool, now float64, covers func(key string) bool) {
	for _, key := range file.PRs.Keys() {
		if !covers(key) {
			continue
		}

		entry, _ := file.PRs.Get(key)
		if open[key] {
			entry.SeenAt = now

			continue
		}

		if now-entry.SeenAt >= keepMs {
			file.PRs.Delete(key)
		}
	}
}

// LaunchedIn is the model of the newest round in file that started the session name in worktree
// inside Conn and has not been closed, and true; or false when there is none. A round pasted
// into a terminal by hand (Manual) is not one Conn hosted.
func LaunchedIn(file *File, worktree, name string) (string, bool) {
	model, found := "", false

	for _, key := range file.PRs.Keys() {
		entry, _ := file.PRs.Get(key)

		for _, launch := range entry.Launches {
			if launchedAs(launch, worktree, name) {
				model, found = launch.Model, true
			}
		}
	}

	return model, found
}

// launchedAs is whether launch started the session name in worktree inside Conn, and has not
// been closed.
func launchedAs(launch *Launch, worktree, name string) bool {
	if launch.ClosedAt != nil {
		return false
	}

	if launch.Manual {
		return false
	}

	if launch.Worktree != worktree {
		return false
	}

	return launch.Name == name
}
