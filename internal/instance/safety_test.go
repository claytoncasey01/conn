package instance_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/instance"
)

// The lock file holds the secret, so it is opened only when it is the user's own plain file in a
// folder no one else can write to: a planted link would hand the secret to whoever planted it,
// and a folder others can write to lets them plant one.
func TestTheLockFileMustBeTheUsersOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T, folder string)
		uid     int
		wantErr error
	}{
		{"a fresh folder", func(*testing.T, string) {}, os.Getuid(), nil},
		{"a folder others may read", chmodFolder(0o755), os.Getuid(), nil},
		{"a symbolic link at the lock path", plantLink, os.Getuid(), instance.ErrUnsafe},
		// Seen by another user, the folder is someone else's: refused before the file is opened.
		{"a folder another user owns", plantFile, os.Getuid() + 1, instance.ErrUnsafe},
		{"a hard-linked lock file", plantHardLink, os.Getuid(), instance.ErrUnsafe},
		{"a folder the group may write to", chmodFolder(0o770), os.Getuid(), instance.ErrUnsafe},
		{"a folder anyone may write to", chmodFolder(0o777), os.Getuid(), instance.ErrUnsafe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			folder := filepath.Join(t.TempDir(), "data")

			err := os.Mkdir(folder, 0o700)
			if err != nil {
				t.Fatal(err)
			}

			tt.prepare(t, folder)

			held, err := instance.AcquireAs(folder, tt.uid)
			if held != nil {
				_ = held.Release()
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Acquire = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr == nil {
				return
			}

			_, _, err = instance.RunningAs(folder, tt.uid)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Running = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The refusal of a folder others can write to says how to fix it.
func TestAWritableFolderSaysWhatToRun(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(t.TempDir(), "data")

	err := os.Mkdir(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	chmodFolder(0o775)(t, folder)

	_, err = instance.Acquire(folder)
	if err == nil {
		t.Fatal("no error")
	}

	if want := "chmod go-w " + folder; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not say %q", err, want)
	}
}

func chmodFolder(mode os.FileMode) func(t *testing.T, folder string) {
	return func(t *testing.T, folder string) {
		t.Helper()

		err := os.Chmod(folder, mode)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func plantLink(t *testing.T, folder string) {
	t.Helper()

	target := filepath.Join(t.TempDir(), "elsewhere")

	err := os.WriteFile(target, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(target, filepath.Join(folder, instance.FileName))
	if err != nil {
		t.Fatal(err)
	}
}

func plantFile(t *testing.T, folder string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(folder, instance.FileName), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func plantHardLink(t *testing.T, folder string) {
	t.Helper()

	plantFile(t, folder)

	err := os.Link(filepath.Join(folder, instance.FileName), filepath.Join(folder, "copy"))
	if err != nil {
		t.Fatal(err)
	}
}
