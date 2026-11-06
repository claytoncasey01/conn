package pty

// NewID is newID, so a test can draw many.
func NewID() (ID, error) { return newID() }

// The limits a test needs to go past.
const (
	ReadBufferBytes   = readBufferBytes
	ViewerQueueChunks = viewerQueueChunks
	WriteBytesMax     = writeBytesMax
	EndedMax          = endedMax
	ColumnsMax        = columnsMax
	RowsMax           = rowsMax
)
