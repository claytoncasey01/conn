package update

import (
	"net/url"
	"time"
)

// CheckTimeout exposes checkTimeout to the tests in package update_test.
const CheckTimeout time.Duration = checkTimeout

// MaxTagBytes exposes maxTagBytes to the tests in package update_test.
const MaxTagBytes = maxTagBytes

// ResponseHeaderTimeout exposes responseHeaderTimeout to the tests in package update_test.
const ResponseHeaderTimeout time.Duration = responseHeaderTimeout

// StartDelay exposes startDelay to the tests in package update_test.
const StartDelay time.Duration = startDelay

// CheckEvery exposes checkEvery to the tests in package update_test.
const CheckEvery time.Duration = checkEvery

// The sentinel errors, exposed to the tests in package update_test.
var (
	ErrBadBase     = errBadBase
	ErrNoRedirect  = errNoRedirect
	ErrStatus      = errStatus
	ErrRequest     = errRequest
	ErrBadLocation = errBadLocation
	ErrAppStatus   = errAppStatus
)

// TagFrom exposes tagFrom to the tests in package update_test.
func TagFrom(location string, request *url.URL, basePath string) (string, error) {
	return tagFrom(location, request, basePath)
}

// OffValues exposes offValues to the tests in package update_test.
func OffValues() []string { return offValues() }

// Newer exposes newer to the tests in package update_test.
func Newer(current, latest string) bool { return newer(current, latest) }

// Evaluate exposes evaluate to the tests in package update_test.
func Evaluate(current, latest string, fetchErr error) Result {
	return evaluate(current, latest, fetchErr)
}

// FailureReason exposes failureReason to the tests in package update_test.
func FailureReason(err error) string { return failureReason(err) }

// AvailableLabel exposes availableLabel to the tests in package update_test.
func AvailableLabel(latest string) string { return availableLabel(latest) }

// OfferTitle exposes offerTitle to the tests in package update_test.
func OfferTitle(latest string) string { return offerTitle(latest) }

// OfferMessage exposes offerMessage to the tests in package update_test.
func OfferMessage(current, latest string) string { return offerMessage(current, latest) }

// WithApp exposes withApp to the tests in package update_test.
func WithApp(available Result, attached bool, appErr error) Result {
	return withApp(available, attached, appErr)
}
