package reviews

// A folder a session starts in by its path, checked so that no one but the person running Conn
// (or root) can change what the path names between the check and the start. The folder must be
// a real folder of the person's own that only they may write, and every folder above it, up to
// /, a real folder (not a link) of theirs or root's that no one else may write: one that others
// may write is accepted only when it is sticky (as /tmp is), since there no one may rename or
// remove an entry that is not their own. A path that holds no link and no folder another user
// may change names the same folder at the start as at the check, so a chdir by path is safe.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// writableByOthers are the permission bits that let the group or anyone write a folder.
	writableByOthers = 0o022
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
	exists   bool
	link     bool
	folder   bool
	owner    uint32
	writable bool
	sticky   bool
}

// leafProblem is what is wrong with the folder a session starts in, or nil: it must be a real
// folder that uid owns and that no one else may write.
func leafProblem(folder string, facts pathFacts, uid int) error {
	problem := shapeProblem(folder, facts)
	if problem != nil {
		return problem
	}

	if int64(facts.owner) != int64(uid) {
		return fmt.Errorf("%w: %s belongs to another user", ErrUnsafeFolder, folder)
	}

	if facts.writable {
		return writableProblem(folder)
	}

	return nil
}

// ancestorProblem is what is wrong with a folder above the one a session starts in, or nil: it
// must be a real folder that uid or root owns, that no one else may write unless it is sticky.
func ancestorProblem(folder string, facts pathFacts, uid int) error {
	problem := shapeProblem(folder, facts)
	if problem != nil {
		return problem
	}

	if int64(facts.owner) != int64(uid) {
		if facts.owner != rootUID {
			return fmt.Errorf("%w: %s belongs to another user", ErrUnsafeFolder, folder)
		}
	}

	if !facts.writable {
		return nil
	}

	if facts.sticky {
		return nil
	}

	return writableProblem(folder)
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

func writableProblem(folder string) error {
	return fmt.Errorf("%w: other users may write %s: run chmod go-w on it", ErrUnsafeFolder,
		folder)
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
		owner: stat.Uid, writable: info.Mode().Perm()&writableByOthers != 0,
		sticky: info.Mode()&fs.ModeSticky != 0,
	}, nil
}

// CheckSafeFolder returns every problem with starting a session in folder by its path, each
// wrapping ErrUnsafeFolder, or nil (see the top of this file). folder must be absolute and
// clean: one that is not is refused, never cleaned, so the path checked is the path used.
func CheckSafeFolder(folder string) error {
	return checkSafeFolder(folder, os.Getuid(), factsOf)
}

// checkSafeFolder is CheckSafeFolder for the person uid, with the facts of each path from look.
func checkSafeFolder(folder string, uid int, look func(string) (pathFacts, error)) error {
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

		problem := ancestorProblem(current, facts, uid)
		if index == 0 {
			problem = leafProblem(current, facts, uid)
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
