// Package sessions is the live session list: the Claude Code sessions the CLI registers in
// ~/.claude/sessions, the processes behind them, and their transcripts, read as they grow. One
// goroutine owns all of it (Loop); the page reads the snapshot it builds.
package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// surrogatePair is how many UTF-16 units a character beyond U+FFFF takes.
const surrogatePair = 2

// Registry statuses.
const (
	regBusy    = "busy"
	regWaiting = "waiting"
	regIdle    = "idle"
)

// procLine is one line of `ps -A -o pid=,ppid=,comm=`, trimmed.
var procLine = regexp.MustCompile(`^(\d+)[` + js.SpaceClass + `]+(\d+)[` + js.SpaceClass +
	`]+([^\n\r\x{2028}\x{2029}]*)$`)

// Reg is a session as the CLI registers it: one JSON file per process.
type Reg struct {
	PID       float64
	SessionID string
	Cwd       string
	Name      *string
	Status    string
	// WaitingFor is set with status waiting: what the session is stopped on.
	WaitingFor *string
	StartedAt  *float64
	UpdatedAt  *float64
	// StatusUpdatedAt is when Status last changed, if the CLI says; a new value is a new prompt.
	StatusUpdatedAt *float64
}

// status is the registry's status as the page knows it: busy, waiting or idle.
func (r Reg) status() string {
	switch r.Status {
	case regBusy, regWaiting:
		return r.Status
	}

	return regIdle
}

// name is the name a session shows: its registry name, else the start of its id.
func (r Reg) name() string {
	if r.Name != nil {
		if *r.Name != "" {
			return *r.Name
		}
	}

	return js.Cut(r.SessionID, sessionIDShown)
}

// key is the pid as the page names a session.
func (r Reg) key() string { return js.Number(r.PID) }

// changed is whether the fields compared between two reads differ.
func (r Reg) changed(next Reg) bool {
	if r.Status != next.Status {
		return true
	}

	if !sameText(r.WaitingFor, next.WaitingFor) {
		return true
	}

	if !sameText(r.Name, next.Name) {
		return true
	}

	if !sameNumber(r.UpdatedAt, next.UpdatedAt) {
		return true
	}

	return !sameNumber(r.StatusUpdatedAt, next.StatusUpdatedAt)
}

func sameText(left, right *string) bool {
	if left == nil {
		return right == nil
	}

	if right == nil {
		return false
	}

	return *left == *right
}

func sameNumber(left, right *float64) bool {
	if left == nil {
		return right == nil
	}

	if right == nil {
		return false
	}

	return *left == *right
}

// parseReg reads a registry file. A file that is not a JSON object, or whose pid is not a number
// or whose sessionId or cwd is not a string, is not a session: the file is skipped.
func parseReg(data []byte) (Reg, bool) {
	parsed, err := js.Parse([]byte(js.DecodeUTF8(data, false)))
	if err != nil {
		return Reg{}, false
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return Reg{}, false
	}

	reg := Reg{
		PID: 0, SessionID: "", Cwd: "", Name: optionalText(object, "name"), Status: "",
		WaitingFor: optionalText(object, "waitingFor"),
		StartedAt:  optionalNumber(object, "startedAt"),
		UpdatedAt:  optionalNumber(object, "updatedAt"), StatusUpdatedAt: nil,
	}
	reg.StatusUpdatedAt = optionalNumber(object, "statusUpdatedAt")

	pid, _ := object.Get("pid")
	sessionID, _ := object.Get("sessionId")
	cwd, _ := object.Get("cwd")
	status, _ := object.Get("status")

	var valid [3]bool

	reg.PID, valid[0] = pid.(float64)
	reg.SessionID, valid[1] = sessionID.(string)
	reg.Cwd, valid[2] = cwd.(string)
	reg.Status, _ = status.(string)

	return reg, valid == [3]bool{true, true, true}
}

func optionalText(object *js.Object, key string) *string {
	value, _ := object.Get(key)

	text, ok := value.(string)
	if !ok {
		return nil
	}

	return &text
}

func optionalNumber(object *js.Object, key string) *float64 {
	value, _ := object.Get(key)

	number, ok := value.(float64)
	if !ok {
		return nil
	}

	return &number
}

// listDir is a folder's entries in the order the file system gives them, unsorted, as readdirSync
// gives them (os.ReadDir would sort them by name).
func listDir(dir string) []string {
	folder, err := os.Open(dir)
	if err != nil {
		return nil
	}

	defer func() { _ = folder.Close() }()

	names, err := folder.Readdirnames(-1)
	if err != nil {
		return nil
	}

	return names
}

// Proc is one process as ps lists it.
type Proc struct {
	PPID float64
	Comm string
	// Path is the executable as ps prints it, with its directory.
	Path string
}

// parseProcs reads `ps -A -o pid=,ppid=,comm=`.
func parseProcs(out string) map[float64]Proc {
	procs := map[float64]Proc{}

	for line := range strings.SplitSeq(out, "\n") {
		match := procLine.FindStringSubmatch(js.Trim(line))
		if match == nil {
			continue
		}

		pid, _ := strconv.ParseFloat(match[1], 64)
		ppid, _ := strconv.ParseFloat(match[2], 64)
		procs[pid] = Proc{PPID: ppid, Comm: nodeBase(match[3]), Path: match[3]}
	}

	return procs
}

// nodeBase is node's path.basename: the last part of a path, trailing slashes ignored.
func nodeBase(path string) string {
	trimmed := strings.TrimRight(path, "/")

	return trimmed[strings.LastIndexByte(trimmed, '/')+1:]
}

// slug is the project folder Claude Code names after a working folder: every UTF-16 unit that is
// not an ASCII letter or digit becomes '-', so a character beyond U+FFFF becomes two.
func slug(cwd string) string {
	var builder strings.Builder

	for _, char := range cwd {
		if isAlphanumeric(char) {
			builder.WriteRune(char)

			continue
		}

		builder.WriteString(strings.Repeat("-", utf16Len(char)))
	}

	return builder.String()
}

func utf16Len(char rune) int {
	if char > maxBMP {
		return surrogatePair
	}

	return 1
}

func isAlphanumeric(char rune) bool {
	if char >= 'a' {
		return char <= 'z'
	}

	if char >= 'A' {
		return char <= 'Z'
	}

	if char >= '0' {
		return char <= '9'
	}

	return false
}

// transcriptOf is a session's transcript: under the slug of its folder first, then in any
// project folder that holds its id; "" when there is none yet.
func transcriptOf(projects, cwd, sessionID string) string {
	name := sessionID + ".jsonl"

	direct := filepath.Join(projects, slug(cwd), name)
	if exists(direct) {
		return direct
	}

	for _, folder := range listDir(projects) {
		path := filepath.Join(projects, folder, name)
		if exists(path) {
			return path
		}
	}

	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return data, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.IsDir()
}
