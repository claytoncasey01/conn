package reviews

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/nodepath"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// privateDirMode and privateFileMode keep the file and its folder the owner's alone.
	privateDirMode  = 0o700
	privateFileMode = 0o600
	// maxBackups is how many copies are kept under one name before Conn gives up and keeps the
	// file as it is.
	maxBackups = 100
	// randomBytes is how much randomness a temp file's name carries.
	randomBytes = 6
	// retryText is what a person can do about a store that could not be opened: Conn opens it
	// again on those, no restart needed.
	retryText = "Nothing is saved and REVIEW waits until it can be: fix that, then save " +
		"Settings or press REFRESH on Reviews to try again"
)

var errTooManyCopies = errors.New("move some away")

// Marshal is the file as JSON.stringify(f, null, 2) writes it, ended with a newline.
func (f *File) Marshal() []byte {
	var out bytes.Buffer

	err := json.Indent(&out, js.Marshal(f), "", "  ")
	if err != nil {
		panic("reviews: the file's JSON did not indent: " + err.Error())
	}

	out.WriteByte('\n')

	return out.Bytes()
}

// tempName is a name beside path that nothing else uses: the process and some randomness.
func tempName(path string) string {
	random := make([]byte, randomBytes)
	_, _ = rand.Read(random)

	return path + "." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(random) + ".tmp"
}

// SaveFile writes the file atomically: a temp file in the same folder, then a rename, so a crash
// or a failed write leaves the previous file whole. Owner-only (0700 folder, 0600 file). An
// error when it cannot write; the old file is then untouched and no temp file is left. rename is
// a seam for tests (nil is os.Rename).
func SaveFile(path string, file *File, rename func(from, to string) error) error {
	if rename == nil {
		rename = os.Rename
	}

	folder := filepath.Dir(path)
	temp := tempName(path)

	err := writePrivate(folder, temp, file.Marshal())
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
	err := os.MkdirAll(folder, privateDirMode)
	if err != nil {
		return fmt.Errorf("making the folder: %w", err)
	}

	// The mode only applies to a folder this call creates; one made earlier may be looser.
	err = os.Chmod(folder, privateDirMode)
	if err != nil {
		return fmt.Errorf("making the folder private: %w", err)
	}

	err = os.WriteFile(temp, data, privateFileMode)
	if err != nil {
		return fmt.Errorf("writing the file: %w", err)
	}

	err = os.Chmod(temp, privateFileMode)
	if err != nil {
		return fmt.Errorf("making the file private: %w", err)
	}

	return nil
}

// ---- the migration ----

