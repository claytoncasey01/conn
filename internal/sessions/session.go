package sessions

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/snapshot"
	"github.com/claytoncasey01/conn/internal/transcript"
)

const (
	// cmuxApp ends the path of the cmux app's own executable.
	cmuxApp = "/cmux.app/Contents/MacOS/cmux"

	jsonlExt  = ".jsonl"
	metaExt   = ".meta.json"
	subagents = "subagents"
	// nestedDepth is the depth a teammate's own subagent is placed at.
	nestedDepth = 2
	// teammateType is the agent type a teammate is listed with.
	teammateType = "teammate"
	background   = "background"
)

// cmuxID is the shape of a cmux id, the whole of an entry's value.
var cmuxID = regexp.MustCompile(`^[0-9A-Fa-f-]{36}$`)

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
	// finder looks for this lead's teammates; made when its transcript first names a team.
	finder    *Finder
	scannedAt float64
	// teammates are the teammate transcripts found for this lead, in the order found.
	teammates []string
	tagRead   *tagRead
	claim     *teamClaim
	// hosted is the id the host issued when this is a session Conn hosts, else "". It is set
	// from the host's own record of the process, never from what the process prints or holds,
	// and such a session is never looked up as a cmux one.
	hosted pty.ID
}

// tagRead is the team tag read from the head of this session's transcript, at what size, and
// whether that is final.
type tagRead struct {
	size  int64
	tag   *TeamTag
	final bool
}

// teamClaim is this session's lead lookup, kept for 3 s.
type teamClaim struct {
	at    float64
	value *snapshot.Team
}

