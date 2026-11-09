package sessions

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/transcript"
)

// A teammate is a claude of its own: it writes an ordinary transcript (its own session id) in
// the lead's project folder, and every line of it carries teamName and agentName. Transcript text
// is untrusted: nothing here fails loudly, and a team or agent name is only ever compared, never
// used as a path.

const (
	// headBytes and headLines bound how much of a transcript's head is read to find its tag: the
	// real first tagged line is within the first 6 lines, and one of them is 80 KB long.
	headBytes = 8 << 20
	headLines = 100
	// maxCandidates is how many new files one lookup or scan reads.
	maxCandidates = 200
	spawnChunk    = 1 << 20
	headChunk     = 64 << 10
	// maxLineBytes is the longest line read; a longer one is dropped unread.
	maxLineBytes  = 4 << 20
	sessionPrefix = "session-"
	// slackMs is how far a teammate's start may run ahead of its spawn: a minute for the clocks.
	slackMs = 60_000
	// maxSpawns is how many spawn results one transcript keeps.
	maxSpawns = 500
	// maxIndexes is how many folders' spawn indexes are kept.
	maxIndexes = 64
	// teamScanMs is how often a lead's folder is searched for new teammates, and how long a
	// teammate's lead lookup is kept.
	teamScanMs = 3000
	// teamLead is the agent name a team's lead carries on its own lines.
	teamLead = "team-lead"
)

// teamSuffix is the id prefix a session-<id> team name carries.
var teamSuffix = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)

// TeamTag is a teammate transcript's team and its own name.
type TeamTag struct {
	Team string
	Name string
}

// TeamHead is what the head of a transcript says: its tag (nil for none), the time of its first
// timestamped line (0 for none), and whether looking at the file again could change that (a tag
// was found, or the bound was reached).
type TeamHead struct {
	Tag     *TeamTag
	FirstAt float64
	Decided bool
}

// headReader reads whole lines from a transcript's head, a line over maxLineBytes skipped
// unread, so a hostile line cannot make it hold more than a chunk and a line.
type headReader struct {
	head     TeamHead
	lines    int
	skipping bool
	bounded  bool
	carry    []byte
}

func (h *headReader) found() bool {
	if h.head.Tag == nil {
		return false
	}

	return h.head.FirstAt != 0
}

func (h *headReader) done() bool {
	if h.bounded {
		return true
	}

	return h.found()
}

// ReadTeamHead reads the head of a transcript: the team and name of the first line that carries
// both as non-empty strings, and the time of its first timestamped line, until both are found,
// the file ends, or headLines lines or headBytes bytes are read.
func ReadTeamHead(path string) TeamHead {
	return readTeamHead(path, headBytes, headLines)
}

func readTeamHead(path string, maxBytes, maxLines int) TeamHead {
	reader := headReader{
		head: TeamHead{Tag: nil, FirstAt: 0, Decided: false}, lines: 0, skipping: false,
		bounded: false, carry: nil,
	}

	file, err := os.Open(path)
	if err != nil {
		return reader.head
	}

	defer func() { _ = file.Close() }()

	buffer := make([]byte, headChunk)
	position := 0

	for !reader.done() {
		if position >= maxBytes {
			reader.bounded = true

			break
		}

		got, err := file.ReadAt(buffer[:min(headChunk, maxBytes-position)], int64(position))
		if got <= 0 {
			break
		}

		position += got
		reader.chunk(buffer[:got], maxLines)

		if err != nil {
			if err != io.EOF {
				break
			}
		}
	}

	reader.head.Decided = reader.head.Tag != nil
	if reader.bounded {
		reader.head.Decided = true
	}

	return reader.head
}

func (h *headReader) chunk(read []byte, maxLines int) {
	data := append(slices.Clip(h.carry), read...)
	h.carry = nil

	for !h.done() {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			break
		}

		line := data[:newline]
		data = data[newline+1:]
		h.lines++

		if h.skipping {
			h.skipping = false
		} else {
			h.line(line)
		}

		if h.lines >= maxLines {
			h.bounded = true
		}
	}

	if len(data) > maxLineBytes {
		h.skipping = true
	} else {
		h.carry = bytes.Clone(data)
	}
}