// MigrateContext is what placing a version 1 file needs.
type MigrateContext struct {
	// Allow is the allowlist as saved: every candidate repo must be on it, and its exact
	// patterns are candidates themselves.
	Allow []string
	// Seen are repos Conn has seen (live sessions, My PRs, History): candidates too, when the
	// list covers them.
	Seen []string
	// OriginOf is a folder's GitHub origin, lowercase, from its git config ("" for none).
	OriginOf func(folder string) string
	// Exists is whether a path exists (nil: os.Stat).
	Exists func(path string) bool
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// PlaceFile is the one repo a version 1 file was about, or "". Version 1 read a single review
// repo, so the file is placed whole, never record by record: (1) the origins of the launch
// worktrees still on disk, when there is at least one and they all agree; (2) with no origin at
// all, the clone folder names the launch paths hold (`reviews/pr-<n>/<folder>`), matched against
// the repo segment of the candidates, when exactly one repo matches for the whole file.
func PlaceFile(legacy *V1File, ctx MigrateContext) string {
	isThere := ctx.Exists
	if isThere == nil {
		isThere = exists
	}

	var launches []*Launch

	for _, key := range legacy.PRs.Keys() {
		entry, _ := legacy.PRs.Get(key)
		launches = append(launches, entry.Launches...)
	}

	origins := js.NewMap[bool]()

	for _, launch := range launches {
		if !placeable(launch.Worktree, isThere) {
			continue
		}

		found := ctx.OriginOf(launch.Worktree)
		if repos.IsName(found) {
			origins.Set(strings.ToLower(found), true)
		}
	}

	if origins.Len() > 1 {
		return ""
	}

	if origins.Len() == 1 {
		only := origins.Keys()[0]
		if repos.Allowed(only, ctx.Allow) {
			return only
		}

		return ""
	}

	return placeByFolder(launches, ctx)
}

func placeable(worktree string, isThere func(string) bool) bool {
	if !strings.HasPrefix(worktree, "/") {
		return false
	}

	if strings.Contains(worktree, "\x00") {
		return false
	}

	return isThere(worktree)
}

// placeCandidates are the repos a version 1 file may be about, lowercase, in the order found:
// the allowlist's exact patterns, then the repos Conn has seen.
func placeCandidates(ctx MigrateContext) *js.Map[bool] {
	candidates := js.NewMap[bool]()

	for _, pattern := range ctx.Allow {
		if strings.HasSuffix(pattern, "/*") {
			continue
		}

		if repos.IsName(pattern) {
			candidates.Set(strings.ToLower(pattern), true)
		}
	}

	for _, repo := range ctx.Seen {
		if repos.IsName(repo) {
			candidates.Set(strings.ToLower(repo), true)
		}
	}

	return candidates
}

// placeByFolder matches the clone folder names the launch paths hold against the repo segment of
// the candidates.
func placeByFolder(launches []*Launch, ctx MigrateContext) string {
	candidates := placeCandidates(ctx)

	folders := map[string]bool{}

	for _, launch := range launches {
		folder := strings.ToLower(nodepath.Base(launch.Worktree))
		if folder != "" {
			folders[folder] = true
		}
	}

	var matched []string

	for _, candidate := range candidates.Keys() {
		if !repos.Allowed(candidate, ctx.Allow) {
			continue
		}

		_, name, _ := strings.Cut(candidate, "/")
		if folders[name] {
			matched = append(matched, candidate)
		}
	}

	if len(matched) == 1 {
		return matched[0]
	}

	return ""
}

// MigrateV1 is a version 1 file as version 2. The whole file goes under its one repo
// (PlaceFile), or every record goes to legacy under its old number. Nothing migrated is marked
// as requested of the reviewer: what lists a placed record is what GitHub says at its next read.
// The one channel cursor is dropped; the DM cursor carries over.
func MigrateV1(legacy *V1File, ctx MigrateContext) (*File, error) {
	out := EmptyFile()
	out.DMCursorTS = legacy.DMCursorTS
	repo := PlaceFile(legacy, ctx)

	for _, old := range legacy.PRs.Keys() {
		entry, _ := legacy.PRs.Get(old)
		copied := *entry
		copied.Requested = false

		if repo == "" {
			out.Legacy.Set(old, &copied)

			continue
		}

		number, _ := digitsNumber(old)

		key, err := PRKey(repo, number)
		if err != nil {
			return nil, err
		}

		out.PRs.Set(key, &copied)
	}

	return out, nil
}

// V1Backup is where a version 1 file is kept before it is migrated.
func V1Backup(path string) string { return path + ".v1.bak" }

// UnreadableBackup is where a file Conn cannot read is kept before it is replaced.
func UnreadableBackup(path string) string { return path + ".unreadable.bak" }

// NthBackup is the name of the nth copy kept under bak: bak itself, then `<name>.2.bak`,
// `<name>.3.bak`, ...
func NthBackup(bak string, nth int) string {
	if nth == 1 {
		return bak
	}

	return strings.TrimSuffix(bak, ".bak") + "." + strconv.Itoa(nth) + ".bak"
}

// backUp copies path aside and answers where it went: bak, or the first free numbered name after
// it, so an earlier copy is never overwritten. A copy already there that is byte for byte this
// file is the copy. Written through a temp file and a hard link, so a crash never leaves a half
// copy under a backup's name. An error when no copy can be made.
func backUp(path, bak string) (string, error) {
	temp := tempName(bak)

	defer func() { _ = os.Remove(temp) }()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", asIs(err)
	}

	err = copyExclusive(path, temp)
	if err != nil {
		return "", err
	}

	for nth := 1; nth <= maxBackups; nth++ {
		name := NthBackup(bak, nth)

		err = os.Link(temp, name)
		if err == nil {
			return name, nil
		}

		if !errors.Is(err, fs.ErrExist) {
			return "", asIs(err)
		}

		if sameFile(name, data) {
			return name, nil
		}
	}

	return "", fmt.Errorf("%d copies named %s are there already: %w", maxBackups,
		nodepath.Base(bak), errTooManyCopies)
}

