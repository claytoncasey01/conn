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

// temporaryPattern names the file a write goes to before it takes the record's place.
const temporaryPattern = ".hosted-*.json"

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

	temporary, err := writeTemporary(dir, append(text, '\n'))
	if err != nil {
		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	err = os.Rename(temporary, filepath.Join(dir, FileName))
	if err != nil {
		_ = os.Remove(temporary)

		return fmt.Errorf("writing the hosted sessions: %w", err)
	}

	return nil
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
// it is a regular file of the person running Conn, at most fileBytesMax bytes, of exactly the
// shape Write writes (an unknown field, such as a command line, refuses the file), with at most
// RecordsMax records, each of which CheckRecord passes. A file that is not is ErrBadFile, or every
// record's problems, and no record of it is returned: Conn never writes such a file, so nothing
// in it is trusted.
func Read(dir string) ([]Record, error) {
	path := filepath.Join(dir, FileName)

	opened, err := openNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer func() { _ = opened.Close() }()

	text, err := readOwnFile(opened)
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

// readOwnFile is the text of opened when it is a regular file the person running Conn owns, of
// at most fileBytesMax bytes.
func readOwnFile(opened *os.File) ([]byte, error) {
	info, err := opened.Stat()
	if err != nil {
		return nil, fmt.Errorf("looking at the file: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: it is not a regular file", ErrBadFile)
	}

	stat, read := info.Sys().(*syscall.Stat_t)
	if !read {
		return nil, fmt.Errorf("%w: its owner cannot be read", ErrBadFile)
	}

	if int64(stat.Uid) != int64(os.Getuid()) {
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

// parse is the records in text, which must be exactly the shape Write writes.
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

	return parsed.Sessions, nil
}
