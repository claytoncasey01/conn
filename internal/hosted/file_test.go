package hosted_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
)

const (
	claudeID     = "0b6c4f7e-2d1a-4c3b-9e8f-112233445566"
	otherClaude  = "7a1e9c2b-5f3d-4e6a-8b7c-998877665544"
	privateMode  = 0o600
	recordsTried = 500
	// claudeIDTail is claudeID's last group, as a number.
	claudeIDTail = 0x112233445566
)

// hostID is a host-issued id for the index-th session.
func hostID(index int) string { return fmt.Sprintf("%032x", index+1) }

// record is the index-th session's record: the first's Claude session is claudeID, and each other
// has one of its own.
func record(index int) hosted.Record {
	return hosted.Record{
		ID: hostID(index), Owner: hosted.OwnerOpenWork, Name: "KEY-" + strconv.Itoa(index+1),
		Dir:             "/data/runs/KEY-" + strconv.Itoa(index+1),
		ClaudeSessionID: fmt.Sprintf("0b6c4f7e-2d1a-4c3b-9e8f-%012x", claudeIDTail+index),
	}
}

func writeText(t *testing.T, dir, text string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(dir, hosted.FileName), []byte(text), privateMode)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteThenRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := []hosted.Record{record(0), record(1)}

	err := hosted.Write(dir, want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := hosted.Read(dir)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(got, want) {
		t.Errorf("read %+v, want %+v", got, want)
	}
}

func TestWriteIsPrivateAndLeavesNoTemporaryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := hosted.Write(dir, []hosted.Record{record(0)})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, hosted.FileName))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != privateMode {
		t.Errorf("mode %v, want %v", info.Mode().Perm(), os.FileMode(privateMode))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("the folder holds %d files, want only %s", len(entries), hosted.FileName)
	}
}

func TestWriteReplacesALinkWithoutFollowingIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")

	err := os.WriteFile(target, []byte("theirs"), privateMode)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(target, filepath.Join(dir, hosted.FileName))
	if err != nil {
		t.Fatal(err)
	}

	err = hosted.Write(dir, []hosted.Record{record(0)})
	if err != nil {
		t.Fatal(err)
	}

	text, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	if string(text) != "theirs" {
		t.Errorf("the link's target became %q", text)
	}

	info, err := os.Lstat(filepath.Join(dir, hosted.FileName))
	if err != nil {
		t.Fatal(err)
	}

	if !info.Mode().IsRegular() {
		t.Errorf("the record is %v, want a regular file in the link's place", info.Mode())
	}
}

func TestWriteRefusesWhatItWouldNotRead(t *testing.T) {
	t.Parallel()

	tooMany := make([]hosted.Record, 0, hosted.MaxRecords()+1)
	for index := range hosted.MaxRecords() + 1 {
		tooMany = append(tooMany, record(index))
	}

	relative, invalid := record(0), record(0)
	relative.Dir = "runs/KEY-1"
	// Each byte that is not UTF-8 is written as U+FFFD, three bytes, so the name read back is
	// over its bound.
	invalid.Name = strings.Repeat("\xff", hosted.MaxNameBytes)

	tests := []struct {
		name    string
		records []hosted.Record
		wantErr error
	}{
		{"more than RecordsMax", tooMany, hosted.ErrBadFile},
		{"a relative folder", []hosted.Record{relative}, hosted.ErrBadRecord},
		{"a name that reads back longer", []hosted.Record{invalid}, hosted.ErrBadRecord},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			err := hosted.Write(dir, tt.records)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Write: %v, want %v", err, tt.wantErr)
			}

			_, err = os.Stat(filepath.Join(dir, hosted.FileName))
			if !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused write left a file: %v", err)
			}
		})
	}
}

// longestRecords are MaxRecords records at their bounds, every byte of their names and folders
// one JSON writes as six.
func longestRecords() []hosted.Record {
	records := make([]hosted.Record, 0, hosted.MaxRecords())
	for index := range hosted.MaxRecords() {
		each := record(index)
		each.Name = strings.Repeat("&", hosted.MaxNameBytes)
		each.Dir = "/" + strings.Repeat("<", hosted.MaxDirBytes-1)
		records = append(records, each)
	}

	return records
}

