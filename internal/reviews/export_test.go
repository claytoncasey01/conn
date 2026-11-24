package reviews

import "math"

// Settle waits for the work Reviews has started (polls, scans, closes, sweeps) to finish.
func (r *Reviews) Settle() { r.work.Wait() }

// NoDiscovery makes the channel scan's look for new posts alone never due, until a REFRESH or a
// settings change.
func (r *Reviews) NoDiscovery() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextDiscovery = math.Inf(1)
}

// WithFile runs change on the file Reviews holds (nil before it is read), holding its lock.
func (r *Reviews) WithFile(change func(file *File)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	change(r.file)
}

// IdleSince is when Reviews first saw a session idle, and whether it has.
func (r *Reviews) IdleSince(sessionID string) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	at, found := r.idleSince[sessionID]

	return at, found
}

// NextLook is when Reviews reads a session's screen again (0 when it has no time set).
func (r *Reviews) NextLook(sessionID string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.nextLook[sessionID]
}

// SetIdleSince sets when Reviews first saw a session idle.
func (r *Reviews) SetIdleSince(sessionID string, at float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.idleSince[sessionID] = at
}

// ScanSince is where a scan planned at now would read channel from (nil: the first-scan
// window).
func (r *Reviews) ScanSince(now float64, channel string) *string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.scanSince(&scanPlan{now: now, unmatched: nil, threads: nil}, channel)
}

// SetPausedUntil sets the rate-limit pause Reviews holds.
func (r *Reviews) SetPausedUntil(until float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pausedUntil = &until
}

// SetDeps changes Reviews' dependencies, holding its lock (a test swaps a fake mid-way).
func (r *Reviews) SetDeps(change func(deps *Deps)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	change(&r.deps)
}

// MaxDiffBytes is the largest diff Positions reads.
const MaxDiffBytes = maxDiffBytes

// Why a PR's diff is not read.
var (
	ErrDiffUnsafe   = errDiffUnsafe
	ErrDiffTooLarge = errDiffTooLarge
)

// Why a review is not built, or not posted.
var (
	ErrBadVerdict      = errBadVerdict
	ErrBadFindings     = errBadFindings
	ErrBlockerApproved = errBlockerApproved
	ErrPostUnsafe      = errPostUnsafe
	ErrPostRefused     = errPostRefused
)

// Why a press of Post review is refused.
const (
	TextPostNoPR       = textPostNoPR
	TextPostNoDraft    = textPostNoDraft
	TextPostInFlight   = textPostInFlight
	TextPostReading    = textPostReading
	TextPostChecking   = textPostChecking
	TextPostUnread     = textPostUnread
	TextPostPosted     = textPostPosted
	TextPostSetAside   = textPostSetAside
	TextPostEnded      = textPostEnded
	TextPostNotAllowed = textPostNotAllowed
	TextPostGitHubOff  = textPostGitHubOff
)

// RefusedPost is a post GitHub refused with a client error, with message: it took no review.
func RefusedPost(message string) error {
	return &postError{message: message, err: errPostRefused}
}

// ErrLookupUnsafe is a PR's reviews not read because its repo, number or login is not safe.
var ErrLookupUnsafe = errLookupUnsafe
