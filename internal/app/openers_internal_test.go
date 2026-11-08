package app

import (
	"context"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
)

const openedID = pty.ID("000000000000000000000000000000cc")

// openedRecorder is a recorder over a host whose one session, openedID, runs.
func openedRecorder(t *testing.T) *hosted.Recorder {
	t.Helper()

	return hosted.NewRecorder(hosted.RecorderDeps{
		Dir:    "/data",
		Live:   func() []pty.Info { return []pty.Info{{ID: openedID}} },
		Linked: func() map[pty.ID]string { return nil },
		Write:  func(string, []hosted.Record) error { return nil },
		Failed: func(err error) { t.Error(err) },
	})
}

// opensInside is an opener that starts every session inside Conn as openedID.
func opensInside(context.Context, pty.Spec) launch.Opened {
	return launch.Opened{OK: true, HostedID: openedID}
}

// ownerOpened is the owner a session opened through open is recorded under.
func ownerOpened(t *testing.T, recorder *hosted.Recorder, open launch.Opener) string {
	t.Helper()

	open(t.Context(), pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: "/data/reviews/pr-1/widgets", Env: nil,
		Name: "pr-1",
	})

	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("recorded %+v, want one session", records)
	}

	return records[0].Owner
}

// Each module's sessions are recorded under it, so the next launch hands each to its own check.
func TestEachModulesSessionsAreRecordedUnderIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		owner string
		open  func(openers ownedOpeners) launch.Opener
	}{
		{hosted.OwnerOpenWork, func(openers ownedOpeners) launch.Opener { return openers.work }},
		{hosted.OwnerReviews, func(openers ownedOpeners) launch.Opener { return openers.reviews }},
		{hosted.OwnerMyPrs, func(openers ownedOpeners) launch.Opener { return openers.myPrs }},
		{hosted.OwnerHistory, func(openers ownedOpeners) launch.Opener { return openers.history }},
	}

	for _, tt := range tests {
		t.Run(tt.owner, func(t *testing.T) {
			t.Parallel()

			recorder := openedRecorder(t)
			open := tt.open(ownedOpenersOf(recorder, opensInside))

			if got := ownerOpened(t, recorder, open); got != tt.owner {
				t.Errorf("recorded under %q, want %q", got, tt.owner)
			}
		})
	}
}

// My PRs launches through Reviews' launch, with its own opener: its sessions are its own.
func TestMyPrsLaunchesRecordUnderMyPrs(t *testing.T) {
	t.Parallel()

	recorder := openedRecorder(t)
	openers := ownedOpenersOf(recorder, opensInside)
	parts := reviewsParts{open: openers.reviews, selfOpen: openers.myPrs}

	if got := ownerOpened(t, recorder, selfLaunchDeps(parts).Open); got != hosted.OwnerMyPrs {
		t.Errorf("recorded under %q, want %q", got, hosted.OwnerMyPrs)
	}
}

// A resumed session is recorded under its record's owner, on its record's conversation, in its
// record's place: the next quit records it for the same check.
func TestAResumedSessionIsRecordedAsItsRecord(t *testing.T) {
	t.Parallel()

	for _, owner := range []string{
		hosted.OwnerOpenWork, hosted.OwnerReviews, hosted.OwnerMyPrs, hosted.OwnerHistory,
	} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()

			recorder := openedRecorder(t)
			record := resumeRecord()
			record.Owner = owner

			_, started := resumeStart(opensInside, recorder)(t.Context(), record, pty.Spec{
				Argv: []string{"/opt/claude"}, Dir: record.Dir, Env: nil, Name: record.Name,
			})
			if !started {
				t.Fatal("not started")
			}

			records := recorder.Records()
			if len(records) != 1 {
				t.Fatalf("recorded %+v, want one session", records)
			}

			want := record
			want.ID = string(openedID)

			if records[0] != want {
				t.Errorf("recorded %+v, want %+v", records[0], want)
			}
		})
	}
}
