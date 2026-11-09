package settings

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/claytoncasey01/conn/internal/js"
)

const (
	fileName = "settings.json"

	// maxFileBytes is the largest settings.json read; a larger one is set aside for the defaults.
	maxFileBytes = 1_000_000

	// The data folder and the file are the person's alone: the file holds their repositories.
	folderMode = 0o700
	fileMode   = 0o600

	// temporaryRandomBytes makes a temporary file's name unique among saves.
	temporaryRandomBytes = 6
)

var errTooLarge = errors.New("settings.json is too large")

// RenameFunc moves a finished temporary file into place: os.Rename, or a test's failure.
type RenameFunc func(from, to string) error

// LoadDetailed reads the file in dataDir. A missing file is the defaults and no remark; any other
// failure is the defaults and the reason, and the file is never touched.
func LoadDetailed(dataDir string) Loaded {
	text, err := readFile(filepath.Join(dataDir, fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Loaded{Settings: Defaults(), Dropped: []string{}, Unreadable: false}
	}

	if errors.Is(err, errTooLarge) {
		return unreadable(fmt.Sprintf("settings.json is larger than %d bytes: using the defaults",
			maxFileBytes))
	}

	if err != nil {
		return unreadable(fmt.Sprintf("settings.json could not be read (%s): using the defaults",
			NodeError(err)))
	}

	raw, err := js.Parse(text)
	if err != nil {
		return unreadable("settings.json is not valid JSON: using the defaults, and the file is " +
			"left as it is until you save")
	}

	return ParseDetailed(raw, true)
}

// Load reads the settings in dataDir.
func Load(dataDir string) Settings { return LoadDetailed(dataDir).Settings }

func readFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}

	if info.Size() > maxFileBytes {
		return nil, errTooLarge
	}

	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}

	return text, nil
}

// Save writes the settings atomically and privately: the old file is whole until the rename,
// and a failed write leaves it (and no temporary file) behind. rename is only a test's; nil is
// os.Rename.
func Save(dataDir string, settings Settings, rename RenameFunc) error {
	if rename == nil {
		rename = os.Rename
	}

	path := filepath.Join(dataDir, fileName)
	temporary := temporaryName(path)

	err := saveVia(temporary, path, settings, rename)
	if err != nil {
		_ = os.Remove(temporary)

		return err
	}

	return nil
}

func saveVia(temporary, path string, settings Settings, rename RenameFunc) error {
	folder := filepath.Dir(path)

	err := os.MkdirAll(folder, folderMode)
	if err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	// MkdirAll's mode only applies to a folder it creates; one made earlier may be looser.
	err = os.Chmod(folder, folderMode)
	if err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	text := js.StringifyIndent(settings, "  ") + "\n"

	err = writeNew(temporary, []byte(text))
	if err != nil {
		return err
	}

	return rename(temporary, path)
}

// writeNew writes a file that must not exist yet, readable by its owner only from the first
// byte.
func writeNew(path string, text []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	_, err = file.Write(text)
	if err != nil {
		_ = file.Close()

		return fmt.Errorf("saving settings: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	// The process umask can only narrow the mode OpenFile asked for; set it exactly.
	err = os.Chmod(path, fileMode)
	if err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}

	return nil
}

func temporaryName(path string) string {
	random := make([]byte, temporaryRandomBytes)
	_, _ = rand.Read(random)

	return path + "." + strconv.Itoa(os.Getpid()) + "." + hex.EncodeToString(random) + ".tmp"
}

// backUp copies the file to settings.json.bak before the first save over one that was set aside,
// so what the person wrote is not lost with it. Never overwrites an existing .bak (an earlier
// rescue stays the one). Returns the path made, "" when one was already there.
func backUp(dataDir string) (string, error) {
	path := filepath.Join(dataDir, fileName)
	backup := path + ".bak"

	source, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("backing up settings: %w", err)
	}

	defer func() { _ = source.Close() }()

	info, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("backing up settings: %w", err)
	}

	if info.IsDir() {
		linkErr := &os.LinkError{Op: "copyfile", Old: path, New: backup, Err: syscall.EISDIR}

		return "", fmt.Errorf("backing up settings: %w", linkErr)
	}

	return copyNew(source, backup)
}

