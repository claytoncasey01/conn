package desk

import "time"

// The limits and the restart count, for tests that state the rules in the rules' own terms.
const (
	ProbeEvery       = probeEvery
	MaxProbeFailures = maxProbeFailures
	MaxRestarts      = maxRestartsInARow
	MaxAddressWait   = maxAddressWait
)

// ErrGaveUp is the error Run wraps when it gives up.
var ErrGaveUp = errGaveUp

// Action is what the window does about the data folder, and the three values it takes.
type Action = action

// The values of Action.
const (
	Own            = ownAction
	Attach         = attachAction
	WaitForAddress = waitForAddressAction
)

// Decide is decide, so a test states the rule on plain values.
func Decide(running bool, holderURL string) Action {
	return decide(running, holderURL)
}

// AddressWait is the longest time the polls for an address add up to.
func AddressWait() time.Duration {
	return time.Duration(addressPolls) * probeEvery
}