// line reads one whole line: its time while none is known, its tag when it may hold one.
func (h *headReader) line(line []byte) {
	if len(line) > maxLineBytes {
		return
	}

	if h.head.FirstAt != 0 {
		if !bytes.Contains(line, []byte("teamName")) {
			return
		}
	}

	parsed, err := js.Parse([]byte(js.DecodeUTF8(line, false)))
	if err != nil {
		return
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return
	}

	if h.head.FirstAt == 0 {
		stamp, _ := object.Get("timestamp")
		if text, isText := stamp.(string); isText {
			h.head.FirstAt = orZero(js.DateParse(text))
		}
	}

	if h.head.Tag == nil {
		h.head.Tag = tagOf(object)
	}
}

// tagOf is a line's team and agent name when both are non-empty strings.
func tagOf(object *js.Object) *TeamTag {
	team, _ := object.Get("teamName")
	name, _ := object.Get("agentName")
	teamText, _ := team.(string)
	nameText, _ := name.(string)

	if teamText == "" {
		return nil
	}

	if nameText == "" {
		return nil
	}

	return &TeamTag{Team: teamText, Name: nameText}
}

// orZero is value || 0: NaN as 0.
func orZero(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}

	return value
}

// spawnRead is one transcript read for its spawn results, from where the last read ended.
type spawnRead struct {
	offset   int64
	carry    []byte
	skipping bool
	spawns   []transcript.Spawn
}

// spawnIndex is the teammate_spawned results in the transcripts of one folder, each file read
// once and then only from where it grew.
type spawnIndex struct {
	dir   string
	files map[string]*spawnRead
}

// spawned is the spawn results a transcript holds so far.
func (x *spawnIndex) spawned(path string) []transcript.Spawn {
	entry, found := x.files[path]
	if !found {
		entry = &spawnRead{offset: 0, carry: nil, skipping: false, spawns: nil}
		x.files[path] = entry
	}

	info, err := os.Stat(path)
	if err != nil {
		return entry.spawns
	}

	size := info.Size()
	if size < entry.offset {
		*entry = spawnRead{offset: 0, carry: nil, skipping: false, spawns: nil}
	}

	if size == entry.offset {
		return entry.spawns
	}

	file, err := os.Open(path)
	if err != nil {
		return entry.spawns
	}

	defer func() { _ = file.Close() }()

	for entry.offset < size {
		buffer := make([]byte, min(spawnChunk, size-entry.offset))

		got, _ := file.ReadAt(buffer, entry.offset)
		if got <= 0 {
			break
		}

		entry.offset += int64(got)
		entry.read(buffer[:got])
	}

	return entry.spawns
}

func (e *spawnRead) read(chunk []byte) {
	data := append(slices.Clip(e.carry), chunk...)
	e.carry = nil

	for {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			break
		}

		line := data[:newline]
		data = data[newline+1:]

		if e.skipping {
			e.skipping = false

			continue
		}

		if bytes.Contains(line, []byte("teammate_spawned")) {
			e.spawn(line)
		}
	}

	if e.skipping {
		return
	}

	if len(data) > maxLineBytes {
		e.skipping = true

		return
	}

	e.carry = bytes.Clone(data)
}

func (e *spawnRead) spawn(line []byte) {
	parsed, err := js.Parse([]byte(js.DecodeUTF8(line, false)))
	if err != nil {
		return
	}

	spawn, found := transcript.SpawnOf(parsed)
	if !found {
		return
	}

	if len(e.spawns) < maxSpawns {
		e.spawns = append(e.spawns, spawn)
	}
}