// copyNew copies source to a new file at path; "" and no error when path already exists.
func copyNew(source io.Reader, path string) (string, error) {
	backup, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if errors.Is(err, fs.ErrExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("backing up settings: %w", err)
	}

	_, err = io.Copy(backup, source)
	if err != nil {
		_ = backup.Close()
		_ = os.Remove(path)

		return "", fmt.Errorf("backing up settings: %w", err)
	}

	err = backup.Close()
	if err != nil {
		return "", fmt.Errorf("backing up settings: %w", err)
	}

	err = os.Chmod(path, fileMode)
	if err != nil {
		return "", fmt.Errorf("backing up settings: %w", err)
	}

	return path, nil
}

// errnoName is how Node names a system error: its code and its text.
type errnoName struct {
	errno syscall.Errno
	code  string
	text  string
}

func errnoNames() []errnoName {
	return []errnoName{
		{syscall.EACCES, "EACCES", "permission denied"},
		{syscall.EPERM, "EPERM", "operation not permitted"},
		{syscall.ENOENT, "ENOENT", "no such file or directory"},
		{syscall.EISDIR, "EISDIR", "illegal operation on a directory"},
		{syscall.ENOTDIR, "ENOTDIR", "not a directory"},
		{syscall.ELOOP, "ELOOP", "too many symbolic links encountered"},
		{syscall.EEXIST, "EEXIST", "file already exists"},
		{syscall.EMFILE, "EMFILE", "too many open files"},
		{syscall.ENOSPC, "ENOSPC", "no space left on device"},
		{syscall.EROFS, "EROFS", "read-only file system"},
		{syscall.EXDEV, "EXDEV", "cross-device link not permitted"},
	}
}

func nameOf(err error) (errnoName, bool) {
	errno, isErrno := errors.AsType[syscall.Errno](err)
	if !isErrno {
		return errnoName{errno: 0, code: "", text: ""}, false
	}

	for _, name := range errnoNames() {
		if name.errno == errno {
			return name, true
		}
	}

	return errnoName{errno: 0, code: "", text: ""}, false
}

// NodeCode is the code Node gives a file error ("ENOENT", "EISDIR"), and false for an error
// that carries no errno this table knows.
func NodeCode(err error) (string, bool) {
	name, known := nameOf(err)

	return name.code, known
}

// NodeError is a file error's first line as Node writes a failed file call: `EACCES: permission
// denied, open '<path>'`. Messages from this package that quote a file error use it, so they
// read the same on every platform Conn runs on.
func NodeError(err error) string {
	pathErr, isPathErr := errors.AsType[*fs.PathError](err)
	if isPathErr {
		return nodePathError(pathErr)
	}

	linkErr, isLinkErr := errors.AsType[*os.LinkError](err)
	if isLinkErr {
		return nodeLinkError(linkErr)
	}

	return firstLine(err.Error())
}

func nodePathError(pathErr *fs.PathError) string {
	name, known := nameOf(pathErr.Err)
	if !known {
		return firstLine(pathErr.Error())
	}

	if pathErr.Op == "read" {
		return fmt.Sprintf("%s: %s, read", name.code, name.text)
	}

	return fmt.Sprintf("%s: %s, %s '%s'", name.code, name.text, pathErr.Op, pathErr.Path)
}

func nodeLinkError(linkErr *os.LinkError) string {
	name, known := nameOf(linkErr.Err)
	if !known {
		return firstLine(linkErr.Error())
	}

	return fmt.Sprintf("%s: %s, %s '%s' -> '%s'", name.code, name.text, linkErr.Op, linkErr.Old,
		linkErr.New)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")

	return line
}