func newSession(reg Reg) *Session {
	return &Session{
		reg: reg, lead: transcript.NewLeadState(0), leadTail: nil, leadPath: "", agents: nil,
		known: map[string]bool{}, firstAt: 0, cmux: nil, looked: false, finder: nil,
		scannedAt: negativeInfinity(), teammates: nil, tagRead: nil, claim: nil, hosted: "",
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
func (s *Session) poll(projects string, now float64, leads *Leads) bool {
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
		s.addSubagents(s.leadPath, "")
		s.findTeammates(now, leads)

		for _, path := range s.teammates {
			s.addSubagents(path, strings.TrimSuffix(filepath.Base(path), jsonlExt))
		}
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

// addSubagents starts reading the subagent transcripts of parentPath (the lead's, or a
// teammate's, whose id is parentID and which then owns them) that are not read yet.
func (s *Session) addSubagents(parentPath, parentID string) {
	for _, found := range subagentFiles(parentPath, parentID, s.known) {
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

// SubagentFile is one subagent transcript with what its meta file says.
type SubagentFile = subagentFile

// SubagentFiles are the subagent transcripts beside a transcript (in <name>/subagents), in name
// order; for a teammate's own subagents (parentID set) the parent is the teammate.
func SubagentFiles(parentPath, parentID string) []SubagentFile {
	return subagentFiles(parentPath, parentID, map[string]bool{})
}

// Path, ID and Meta of a subagent file.
func (f subagentFile) Path() string               { return f.path }
func (f subagentFile) ID() string                 { return f.id }
func (f subagentFile) Meta() transcript.AgentMeta { return f.meta }

// subagentFiles are the subagent transcripts beside a transcript (in <name>/subagents), in name
// order, each with what its meta file says; one already known is left out, its meta file not
// read again.
func subagentFiles(parentPath, parentID string, known map[string]bool) []subagentFile {
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

		meta := readMeta(strings.TrimSuffix(path, jsonlExt) + metaExt)

		// Only a direct child of the teammate is placed under it; a deeper one (depth 2 or
		// more) already names its own parent.
		if parentID != "" {
			if meta.SpawnDepth < nestedDepth {
				id := parentID
				meta.ParentID = &id
				meta.SpawnDepth = nestedDepth
			}
		}

		found = append(found, subagentFile{
			path: path, id: strings.TrimPrefix(strings.TrimSuffix(name, jsonlExt), "agent-"),
			meta: meta,
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
func underCmux(pid float64, procs map[float64]Proc, hosted map[float64]pty.ID) bool {
	seen := map[float64]bool{}

	for proc, found := procs[pid]; found; proc, found = procs[proc.PPID] {
		if seen[proc.PPID] {
			return false
		}

		if strings.HasSuffix(proc.Path, cmuxApp) {
			return true
		}

		// What a hosted session started runs on Conn's terminal, whatever Conn runs under.
		if _, isHosted := hosted[proc.PPID]; isHosted {
			return false
		}

		seen[proc.PPID] = true
	}

	return false
}

// cmuxOf is a session's cmux surface (see cmuxTarget), read with this machine's processes and
// the host's running sessions.
func (s *store) cmuxOf(session *Session) *Target {
	return session.cmuxTarget(s.procs, s.deps.Env, s.hosts.byPID)
}

// cmuxTarget is the exact cmux surface this CLI was started in, from its own environment.
// Matching a workspace by name is not safe: names collide, a workspace can hold several panes,
// and text typed into the wrong surface can land in a shell. The environment alone is not enough
// either: a background session started from a cmux terminal inherits that terminal's ids but has
// no terminal of its own, so the cmux app must also be one of its ancestors. Looked up once;
// env reads `ps eww -p <pid>`.
func (s *Session) cmuxTarget(
	procs map[float64]Proc, env func(pid float64) ([]string, error), hosted map[float64]pty.ID,
) *Target {
	// A hosted session runs on Conn's own terminal: its environment is never read.
	if s.hosted != "" {
		return nil
	}

	if s.looked {
		return s.cmux
	}

	if _, alive := procs[s.reg.PID]; !alive {
		return nil
	}

	s.looked = true

	if !underCmux(s.reg.PID, procs, hosted) {
		return nil
	}

	environment, err := env(s.reg.PID)
	if err != nil {
		return nil
	}

	workspace, found := cmuxIDOf(environment, workspaceName)
	if !found {
		return nil
	}

	surface, found := cmuxIDOf(environment, surfaceName)
	if !found {
		return nil
	}

	s.cmux = &Target{Workspace: workspace, Surface: surface}

	return s.cmux
}

// The environment entries that name a cmux pane.
const (
	workspaceName = "CMUX_WORKSPACE_ID"
	surfaceName   = "CMUX_SURFACE_ID"
)

// cmuxIDOf is the cmux id an environment's entry name holds, whole, and false for none.
func cmuxIDOf(environment []string, name string) (string, bool) {
	value, found := environmentValue(environment, name)
	if !found {
		return "", false
	}

	if !cmuxID.MatchString(value) {
		return "", false
	}

	return value, true
}

// findTeammates looks in the lead's project folder for the transcripts of the teams it spawned
// into, at most every 3 s. A teammate runs on its own, so a parent that got a result back never
// marks it done.
func (s *Session) findTeammates(now float64, leads *Leads) {
	if len(s.lead.Teams) == 0 {
		return
	}

	if now-s.scannedAt < teamScanMs {
		return
	}

	s.scannedAt = now

	if s.finder == nil {
		since := 0.0
		if js.Truthy(s.firstAt) {
			since = s.firstAt - slackMs
		}

		s.finder = NewFinder(leads, s.leadPath, since, 0)
	}

	for _, mate := range s.finder.Find(s.lead.Teams) {
		if s.known[mate.Path] {
			continue
		}

		s.known[mate.Path] = true
		s.teammates = append(s.teammates, mate.Path)

		kind, name, shape := teammateType, mate.Name, background
		meta := transcript.AgentMeta{
			AgentType: &kind, Description: &name, ToolUseID: nil, SpawnDepth: 0,
			RequestShape: &shape, ParentID: nil,
		}
		s.agents = append(s.agents, &agentEntry{
			path: mate.Path, tail: transcript.NewTail(mate.Path),
			state: transcript.NewAgentState(strings.TrimSuffix(filepath.Base(mate.Path), jsonlExt),
				meta),
		})
	}
}

// teamTag is the team tag at the head of this session's transcript, looked at again only while
// the file grows and its head has not been read through its bound.
func (s *Session) teamTag() *TeamTag {
	var size int64
	if s.leadTail != nil {
		size = s.leadTail.Size
	}

	if s.tagStale(size) {
		head := ReadTeamHead(s.leadPath)
		s.tagRead = &tagRead{size: size, tag: head.Tag, final: head.Decided}
	}

	return s.tagRead.tag
}

func (s *Session) tagStale(size int64) bool {
	if s.tagRead == nil {
		return true
	}

	if s.tagRead.tag != nil {
		return false
	}

	if s.tagRead.final {
		return false
	}

	return s.tagRead.size != size
}

// team is set when this session is a teammate of a lead: a transcript in the same folder spawned
// it (or, failing that, its team). Its cost and calls are the lead's; a lone teammate is an
// ordinary session. The lookup is kept for 3 s, unless fresh (the archive, which also reads
// without a cap: a final row must not rest on a lookup that ran out of reads). LeadID is left
// for the caller.
func (s *Session) team(now float64, leads *Leads, fresh bool) *snapshot.Team {
	if s.leadPath == "" {
		return nil
	}

	tag := s.teamTag()
	if tag == nil {
		return nil
	}

	if tag.Name == teamLead {
		return nil
	}

	if s.claim == nil {
		s.claim = &teamClaim{at: negativeInfinity(), value: nil}
	}

	if fresh {
		s.claim.at = negativeInfinity()
	}

	if now-s.claim.at >= teamScanMs {
		s.claim = &teamClaim{at: now, value: nil}

		limit := 0
		if fresh {
			limit = -1
		}

		lead := leads.Path(s.leadPath, *tag, LeadOpts{Limit: limit, FirstAt: nil, First: ""})
		if lead != "" {
			s.claim.value = &snapshot.Team{
				Name: tag.Team, LeadSessionID: strings.TrimSuffix(filepath.Base(lead), jsonlExt),
				LeadID: nil,
			}
		}
	}

	return s.claim.value
}
