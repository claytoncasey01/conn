package update

import (
	"context"
	"errors"
	"strings"
)

const (
	// CheckVariable is the environment variable that switches the check off (see Enabled).
	CheckVariable = "CONN_UPDATE_CHECK"

	// BaseVariable is the environment variable that replaces ReleasesURL as the address asked,
	// for trying a build against a test server. Release Notes always opens ReleasesURL.
	BaseVariable = "CONN_UPDATE_BASE"

	// MenuLabel is the app menu item that checks at once.
	MenuLabel = "Check for Updates…"

	// ButtonCopy puts Command on the clipboard.
	ButtonCopy = "Copy Command"
	// ButtonNotes opens the release's page in the browser.
	ButtonNotes = "Release Notes"
	// ButtonLater closes the offer.
	ButtonLater = "Later"
	// ButtonOK closes an answer.
	ButtonOK = "OK"
)

// Dialog is which dialog the app shows for a check.
type Dialog int

const (
	// DialogNone shows nothing.
	DialogNone Dialog = iota
	// DialogOffer is the update offer, with the Copy Command, Release Notes and Later buttons.
	DialogOffer
	// DialogInfo is an answer with one OK button.
	DialogInfo
	// DialogWarning is a failed check, with one OK button.
	DialogWarning
)

// Notice is everything the app says about one check. Its text holds only the current version,
// a tag that passed the version pattern, Command and fixed wording: nothing the server sent.
type Notice struct {
	// Dialog is which dialog to show, DialogNone for none.
	Dialog Dialog
	// Title and Message are the dialog's text, empty for DialogNone.
	Title   string
	Message string
	// Note is one line for stderr, empty for none.
	Note string
	// Label is the menu item's new label, empty to leave it as it is.
	Label string
}

// failureRow is a short, fixed reason for one kind of failed check.
type failureRow struct {
	err    error
	reason string
}

// defaultFailureReason is the reason for a failure no row of failureReasons names.
const defaultFailureReason = "The check failed for an unexpected reason."

// failureReasons maps what went wrong to what the person is told, first match wins. The reason
// is fixed text, never the error's own, so nothing the server sent can reach the screen.
func failureReasons() []failureRow {
	const noRelease = "GitHub's answer did not name a release."

	return []failureRow{
		{context.DeadlineExceeded, "GitHub did not answer in time."},
		{errRequest, "Could not reach GitHub."},
		{errBadBase, "The releases address in " + BaseVariable + " is not usable."},
		{errStatus, noRelease},
		{errNoRedirect, noRelease},
		{errBadLocation, noRelease},
		{errNotVersion, noRelease},
		{errTagTooLong, noRelease},
		{errAppStatus, "GitHub's answer did not say whether the release's app is ready."},
	}
}

// failureReason is a short sentence saying why a check failed, chosen from fixed text.
func failureReason(err error) string {
	for _, row := range failureReasons() {
		if errors.Is(err, row.err) {
			return row.reason
		}
	}

	return defaultFailureReason
}

// availableLabel is the menu item's label once latest is known to be available.
func availableLabel(latest string) string {
	return "Update Available: " + releaseTag(latest) + "…"
}

// offerTitle is the title of the update offer.
func offerTitle(latest string) string {
	return "Conn " + releaseTag(latest) + " is available"
}

// offerMessage says what is available and what the person has, that Conn must be quit first,
// and the command.
func offerMessage(current, latest string) string {
	return availableSentence(current, latest) + ". " +
		"To update, quit Conn first, because the installer will not replace a running app. " +
		"Then run this command in Terminal:\n\n" + Command
}

// availableSentence says that latest is available and which version the person has, with no
// full stop, so the offer and the stderr note say it the same way.
func availableSentence(current, latest string) string {
	return "Conn " + releaseTag(latest) + " is available (you have " + current + ")"
}

