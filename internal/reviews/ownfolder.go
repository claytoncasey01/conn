package reviews

// Conn's own files in a worktree's .conn folder: written only into, and read only from, a real
// folder (not a link to one) that the person running Conn owns, since a repository can commit
// .conn as a link and the files would land wherever it points. The folder is looked at once by
// its path, for words that say what is wrong; every step after works on the folder it opened
// (a descriptor opened with no link followed), never on the path looked up again, so a link put
// in the folder's place meanwhile changes nothing. No file in it is ever reached through a link.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// operationOpen names an open in a path error, as the os package does.
const operationOpen = "open"

// folderFacts are what decides whether a folder is one of the person's own.
type folderFacts struct {
	link   bool
	folder bool
	owner  uint32
}

// checkFolderFacts refuses a folder unless its facts say it is a real folder uid owns.
func checkFolderFacts(folder string, facts folderFacts, uid int) error {
	if facts.link {
		return fmt.Errorf("%w: %s is a link", ErrNotOwnFolder, folder)
	}

	if !facts.folder {
		return fmt.Errorf("%w: %s is not a folder", ErrNotOwnFolder, folder)
	}

	if int64(facts.owner) != int64(uid) {
		return fmt.Errorf("%w: %s belongs to another user", ErrNotOwnFolder, folder)
	}

	return nil
}

// CheckOwnFolder refuses folder unless it is a real folder (not a link to one) that the person
// running Conn owns, looked at by its path.
func CheckOwnFolder(folder string) error {
	info, err := os.Lstat(folder)
	if err != nil {
		return asIs(err)
	}

	owner, read := info.Sys().(*syscall.Stat_t)
	if !read {
		return fmt.Errorf("%w: the owner of %s cannot be read", ErrNotOwnFolder, folder)
	}

	facts := folderFacts{
		link: info.Mode()&fs.ModeSymlink != 0, folder: info.IsDir(), owner: owner.Uid,
	}

	return checkFolderFacts(folder, facts, os.Getuid())
}

// openOwnFolder opens folder with no link followed and checks what it opened: a folder the
// person owns. The caller closes the descriptor.
func openOwnFolder(folder string) (int, error) {
	descriptor, err := unix.Open(folder,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)

	switch {
	case errors.Is(err, unix.ELOOP):
		return -1, fmt.Errorf("%w: %s is a link", ErrNotOwnFolder, folder)
	case errors.Is(err, unix.ENOTDIR):
		return -1, fmt.Errorf("%w: %s is not a folder", ErrNotOwnFolder, folder)
	case err != nil:
		return -1, asIs(&fs.PathError{Op: operationOpen, Path: folder, Err: err})
	}

	var stat unix.Stat_t

	err = unix.Fstat(descriptor, &stat)
	if err != nil {
		_ = unix.Close(descriptor)

		return -1, asIs(&fs.PathError{Op: "stat", Path: folder, Err: err})
	}

	facts := folderFacts{
		link: false, folder: stat.Mode&unix.S_IFMT == unix.S_IFDIR, owner: stat.Uid,
	}

	err = checkFolderFacts(folder, facts, os.Getuid())
	if err != nil {
		_ = unix.Close(descriptor)

		return -1, err
	}

	return descriptor, nil
}

// OpenInOwnFolder opens the file name in folder, for reading, only through a folder of the
// person's own and with no link followed at either: a link at the file is an error (ELOOP), and
// a missing file is fs.ErrNotExist. A pipe or a device opens without waiting; the caller checks
// that what it opened is a regular file.
func OpenInOwnFolder(folder, name string) (*os.File, error) {
	err := CheckOwnFolder(folder)
	if err != nil {
		return nil, err
	}

	return openInFolder(folder, name)
}

// openInFolder opens name for reading through the folder's own descriptor.
func openInFolder(folder, name string) (*os.File, error) {
	descriptor, err := openOwnFolder(folder)
	if err != nil {
		return nil, err
	}

	defer func() { _ = unix.Close(descriptor) }()

	path := filepath.Join(folder, name)

	opened, err := unix.Openat(descriptor, name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, asIs(&fs.PathError{Op: operationOpen, Path: path, Err: err})
	}

	return os.NewFile(uintptr(opened), path), nil
}

// writeNewPrivate writes a new file at path, 0600, through its folder's own descriptor:
// whatever was at the path is removed first, so a link is never followed; a folder is left for
// the write to fail on.
func writeNewPrivate(path, content string) error {
	folder, name := filepath.Dir(path), filepath.Base(path)

	descriptor, err := openOwnFolder(folder)
	if err != nil {
		return err
	}

	defer func() { _ = unix.Close(descriptor) }()

	err = removeUnlessFolder(descriptor, path, name)
	if err != nil {
		return err
	}

	// O_EXCL refuses whatever appeared at the path since: the file is a new one, made 0600.
	opened, err := unix.Openat(descriptor, name,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, privateFileMode)
	if err != nil {
		return asIs(&fs.PathError{Op: operationOpen, Path: path, Err: err})
	}

	file := os.NewFile(uintptr(opened), path)

	_, err = file.WriteString(content)
	if err == nil {
		// The umask took its share at the create: the mode is set on the file that was made.
		err = file.Chmod(privateFileMode)
	}

	closeErr := file.Close()

	if err != nil {
		return asIs(err)
	}

	return asIs(closeErr)
}

// removeUnlessFolder removes what is at name in the folder, unless it is a folder or nothing.
func removeUnlessFolder(descriptor int, path, name string) error {
	var stat unix.Stat_t

	err := unix.Fstatat(descriptor, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}

	if err != nil {
		return asIs(&fs.PathError{Op: "stat", Path: path, Err: err})
	}

	if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
		return nil
	}

	err = unix.Unlinkat(descriptor, name, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}

	if err != nil {
		return asIs(&fs.PathError{Op: "remove", Path: path, Err: err})
	}

	return nil
}
