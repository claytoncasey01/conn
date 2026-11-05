package desk

import "time"

// The limits and the restart count, for tests that state the rules in the rules' own terms.
const (
	ProbeEvery       = probeEvery
	ProbeFailuresMax = probeFailuresMax
	RestartsMax      = restartsMax
	RestartWindow    = restartWindow
)

// Restarts is the unexported restart count, usable as a zero value.
type Restarts = restarts

// RecordRestart is restarts.record.
func RecordRestart(counted *Restarts, now time.Time) bool { return counted.record(now) }