// BaseFrom is the releases address to ask, given the value of CONN_UPDATE_BASE without the space
// around it: ReleasesURL when it is unset or blank, so only a developer's deliberate setting
// replaces it.
func BaseFrom(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ReleasesURL
	}

	return trimmed
}

// ReleaseNotesURL is the page of the release tag on ReleasesURL. It is built only from the
// constant and a tag of the version shape, never from the address the check asked.
func ReleaseNotesURL(tag string) string {
	return ReleasesURL + "/tag/" + releaseTag(tag)
}

// Tell is what the app says about event, given the version of this build. An offer is shown
// once per release (Fresh) and always when asked; an answer only when asked; a found release, a
// release being published and a failure are noted on stderr every time.
func Tell(event Event, current string) Notice {
	switch event.Result.Kind {
	case Available:
		return tellAvailable(event, current)
	case Failed:
		return tellFailed(event)
	case UpToDate:
		return tellUpToDate(event, current)
	case Unchecked:
		return tellUnchecked(event, current)
	case Publishing:
		return tellPublishing(event)
	}

	panic("update.Tell: a Kind this switch does not know")
}

// tellAvailable sets the label, notes the release, and offers it when it is news.
func tellAvailable(event Event, current string) Notice {
	latest := releaseTag(event.Result.Latest)
	notice := Notice{
		Dialog:  DialogNone,
		Title:   "",
		Message: "",
		Note:    availableSentence(current, latest),
		Label:   availableLabel(latest),
	}

	if event.Fresh {
		notice.Dialog = DialogOffer
		notice.Title = offerTitle(latest)
		notice.Message = offerMessage(current, latest)
	}

	return notice
}

// tellFailed notes the failure, and answers with it when asked.
func tellFailed(event Event) Notice {
	reason := failureReason(event.Result.Err)
	notice := Notice{
		Dialog: DialogNone, Title: "", Message: "", Label: "",
		Note: "could not check for updates: " + reason,
	}

	if event.Manual {
		notice.Dialog = DialogWarning
		notice.Title = "Could not check for updates"
		notice.Message = reason
	}

	return notice
}

// tellUpToDate answers when asked, and says nothing otherwise.
func tellUpToDate(event Event, current string) Notice {
	if !event.Manual {
		return Notice{Dialog: DialogNone, Title: "", Message: "", Note: "", Label: ""}
	}

	return Notice{
		Dialog:  DialogInfo,
		Title:   "Conn is up to date (" + current + ")",
		Message: "The latest release is " + releaseTag(event.Result.Latest) + ".",
		Note:    "",
		Label:   "",
	}
}

// tellUnchecked answers when asked that this build does not check.
func tellUnchecked(event Event, current string) Notice {
	if !event.Manual {
		return Notice{Dialog: DialogNone, Title: "", Message: "", Note: "", Label: ""}
	}

	return Notice{
		Dialog:  DialogInfo,
		Title:   "This is a development build, which does not check for updates",
		Message: "Its version is " + current + ", not a release such as v1.2.3.",
		Note:    "",
		Label:   "",
	}
}

// tellPublishing notes a release whose app is not attached yet, and answers with it when asked.
// It offers nothing and claims no failure, because the next check looks again.
func tellPublishing(event Event) Notice {
	latest := releaseTag(event.Result.Latest)
	notice := Notice{
		Dialog: DialogNone, Title: "", Message: "", Label: "",
		Note: "Conn " + latest + " is being published, without its app yet",
	}

	if event.Manual {
		notice.Dialog = DialogInfo
		notice.Title = "A new release is being published"
		notice.Message = "Try again in a few minutes."
	}

	return notice
}

// releaseTag returns tag when it is a release tag short enough to show. Run reports only such
// tags, so any other is a bug in this package, not something the network sent.
func releaseTag(tag string) string {
	if len(tag) > maxTagBytes {
		panic("update: a release tag longer than tagBytesMax reached the text")
	}

	if !versionPattern.MatchString(tag) {
		panic("update: a tag that is not a release version reached the text")
	}

	return tag
}
