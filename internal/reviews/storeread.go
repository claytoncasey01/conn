package reviews

import (
	"errors"
	"io/fs"
	"os"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// asEntry is one record, of either version: the fields version 2 added are optional and take
// their defaults.
func asEntry(value any) *Entry {
	raw := object(value)
	if raw == nil {
		return nil
	}

	firstSeenAt, firstOK := number(memberOf(raw, "firstSeenAt"))
	askValue, askPresent := member(raw, "slackAskAt")
	slackAskAt, askOK := numberOrNull(askValue, askPresent)
	launches, launchesOK := memberOf(raw, "launches").([]any)

	if !all(firstOK, askOK, launchesOK) {
		return nil
	}

	// A file from before the DM scan has neither DM field: that is null, not a bad record.
	lastValue, lastPresent := member(raw, "dmLastAt")

	dmLastAt, lastOK := numberOrNull(lastValue, lastPresent)
	if !lastPresent {
		lastOK = true
	}

	slackValue, slackPresent := member(raw, "slack")
	slack, slackOK := asSlack(slackValue, slackPresent)
	direct, dmOK := asDM(memberOf(raw, "dm"))
	scan, scanOK := asScan(memberOf(raw, "scan"))
	threadScan, threadOK := asScan(memberOf(raw, "threadScan"))
	headSha, headOK := text(memberOf(object(memberOf(raw, "threadScan")), "headSha"))

	if !all(lastOK, slackOK, dmOK, scanOK, threadOK, headOK) {
		return nil
	}

	entry := &Entry{
		Slack: slack, SlackAskAt: slackAskAt, DM: direct, DMLastAt: dmLastAt,
		FirstSeenAt: firstSeenAt, Launches: readLaunches(launches), Scan: scan,
		ThreadScan: ThreadScan{
			Tries: threadScan.Tries, LastAt: threadScan.LastAt, HeadSha: headSha,
		},
		Requested: memberOf(raw, "requested") == true, Ended: asEnded(memberOf(raw, "ended")),
		SeenAt: firstSeenAt, PlacedAt: nil,
	}

	if seenAt, ok := number(memberOf(raw, "seenAt")); ok {
		entry.SeenAt = seenAt
	}

	if placedAt, ok := number(memberOf(raw, "placedAt")); ok {
		entry.PlacedAt = &placedAt
	}

	return entry
}

func readLaunches(values []any) []*Launch {
	launches := []*Launch{}

	for _, value := range values {
		launch := asLaunch(value)
		if launch != nil {
			launches = append(launches, launch)
		}
	}

	return launches
}

func asFound(value any) *Found {
	raw := object(value)
	if raw == nil {
		return nil
	}

	when, atOK := number(memberOf(raw, "at"))
	if !atOK {
		return nil
	}

	slack, slackOK := asFoundSlack(memberOf(raw, "slack"))
	direct, dmOK := asFoundDM(memberOf(raw, "dm"))

	if !all(slackOK, dmOK) {
		return nil
	}

	if slack == nil {
		if direct == nil {
			return nil
		}
	}

	return &Found{At: when, Slack: slack, DM: direct}
}

func asFoundSlack(value any) (*FoundSlack, bool) {
	if value == nil {
		return nil, true
	}

	raw := object(value)
	if raw == nil {
		return nil, false
	}

	stamp, _ := text(memberOf(raw, "ts"))
	permalink, linkOK := text(memberOf(raw, "permalink"))
	channel, _ := text(memberOf(raw, "channel"))

	if !all(slackTS.MatchString(stamp), linkOK, channelName.MatchString(channel)) {
		return nil, false
	}

	return &FoundSlack{TS: stamp, Permalink: permalink, Channel: channel}, true
}

func asFoundDM(value any) (*FoundDM, bool) {
	if value == nil {
		return nil, true
	}

	raw := object(value)
	if raw == nil {
		return nil, false
	}

	first, _ := text(memberOf(raw, "firstTs"))
	last, _ := text(memberOf(raw, "lastTs"))
	permalink, linkOK := text(memberOf(raw, "permalink"))

	if !all(slackTS.MatchString(first), slackTS.MatchString(last), linkOK) {
		return nil, false
	}

	return &FoundDM{FirstTS: first, Permalink: permalink, LastTS: last}, true
}

// records are the records of an object, each under a key ok accepts, a bad record dropped alone.
func records(value any, accepted func(key string) bool) *js.Map[*Entry] {
	out := js.NewMap[*Entry]()

	raw := object(value)
	if raw == nil {
		return out
	}

	for _, key := range raw.Keys() {
		if !accepted(key) {
			continue
		}

		entry := asEntry(memberOf(raw, key))
		if entry != nil {
			out.Set(key, entry)
		}
	}

	return out
}

func isKey(key string) bool {
	_, ok := ParseKey(key)

	return ok
}

// readV2 is a version 2 file's content; anything else in a field is dropped alone.
func readV2(raw *js.Object) *File {
	out := EmptyFile()

	if cursors := object(memberOf(raw, "cursors")); cursors != nil {
		for _, channel := range cursors.Keys() {
			ts, _ := text(memberOf(cursors, channel))
			if all(channelName.MatchString(channel), slackTS.MatchString(ts)) {
				out.Cursors.Set(channel, ts)
			}
		}
	}

	// The DM scan does arithmetic on its cursor, so only a Slack ts is kept.
	if cursor, _ := text(memberOf(raw, "dmCursorTs")); slackTS.MatchString(cursor) {
		out.DMCursorTS = &cursor
	}

	out.PRs = records(memberOf(raw, "prs"), isKey)
	out.Legacy = records(memberOf(raw, "legacy"), numberKey.MatchString)

	if found := object(memberOf(raw, "found")); found != nil {
		for _, key := range found.Keys() {
			if !isKey(key) {
				continue
			}

			discovery := asFound(memberOf(found, key))
			if discovery == nil {
				continue
			}

			if out.Found.Len() < FoundMax {
				out.Found.Set(key, discovery)
			}
		}
	}

	return out
}

// V1File is a version 1 file as it was: by PR number, one channel's cursor, the DM cursor.
type V1File struct {
	CursorTS   *string         `json:"cursorTs"`
	DMCursorTS *string         `json:"dmCursorTs"`
	PRs        *js.Map[*Entry] `json:"prs"`
}

// The kinds of thing ReadFile finds.
const (
	ReadV2         = "v2"
	ReadV1         = "v1"
	ReadNone       = "none"
	ReadUnreadable = "unreadable"
)

// Read is what is at a path: a version 2 file, a version 1 file (to migrate), nothing, or a file
// that is there and cannot be used, with why.
type Read struct {
	Kind string
	V2   *File
	V1   *V1File
	Why  string
}

// ReadFile is what is at path: a version 2 file, a version 1 file (to migrate), nothing (no
// file), or a file that is there and cannot be used (unreadable, not JSON, not an object,
// another version): that one must be kept before anything is written over it.
func ReadFile(path string) Read {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Read{Kind: ReadNone, V2: nil, V1: nil, Why: ""}
		}

		code, known := settings.NodeCode(err)
		if !known {
			code = settings.NodeError(err)
		}

		return unreadable("it could not be read (" + code + ")")
	}

	return ReadText(data)
}

