package hosted

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// temporaryPattern names the file a write goes to before it takes the record's place.
	temporaryPattern = ".hosted-*.json"
	// fileMode is the record's mode, as CreateTemp makes it: its owner's alone.
	fileMode os.FileMode = 0o600
	// RefusedSuffix names, after FileName, where a record file Read refused is set aside.
	RefusedSuffix = ".refused"
	// folderEntriesMax bounds the entries of the data folder RemoveTemporaries looks at: far
	// more than Conn keeps there.
	folderEntriesMax = 1024
)

// file is the record file's shape.
type file struct {
	Version  int      `json:"version"`
	Sessions []Record `json:"sessions"`
}

// Write replaces the record in dir with records, atomically: the old file is whole until the
// rename, a failed write leaves it and no temporary file behind, and the new file is readable by
// its owner only from its first byte. A link at the record's name is replaced, never followed.
// A record that CheckRecord refuses, or more than RecordsMax, is an error and nothing is written.
func Write(dir string, records []Record) error {
	err := checkAll(records)
	if err != nil {
		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	sessions := records
	if sessions == nil {
		sessions = []Record{}
	}

	text, err := json.Marshal(file{Version: fileVersion, Sessions: sessions})
	if err != nil {
		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	text = append(text, '\n')

	// What Read would refuse is not written: JSON writes some bytes longer than they are (an
	// escaped "&", a byte that is not UTF-8), so a record in bounds can read back out of them.
	err = readsBack(text)
	if err != nil {
		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	temporary, err := writeTemporary(dir, text)
	if err != nil {
		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	path := filepath.Join(dir, FileName)

	err = os.Rename(temporary, path)
	if err != nil {
		_ = os.Remove(temporary)

		return fmt.Errorf("writing the hosted sessions: replacing %s: %w", path, renameCause(err))
	}

	// The rename is atomic; flushing the folder makes it last through a power loss too.
	err = syncFolder(dir)
	if err != nil {
		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	return nil
}

// syncFolder flushes dir's entries to the disk.
func syncFolder(dir string) error {
	folder, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s: %w", dir, err)
	}

	err = folder.Sync()
	closeErr := folder.Close()

	err = errors.Join(err, closeErr)
	if err != nil {
		return fmt.Errorf("flushing %s: %w", dir, err)
	}

	return nil
}

// SetAside moves a record file Read refused out of the way, to FileName+RefusedSuffix (in place
// of one set aside before), so the record written next does not destroy it: a regular file, or
// a link as it is (never followed). Nothing else at the name is moved, and no file is no error.
func SetAside(dir string) error {
	path := filepath.Join(dir, FileName)

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("setting the refused record aside: %w", err)
	}

	if !settable(info.Mode()) {
		return nil
	}

	err = os.Rename(path, path+RefusedSuffix)
	if err != nil {
		return fmt.Errorf("setting the refused record aside: %w", err)
	}

	return nil
}

// settable is whether a file of mode is set aside: a regular file or a link.
func settable(mode os.FileMode) bool {
	if mode.IsRegular() {
		return true
	}

	return mode&os.ModeSymlink != 0
}

// RemoveTemporaries removes the temporary files of writes a crash cut short from dir: regular
// files named as Write names them, among the first folderEntriesMax entries. Only the Conn
// holding the data folder calls it, so no write of its own is under way.
func RemoveTemporaries(dir string) error {
	folder, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("removing temporary records: %w", err)
	}

	entries, err := folder.ReadDir(folderEntriesMax)
	_ = folder.Close()

	if err != nil {
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("removing temporary records: %w", err)
		}
	}

	var problems []error

	for _, entry := range entries {
		if !isTemporary(entry) {
			continue
		}

		problems = append(problems, os.Remove(filepath.Join(dir, entry.Name())))
	}

	err = errors.Join(problems...)
	if err != nil {
		return fmt.Errorf("removing temporary records: %w", err)
	}

	return nil
}

// isTemporary is whether entry is a regular file named as Write names its temporary files.
func isTemporary(entry os.DirEntry) bool {
	if !entry.Type().IsRegular() {
		return false
	}

	matched, err := filepath.Match(temporaryPattern, entry.Name())
	if err != nil {
		return false
	}

	return matched
}

// checkWritable is every problem with record, as CheckRecord says them, or as the record reads
// back once Write has written it: JSON writes some bytes longer than they are.
func checkWritable(record Record) error {
	err := CheckRecord(record)
	if err != nil {
		return err
	}

	text, err := json.Marshal(file{Version: fileVersion, Sessions: []Record{record}})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadRecord, err)
	}

	return readsBack(append(text, '\n'))
}

// readsBack is nil when Read reads text: no larger than a record file is, and parsed back into
// records it would write as text again.
func readsBack(text []byte) error {
	if int64(len(text)) > fileBytesMax() {
		return fmt.Errorf("%w: it would be more than %d bytes", ErrBadFile, fileBytesMax())
	}

	_, err := parse(text)
	if err != nil {
		return err
	}

	return nil
}