// What Write writes is what Read reads: the same records come back, the longest a full host
// records among them.
func TestWhatWriteWritesReadsBack(t *testing.T) {
	t.Parallel()

	named := record(0)
	named.Name = "review A & B <x>"

	tests := []struct {
		name    string
		records []hosted.Record
	}{
		{"a name JSON escapes", []hosted.Record{named}},
		{"the longest records of a full host", longestRecords()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			err := hosted.Write(dir, tt.records)
			if err != nil {
				t.Fatal(err)
			}

			got, err := hosted.Read(dir)
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(got, tt.records) {
				t.Errorf("read %d records, want the %d written", len(got), len(tt.records))
			}
		})
	}
}

func TestReadNoFile(t *testing.T) {
	t.Parallel()

	got, err := hosted.Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 0 {
		t.Errorf("read %+v from no file", got)
	}
}

// fileOf is the record file's text with sessions.
func fileOf(t *testing.T, sessions any) string {
	t.Helper()

	text, err := json.Marshal(map[string]any{"version": 1, "sessions": sessions})
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

// tamperedTexts are record files Conn never writes, each refused whole.
func tamperedTexts(t *testing.T) []struct {
	name, text string
	wantErr    error
} {
	t.Helper()

	withCommand := map[string]any{
		"id": hostID(0), "owner": "openwork", "name": "KEY-1", "dir": "/data/runs/KEY-1",
		"claudeSessionId": claudeID, "argv": []string{"/bin/sh", "-c", "touch /tmp/owned"},
	}
	many := make([]hosted.Record, 0, recordsTried)

	for index := range recordsTried {
		many = append(many, record(index))
	}

	notUUID, notHostID, unknownOwner := record(0), record(0), record(0)
	notUUID.ClaudeSessionID = "--continue; rm -rf ~"
	notHostID.ID = "../../etc"
	unknownOwner.Owner = "shell"

	return []struct {
		name, text string
		wantErr    error
	}{
		{"a command line field", fileOf(t, []any{withCommand}), hosted.ErrBadFile},
		{
			"a command beside the sessions", `{"version":1,"sessions":[],"command":"sh"}`,
			hosted.ErrBadFile,
		},
		{"500 entries", fileOf(t, many), hosted.ErrBadFile},
		{
			"a session id that is not a UUID", fileOf(t, []hosted.Record{notUUID}),
			hosted.ErrBadRecord,
		},
		{"an id no host issues", fileOf(t, []hosted.Record{notHostID}), hosted.ErrBadRecord},
		{
			"an owner that is no module", fileOf(t, []hosted.Record{unknownOwner}),
			hosted.ErrBadRecord,
		},
		// As Write writes it, but for the version: only the version check refuses it.
		{"another version", `{"version":2,"sessions":[]}` + "\n", hosted.ErrBadFile},
		// encoding/json matches keys whatever their case, and the last of two wins.
		{"a key in another case", `{"VERSION":1,"sessions":[]}` + "\n", hosted.ErrBadFile},
		{
			"a key twice", `{"version":1,"sessions":[],"sessions":[]}` + "\n",
			hosted.ErrBadFile,
		},
		{"the keys in another order", `{"sessions":[],"version":1}` + "\n", hosted.ErrBadFile},
		{"not JSON", `sessions: all`, hosted.ErrBadFile},
		{
			"two objects", `{"version":1,"sessions":[]}{"version":1,"sessions":[]}`,
			hosted.ErrBadFile,
		},
	}
}

func TestReadRefusesATamperedFile(t *testing.T) {
	t.Parallel()

	for _, tt := range tamperedTexts(t) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeText(t, dir, tt.text)

			got, err := hosted.Read(dir)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Read: %v, want %v", err, tt.wantErr)
			}

			if len(got) != 0 {
				t.Errorf("a tampered file gave %d records", len(got))
			}
		})
	}
}

func TestReadRefusesALinkAsTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// The link's target is a record Write wrote: only the link refuses it.
	elsewhere := t.TempDir()

	err := hosted.Write(elsewhere, []hosted.Record{record(0)})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(elsewhere, hosted.FileName)

	err = os.Symlink(target, filepath.Join(dir, hosted.FileName))
	if err != nil {
		t.Fatal(err)
	}

	got, err := hosted.Read(dir)
	if !errors.Is(err, hosted.ErrBadFile) {
		t.Errorf("Read: %v, want %v", err, hosted.ErrBadFile)
	}

	if len(got) != 0 {
		t.Errorf("a link gave %d records", len(got))
	}
}

func TestReadRefusesAFifoWithoutWaiting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := syscall.Mkfifo(filepath.Join(dir, hosted.FileName), privateMode)
	if err != nil {
		t.Fatal(err)
	}

	_, err = hosted.Read(dir)
	if !errors.Is(err, hosted.ErrBadFile) {
		t.Errorf("Read: %v, want %v", err, hosted.ErrBadFile)
	}
}

