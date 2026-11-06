// Package instance keeps one Conn per data folder. Two servers on one folder would both poll
// GitHub and both archive transcripts, so a server holds a lock on a file in the folder for as
// long as it runs, and writes into it its process id, the address it answers on and its secret:
// a second `conn serve` or app points at the first with a page that works, and `conn stop` stops
// it. The file is private to the user (mode 0600), because the secret is what keeps the API from
// another user of the machine.
package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/secret"
)

const (
	// FileName is the lock file in the data folder.
	FileName = "conn.lock"

	folderMode = 0o700
	fileMode   = 0o600

	// fileBytesMax is the most of the lock file that is read: a process id, an address and a
	// secret.
	fileBytesMax = 4 << 10

	// acquireAttempts and acquireWait bound how long Acquire rides out a lock that is held for a
	// moment. Running takes the lock to find out whether anyone holds it, so a server starting
	// at the same instant as a `conn stop`, a second `conn serve` or a script that checks sees
	// the folder held for the few microseconds the check lasts. A Conn that really holds the
	// folder keeps it far longer than this window.
	acquireAttempts = 20
	acquireWait     = 10 * time.Millisecond
)

// ErrHeld is a data folder another Conn holds.
var ErrHeld = errors.New("another conn is running on this data folder")

// Holder is what the Conn holding a folder wrote: its process id, the address it answers on and
// the secret its API asks for. All are zero until it has started listening.
type Holder struct {
	PID    int
	URL    string
	Secret secret.Secret
}

// holderFile is a Holder as the lock file holds it. A Secret has no JSON form of its own, so that
// no other JSON shows it; this is the one place it is written as text.
type holderFile struct {
	PID    int    `json:"pid"`
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

// Lock is a data folder this process holds. Keep it until the server stops: the lock lasts as
// long as its file stays open.
type Lock struct {
	file *os.File
}

// Acquire takes the folder's lock, creating the folder (private to the user) and the lock file
// when they are missing. It is ErrHeld when another process holds it for the whole of a short
// wait (acquireAttempts times acquireWait).
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

	// The mode OpenFile is given applies only to a file it creates: one already there keeps its
	// own, so it is set here, before anything secret is written.
	err = file.Chmod(fileMode)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("making the lock file private: %w", err)
	}

	err = lockWithRetry(file)
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	return &Lock{file: file}, nil
}

// lockWithRetry takes the lock, trying again for a short time as long as another process holds it.
func lockWithRetry(file *os.File) error {
	var err error

	for attempt := range acquireAttempts {
		err = lock(file)
		if !errors.Is(err, ErrHeld) {
			return err
		}

		if attempt < acquireAttempts-1 {
			time.Sleep(acquireWait)
		}
	}

	return err
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

// Publish writes the holder's details into the lock file, replacing what was there. The file
// cannot be replaced by a renamed copy, which would be written whole, because the lock is held on
// this file: a new one at the same name would be free for a second Conn to take. So it is emptied
// and written in one write, once per server, and a reader that comes in between reads an empty or
// cut-off file, which does not parse and so reads as a holder that has not written yet (see read).
func (l *Lock) Publish(holder Holder) error {
	data, err := json.Marshal(holderFile{
		PID: holder.PID, URL: holder.URL, Secret: holder.Secret.String(),
	})
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

// read is what the holder wrote, or zero when it has written nothing readable yet: an empty or
// cut-off file, or a secret that is not one.
func read(file *os.File) Holder {
	data, err := io.ReadAll(io.LimitReader(file, fileBytesMax))
	if err != nil {
		return Holder{}
	}

	var written holderFile

	err = json.Unmarshal(data, &written)
	if err != nil {
		return Holder{}
	}

	holder := Holder{PID: written.PID, URL: written.URL, Secret: secret.Secret{}}
	if written.Secret == "" {
		// A Conn from before the secret writes none, and asks for none.
		return holder
	}

	holder.Secret, err = secret.Parse(written.Secret)
	if err != nil {
		return Holder{}
	}

	return holder
}
