// Package archive keeps every session in History: a throttled checkpoint of the live state while
// it runs, a full replay of its transcript when it ends, and, at startup, the sessions that ended
// while conn was down. It knows nothing of the live session list, claude, gh or ps: it is handed
// plain facts.
package archive

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/snapshot"
	"github.com/claytoncasey01/conn/internal/transcript"
)

const (
	replayChunk = 1 << 20
	jsonlExt    = ".jsonl"
	// teamSlackMs is how far a teammate's transcript may start before its lead's first line.
	teamSlackMs = 60_000
	// nanosPerMs turns a file time in nanoseconds into ms, as Node's mtimeMs is.
	nanosPerMs = 1e6
)

var uuid = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Replayed is a session read from its transcript to the end.
type Replayed struct {
	SessionID string
	Path      string
	Parts     snapshot.Parts
	// Cwd is the working folder the lines name.
	Cwd string
	// FirstAt and LastAt are the first and last line times of the lead transcript (ms; 0 when no
	// line has one).
	FirstAt, LastAt float64
	// Size is the lead transcript's size when its read ended.
	Size float64
}

// pump reads a transcript to its end in chunks, handing each line to feed.
func pump(path string, feed func(line any)) *transcript.Tail {
	tail := transcript.NewTail(path)

	for {
		for _, line := range tail.Read(replayChunk) {
			feed(line)
		}

		if tail.AtEnd() {
			return tail
		}
	}
}

// Replay is the session in a transcript file, read to its end, or false when the file is gone or
// holds no line, and also when it vanishes during the replay: half a session is not a faithful
// archive. A subagent or teammate file that vanishes keeps what it had been read to. A lead that
// spawned a team brings its teammates' transcripts along as agents.
func Replay(path string, leads *sessions.Leads) (Replayed, bool) {
	lead := transcript.NewLeadState(-1)
	lines := 0
	firstAt := 0.0

	tail := pump(path, func(line any) {
		lead.Feed(line)

		lines++

		if js.Truthy(firstAt) {
			return
		}

		object, isObject := line.(*js.Object)
		if !isObject {
			return
		}

		stamp, _ := object.Get("timestamp")
		if js.Truthy(stamp) {
			firstAt = js.DateParse(js.String(stamp))
		}
	})

	if lines == 0 {
		return Replayed{}, false
	}

	if !exists(path) {
		return Replayed{}, false
	}

	agents := replayAgents(path, "", nil)
	agents = append(agents, replayTeam(path, lead, firstAt, leads)...)

	// The session may have been removed while its subagents were read.
	if !exists(path) {
		return Replayed{}, false
	}

	return Replayed{
		SessionID: strings.TrimSuffix(filepath.Base(path), jsonlExt), Path: path,
		Parts: snapshot.Parts{Lead: lead, Agents: agents}, Cwd: lead.Cwd, FirstAt: firstAt,
		LastAt: lead.LastAt, Size: float64(tail.Size),
	}, true
}

// replayAgents reads the subagents of parentPath (owned by parentID when it is a teammate).
func replayAgents(parentPath, parentID string, agents []*transcript.AgentState,
) []*transcript.AgentState {
	for _, file := range sessions.SubagentFiles(parentPath, parentID) {
		state := transcript.NewAgentState(file.ID(), file.Meta())
		pump(file.Path(), state.Feed)
		agents = append(agents, state)
	}

	return agents
}

// replayTeam reads the teammates a lead spawned, and their own subagents, in path order.
func replayTeam(path string, lead *transcript.LeadState, firstAt float64, leads *sessions.Leads,
) []*transcript.AgentState {
	if len(lead.Teams) == 0 {
		return nil
	}

	since := 0.0
	if js.Truthy(firstAt) {
		since = firstAt - teamSlackMs
	}

	found := sessions.NewFinder(leads, path, since, -1).Find(lead.Teams)
	slices.SortStableFunc(found, func(left, right sessions.Teammate) int {
		return strings.Compare(left.Path, right.Path)
	})

	agents := []*transcript.AgentState{}

	for _, mate := range found {
		kind, name, shape := "teammate", mate.Name, "background"
		state := transcript.NewAgentState(strings.TrimSuffix(filepath.Base(mate.Path), jsonlExt),
			transcript.AgentMeta{
				AgentType: &kind, Description: &name, ToolUseID: nil, SpawnDepth: 0,
				RequestShape: &shape, ParentID: nil,
			})
		pump(mate.Path, state.Feed)
		agents = append(agents, state)
		agents = replayAgents(mate.Path, state.ID, agents)
	}

	return agents
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// Transcript is a session transcript found on disk.
type Transcript struct {
	Path      string
	SessionID string
	MtimeMs   float64
	Size      float64
}

// Discover is every <uuid>.jsonl directly inside a project folder of root: subagent files and
// other names are left out.
func Discover(root string) []Transcript {
	out := []Transcript{}

	for _, project := range listDir(root) {
		dir := filepath.Join(root, project)

		info, err := os.Lstat(dir)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			continue
		}

		for _, name := range listDir(dir) {
			sessionID, isTranscript := strings.CutSuffix(name, jsonlExt)
			if !isTranscript {
				continue
			}

			if !uuid.MatchString(sessionID) {
				continue
			}

			path := filepath.Join(dir, name)

			file, err := os.Stat(path)
			if err != nil {
				continue
			}

			if file.Mode().IsRegular() {
				out = append(out, Transcript{
					Path: path, SessionID: sessionID, MtimeMs: mtimeMs(file),
					Size: float64(file.Size()),
				})
			}
		}
	}

	return out
}

func mtimeMs(info os.FileInfo) float64 {
	return float64(info.ModTime().UnixNano()) / nanosPerMs
}

// listDir is a folder's entries in the order the file system gives them, as readdirSync does.
func listDir(dir string) []string {
	folder, err := os.Open(dir)
	if err != nil {
		return nil
	}

	defer func() { _ = folder.Close() }()

	names, _ := folder.Readdirnames(-1)

	return names
}
