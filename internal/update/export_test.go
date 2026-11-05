package update

import (
	"net/url"
	"time"
)

// CheckTimeout exposes checkTimeout to the tests in package update_test.
const CheckTimeout time.Duration = checkTimeout

// TagBytesMax exposes tagBytesMax to the tests in package update_test.
const TagBytesMax = tagBytesMax

// ResponseHeaderTimeout exposes responseHeaderTimeout to the tests in package update_test.
const ResponseHeaderTimeout time.Duration = responseHeaderTimeout

// TagFrom exposes tagFrom to the tests in package update_test.
func TagFrom(location string, request *url.URL, basePath string) (string, error) {
	return tagFrom(location, request, basePath)
}

// OffValues exposes offValues to the tests in package update_test.
func OffValues() []string { return offValues() }