// isRead is whether the file has been read to its current end, so looking at it costs nothing.
func (x *spawnIndex) isRead(path string, size int64) bool {
	entry, found := x.files[path]
	if !found {
		return false
	}

	return entry.offset >= size
}

// candidate is a transcript in the folder that may be a lead.
type candidate struct {
	path    string
	mtimeMs float64
	size    int64
}

// mtimeMs is a file's modification time in ms, as Node's stat gives it.
func mtimeMs(info os.FileInfo) float64 {
	return float64(info.ModTime().UnixNano()) / float64(time.Millisecond)
}

// How likely a candidate is to be the lead: read first, named like the team, any other.
const (
	rankFirst = iota
	rankNamed
	rankOther
)

// transcripts are the .jsonl files of a folder touched since sinceMs, other than skip (a name),
// in the folder's own order.
func transcripts(dir, skip string, sinceMs float64) ([]candidate, []string, bool) {
	names := listDir(dir)
	if names == nil {
		_, err := os.Stat(dir)
		if err != nil {
			return nil, nil, false
		}
	}

	present := []string{}
	found := []candidate{}

	for _, name := range names {
		if !strings.HasSuffix(name, jsonlExt) {
			continue
		}

		path := filepath.Join(dir, name)
		present = append(present, path)

		if name == skip {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}

		if mtimeMs(info) >= sinceMs {
			found = append(found, candidate{path: path, mtimeMs: mtimeMs(info), size: info.Size()})
		}
	}

	return found, present, true
}

// hit is a file whose spawn names a teammate.
type hit struct {
	path    string
	at      float64
	mtimeMs float64
}

// leadQuery is one lookup: the teammate's team and name, its own file, the time window, the read
// budget, its first line's time and the lead read first ("" for none).
type leadQuery struct {
	team, name, own string
	sinceMs         float64
	limit           int
	firstAt         float64
	first           string
}

// lead is the transcript in this folder that spawned this teammate, other than its own. Only
// files touched since sinceMs count. Of the files whose spawn names this teammate, those whose
// spawn is not later than the teammate's own start are candidates and the nearest spawn before
// it wins (then the older file, then its name); when none names it the file that spawned into the
// team is the answer only if it is the only one. limit caps only the files that have to be read,
// never which are considered.
func (x *spawnIndex) lead(query leadQuery) string {
	found, present, readable := transcripts(x.dir, filepath.Base(query.own), query.sinceMs)
	if !readable {
		return ""
	}

	// A file rewritten in place keeps the spawns it was first read with; transcripts are only
	// ever appended to.
	for path := range x.files {
		if !slices.Contains(present, path) {
			delete(x.files, path)
		}
	}

	rank := x.ranker(query)

	js.Sort(found, func(left, right candidate) float64 {
		if order := rank(left) - rank(right); order != 0 {
			return order
		}

		if order := left.mtimeMs - right.mtimeMs; order != 0 {
			return order
		}

		return float64(strings.Compare(left.path, right.path))
	})

	exact, spawners, only := x.spawners(found, query)
	if len(exact) > 0 {
		return nearest(exact, query.firstAt)
	}

	// Nothing names this teammate: only a team with one spawner can be told apart without a
	// name.
	if spawners == 1 {
		return only
	}

	return ""
}

// ranker puts the lead's own file (when the caller is that lead's scan) and the file named like
// the team first, so the read budget reaches them before the rest.
func (x *spawnIndex) ranker(query leadQuery) func(candidate) float64 {
	suffix := ""
	if rest, found := strings.CutPrefix(query.team, sessionPrefix); found {
		suffix = strings.ToLower(rest)
	}

	return func(file candidate) float64 {
		if query.first != "" {
			if filepath.Base(file.path) == filepath.Base(query.first) {
				return rankFirst
			}
		}

		if teamSuffix.MatchString(suffix) {
			id := strings.ToLower(strings.TrimSuffix(filepath.Base(file.path), jsonlExt))
			if strings.HasPrefix(id, suffix) {
				return rankNamed
			}
		}

		return rankOther
	}
}

