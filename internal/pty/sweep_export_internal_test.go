//go:build darwin || linux

package pty

import "errors"

// errNoListingHere is a listing of /dev/fd that cannot be read.
var errNoListingHere = errors.New("no listing in this test")

// SweepWithoutListing is the spawn's close-on-exec sweep where /dev/fd cannot be listed.
func SweepWithoutListing() error {
	return sweepDescriptors(descriptorSources{
		list:  func() ([]string, error) { return nil, errNoListingHere },
		limit: descriptorLimit,
	})
}
