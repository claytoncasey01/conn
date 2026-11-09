package reviews

// A folder a session starts in by its path, checked so that no other user can change which
// folder the path names between the check and the start. The folder must be a real folder of
// the person's own that no one else may write, and every folder above it, up to /, a real folder
// (not a link) of theirs or root's that no one else may write. Two exceptions, each because no
// other user gains anything by them: a folder whose group may write it passes when that group is
// the person's private group (a user-private group, as Debian, Ubuntu and Fedora make, with a
// umask of 002: the group named for the person, their own group id, and no other member); and a
// folder above that others may write passes when it is sticky (as /tmp is), since there no one
// may rename or remove an entry that is not their own.
//
// This holds the path, not what is in the folder, and only as far as owners and mode bits tell:
// it does not see macOS ACLs, a volume mounted to ignore ownership, or a network file system.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// groupWriteBit and otherWriteBit let a folder's group, and anyone, write it.
	groupWriteBit = 0o020
	otherWriteBit = 0o002
	// rootUID is the user id of root, who may own the folders above a person's own.
	rootUID = 0
	// pathPartsMax bounds the walk from a folder up to /: a path is at most PATH_MAX (4096)
	// bytes, so it has at most 2048 parts.
	pathPartsMax = 2048
	// groupFile is the system's list of groups, read to tell a private group.
	groupFile = "/etc/group"
	// groupFileBytesMax and groupLinesMax bound that read: a group file larger, or longer, is
	// not read, and every group counts as shared.
	groupFileBytesMax = 1 << 20
	groupLinesMax     = 10_000
	// groupFields is how many fields a line of the group file has: name, password, id, members.
	groupFields = 4
)

// ErrUnsafeFolder is a folder a session may not start in by its path; each problem wraps it.
var ErrUnsafeFolder = errors.New("unsafe folder")

// pathFacts are what decides whether one folder on a path is safe.
type pathFacts struct {
	exists bool
	link   bool
	folder bool
	owner  uint32
	group  uint32
	// groupWrite and otherWrite are whether the folder's group, and anyone, may write it.
	groupWrite bool
	otherWrite bool
	sticky     bool
}

// person is who runs Conn, as the check needs it: their user id, their group id and name, and
// the group file's text ("" when it could not be read, so no group is private).
type person struct {
	uid    int
	gid    int
	name   string
	groups string
}

// privateGroup is whether gid is the person's private group: their own group id, listed in the
// group file under their own name, with no member but them. A group the file does not list (an
// NSS or LDAP group), or a file that could not be read, is shared.
func privateGroup(who person, gid uint32) bool {
	if int64(gid) != int64(who.gid) {
		return false
	}

	if who.name == "" {
		return false
	}

	lines := strings.Split(who.groups, "\n")

	for _, line := range lines[:min(len(lines), groupLinesMax)] {
		fields := strings.Split(line, ":")
		if len(fields) != groupFields {
			continue
		}

		if fields[2] != strconv.FormatUint(uint64(gid), 10) {
			continue
		}

		return groupIsOnly(fields, who.name)
	}

	return false
}

// groupIsOnly is whether a group file line's fields name the group name and list no member but
// name.
func groupIsOnly(fields []string, name string) bool {
	if fields[0] != name {
		return false
	}

	for member := range strings.SplitSeq(fields[3], ",") {
		if member == "" {
			continue
		}

		if member != name {
			return false
		}
	}

	return true
}

// writeProblem is what is wrong with who may write folder, or nil. anyoneMay is whether a sticky
// folder may be written by anyone (a folder above the one a session starts in).
func writeProblem(folder string, facts pathFacts, who person, anyoneMay bool) error {
	if facts.otherWrite {
		if !anyoneMay {
			return fmt.Errorf("%w: other users may write %s: run chmod o-w on it",
				ErrUnsafeFolder, folder)
		}

		if !facts.sticky {
			return fmt.Errorf("%w: other users may write %s: run chmod o-w on it",
				ErrUnsafeFolder, folder)
		}
	}

	if !facts.groupWrite {
		return nil
	}

	if privateGroup(who, facts.group) {
		return nil
	}

	if anyoneMay {
		if facts.sticky {
			return nil
		}
	}

	return fmt.Errorf("%w: %s may be written by its group (%d), which is not your private "+
		"group: run chmod g-w on it, or give it a group only you are in", ErrUnsafeFolder, folder,
		facts.group)
}