// spawners reads the candidates' spawns: the hits naming this teammate, how many files spawned
// into its team, and the last of them.
func (x *spawnIndex) spawners(found []candidate, query leadQuery) ([]hit, int, string) {
	exact := []hit{}
	spawners := map[string]bool{}
	only := ""
	reads := 0

	for _, file := range found {
		if !x.isRead(file.path, file.size) {
			if reads >= query.limit {
				continue
			}

			reads++
		}

		for _, spawn := range x.spawned(file.path) {
			if spawn.Team != query.team {
				continue
			}

			spawners[file.path] = true
			only = file.path

			if spawn.Name == query.name {
				exact = append(exact, hit{path: file.path, at: spawn.At, mtimeMs: file.mtimeMs})
			}
		}
	}

	return exact, len(spawners), only
}

// nearest is the spawner of a teammate: a spawn recorded after the teammate began cannot be its
// spawner, so of the rest the nearest before it wins (a time-less spawn ranks last); when every
// spawn is later than the start (clocks that disagree), the earliest.
func nearest(exact []hit, firstAt float64) string {
	tie := func(left, right hit) float64 {
		if order := left.mtimeMs - right.mtimeMs; order != 0 {
			return order
		}

		return float64(strings.Compare(left.path, right.path))
	}

	before := []hit{}

	for _, spawn := range exact {
		if spawn.at == 0 {
			before = append(before, spawn)

			continue
		}

		if firstAt == 0 {
			before = append(before, spawn)

			continue
		}

		if spawn.at <= firstAt+slackMs {
			before = append(before, spawn)
		}
	}

	if len(before) > 0 {
		js.Sort(before, func(left, right hit) float64 {
			if order := right.at - left.at; order != 0 {
				return order
			}

			return tie(left, right)
		})

		return before[0].path
	}

	js.Sort(exact, func(left, right hit) float64 {
		if order := left.at - right.at; order != 0 {
			return order
		}

		return tie(left, right)
	})

	return exact[0].path
}

// Leads finds the lead of a teammate transcript, keeping one spawn index per folder (the oldest
// dropped past maxIndexes). It is safe to use from several goroutines.
type Leads struct {
	mutex   sync.Mutex
	indexes map[string]*spawnIndex
	order   []string
}

// NewLeads is an empty set of spawn indexes.
func NewLeads() *Leads {
	return &Leads{mutex: sync.Mutex{}, indexes: map[string]*spawnIndex{}, order: nil}
}

// LeadOpts tune a lookup: Limit caps the new files read (0 for the default 200, a negative
// number for no cap), FirstAt is the teammate's first line when the caller has read it (nil to
// read it now), First is the lead whose scan this is, read first.
type LeadOpts struct {
	Limit   int
	FirstAt *float64
	First   string
}

// Path is the lead of a teammate transcript: another transcript in the same folder that spawned
// this teammate (a teammate_spawned result with its team name and its name), else one that
// spawned into its team. Not decided by file names: a resumed lead has a new id, and a later
// session may spawn into the same team. "" for team-lead, when there is no such file, and for the
// transcript itself; "" too when the limit of new reads ran out first.
func (l *Leads) Path(path string, tag TeamTag, opts LeadOpts) string {
	if tag.Name == teamLead {
		return ""
	}

	dir := filepath.Dir(path)

	l.mutex.Lock()
	defer l.mutex.Unlock()

	index, found := l.indexes[dir]
	if !found {
		if len(l.order) >= maxIndexes {
			delete(l.indexes, l.order[0])
			l.order = l.order[1:]
		}

		index = &spawnIndex{dir: dir, files: map[string]*spawnRead{}}
		l.indexes[dir] = index
		l.order = append(l.order, dir)
	}

	var firstAt float64
	if opts.FirstAt != nil {
		firstAt = *opts.FirstAt
	} else {
		firstAt = ReadTeamHead(path).FirstAt
	}

	since := 0.0
	if firstAt != 0 {
		since = firstAt - slackMs
	}

	limit := opts.Limit
	if limit == 0 {
		limit = maxCandidates
	}

	if limit < 0 {
		limit = math.MaxInt
	}

	return index.lead(leadQuery{
		team: tag.Team, name: tag.Name, own: path, sinceMs: since, limit: limit,
		firstAt: firstAt, first: opts.First,
	})
}

