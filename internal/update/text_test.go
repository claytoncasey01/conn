package update_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/update"
)

// serverText stands for text a server sent, which must never reach what the person sees.
const serverText = "<script>evil</script>"

// errServer is an error no row of the reasons table knows, holding the server's text.
var errServer = errors.New(serverText)

func TestTellDialog(t *testing.T) {
	t.Parallel()

	failed := update.Result{Kind: update.Failed, Latest: "", Err: update.ErrRequest}
	available := update.Result{Kind: update.Available, Latest: "v1.2.4", Err: nil}
	upToDate := update.Result{Kind: update.UpToDate, Latest: "v1.2.3", Err: nil}
	unchecked := update.Result{Kind: update.Unchecked, Latest: "", Err: nil}
	publishing := update.Result{Kind: update.Publishing, Latest: "v1.2.4", Err: nil}

	tests := []struct {
		name   string
		event  update.Event
		dialog update.Dialog
		title  string
	}{
		{
			"fresh release", newEvent(available, false, true), update.DialogOffer,
			"Conn v1.2.4 is available",
		},
		{"release told before", newEvent(available, false, false), update.DialogNone, ""},
		{
			"release asked for", newEvent(available, true, true), update.DialogOffer,
			"Conn v1.2.4 is available",
		},
		{"failure by itself", newEvent(failed, false, false), update.DialogNone, ""},
		{
			"failure asked for", newEvent(failed, true, true), update.DialogWarning,
			"Could not check for updates",
		},
		{"up to date by itself", newEvent(upToDate, false, false), update.DialogNone, ""},
		{
			"up to date asked for", newEvent(upToDate, true, true), update.DialogInfo,
			"Conn is up to date (v1.2.3)",
		},
		{"dev build by itself", newEvent(unchecked, false, false), update.DialogNone, ""},
		{
			"dev build asked for", newEvent(unchecked, true, true), update.DialogInfo,
			"This is a development build, which does not check for updates",
		},
		{"publishing by itself", newEvent(publishing, false, false), update.DialogNone, ""},
		{
			"publishing asked for", newEvent(publishing, true, true), update.DialogInfo,
			"A new release is being published",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			notice := update.Tell(tt.event, "v1.2.3")
			if notice.Dialog != tt.dialog {
				t.Errorf("Dialog = %v, want %v", notice.Dialog, tt.dialog)
			}

			if notice.Title != tt.title {
				t.Errorf("Title = %q, want %q", notice.Title, tt.title)
			}
		})
	}
}

func TestTellNoteAndLabel(t *testing.T) {
	t.Parallel()

	failed := update.Result{Kind: update.Failed, Latest: "", Err: update.ErrRequest}
	available := update.Result{Kind: update.Available, Latest: "v1.2.4", Err: nil}
	upToDate := update.Result{Kind: update.UpToDate, Latest: "v1.2.3", Err: nil}
	publishing := update.Result{Kind: update.Publishing, Latest: "v1.2.4", Err: nil}

	tests := []struct {
		name  string
		event update.Event
		note  string
		label string
	}{
		{
			"fresh release", newEvent(available, false, true),
			"Conn v1.2.4 is available (you have v1.2.3)", "Update Available: v1.2.4…",
		},
		{
			"release told before", newEvent(available, false, false),
			"Conn v1.2.4 is available (you have v1.2.3)", "Update Available: v1.2.4…",
		},
		{
			"failure", newEvent(failed, false, false),
			"could not check for updates: Could not reach GitHub.", "",
		},
		{"up to date", newEvent(upToDate, true, true), "", ""},
		{
			"publishing", newEvent(publishing, false, false),
			"Conn v1.2.4 is being published, without its app yet", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			notice := update.Tell(tt.event, "v1.2.3")
			if notice.Note != tt.note {
				t.Errorf("Note = %q, want %q", notice.Note, tt.note)
			}

			if notice.Label != tt.label {
				t.Errorf("Label = %q, want %q", notice.Label, tt.label)
			}
		})
	}
}

func TestOfferText(t *testing.T) {
	t.Parallel()

	event := update.Event{
		Result: update.Result{Kind: update.Available, Latest: "v1.2.4", Err: nil},
		Manual: false,
		Fresh:  true,
	}
	notice := update.Tell(event, "v1.2.3")

	tests := []struct {
		name string
		text string
		want string
	}{
		{"title names the release", notice.Title, "v1.2.4"},
		{"message names the release", notice.Message, "Conn v1.2.4 is available"},
		{"message names what the person has", notice.Message, "(you have v1.2.3)"},
		{"message says to quit first", notice.Message, "quit Conn first"},
		{"message holds the command", notice.Message, update.Command},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(tt.text, tt.want) {
				t.Errorf("%q does not contain %q", tt.text, tt.want)
			}
		})
	}
}

