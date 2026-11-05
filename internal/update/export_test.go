package update

import (
	"net/url"
	"time"
)

// CheckTimeout exposes checkTimeout to the tests in package update_test.
const CheckTimeout time.Duration = checkTimeout

// TagFrom exposes tagFrom to the tests in package update_test.
func TagFrom(location string, request *url.URL, basePath string) (string, error) {
	return tagFrom(location, request, basePath)
}
