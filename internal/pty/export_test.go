package pty

// NewID is newID, so a test can draw many.
func NewID() (ID, error) { return newID() }

// The limits a test needs to go past.
const (
	ReadBufferBytes     = readBufferBytes
	ViewerQueueBytesMax = viewerQueueBytesMax
	WriteWaitMax        = writeWaitMax
	KillWait            = killWait
	WriteBytesMax       = writeBytesMax
	EndedMax            = endedMax
	ColumnsMax          = columnsMax
	RowsMax             = rowsMax
	QueriesPendingMax   = queriesPendingMax
	AnswersQueuedMax    = answersQueuedMax
)

// DeniedNames and DeniedPrefixes are the deny tables, so a test can try every entry.
func DeniedNames() []string { return deniedNames() }

// DeniedPrefixes is deniedPrefixes.
func DeniedPrefixes() []string { return deniedPrefixes() }

// ReplacedCount is how many variables the host sets itself.
func ReplacedCount() int { return len(replacedVariables()) }
