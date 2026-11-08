package instance

import (
	"errors"
	"testing"
)

// The checks on plain facts, for the cases a test cannot make on disk: a file another user owns.
func TestFileProblem(t *testing.T) {
	t.Parallel()

	const userID = 501

	file := func(uid int, links uint64, regular bool) owner {
		return owner{folder: false, regular: regular, uid: uid, mode: 0o600, links: links}
	}

	tests := []struct {
		name    string
		facts   owner
		wantErr error
	}{
		{"my plain file", file(userID, 1, true), nil},
		{"another user's file", file(userID+1, 1, true), ErrUnsafe},
		{"root's file", file(0, 1, true), ErrUnsafe},
		{"a second name", file(userID, 2, true), ErrUnsafe},
		{"not a plain file", file(userID, 1, false), ErrUnsafe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := fileProblem("/data/conn.lock", tt.facts, userID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("fileProblem = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestFolderProblem(t *testing.T) {
	t.Parallel()

	const userID = 501

	tests := []struct {
		name    string
		facts   owner
		wantErr error
	}{
		{"private", owner{folder: true, regular: false, uid: userID, mode: 0o700, links: 2}, nil},
		{
			"readable by others",
			owner{folder: true, regular: false, uid: userID, mode: 0o755, links: 2},
			nil,
		},
		{
			"group writable",
			owner{folder: true, regular: false, uid: userID, mode: 0o775, links: 2},
			ErrUnsafe,
		},
		{
			"other writable",
			owner{folder: true, regular: false, uid: userID, mode: 0o757, links: 2},
			ErrUnsafe,
		},
		{
			"another user's",
			owner{folder: true, regular: false, uid: userID + 1, mode: 0o700, links: 2},
			ErrUnsafe,
		},
		{
			"not a folder",
			owner{folder: false, regular: true, uid: userID, mode: 0o700, links: 1},
			ErrUnsafe,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := folderProblem("/data", tt.facts, userID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("folderProblem = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
