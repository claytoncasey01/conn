package desk_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/desk"
)

// notificationCasesFile holds the limits and cases the page's tests read too
// (src/lib/nativeNotify.test.ts), so the page never sends what the app refuses.
const notificationCasesFile = "../../testdata/notification-cases.json"

// notificationCases is testdata/notification-cases.json.
type notificationCases struct {
	Limits struct {
		Title int `json:"title"`
		Body  int `json:"body"`
		Tag   int `json:"tag"`
		Href  int `json:"href"`
	} `json:"limits"`
	Cases []struct {
		Name string `json:"name"`
		Sent struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Tag   string `json:"tag"`
			Href  string `json:"href"`
		} `json:"sent"`
		OK bool `json:"ok"`
	} `json:"cases"`
}

func readNotificationCases(t *testing.T) notificationCases {
	t.Helper()

	data, err := os.ReadFile(notificationCasesFile)
	if err != nil {
		t.Fatalf("reading %s: %v", notificationCasesFile, err)
	}

	var cases notificationCases

	err = json.Unmarshal(data, &cases)
	if err != nil {
		t.Fatalf("parsing %s: %v", notificationCasesFile, err)
	}

	return cases
}

// The limits in the shared file are the app's, so the page cuts its text to what the app takes.
func TestNotificationLimitsMatchTheSharedCases(t *testing.T) {
	t.Parallel()

	limits := readNotificationCases(t).Limits

	tests := []struct {
		name        string
		shared, app int
	}{
		{"title", limits.Title, desk.MaxNotificationTitleRunes},
		{"body", limits.Body, desk.MaxNotificationBodyRunes},
		{"tag", limits.Tag, desk.MaxNotificationTagRunes},
		{"href", limits.Href, desk.MaxNotificationHrefRunes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.shared != tt.app {
				t.Errorf("%s holds %d for the %s, the app %d",
					notificationCasesFile, tt.shared, tt.name, tt.app)
			}
		})
	}
}

func TestCheckNotificationSharedCases(t *testing.T) {
	t.Parallel()

	for _, tt := range readNotificationCases(t).Cases {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			err := desk.CheckNotification(desk.Notification{
				Title: tt.Sent.Title, Body: tt.Sent.Body, Tag: tt.Sent.Tag, Href: tt.Sent.Href,
			})
			if tt.OK && err != nil {
				t.Errorf("refused: %v", err)
			}

			if !tt.OK && !errors.Is(err, desk.ErrNotification) {
				t.Errorf("got %v, want ErrNotification", err)
			}
		})
	}
}

// Each field is held to its limit: at the limit it is shown, one character over it is refused.
func TestCheckNotificationLimits(t *testing.T) {
	t.Parallel()

	fits := desk.Notification{Title: "t", Body: "b", Tag: "g", Href: "#/s/1"}
	// A two-byte character, so the limit is shown to count characters, not bytes.
	long := func(count int) string { return strings.Repeat("é", count) }
	route := func(count int) string { return "#/" + strings.Repeat("a", count-len("#/")) }

	tests := []struct {
		name    string
		edit    func(*desk.Notification)
		wantErr error
	}{
		{"title at the limit", func(n *desk.Notification) {
			n.Title = long(desk.MaxNotificationTitleRunes)
		}, nil},
		{"title over it", func(n *desk.Notification) {
			n.Title = long(desk.MaxNotificationTitleRunes + 1)
		}, desk.ErrNotification},
		{"body at the limit", func(n *desk.Notification) {
			n.Body = long(desk.MaxNotificationBodyRunes)
		}, nil},
		{"body over it", func(n *desk.Notification) {
			n.Body = long(desk.MaxNotificationBodyRunes + 1)
		}, desk.ErrNotification},
		{"tag at the limit", func(n *desk.Notification) {
			n.Tag = long(desk.MaxNotificationTagRunes)
		}, nil},
		{"tag over it", func(n *desk.Notification) {
			n.Tag = long(desk.MaxNotificationTagRunes + 1)
		}, desk.ErrNotification},
		{"href at the limit", func(n *desk.Notification) {
			n.Href = route(desk.MaxNotificationHrefRunes)
		}, nil},
		{"href over it", func(n *desk.Notification) {
			n.Href = route(desk.MaxNotificationHrefRunes + 1)
		}, desk.ErrNotification},
		{"text that is not UTF-8", func(n *desk.Notification) {
			n.Body = "\xff"
		}, desk.ErrNotification},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			notification := fits
			tt.edit(&notification)

			err := desk.CheckNotification(notification)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Every problem is reported at once, not only the first.
func TestCheckNotificationReportsEveryProblem(t *testing.T) {
	t.Parallel()

	err := desk.CheckNotification(desk.Notification{
		Title: "", Body: "a\nb", Tag: "", Href: "https://example.com/",
	})

	for _, field := range []string{"title", "body", "tag", "href"} {
		if err == nil || !strings.Contains(err.Error(), "the "+field) {
			t.Errorf("the error does not name the %s: %v", field, err)
		}
	}
}
