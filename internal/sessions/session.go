package sessions

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/snapshot"
	"github.com/claytoncasey01/conn/internal/transcript"
)

const (
	// cmuxApp ends the path of the cmux app's own executable.
	cmuxApp = "/cmux.app/Contents/MacOS/cmux"

	jsonlExt  = ".jsonl"
	metaExt   = ".meta.json"
	subagents = "subagents"
)

// cmuxID is one cmux id variable in a process's environment, as `ps eww` prints it.
var (
	workspaceVar = regexp.MustCompile(`\bCMUX_WORKSPACE_ID=([0-9A-Fa-f-]{36})\b`)
	surfaceVar   = regexp.MustCompile(`\bCMUX_SURFACE_ID=([0-9A-Fa-f-]{36})\b`)
)

// Target is the exact cmux surface a CLI runs in.
type Target struct {
	Workspace string
	Surface   string
}

// agentEntry is one subagent transcript being read.
type agentEntry struct {
	path  string
	tail  *transcript.Tail
	state *transcript.AgentState
}

// Session is one live session: its registry entry and its transcripts, read as they grow.
type Session struct {
	reg      Reg
	lead     *transcript.LeadState
	leadTail *transcript.Tail
	leadPath string
	// agents are the subagent transcripts in the order they were found.
	agents []*agentEntry
	known  map[string]bool
	// firstAt is the time of the lead's first timestamped line (NaN when it did not parse).
	firstAt float64
	// cmux is nil until looked up; looked is set once it has been, a nil cmux then meaning the
	// CLI does not run in a cmux surface.
	cmux   *Target
	looked bool
}

func newSession(reg Reg) *Session {
	return &Session{
		reg: reg, lead: transcript.NewLeadState(0), leadTail: nil, leadPath: "", agents: nil,
		known: map[string]bool{}, firstAt: 0, cmux: nil, looked: false,
	}
}

// parts are the state objects the snapshot is built from, agents in the order they were found.
func (s *Session) parts() snapshot.Parts {
	agents := make([]*transcript.AgentState, 0, len(s.agents))
	for _, entry := range s.agents {
		agents = append(agents, entry.state)
	}

	return snapshot.Parts{Lead: s.lead, Agents: agents}
}

// poll pulls in whatever was appended, and is true when anything was.
func (s *Session) poll(projects string) bool {
	changed := false

	if s.leadTail == nil {
		s.leadPath = transcriptOf(projects, s.reg.Cwd, s.reg.SessionID)
		if s.leadPath != "" {
			s.leadTail = transcript.NewTail(s.leadPath)
		}
	}

	if s.leadTail != nil {
		for _, line := range s.leadTail.ReadAll() {
			s.lead.Feed(line)
			s.noteFirst(line)

			changed = true
		}
	}

	if s.leadPath != "" {
		s.addSubagents(s.leadPath)
	}

	for _, entry := range s.agents {
		for _, line := range entry.tail.ReadAll() {
			entry.state.Feed(line)

			changed = true
		}
	}

	return changed
}

// noteFirst keeps the time of the first line with a timestamp, as Date.parse reads it.
func (s *Session) noteFirst(line any) {
	if js.Truthy(s.firstAt) {
		return
	}

	object, isObject := line.(*js.Object)
	if !isObject {
		return
	}

	stamp, _ := object.Get("timestamp")
	if js.Truthy(stamp) {
		s.firstAt = js.DateParse(js.String(stamp))
	}
}

// addSubagents starts reading the subagent transcripts of the lead that are not read yet.
func (s *Session) addSubagents(parentPath string) {
	for _, found := range subagentFiles(parentPath, s.known) {
		s.known[found.path] = true
		s.agents = append(s.agents, &agentEntry{
			path: found.path, tail: transcript.NewTail(found.path),
			state: transcript.NewAgentState(found.id, found.meta),
		})
	}
}

type subagentFile struct {
	path string
	id   string
	meta transcript.AgentMeta
}

// subagentFiles are the subagent transcripts beside a transcript (in <name>/subagents), in name
// order, each with what its meta file says; one already known is left out, its meta file not
// read again.
func subagentFiles(parentPath string, known map[string]bool) []subagentFile {
	dir := filepath.Join(strings.TrimSuffix(parentPath, jsonlExt), subagents)
	names := listDir(dir)
	slices.SortStableFunc(names, js.CompareUTF16)

	found := []subagentFile{}

	for _, name := range names {
		if !strings.HasSuffix(name, jsonlExt) {
			continue
		}

		path := filepath.Join(dir, name)
		if known[path] {
			continue
		}

		found = append(found, subagentFile{
			path: path,
			id:   strings.TrimPrefix(strings.TrimSuffix(name, jsonlExt), "agent-"),
			meta: readMeta(strings.TrimSuffix(path, jsonlExt) + metaExt),
		})
	}

	return found
}

// readMeta reads a subagent's meta file; one that cannot be read says nothing.
func readMeta(path string) transcript.AgentMeta {
	meta := transcript.AgentMeta{
		AgentType: nil, Description: nil, ToolUseID: nil, SpawnDepth: 0, RequestShape: nil,
		ParentID: nil,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return meta
	}

	parsed, err := js.Parse([]byte(js.DecodeUTF8(data, false)))
	if err != nil {
		return meta
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return meta
	}

	meta.AgentType = optionalText(object, "agentType")
	meta.Description = optionalText(object, "description")
	meta.ToolUseID = optionalText(object, "toolUseId")
	meta.RequestShape = optionalText(object, "requestShape")
	meta.ParentID = optionalText(object, "parentId")

	depth := optionalNumber(object, "spawnDepth")
	if depth != nil {
		meta.SpawnDepth = *depth
	}

	return meta
}

// underCmux is whether the cmux app itself is up the process tree: the process runs in one of
// its terminals.
func underCmux(pid float64, procs map[float64]Proc) bool {
	seen := map[float64]bool{}

	for proc, found := procs[pid]; found; proc, found = procs[proc.PPID] {
		if seen[proc.PPID] {
			return false
		}

		if strings.HasSuffix(proc.Path, cmuxApp) {
			return true
		}

		seen[proc.PPID] = true
	}

	return false
}

// cmuxTarget is the exact cmux surface this CLI was started in, from its own environment.
// Matching a workspace by name is not safe: names collide, a workspace can hold several panes,
// and text typed into the wrong surface can land in a shell. The environment alone is not enough
// either: a background session started from a cmux terminal inherits that terminal's ids but has
// no terminal of its own, so the cmux app must also be one of its ancestors. Looked up once;
// env reads `ps eww -p <pid>`.
func (s *Session) cmuxTarget(
	procs map[float64]Proc, env func(pid float64) (string, error),
) *Target {
	if s.looked {
		return s.cmux
	}

	if _, alive := procs[s.reg.PID]; !alive {
		return nil
	}

	s.looked = true

	if !underCmux(s.reg.PID, procs) {
		return nil
	}

	out, err := env(s.reg.PID)
	if err != nil {
		return nil
	}

	workspace := workspaceVar.FindStringSubmatch(out)
	surface := surfaceVar.FindStringSubmatch(out)

	if workspace == nil {
		return nil
	}

	if surface == nil {
		return nil
	}

	s.cmux = &Target{Workspace: workspace[1], Surface: surface[1]}

	return s.cmux
}
