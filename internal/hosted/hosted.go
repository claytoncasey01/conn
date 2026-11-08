// Package hosted keeps the record of the sessions Conn hosts, so that a full quit and the next
// launch bring them back. The record is $CONN_DATA/hosted.json: for each session that runs, the
// host's id for it, its owner (the module that started it), its name, its folder and Claude's
// session id. It never holds a command line, an argument or an environment.
//
// The record is kept up to date as sessions start and end (Recorder), so a crash leaves it as it
// was. At a quit the record is written once more, then the sessions are stopped, and their ends
// are not recorded: the next launch finds every session that ran at the quit. While a launch
// resumes, the records it has not reached yet are carried in the record as they were, so a quit
// or a crash during the resume loses none of them.
//
// At launch each record is only a key: Resume hands it to its owner's own check, which looks the
// session up in the module's own state and rebuilds the program and its arguments from there,
// re-checking everything the module checks today. A record no owner vouches for starts nothing,
// so an edited file cannot start anything the app would not start itself, and at most
// pty's sessionsMax are resumed.
package hosted

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/pty"
)

// The owners of a hosted session: the module that started it, and whose check resumes it.
const (
	// OwnerOpenWork is an Open work run's leg; the name is the run's key, the folder the leg's
	// worktree.
	OwnerOpenWork = "openwork"
	// OwnerReviews is a review; the folder is its worktree under Conn's reviews folder.
	OwnerReviews = "reviews"
	// OwnerMyPrs is a My PRs self-review; the folder is its worktree under Conn's myprs folder.
	OwnerMyPrs = "myprs"
	// OwnerHistory is a session resumed from History; Claude's session id names it.
	OwnerHistory = "history"
)

const (
	// FileName is the record's file in the data folder.
	FileName = "hosted.json"

	// nameBytesMax bounds a session's name in the record.
	nameBytesMax = 1 << 10
	// dirBytesMax bounds a session's folder in the record: PATH_MAX on Linux.
	dirBytesMax = 4 << 10
	// recordBytesMax bounds the JSON of one record: its fields at their most, quoted, and the
	// keys around them.
	recordBytesMax = 2*(nameBytesMax+dirBytesMax) + 1<<10
	// fileVersion is the shape of the file this code writes and reads.
	fileVersion = 1
)

// RecordsMax is the most records the file holds, and the most sessions Resume starts: the most a
// host runs at once.
func RecordsMax() int { return pty.DefaultLimits().SessionsMax }

// fileBytesMax bounds the file: RecordsMax records at their most, and the object around them.
func fileBytesMax() int64 { return int64(RecordsMax()*recordBytesMax + 1<<10) }

var (
	// ErrBadRecord is a record Conn would not have written; each problem wraps it.
	ErrBadRecord = errors.New("not a hosted session record")
	// ErrBadFile is a record file Conn would not have written: a link, not a regular file,
	// another user's, too large, not the shape it writes, or more than RecordsMax records.
	ErrBadFile = errors.New("not a hosted sessions file")
)

// Record is one hosted session as the file keeps it: ids and names only.
type Record struct {
	// ID is the id the host issued for the session (pty.ParseID's shape).
	ID string `json:"id"`
	// Owner is the module that started it: one of the Owner constants.
	Owner string `json:"owner"`
	// Name is the session's name, as the module gave it.
	Name string `json:"name"`
	// Dir is the folder it runs in, an absolute path.
	Dir string `json:"dir"`
	// ClaudeSessionID is Claude's own id for its conversation once the session list has linked
	// it, a UUID, or "" until then.
	ClaudeSessionID string `json:"claudeSessionId"`
}

// CheckRecord is every problem with record, each wrapping ErrBadRecord, or nil.
func CheckRecord(record Record) error {
	var problems []error

	bad := func(what string) {
		problems = append(problems, fmt.Errorf("%w: %s", ErrBadRecord, what))
	}

	_, err := pty.ParseID(record.ID)
	if err != nil {
		bad("the id is not one a host issues")
	}

	if !slices.Contains(owners(), record.Owner) {
		bad("the owner is not a module that starts sessions")
	}

	problems = append(problems, checkName(record.Name)...)
	problems = append(problems, checkDir(record.Dir)...)

	if record.ClaudeSessionID != "" {
		if !cmux.IsSessionID(record.ClaudeSessionID) {
			bad("Claude's session id is not a UUID")
		}
	}

	return errors.Join(problems...)
}

func checkName(name string) []error {
	if name == "" {
		return []error{fmt.Errorf("%w: the session has no name", ErrBadRecord)}
	}

	if len(name) > nameBytesMax {
		return []error{fmt.Errorf("%w: the name is %d bytes, more than %d", ErrBadRecord,
			len(name), nameBytesMax)}
	}

	return nil
}

func checkDir(dir string) []error {
	if len(dir) > dirBytesMax {
		return []error{fmt.Errorf("%w: the folder is %d bytes, more than %d", ErrBadRecord,
			len(dir), dirBytesMax)}
	}

	if !filepath.IsAbs(dir) {
		return []error{fmt.Errorf("%w: the folder is not an absolute path", ErrBadRecord)}
	}

	if filepath.Clean(dir) != dir {
		return []error{fmt.Errorf("%w: the folder is not a clean path", ErrBadRecord)}
	}

	return nil
}

// owners are the modules that start hosted sessions.
func owners() []string {
	return []string{OwnerOpenWork, OwnerReviews, OwnerMyPrs, OwnerHistory}
}
