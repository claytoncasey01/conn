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
	"io/fs"
	"os"
	"path/filepath"
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

// writeProblem is what is wrong with who may write folder, or nil. anyoneMay is whether a sticky
// folder may be written by anyone (a folder above the one a session starts in).
func writeProblem(folder string, facts pathFacts, who Person, anyoneMay bool) error {
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
func leafProblem(folder string, facts pathFacts, who Person) error {
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
func ancestorProblem(folder string, facts pathFacts, who Person) error {
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

// CheckSafeFolder returns every problem with starting a session in folder by its path, each
// wrapping ErrUnsafeFolder, or nil (see the top of this file). folder must be absolute and
// clean: one that is not is refused, never cleaned, so the path checked is the path used. It reads
// who runs Conn (WhoRuns) for this one check; a caller that checks several folders for one
// request reads it once and calls CheckSafeFolderFor.
func CheckSafeFolder(folder string) error {
	return CheckSafeFolderFor(WhoRuns(), folder)
}

// CheckSafeFolderFor is CheckSafeFolder for who, read once by the caller (WhoRuns).
func CheckSafeFolderFor(who Person, folder string) error {
	return checkSafeFolder(folder, who, factsOf)
}

// checkSafeFolder is CheckSafeFolder for who, with the facts of each path from look.
func checkSafeFolder(folder string, who Person, look func(string) (pathFacts, error)) error {
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
