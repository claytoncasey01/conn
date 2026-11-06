package instance

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// writableByOthers is the group and other write bits: a folder with either lets another user
	// put a file or a link where the lock file goes.
	writableByOthers = 0o022
	// linksMax is the one name a lock file may have: a second one is a hard link someone made to
	// read what is written into it.
	linksMax = 1
)

// ErrUnsafe is a data folder or lock file that is not safe to keep the secret in: a folder another
// user owns or can write to, or a lock file that is a link, not a plain file, another user's, or
// has a second name. The error says what is wrong and what to do.
var ErrUnsafe = errors.New("the data folder is not safe for conn's secret")

// owner is what the checks need of a file: whether it is a folder or a plain file, who owns it,
// its permission bits and how many names it has.
type owner struct {
	folder  bool
	regular bool
	uid     int
	mode    fs.FileMode
	links   uint64
}

// ownerOf reads info's owner facts. A FileInfo without the system's stat is refused: the owner
// cannot be known.
func ownerOf(info fs.FileInfo) (owner, error) {
	stat, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return owner{}, fmt.Errorf("%w: the owner of %s cannot be read", ErrUnsafe, info.Name())
	}

	return owner{
		folder:  info.IsDir(),
		regular: info.Mode().IsRegular(),
		uid:     int(stat.Uid),
		mode:    info.Mode().Perm(),
		links:   uint64(stat.Nlink),
	}, nil
}

// folderProblem is why folder, owned as facts says, is not safe for a process of user uid, or nil.
// A folder others may read (0755, as an older Conn may have left it) is fine: the lock file
// itself is 0600.
func folderProblem(folder string, facts owner, uid int) error {
	if !facts.folder {
		return fmt.Errorf("%w: %s is not a folder", ErrUnsafe, folder)
	}

	if facts.uid != uid {
		return fmt.Errorf(
			"%w: %s belongs to another user: use a data folder of your own (CONN_DATA)",
			ErrUnsafe, folder)
	}

	if facts.mode&writableByOthers != 0 {
		return fmt.Errorf("%w: other users can write to %s: run chmod go-w %s",
			ErrUnsafe, folder, folder)
	}

	return nil
}

// fileProblem is why the lock file at path, owned as facts says, is not safe for a process of
// user uid, or nil.
func fileProblem(path string, facts owner, uid int) error {
	if !facts.regular {
		return fmt.Errorf("%w: %s is not a plain file: remove it", ErrUnsafe, path)
	}

	if facts.uid != uid {
		return fmt.Errorf("%w: %s belongs to another user: remove it", ErrUnsafe, path)
	}

	if facts.links > linksMax {
		return fmt.Errorf("%w: %s has another name (a hard link): remove both", ErrUnsafe, path)
	}

	return nil
}

// openLockFile opens folder's lock file with flags for a process of user uid, once the folder
// and the file are known to be the user's alone. A link at the lock path is never followed, and
// the file is checked through the descriptor it was opened as, so it cannot be swapped between
// the check and the use. The file is made 0600 before anything is written into it. A missing
// folder or file (without O_CREATE) is os.ErrNotExist.
func openLockFile(folder string, uid, flags int) (*os.File, error) {
	folderInfo, err := os.Stat(folder)
	if err != nil {
		return nil, fmt.Errorf("reading the data folder: %w", err)
	}

	folderFacts, err := ownerOf(folderInfo)
	if err != nil {
		return nil, err
	}

	err = folderProblem(folder, folderFacts, uid)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(folder, FileName)

	file, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, fileMode)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%w: %s is a symbolic link: remove it", ErrUnsafe, path)
	}

	if err != nil {
		return nil, fmt.Errorf("opening the lock file: %w", err)
	}

	err = checkOpened(file, path, uid)
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	return file, nil
}

// checkOpened checks the open lock file, then makes it 0600: the mode OpenFile is given applies
// only to a file it creates.
func checkOpened(file *os.File, path string, uid int) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("reading the lock file: %w", err)
	}

	facts, err := ownerOf(info)
	if err != nil {
		return err
	}

	err = fileProblem(path, facts, uid)
	if err != nil {
		return err
	}

	err = file.Chmod(fileMode)
	if err != nil {
		return fmt.Errorf("making the lock file private: %w", err)
	}

	return nil
}