func TestPublishingAnswerClaimsNoFailure(t *testing.T) {
	t.Parallel()

	event := update.Event{
		Result: update.Result{Kind: update.Publishing, Latest: "v1.2.4", Err: nil},
		Manual: true,
		Fresh:  true,
	}

	notice := update.Tell(event, "v1.2.3")
	if notice.Message != "Try again in a few minutes." {
		t.Errorf("Message = %q, want %q", notice.Message, "Try again in a few minutes.")
	}
}

func TestFailureReason(t *testing.T) {
	t.Parallel()

	noRelease := "GitHub's answer did not name a release."
	tooLong := update.Evaluate("v1.0.0", longTag(update.MaxTagBytes+1), nil).Err

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"request", update.ErrRequest, "Could not reach GitHub."},
		{
			"deadline", fmt.Errorf("%w: %w", update.ErrRequest, context.DeadlineExceeded),
			"GitHub did not answer in time.",
		},
		{"bad base", update.ErrBadBase, "The releases address in CONN_UPDATE_BASE is not usable."},
		{"status", update.ErrStatus, noRelease},
		{"no redirect", update.ErrNoRedirect, noRelease},
		{"bad location", update.ErrBadLocation, noRelease},
		{"not a version", update.Evaluate("v1.0.0", "garbage", nil).Err, noRelease},
		{"tag too long", tooLong, noRelease},
		{"server text", fmt.Errorf("%s: %w", serverText, update.ErrStatus), noRelease},
		{
			"app status", update.ErrAppStatus,
			"GitHub's answer did not say whether the release's app is ready.",
		},
		{"unknown", errServer, "The check failed for an unexpected reason."},
		{"nil", nil, "The check failed for an unexpected reason."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := update.FailureReason(tt.err)
			if got != tt.want {
				t.Errorf("FailureReason(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestFailedTextHoldsNoServerText(t *testing.T) {
	t.Parallel()

	failed := update.Result{
		Kind:   update.Failed,
		Latest: "",
		Err:    fmt.Errorf("%s: %w", serverText, update.ErrRequest),
	}

	tests := []struct {
		name  string
		event update.Event
	}{
		{"asked for", newEvent(failed, true, true)},
		{"by itself", newEvent(failed, false, false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			notice := update.Tell(tt.event, "v1.2.3")
			fields := []struct {
				name string
				text string
			}{
				{"Title", notice.Title},
				{"Message", notice.Message},
				{"Note", notice.Note},
				{"Label", notice.Label},
			}

			for _, field := range fields {
				if strings.Contains(field.text, serverText) {
					t.Errorf("%s %q holds the server's text", field.name, field.text)
				}
			}
		})
	}
}

func TestBaseFrom(t *testing.T) {
	t.Parallel()

	const testBase = "http://127.0.0.1:8080/x/releases"

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"unset", "", update.ReleasesURL},
		{"blank", " \t", update.ReleasesURL},
		{"set", "http://127.0.0.1:8080/x/releases", "http://127.0.0.1:8080/x/releases"},
		{"set with spaces", " " + testBase + "\n", testBase},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := update.BaseFrom(tt.raw)
			if got != tt.want {
				t.Errorf("BaseFrom(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestReleaseNotesURL(t *testing.T) {
	t.Parallel()

	got := update.ReleaseNotesURL("v1.2.4")
	want := "https://github.com/claytoncasey01/conn/releases/tag/v1.2.4"

	if got != want {
		t.Errorf("ReleaseNotesURL = %q, want %q", got, want)
	}
}

func TestTextRefusesATagThatIsNotAVersion(t *testing.T) {
	t.Parallel()

	tags := []struct {
		name string
		tag  string
	}{
		{"path", "v1.2.4/../../evil"},
		{"query", "v1.2.4?x=1"},
		{"empty", ""},
		{"too long", "v1.2.4-" + strings.Repeat("a", 64)},
	}

	texts := []struct {
		name string
		text func(tag string) string
	}{
		{"ReleaseNotesURL", update.ReleaseNotesURL},
		{"AvailableLabel", update.AvailableLabel},
		{"OfferTitle", update.OfferTitle},
		{"OfferMessage", func(tag string) string { return update.OfferMessage("v1.2.3", tag) }},
		{"Tell", func(tag string) string {
			result := update.Result{Kind: update.Available, Latest: tag, Err: nil}

			return update.Tell(newEvent(result, false, true), "v1.2.3").Title
		}},
	}

	for _, text := range texts {
		for _, tag := range tags {
			t.Run(text.name+" "+tag.name, func(t *testing.T) {
				t.Parallel()

				defer func() {
					if recover() == nil {
						t.Errorf("%s(%q) did not panic", text.name, tag.tag)
					}
				}()

				_ = text.text(tag.tag)
			})
		}
	}
}

// newEvent is an Event with its fields named, for the tables above.
func newEvent(result update.Result, manual, fresh bool) update.Event {
	return update.Event{Result: result, Manual: manual, Fresh: fresh}
}