// ReadText is what data, the text of a reviews.json, holds: as ReadFile.
func ReadText(data []byte) Read {
	parsed, err := js.Parse([]byte(js.DecodeUTF8(data, false)))
	if err != nil {
		return unreadable("it is not valid JSON")
	}

	raw := object(parsed)
	if raw == nil {
		return unreadable("it is not a JSON object")
	}

	version, present := member(raw, "version")
	if version == float64(fileVersion) {
		return Read{Kind: ReadV2, V2: readV2(raw), V1: nil, Why: ""}
	}

	if version != float64(1) {
		shown := "none"
		if present {
			shown = js.Stringify(version)
		}

		return unreadable("it is version " + shown + ", which this conn does not read")
	}

	return Read{Kind: ReadV1, V2: nil, V1: readV1(raw), Why: ""}
}

func unreadable(why string) Read {
	return Read{Kind: ReadUnreadable, V2: nil, V1: nil, Why: why}
}

func readV1(raw *js.Object) *V1File {
	out := &V1File{CursorTS: nil, DMCursorTS: nil, PRs: nil}

	if cursor, _ := text(memberOf(raw, "cursorTs")); slackTS.MatchString(cursor) {
		out.CursorTS = &cursor
	}

	if cursor, _ := text(memberOf(raw, "dmCursorTs")); slackTS.MatchString(cursor) {
		out.DMCursorTS = &cursor
	}

	out.PRs = records(memberOf(raw, "prs"), numberKey.MatchString)

	return out
}

// LoadFile reads the version 2 file at path: anything else (a version 1 file included) is an
// empty one, and a bad record is dropped alone.
func LoadFile(path string) *File {
	return loaded(ReadFile(path))
}

// LoadText is the version 2 file data holds, as LoadFile.
func LoadText(data []byte) *File {
	return loaded(ReadText(data))
}

func loaded(read Read) *File {
	if read.Kind == ReadV2 {
		return read.V2
	}

	return EmptyFile()
}
