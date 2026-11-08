package hosted_test

import (
	"context"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
)

const allowedRoot = "/data/runs/"

// starts is a Start that records what it was asked to start.
type starts struct{ specs []pty.Spec }

func (s *starts) start(_ context.Context, _ hosted.Record, spec pty.Spec) (pty.ID, bool) {
	s.specs = append(s.specs, spec)

	return pty.ID(hostID(len(s.specs) + recordsTried)), true
}

// ownSpec is what the stand-in owner builds: its own program and arguments, never the record's,
// in the folder only when the owner allows it.
func ownSpec(record hosted.Record) pty.Spec {
	return pty.Spec{
		Argv: []string{"/opt/claude", "--resume", claudeID, "--name", record.Name},
		Dir:  record.Dir, Env: nil, Name: record.Name,
	}
}

// ownerChecks vouch for an Open work record whose folder is under allowedRoot.
func ownerChecks() hosted.Checks {
	return hosted.Checks{
		hosted.OwnerOpenWork: func(_ context.Context, record hosted.Record) (pty.Spec, bool) {
			if len(record.Dir) <= len(allowedRoot) {
				return pty.Spec{}, false
			}

			if record.Dir[:len(allowedRoot)] != allowedRoot {
				return pty.Spec{}, false
			}

			return ownSpec(record), true
		},
	}
}

func manyRecords(count int) []hosted.Record {
	records := make([]hosted.Record, 0, count)
	for index := range count {
		records = append(records, record(index))
	}

	return records
}

func TestResume(t *testing.T) {
	t.Parallel()

	outside, notUUID, noCheck := record(0), record(0), record(0)
	outside.Dir = "/etc"
	notUUID.ClaudeSessionID = "not-a-uuid"
	noCheck.Owner = hosted.OwnerHistory

	tests := []struct {
		name        string
		records     []hosted.Record
		wantStarted []pty.Spec
		wantSkipped int
	}{
		{
			"each vouched record", manyRecords(2),
			[]pty.Spec{ownSpec(record(0)), ownSpec(record(1))},
			0,
		},
		{"a folder the owner does not allow", []hosted.Record{outside}, nil, 1},
		{"a session id that is not a UUID", []hosted.Record{notUUID}, nil, 1},
		{"an owner with no check", []hosted.Record{noCheck}, nil, 1},
		{
			"the same session twice",
			[]hosted.Record{record(0), record(0)},
			[]pty.Spec{ownSpec(record(0))},
			1,
		},
		{
			"500 records", manyRecords(recordsTried), specsOf(manyRecords(hosted.RecordsMax())),
			recordsTried - hosted.RecordsMax(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var started starts

			outcome := hosted.Resume(t.Context(), tt.records, ownerChecks(), started.start)

			if !slices.EqualFunc(started.specs, tt.wantStarted, specEqual) {
				t.Errorf("started %+v, want %+v", started.specs, tt.wantStarted)
			}

			if outcome.Skipped != tt.wantSkipped {
				t.Errorf("skipped %d, want %d", outcome.Skipped, tt.wantSkipped)
			}

			if len(outcome.Resumed) != len(tt.wantStarted) {
				t.Errorf("resumed %d, want %d", len(outcome.Resumed), len(tt.wantStarted))
			}
		})
	}
}

func specsOf(records []hosted.Record) []pty.Spec {
	specs := make([]pty.Spec, 0, len(records))
	for _, each := range records {
		specs = append(specs, ownSpec(each))
	}

	return specs
}

func specEqual(got, want pty.Spec) bool {
	if !slices.Equal(got.Argv, want.Argv) {
		return false
	}

	if got.Dir != want.Dir {
		return false
	}

	return got.Name == want.Name
}

func TestResumeSaysWhatItStarted(t *testing.T) {
	t.Parallel()

	var started starts

	outcome := hosted.Resume(t.Context(), manyRecords(1), ownerChecks(), started.start)

	want := []hosted.Resumed{{ID: pty.ID(hostID(1 + recordsTried)), Name: "KEY-1"}}
	if !slices.Equal(outcome.Resumed, want) {
		t.Errorf("resumed %+v, want %+v", outcome.Resumed, want)
	}
}

func TestResumeCountsAStartThatFailed(t *testing.T) {
	t.Parallel()

	failing := func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) { return "", false }

	outcome := hosted.Resume(t.Context(), manyRecords(1), ownerChecks(), failing)

	if len(outcome.Resumed) != 0 {
		t.Errorf("resumed %+v from a start that failed", outcome.Resumed)
	}

	if outcome.Skipped != 1 {
		t.Errorf("skipped %d, want 1", outcome.Skipped)
	}
}

func TestResumeStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var started starts

	outcome := hosted.Resume(ctx, manyRecords(2), ownerChecks(), started.start)

	if len(started.specs) != 0 {
		t.Errorf("started %d after the context ended", len(started.specs))
	}

	if outcome.Skipped != 2 {
		t.Errorf("skipped %d, want 2", outcome.Skipped)
	}

	if !slices.Equal(outcome.Unreached, manyRecords(2)) {
		t.Errorf("unreached %+v, want both records", outcome.Unreached)
	}
}

func TestResumeHandsTheStartItsRecord(t *testing.T) {
	t.Parallel()

	var got hosted.Record

	start := func(_ context.Context, record hosted.Record, _ pty.Spec) (pty.ID, bool) {
		got = record

		return pty.ID(hostID(0)), true
	}

	hosted.Resume(t.Context(), manyRecords(1), ownerChecks(), start)

	if got != record(0) {
		t.Errorf("started for %+v, want %+v", got, record(0))
	}
}

// A context that ends during the resume leaves the records not yet resumed unreached, the one
// whose start the end refused among them, so the record can keep them for the next launch.
func TestResumeSaysWhichRecordsItDidNotReach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// startedBeforeEnd is whether the start that ends the context still starts its session.
		startedBeforeEnd bool
		wantUnreached    []hosted.Record
	}{
		{"the quit comes after the first start", true, manyRecords(3)[1:]},
		{"the quit refuses the first start", false, manyRecords(3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			start := func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) {
				cancel()

				return pty.ID(hostID(recordsTried)), tt.startedBeforeEnd
			}

			outcome := hosted.Resume(ctx, manyRecords(3), ownerChecks(), start)

			if !slices.Equal(outcome.Unreached, tt.wantUnreached) {
				t.Errorf("unreached %+v, want %+v", outcome.Unreached, tt.wantUnreached)
			}
		})
	}
}

// A check that refuses because the context ended has not looked at the record: it is unreached.
func TestResumeLeavesARecordWhoseCheckTheEndCutShortUnreached(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	checks := hosted.Checks{
		hosted.OwnerOpenWork: func(context.Context, hosted.Record) (pty.Spec, bool) {
			cancel()

			return pty.Spec{}, false
		},
	}

	var started starts

	outcome := hosted.Resume(ctx, manyRecords(2), checks, started.start)

	if !slices.Equal(outcome.Unreached, manyRecords(2)) {
		t.Errorf("unreached %+v, want both records", outcome.Unreached)
	}
}

func TestResumeThatFinishesLeavesNothingUnreached(t *testing.T) {
	t.Parallel()

	var started starts

	outcome := hosted.Resume(t.Context(), manyRecords(2), ownerChecks(), started.start)

	if len(outcome.Unreached) != 0 {
		t.Errorf("unreached %+v after a resume that finished", outcome.Unreached)
	}
}
