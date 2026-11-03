// Package instance keeps one Conn per data folder. Two servers on one folder would both poll
// GitHub and both archive transcripts, so a server holds a lock on a file in the folder for as
// long as it runs, and writes into it its process id and the address it answers on: a second
// `conn serve` points at the first, and `conn stop` stops it.
package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// FileName is the lock file in the data folder.
	FileName = "conn.lock"

	folderMode = 0o700
	fileMode   = 0o600

	// fileBytesMax is the most of the lock file that is read: a process id and an address.
	fileBytesMax = 4 << 10
)

// ErrHeld is a data folder another Conn holds.
var ErrHeld = errors.New("another conn is running on this data folder")

// Holder is what the Conn holding a folder wrote: its process id and the address it answers on.
// Both are zero until it has started listening.
type Holder struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

// Lock is a data folder this process holds. Keep it until the server stops: the lock lasts as
// long as its file stays open.
type Lock struct {
	file *os.File
}

// Acquire takes the folder's lock, creating the folder (private to the user) and the lock file
// when they are missing. It is ErrHeld when another process holds it.
func Acquire(folder string) (*Lock, error) {
	err := os.MkdirAll(folder, folderMode)
	if err != nil {
		return nil, fmt.Errorf("making the data folder: %w", err)
	}

	file, err := os.OpenFile(
		filepath.Join(folder, FileName), os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("opening the lock file: %w", err)
	}

	err = lock(file)
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	return &Lock{file: file}, nil
}

// lock takes an exclusive lock on file without waiting.
func lock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrHeld
	}

	if err != nil {
		return fmt.Errorf("locking the data folder: %w", err)
	}

	return nil
}

// Publish writes the holder's details into the lock file, replacing what was there.
func (l *Lock) Publish(holder Holder) error {
	data, err := json.Marshal(holder)
	if err != nil {
		return fmt.Errorf("writing the lock file: %w", err)
	}

	err = l.file.Truncate(0)
	if err != nil {
		return fmt.Errorf("writing the lock file: %w", err)
	}

	_, err = l.file.WriteAt(append(data, '\n'), 0)
	if err != nil {
		return fmt.Errorf("writing the lock file: %w", err)
	}

	err = l.file.Sync()
	if err != nil {
		return fmt.Errorf("writing the lock file: %w", err)
	}

	return nil
}

// Release empties the lock file and lets the folder go. The file stays, so a stale address is
// never read as a live one.
func (l *Lock) Release() error {
	truncateErr := l.file.Truncate(0)
	closeErr := l.file.Close()

	return errors.Join(truncateErr, closeErr)
}

// Running is the Conn holding the folder, and false when none does. Its Holder is zero when it
// has not written its details yet.
func Running(folder string) (Holder, bool, error) {
	file, err := os.OpenFile(filepath.Join(folder, FileName), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Holder{}, false, nil
	}

	if err != nil {
		return Holder{}, false, fmt.Errorf("opening the lock file: %w", err)
	}

	defer func() { _ = file.Close() }()

	err = lock(file)
	if err == nil {
		// Nobody held it: closing the file lets it go again.
		return Holder{}, false, nil
	}

	if !errors.Is(err, ErrHeld) {
		return Holder{}, false, err
	}

	return read(file), true, nil
}

// read is what the holder wrote, or zero when it has written nothing readable yet.
func read(file *os.File) Holder {
	data, err := io.ReadAll(io.LimitReader(file, fileBytesMax))
	if err != nil {
		return Holder{}
	}

	var holder Holder

	err = json.Unmarshal(data, &holder)
	if err != nil {
		return Holder{}
	}

	return holder
}
