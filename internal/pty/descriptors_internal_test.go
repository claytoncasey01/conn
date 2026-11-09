//go:build darwin || linux

package pty

import (
	"errors"
	"testing"
)

var (
	errNoListing = errors.New("no /dev/fd here")
	errNoLimit   = errors.New("no descriptor limit")
)

// Where /dev/fd cannot be listed (a Linux without /proc), the sweep walks the descriptor numbers
// up to the process's limit instead; it refuses the spawn only when it cannot learn even that.
func TestTheSweepFallsBackToAWalkAndFailsClosedWithout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		list    func() ([]string, error)
		limit   func() (int, error)
		wantErr error
	}{
		{
			"the listing", func() ([]string, error) { return []string{"0", "1", "2"}, nil },
			func() (int, error) { return 0, errNoLimit }, nil,
		},
		{
			"no listing, a limit", func() ([]string, error) { return nil, errNoListing },
			func() (int, error) { return firstInherited + 1, nil }, nil,
		},
		{
			"no listing and no limit", func() ([]string, error) { return nil, errNoListing },
			func() (int, error) { return 0, errNoLimit }, errNoLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := sweepDescriptors(descriptorSources{list: tt.list, limit: tt.limit})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("sweep = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The walk covers every number below the limit, the limit itself capped at maxDescriptorsSwept.
func TestTheWalkStopsAtTheCappedLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"a small limit", 10, 10},
		{"a limit past the cap", maxDescriptorsSwept * 4, maxDescriptorsSwept},
		{"no room past stderr", 2, firstInherited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			names, err := walkedDescriptors(func() (int, error) { return tt.limit, nil })
			if err != nil {
				t.Fatal(err)
			}

			if len(names) != tt.want-firstInherited {
				t.Fatalf("walked %d numbers, want %d", len(names), tt.want-firstInherited)
			}
		})
	}
}
