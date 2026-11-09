package pty

// NewID is newID, so a test can draw many.
func NewID() (ID, error) { return newID() }

// The limits a test needs to go past.
const (
	ReadBufferBytes     = readBufferBytes
	MaxViewerQueueBytes = maxViewerQueueBytes
	MaxWriteWait        = maxWriteWait
	KillWait            = killWait
	MaxWriteBytes       = maxWriteBytes
	MaxEnded            = maxEnded
	MaxColumns          = maxColumns
	MaxRows             = maxRows
	MaxQueriesPending   = maxQueriesPending
	MaxAnswersQueued    = maxAnswersQueued
	MaxCells            = maxCells
)

// DeniedNames and DeniedPrefixes are the deny tables, so a test can try every entry.
func DeniedNames() []string { return deniedNames() }

// DeniedPrefixes is deniedPrefixes.
func DeniedPrefixes() []string { return deniedPrefixes() }

// ReplacedCount is how many variables the host sets itself.
func ReplacedCount() int { return len(replacedVariables()) }

// AnswersHeldBack is how many answers to the session's queries were held back inside a paste.
func (h *Host) AnswersHeldBack(sessionID ID) uint64 {
	held, err := h.find(sessionID)
	if err != nil {
		return 0
	}

	return held.answersHeldBack.Load()
}

// PasteResets is how many times the session's screen model read bracketed paste turned off.
func (h *Host) PasteResets(sessionID ID) uint64 {
	held, err := h.find(sessionID)
	if err != nil {
		return 0
	}

	held.screenMu.Lock()
	defer held.screenMu.Unlock()

	return held.screen.PasteResets()
}