func sameFile(name string, data []byte) bool {
	info, err := os.Lstat(name)
	if err != nil {
		return false
	}

	if !info.Mode().IsRegular() {
		return false
	}

	have, err := os.ReadFile(name)
	if err != nil {
		return false
	}

	return bytes.Equal(have, data)
}

// copyExclusive copies from to a new file to, made 0600.
func copyExclusive(from, dest string) error {
	source, err := os.Open(from)
	if err != nil {
		return asIs(err)
	}

	defer func() { _ = source.Close() }()

	target, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return asIs(err)
	}

	_, err = io.Copy(target, source)
	closeErr := target.Close()

	if err != nil {
		return fmt.Errorf("copying: %w", err)
	}

	if closeErr != nil {
		return fmt.Errorf("closing the copy: %w", closeErr)
	}

	return asIs(os.Chmod(dest, privateFileMode))
}

// Opened is a store as it was opened: the file, whether it may be written, how many version 1
// records were migrated (nil when there was no version 1 file), where an old file was kept, and
// why it is not writable.
type Opened struct {
	File     *File
	Writable bool
	Migrated *int
	KeptAs   string
	Error    string
}

// OpenFile is the file at path, migrated first when it is version 1: the old file is copied to
// reviews.json.v1.bak (or .v1.2.bak and on), then the version 2 file is written over it. A file
// that is there but cannot be used is copied to reviews.json.unreadable.bak and then starts
// empty. When a copy cannot be made, nothing is migrated or written: the file comes back empty
// and not writable, so no save can wipe the old one. An error only when a version 1 record has a
// number no key can hold.
func OpenFile(path string, ctx MigrateContext) (Opened, error) {
	read := ReadFile(path)

	switch read.Kind {
	case ReadV2:
		return Opened{File: read.V2, Writable: true, Migrated: nil, KeptAs: "", Error: ""}, nil
	case ReadNone:
		return Opened{File: EmptyFile(), Writable: true, Migrated: nil, KeptAs: "", Error: ""}, nil
	case ReadUnreadable:
		keptAs, err := backUp(path, UnreadableBackup(path))
		if err != nil {
			return notWritable("reviews.json is not usable (" + read.Why +
				") and could not be kept aside: " + settings.NodeError(err) + ". " + retryText), nil
		}

		return Opened{
			File: EmptyFile(), Writable: true, Migrated: nil, KeptAs: keptAs, Error: "",
		}, nil
	}

	file, err := MigrateV1(read.V1, ctx)
	if err != nil {
		return Opened{}, err
	}

	keptAs, err := backUp(path, V1Backup(path))
	if err == nil {
		err = SaveFile(path, file, nil)
	}

	if err != nil {
		return notWritable("reviews.json could not be migrated: " + settings.NodeError(err) +
			". " + retryText), nil
	}

	migrated := read.V1.PRs.Len()

	return Opened{File: file, Writable: true, Migrated: &migrated, KeptAs: keptAs, Error: ""}, nil
}

func notWritable(why string) Opened {
	return Opened{File: EmptyFile(), Writable: false, Migrated: nil, KeptAs: "", Error: why}
}

// placeAll places every legacy record at once.
const placeAll = "all"