// decidedFile is what a finder knows of one file: its tag (nil: none yet), whether that is final,
// the size it was read at and its first line's time.
type decidedFile struct {
	size    int64
	tag     *TeamTag
	final   bool
	firstAt float64
}

// Finder looks through a lead's project folder for the transcripts of its teams. Cheap by
// construction: only files touched since the lead started, each decided once (a file without a
// tag is looked at again only after it grew), and at most 200 files read per scan. A tagged file
// is the lead's only when Leads.Path names this lead for it, the same test that claims a
// teammate for its lead, so the two cannot disagree.
type Finder struct {
	leads    *Leads
	dir      string
	leadPath string
	sinceMs  float64
	limit    int
	decided  map[string]decidedFile
}

// NewFinder looks in the folder of leadPath for files touched since sinceMs, reading at most
// limit new files per scan (0 for the default 200, a negative number for no cap: a replay is one
// look and may write a final row).
func NewFinder(leads *Leads, leadPath string, sinceMs float64, limit int) *Finder {
	switch {
	case limit == 0:
		limit = maxCandidates
	case limit < 0:
		limit = math.MaxInt
	}

	return &Finder{
		leads: leads, dir: filepath.Dir(leadPath), leadPath: leadPath, sinceMs: sinceMs,
		limit: limit, decided: map[string]decidedFile{},
	}
}

// Teammate is one teammate transcript this lead holds.
type Teammate struct {
	Path string
	Name string
}

// Find is the files tagged with one of teams that this lead holds (never team-lead, never the
// lead's own file), oldest first: a teammate starts soon after its lead, so it is among the first
// files touched since; the cap leaves the rest for the next scan.
func (f *Finder) Find(teams []string) []Teammate {
	found, _, readable := transcripts(f.dir, filepath.Base(f.leadPath), f.sinceMs)
	if !readable {
		return nil
	}

	js.Sort(found, func(left, right candidate) float64 {
		if order := left.mtimeMs - right.mtimeMs; order != 0 {
			return order
		}

		return float64(strings.Compare(left.path, right.path))
	})

	out := []Teammate{}
	examined := 0
	own := filepath.Join(f.dir, filepath.Base(f.leadPath))

	for _, file := range found {
		known, seen := f.decided[file.path]
		if f.stale(known, seen, file.size) {
			if examined >= f.limit {
				continue
			}

			examined++

			head := ReadTeamHead(file.path)
			known = decidedFile{
				size: file.size, tag: head.Tag, final: head.Decided,
				firstAt: head.FirstAt,
			}
			f.decided[file.path] = known
		}

		if f.holds(known, teams, file.path, own) {
			out = append(out, Teammate{Path: file.path, Name: known.tag.Name})
		}
	}

	return out
}

// stale is whether a file has to be read: never read, or read without a tag before it grew and
// before its head was read through the bound.
func (f *Finder) stale(known decidedFile, seen bool, size int64) bool {
	if !seen {
		return true
	}

	if known.tag != nil {
		return false
	}

	if known.final {
		return false
	}

	return size != known.size
}

func (f *Finder) holds(known decidedFile, teams []string, path, own string) bool {
	if known.tag == nil {
		return false
	}

	if !slices.Contains(teams, known.tag.Team) {
		return false
	}

	if known.tag.Name == teamLead {
		return false
	}

	firstAt := known.firstAt
	lead := f.leads.Path(path, *known.tag, LeadOpts{
		Limit: f.limit, FirstAt: &firstAt,
		First: f.leadPath,
	})

	return lead == own
}
