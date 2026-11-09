package desk

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The limits on a notification the page asks the app to show, in characters. The page cuts its
// text to fit them before it asks (src/lib/nativeNotify.ts), and
// testdata/notification-cases.json holds both sides to the same numbers.
const (
	// MaxNotificationTitleRunes bounds the title: a session's name and an agent's, and a verb.
	MaxNotificationTitleRunes = 256
	// MaxNotificationBodyRunes bounds the body: a prompt's words, cut to one line.
	MaxNotificationBodyRunes = 1024
	// MaxNotificationTagRunes bounds the tag: an `s`, `r` or PR key and an id.
	MaxNotificationTagRunes = 128
	// MaxNotificationHrefRunes bounds the page's route the notification opens.
	MaxNotificationHrefRunes = 512
)

// hrefPrefix starts every route of the page, so a notification can only open a page of Conn.
const hrefPrefix = "#/"

// ErrNotification is a notification the app will not show; the wrapping error says why.
var ErrNotification = errors.New("the notification cannot be shown")

// Notification is what the page asks the app to show. A later one with the same Tag replaces an
// earlier one, and clicking one opens Href in the window.
type Notification struct {
	Title string
	Body  string
	Tag   string
	Href  string
}

// CheckNotification is nil when the app may show notification, and every problem with it
// otherwise, each wrapping ErrNotification. Text must be one line with no control characters,
// within its limit; the title, tag and href must not be empty, and href must be a route of the
// page.
func CheckNotification(notification Notification) error {
	return errors.Join(
		checkText("title", notification.Title, MaxNotificationTitleRunes, true),
		checkText("body", notification.Body, MaxNotificationBodyRunes, false),
		checkText("tag", notification.Tag, MaxNotificationTagRunes, true),
		checkText("href", notification.Href, MaxNotificationHrefRunes, true),
		checkHref(notification.Href),
	)
}

// checkText is nil when text is valid UTF-8 of at most limit characters with no control
// character, and not empty when required.
func checkText(field, text string, limit int, required bool) error {
	if required && text == "" {
		return fmt.Errorf("%w: the %s is empty", ErrNotification, field)
	}

	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: the %s is not UTF-8", ErrNotification, field)
	}

	count := utf8.RuneCountInString(text)
	if count > limit {
		return fmt.Errorf("%w: the %s has %d characters, over the limit of %d",
			ErrNotification, field, count, limit)
	}

	if strings.ContainsFunc(text, unicode.IsControl) {
		return fmt.Errorf("%w: the %s has a control character", ErrNotification, field)
	}

	return nil
}

// checkHref is nil when href is a route of the page, or empty (checkText reports that).
func checkHref(href string) error {
	if href == "" || strings.HasPrefix(href, hrefPrefix) {
		return nil
	}

	return fmt.Errorf("%w: the href is not a route of the page: it must start with %q",
		ErrNotification, hrefPrefix)
}
