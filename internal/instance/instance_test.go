package instance_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/secret"
)

func TestOneConnPerDataFolder(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(t.TempDir(), "data")

	first, err := instance.Acquire(folder)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	_, err = instance.Acquire(folder)
	if !errors.Is(err, instance.ErrHeld) {
		t.Fatalf("a second Acquire = %v, want ErrHeld", err)
	}

	err = first.Release()
	if err != nil {
		t.Fatal(err)
	}

	again, err := instance.Acquire(folder)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}

	_ = again.Release()
}

func TestTheFolderAndFileArePrivate(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(t.TempDir(), "data")

	held, err := instance.Acquire(folder)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = held.Release() }()

	for path, want := range map[string]os.FileMode{
		folder: 0o700, filepath.Join(folder, instance.FileName): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

// running is instance.Running, failing the test on an error.
func running(t *testing.T, folder string) (instance.Holder, bool) {
	t.Helper()

	holder, held, err := instance.Running(folder)
	if err != nil {
		t.Fatal(err)
	}

	return holder, held
}

func acquire(t *testing.T, folder string) *instance.Lock {
	t.Helper()

	held, err := instance.Acquire(folder)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	return held
}

func TestRunningSaysWhoHoldsTheFolder(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()

	if _, held := running(t, folder); held {
		t.Error("Running before anything ran = true")
	}

	lock := acquire(t, folder)

	holder, held := running(t, folder)
	if !held {
		t.Fatal("Running = false while the folder is held")
	}

	if holder != (instance.Holder{}) {
		t.Errorf("holder = %+v before Publish, want zero", holder)
	}

	want := instance.Holder{PID: 42, URL: "http://127.0.0.1:7777"}

	err := lock.Publish(want)
	if err != nil {
		t.Fatal(err)
	}

	if holder, _ := running(t, folder); holder != want {
		t.Errorf("holder = %+v, want %+v", holder, want)
	}

	err = lock.Release()
	if err != nil {
		t.Fatal(err)
	}

	if _, held := running(t, folder); held {
		t.Error("Running after Release = true")
	}

	// Asking who runs never holds the folder itself.
	_ = acquire(t, folder).Release()
}

func TestAcquireRidesOutALockHeldForAMoment(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(t.TempDir(), "data")

	// Another process holds the folder only while it checks whether anyone does.
	checking, err := instance.Acquire(folder)
	if err != nil {
		t.Fatal(err)
	}

	released := make(chan error, 1)

	go func() {
		time.Sleep(20 * time.Millisecond)

		released <- checking.Release()
	}()

	started, err := instance.Acquire(folder)
	if err != nil {
		t.Fatalf("Acquire while the folder was held for a moment = %v, want it to wait", err)
	}

	_ = started.Release()

	err = <-released
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheSecretGoesThroughTheLockFile(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	lock := acquire(t, folder)

	defer func() { _ = lock.Release() }()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	err = lock.Publish(instance.Holder{PID: 42, URL: "http://127.0.0.1:7777", Secret: made})
	if err != nil {
		t.Fatal(err)
	}

	holder, _ := running(t, folder)
	if !holder.Secret.Matches(made.String()) {
		t.Fatal("the secret read back is not the one published")
	}

	if holder.URL != "http://127.0.0.1:7777" {
		t.Fatalf("URL = %q", holder.URL)
	}
}

// A lock file left looser by something else (an older build, a copy) is made private again
// before the secret is written into it.
func TestALooseLockFileIsMadePrivate(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	path := filepath.Join(folder, instance.FileName)

	err := os.WriteFile(path, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chmod(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	lock := acquire(t, folder)

	defer func() { _ = lock.Release() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestALockFileWithABadSecretHasNoHolder(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	lock := acquire(t, folder)

	defer func() { _ = lock.Release() }()

	err := os.WriteFile(filepath.Join(folder, instance.FileName),
		[]byte(`{"pid":42,"url":"http://127.0.0.1:7777","secret":"short"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	holder, held := running(t, folder)
	if !held {
		t.Fatal("Running = false while the folder is held")
	}

	if holder != (instance.Holder{}) {
		t.Fatalf("holder = %+v, want zero", holder)
	}
}
