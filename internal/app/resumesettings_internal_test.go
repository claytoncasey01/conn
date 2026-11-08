package app

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

// keptSaid is what a launch with sessions opening elsewhere says of a History record and a
// review record.
const keptSaid = "1 hosted sessions kept; they resume when sessions open inside Conn again; " +
	reviewsNotKept

// With sessions opening elsewhere, a launch resumes nothing but keeps every record for when they
// open inside Conn again; with resume on launch off, it drops them. Either way it says so.
func TestResumeAtLaunchKeepsOrDropsTheRecordsAsTheSettingsSay(t *testing.T) {
	t.Parallel()

	kept := historyRecord()
	records := []hosted.Record{resumeRecord(), kept}

	tests := []struct {
		name          string
		sessions      settings.Sessions
		wantUnreached []hosted.Record
		wantSaid      string
	}{
		{
			"sessions open in a terminal app",
			settings.Sessions{Where: settings.WhereTerminal, ResumeOnLaunch: true},
			[]hosted.Record{kept},
			keptSaid,
		},
		{
			"sessions open as a command",
			settings.Sessions{Where: settings.WhereCommand, ResumeOnLaunch: true},
			[]hosted.Record{kept},
			keptSaid,
		},
		{
			"resume on launch off",
			settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: false},
			nil, "2 hosted sessions dropped: resume on launch is off",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var said bytes.Buffer

			outcome := resumeAtLaunch(t.Context(), launchResume{
				sessions: tt.sessions, records: records, readErr: nil, checks: resumeChecks(),
				start: func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) {
					return "", false
				},
				ready: ticked, stderr: &said,
			})

			if !slices.Equal(outcome.Unreached, tt.wantUnreached) {
				t.Errorf("kept %+v, want %+v", outcome.Unreached, tt.wantUnreached)
			}

			if !strings.Contains(said.String(), tt.wantSaid) {
				t.Errorf("said %q, want %q", said.String(), tt.wantSaid)
			}
		})
	}
}