// PlaceLegacy moves legacy records to repo (the person placed them on the Settings page): one
// number, or every one at once ("all"). A record whose key is already taken stays in legacy.
// Each record placed is stamped placedAt. Returns how many moved; 0 when the repo is not one.
func PlaceLegacy(file *File, which, repo string, now float64) (int, error) {
	if !repos.IsName(repo) {
		return 0, nil
	}

	numbers := []string{which}
	if which == placeAll {
		numbers = file.Legacy.Keys()
	}

	moved := 0

	for _, old := range numbers {
		entry, found := file.Legacy.Get(old)
		if !found {
			continue
		}

		number, _ := digitsNumber(old)

		key, err := PRKey(repo, number)
		if err != nil {
			return moved, err
		}

		if file.PRs.Has(key) {
			continue
		}

		placedAt := now
		entry.PlacedAt = &placedAt
		file.PRs.Set(key, entry)
		file.Legacy.Delete(old)

		moved++
	}

	return moved, nil
}

// TrackOf is the record for key, created (first seen now) when there is none.
func TrackOf(file *File, key string, now float64) *Entry {
	entry, found := file.PRs.Get(key)
	if found {
		return entry
	}

	entry = &Entry{
		Slack: nil, SlackAskAt: nil, DM: nil, DMLastAt: nil, FirstSeenAt: now,
		Launches: []*Launch{}, Scan: Scan{Tries: 0, LastAt: 0},
		ThreadScan: ThreadScan{Tries: 0, LastAt: 0, HeadSha: ""}, Requested: false, Ended: nil,
		SeenAt: now, PlacedAt: nil,
	}
	file.PRs.Set(key, entry)

	return entry
}

// touchedFixed is how many times touched reads from a record before its launches.
const touchedFixed = 8

// touched is the last time anything touched a record: seen open, a post, a DM, a re-ask, a launch
// or its close, the read that found it ended, a placing.
func touched(entry *Entry) float64 {
	values := make([]float64, 0, touchedFixed+2*len(entry.Launches))
	values = append(values,
		entry.FirstSeenAt, entry.SeenAt, valueOr(entry.PlacedAt, 0), 0, 0, 0, 0, 0)

	if entry.Ended != nil {
		values[3] = entry.Ended.At
	}

	if entry.Slack != nil {
		values[4] = entry.Slack.PostedAt
	}

	values[5] = valueOr(entry.SlackAskAt, 0)

	if entry.DM != nil {
		values[6] = entry.DM.PostedAt
	}

	values[7] = valueOr(entry.DMLastAt, 0)

	for _, launch := range entry.Launches {
		values = append(values, launch.At, valueOr(launch.ClosedAt, 0))
	}

	latest := values[0]
	for _, value := range values[1:] {
		latest = max(latest, value)
	}

	return latest
}

func valueOr(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}

	return *value
}

// Prune forgets the records of allowed repos that are known not to be open now and that nothing
// has touched for 14 days, and the discoveries a day old. isOpen(key) is true for a record that
// is open, or whose state this poll could not decide: only a read that answered may let a record
// go. keep holds any other record back (a worktree still on disk). A repo that is not allowed
// keeps every record as it is: hidden, not forgotten.
func Prune(file *File, isOpen func(key string) bool, allow []string, now float64,
	keep func(key string, entry *Entry) bool,
) {
	for _, key := range file.PRs.Keys() {
		parts, ok := ParseKey(key)
		if !ok {
			continue
		}

		if !repos.Allowed(parts.Repo, allow) {
			continue
		}

		if isOpen(key) {
			continue
		}

		entry, _ := file.PRs.Get(key)
		if now-touched(entry) < keepMs {
			continue
		}

		if keep != nil {
			if keep(key, entry) {
				continue
			}
		}

		file.PRs.Delete(key)
	}

	for _, key := range file.Found.Keys() {
		found, _ := file.Found.Get(key)
		if now-found.At >= foundKeepMs {
			file.Found.Delete(key)
		}
	}
}

// nodeError is a file error worded as Node words it, so a message that quotes it reads the same.
func nodeError(err error) error { return failure(settings.NodeError(err)) }
