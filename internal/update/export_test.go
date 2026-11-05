package update

import "net/url"

// TagFrom exposes tagFrom to the tests in package update_test.
func TagFrom(location string, request *url.URL, basePath string) (string, error) {
	return tagFrom(location, request, basePath)
}