// A record another user owns is never read: only the person running Conn writes it.
func TestReadRefusesAnotherUsersFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uid     int
		wantErr error
	}{
		{"the person's own", os.Getuid(), nil},
		{"another user's", os.Getuid() + 1, hosted.ErrBadFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			err := hosted.Write(dir, []hosted.Record{record(0)})
			if err != nil {
				t.Fatal(err)
			}

			_, err = hosted.ReadAs(dir, tt.uid)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The record is readable by its owner only, as Write makes it: a file anyone else may read or
// write is not one Conn wrote.
func TestReadRefusesAFileOfAnotherMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    os.FileMode
		wantErr error
	}{
		{"read and write by its owner only", privateMode, nil},
		{"read by others", 0o644, hosted.ErrBadFile},
		{"written by anyone", 0o666, hosted.ErrBadFile},
		{"read by its owner only", 0o400, hosted.ErrBadFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			err := hosted.Write(dir, []hosted.Record{record(0)})
			if err != nil {
				t.Fatal(err)
			}

			err = os.Chmod(filepath.Join(dir, hosted.FileName), tt.mode)
			if err != nil {
				t.Fatal(err)
			}

			_, err = hosted.Read(dir)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// A write a crash cut short leaves its temporary file; the next launch removes it, and only it.
func TestRemoveTemporaries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, name := range []string{".hosted-123.json", ".hosted-x.json", "other.json"} {
		err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), privateMode)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := hosted.Write(dir, []hosted.Record{record(0)})
	if err != nil {
		t.Fatal(err)
	}

	err = os.Mkdir(filepath.Join(dir, ".hosted-folder.json"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = hosted.RemoveTemporaries(dir)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	want := []string{".hosted-folder.json", hosted.FileName, "other.json"}
	if !slices.Equal(names, want) {
		t.Errorf("left %q, want %q", names, want)
	}
}

// A record file Read refused is moved aside, not written over: a person can still recover it.
func TestSetAside(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arrange   func(t *testing.T, path string)
		wantMoved bool
	}{
		{"a file of another mode", func(t *testing.T, path string) {
			t.Helper()

			err := os.WriteFile(path, []byte("records"), 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a link", func(t *testing.T, path string) {
			t.Helper()

			err := os.Symlink("/etc/hosts", path)
			if err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a folder", func(t *testing.T, path string) {
			t.Helper()

			err := os.Mkdir(path, 0o700)
			if err != nil {
				t.Fatal(err)
			}
		}, false},
		{"no file", func(*testing.T, string) {}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tt.arrange(t, filepath.Join(dir, hosted.FileName))

			err := hosted.SetAside(dir)
			if err != nil {
				t.Fatal(err)
			}

			_, err = os.Lstat(filepath.Join(dir, hosted.FileName+hosted.RefusedSuffix))
			if (err == nil) != tt.wantMoved {
				t.Errorf("set aside: %v, want moved %v", err, tt.wantMoved)
			}
		})
	}
}

// writtenText is the text Write writes for records, whatever their size: Write itself refuses
// a file over the bound.
func writtenText(t *testing.T, records []hosted.Record) string {
	t.Helper()

	text, err := json.Marshal(struct {
		Version  int             `json:"version"`
		Sessions []hosted.Record `json:"sessions"`
	}{Version: 1, Sessions: records})
	if err != nil {
		t.Fatal(err)
	}

	return string(text) + "\n"
}

// A record file is read only up to its bound. Write never writes one that long (MaxRecords of
// the longest records fit), so a file over it is not one Conn wrote.
func TestReadRefusesAFileOverItsSize(t *testing.T) {
	t.Parallel()

	written := writtenText(t, longestRecords())
	if len(written) > hosted.MaxFileBytes() {
		t.Fatalf("the longest records are %d bytes, over the bound %d", len(written),
			hosted.MaxFileBytes())
	}

	// The same records, with spaces up to a byte over the bound before the closing brace.
	padded := written[:len(written)-2] +
		strings.Repeat(" ", hosted.MaxFileBytes()+1-len(written)) + "}\n"

	dir := t.TempDir()
	writeText(t, dir, padded)

	_, err := hosted.Read(dir)
	if !errors.Is(err, hosted.ErrBadFile) {
		t.Errorf("Read: %v, want %v", err, hosted.ErrBadFile)
	}
}