// renameCause is why a rename failed, without the paths: the temporary file's name is new at
// each try, so the same failure reads the same.
func renameCause(err error) error {
	linkErr, isLink := errors.AsType[*os.LinkError](err)
	if isLink {
		return linkErr.Err
	}

	return err
}

// checkAll is every problem with records: too many, and each record's own.
func checkAll(records []Record) error {
	problems := []error{}

	if len(records) > RecordsMax() {
		problems = append(problems, fmt.Errorf("%w: %d records, at most %d", ErrBadFile,
			len(records), RecordsMax()))
	}

	for _, record := range records {
		problems = append(problems, CheckRecord(record))
	}

	return errors.Join(problems...)
}

// writeTemporary writes text to a new file in dir, made with no other file in its place
// (CreateTemp's O_EXCL) and mode 0600, flushed to the disk, and returns its path.
func writeTemporary(dir string, text []byte) (string, error) {
	temporary, err := os.CreateTemp(dir, temporaryPattern)
	if err != nil {
		return "", fmt.Errorf("making a temporary file: %w", err)
	}

	path := temporary.Name()

	_, err = temporary.Write(text)
	if err == nil {
		err = temporary.Sync()
	}

	closeErr := temporary.Close()

	err = errors.Join(err, closeErr)
	if err != nil {
		_ = os.Remove(path)

		return "", fmt.Errorf("writing a temporary file: %w", err)
	}

	return path, nil
}

// Read is the record in dir: none when there is no file. The file is opened with no link
// followed and without waiting (a fifo at its name opens and is refused), and is read only when
// it is a regular file of the person running Conn, of mode 0600, at most fileBytesMax bytes,
// with at most RecordsMax records, each of which CheckRecord passes, and exactly the bytes Write
// writes for them (an unknown field, such as a command line, a key in another case or a key
// twice refuses the file). A file that is not is ErrBadFile, or every record's problems, and no
// record of it is returned: Conn never writes such a file, so nothing in it is trusted.
func Read(dir string) ([]Record, error) {
	return readAs(dir, os.Getuid())
}

// readAs is Read for the person whose user id is uid.
func readAs(dir string, uid int) ([]Record, error) {
	path := filepath.Join(dir, FileName)

	opened, err := openNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer func() { _ = opened.Close() }()

	text, err := readOwnFile(opened, uid)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	records, err := parse(text)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return records, nil
}

// openNoFollow opens path for reading with no link followed and without blocking on a fifo.
func openNoFollow(path string) (*os.File, error) {
	descriptor, err := unix.Open(path,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)

	switch {
	case errors.Is(err, unix.ENOENT):
		return nil, fmt.Errorf("reading %s: %w", path, os.ErrNotExist)
	case errors.Is(err, unix.ELOOP):
		return nil, fmt.Errorf("reading %s: %w: it is a link", path, ErrBadFile)
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return os.NewFile(uintptr(descriptor), path), nil
}

// readOwnFile is the text of opened when it is a regular file the user uid owns, readable and
// writable by it alone (fileMode), of at most fileBytesMax bytes.
func readOwnFile(opened *os.File, uid int) ([]byte, error) {
	info, err := opened.Stat()
	if err != nil {
		return nil, fmt.Errorf("looking at the file: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: it is not a regular file", ErrBadFile)
	}

	if info.Mode().Perm() != fileMode {
		return nil, fmt.Errorf("%w: its mode is %v, not %v", ErrBadFile, info.Mode().Perm(),
			fileMode)
	}

	stat, read := info.Sys().(*syscall.Stat_t)
	if !read {
		return nil, fmt.Errorf("%w: its owner cannot be read", ErrBadFile)
	}

	if int64(stat.Uid) != int64(uid) {
		return nil, fmt.Errorf("%w: it belongs to another user", ErrBadFile)
	}

	text, err := io.ReadAll(io.LimitReader(opened, fileBytesMax()+1))
	if err != nil {
		return nil, fmt.Errorf("reading the file: %w", err)
	}

	if int64(len(text)) > fileBytesMax() {
		return nil, fmt.Errorf("%w: it is more than %d bytes", ErrBadFile, fileBytesMax())
	}

	return text, nil
}

// parse is the records in text, which must be exactly the bytes Write writes for them.
func parse(text []byte) ([]Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()

	var parsed file

	err := decoder.Decode(&parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadFile, err)
	}

	var after json.RawMessage

	err = decoder.Decode(&after)
	if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: there is more after the record", ErrBadFile)
	}

	if parsed.Version != fileVersion {
		return nil, fmt.Errorf("%w: version %d, want %d", ErrBadFile, parsed.Version,
			fileVersion)
	}

	err = checkAll(parsed.Sessions)
	if err != nil {
		return nil, err
	}

	// encoding/json matches keys whatever their case and lets the last of two win: only the
	// bytes Write would write for these records are its shape.
	written, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadFile, err)
	}

	if !bytes.Equal(append(written, '\n'), text) {
		return nil, fmt.Errorf("%w: it is not written as Conn writes it", ErrBadFile)
	}

	return parsed.Sessions, nil
}
