package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdbool.h>
#include <stddef.h>
#include "icon_darwin.h"
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/cgo"
	"unsafe"

	"github.com/claytoncasey01/conn/internal/desk"
)

// iconFolderMode is the icon's folder in Application Support: this user's only, like the data
// folder. The file is made by os.CreateTemp, so it is this user's only too, and the Dock, which
// runs as this user, reads it.
const iconFolderMode = 0o700

// errIcon is an icon bridge that could not be set up: the window works, but the page cannot
// change the app's icon.
var errIcon = errors.New("the app icon cannot be changed from the page")

// installIcon shows the icon the page last chose, and gives the page the connIcon message
// handler, through which it changes that icon (icon_darwin.m has the messages). It must run on
// the main thread, after installNotify, whose origin check the handler uses, and before the
// window loads the server's page. Like the notifications' handle, the shell's handle is kept by
// the Objective-C side for the life of the process and never deleted.
func (s *shell) installIcon() error {
	window := s.window.NativeWindow()
	if window == nil {
		return fmt.Errorf("%w: the window has not been made", errIcon)
	}

	handle := cgo.NewHandle(s)

	// The window is Objective-C's, not Go's, so its address is passed as a number, which the
	// garbage collector has no part in.
	problem := C.connInstallIcon(C.uintptr_t(handle), C.uintptr_t(uintptr(window)))
	if problem != nil {
		return fmt.Errorf("%w: %s", errIcon, C.GoString(problem))
	}

	return nil
}

// connIconMaxBytes is desk.MaxIconBytes, for the Objective-C side, which refuses a longer base64
// string before it decodes it.
//
//export connIconMaxBytes
func connIconMaxBytes() C.size_t {
	return C.size_t(desk.MaxIconBytes)
}

// connIconWrite checks the icon of length bytes at data (desk.Icon) and writes it to path,
// replacing the file whole, and says whether it did, saying on stderr why not. The length is
// checked before the bytes are copied, so a page cannot make the app copy more than the limit.
//
//export connIconWrite
func connIconWrite(handle C.uintptr_t, path *C.char, data unsafe.Pointer, length C.size_t) C.bool {
	opened := shellOf(handle)

	if uint64(length) > desk.MaxIconBytes {
		_, _ = fmt.Fprintf(opened.proc.stderr, "conn-app: %v: it has %d bytes, over %d\n",
			desk.ErrIcon, uint64(length), desk.MaxIconBytes)

		return false
	}

	// Copied into Go's memory, so nothing reads the Objective-C bytes once this returns. C.GoBytes
	// would do the same, but the code cgo generates for it fails the nlreturn lint.
	sent := bytes.Clone(unsafe.Slice((*byte)(data), int(length)))

	icon, err := desk.Icon(sent)
	if err == nil {
		err = writeIcon(C.GoString(path), icon)
	}

	if err != nil {
		_, _ = fmt.Fprintf(opened.proc.stderr, "conn-app: %v\n", err)

		return false
	}

	return true
}

// connIconRemove removes the icon file at path, and says whether there is none now, saying on
// stderr why not.
//
//export connIconRemove
func connIconRemove(handle C.uintptr_t, path *C.char) C.bool {
	opened := shellOf(handle)

	err := os.Remove(C.GoString(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_, _ = fmt.Fprintf(opened.proc.stderr, "conn-app: removing the app icon: %v\n", err)

		return false
	}

	return true
}

// writeIcon writes icon to path through a file beside it, renamed over path once whole, so the
// Dock never reads half an icon.
func writeIcon(path string, icon []byte) error {
	folder := filepath.Dir(path)

	err := os.MkdirAll(folder, iconFolderMode)
	if err != nil {
		return fmt.Errorf("making the app icon's folder: %w", err)
	}

	temporary, err := os.CreateTemp(folder, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing the app icon: %w", err)
	}

	// The file is synced before the rename, so a crash leaves the old icon or the new one, never
	// an empty file the Dock would show as nothing.
	_, err = temporary.Write(icon)
	if err == nil {
		err = temporary.Sync()
	}

	closeErr := temporary.Close()

	err = errors.Join(err, closeErr)
	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}

	if err != nil {
		_ = os.Remove(temporary.Name())

		return fmt.Errorf("writing the app icon: %w", err)
	}

	return nil
}