// leafProblem is what is wrong with the folder a session starts in, or nil: it must be a real
// folder the person owns that no one else may write.
func leafProblem(folder string, facts pathFacts, who person) error {
	problem := shapeProblem(folder, facts)
	if problem != nil {
		return problem
	}

	if int64(facts.owner) != int64(who.uid) {
		return fmt.Errorf("%w: %s belongs to another user", ErrUnsafeFolder, folder)
	}

	return writeProblem(folder, facts, who, false)
}

// ancestorProblem is what is wrong with a folder above the one a session starts in, or nil: it
// must be a real folder the person or root owns that no one else may write, unless it is sticky.
func ancestorProblem(folder string, facts pathFacts, who person) error {
	problem := shapeProblem(folder, facts)
	if problem != nil {
		return problem
	}

	if int64(facts.owner) != int64(who.uid) {
		if facts.owner != rootUID {
			return fmt.Errorf("%w: %s belongs to another user", ErrUnsafeFolder, folder)
		}
	}

	return writeProblem(folder, facts, who, true)
}

// shapeProblem is a path part that is missing, a link or not a folder, or nil.
func shapeProblem(folder string, facts pathFacts) error {
	if !facts.exists {
		return fmt.Errorf("%w: %s does not exist", ErrUnsafeFolder, folder)
	}

	if facts.link {
		return fmt.Errorf("%w: %s is a link: use the folder it points to, or put that folder "+
			"in its place", ErrUnsafeFolder, folder)
	}

	if !facts.folder {
		return fmt.Errorf("%w: %s is not a folder", ErrUnsafeFolder, folder)
	}

	return nil
}

// factsOf are the facts of path, looked at with no link followed.
func factsOf(path string) (pathFacts, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return pathFacts{exists: false}, nil
	}

	if err != nil {
		return pathFacts{}, fmt.Errorf("looking at %s: %w", path, err)
	}

	stat, read := info.Sys().(*syscall.Stat_t)
	if !read {
		return pathFacts{}, fmt.Errorf("%w: the owner of %s cannot be read", ErrUnsafeFolder,
			path)
	}

	return pathFacts{
		exists: true, link: info.Mode()&fs.ModeSymlink != 0, folder: info.IsDir(),
		owner: stat.Uid, group: stat.Gid,
		groupWrite: info.Mode().Perm()&groupWriteBit != 0,
		otherWrite: info.Mode().Perm()&otherWriteBit != 0,
		sticky:     info.Mode()&fs.ModeSticky != 0,
	}, nil
}

// whoRuns is the person running Conn. A name or a group file that cannot be read leaves it
// empty, so no group counts as private.
func whoRuns() person {
	who := person{uid: os.Getuid(), gid: os.Getgid(), name: "", groups: readGroups()}

	current, err := user.Current()
	if err == nil {
		who.name = current.Username
	}

	return who
}

// readGroups is the group file's text, or "" when it cannot be read or is larger than
// groupFileBytesMax.
func readGroups() string {
	file, err := os.Open(groupFile)
	if err != nil {
		return ""
	}

	// Only read: nothing useful can be done with a failed close.
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, groupFileBytesMax+1))
	if err != nil {
		return ""
	}

	if len(data) > groupFileBytesMax {
		return ""
	}

	return string(data)
}

// CheckSafeFolder returns every problem with starting a session in folder by its path, each
// wrapping ErrUnsafeFolder, or nil (see the top of this file). folder must be absolute and
// clean: one that is not is refused, never cleaned, so the path checked is the path used.
func CheckSafeFolder(folder string) error {
	return checkSafeFolder(folder, whoRuns(), factsOf)
}

// checkSafeFolder is CheckSafeFolder for who, with the facts of each path from look.
func checkSafeFolder(folder string, who person, look func(string) (pathFacts, error)) error {
	if !filepath.IsAbs(folder) {
		return fmt.Errorf("%w: %s is not an absolute path", ErrUnsafeFolder, folder)
	}

	if filepath.Clean(folder) != folder {
		return fmt.Errorf("%w: %s is not a clean path", ErrUnsafeFolder, folder)
	}

	var problems []error

	current := folder

	for index := range pathPartsMax {
		facts, err := look(current)
		if err != nil {
			return err
		}

		problem := ancestorProblem(current, facts, who)
		if index == 0 {
			problem = leafProblem(current, facts, who)
		}

		if problem != nil {
			problems = append(problems, problem)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return errors.Join(problems...)
		}

		current = parent
	}

	return fmt.Errorf("%w: %s has more than %d parts", ErrUnsafeFolder, folder, pathPartsMax)
}
