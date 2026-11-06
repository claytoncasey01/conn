package pty

// NewID is newID, so a test can draw many.
func NewID() (ID, error) { return newID() }

// The limits a test needs to go past.
const (
	ReadBufferBytes     = readBufferBytes
	ViewerQueueBytesMax = viewerQueueBytesMax
	WriteWaitMax        = writeWaitMax
	WriteBytesMax       = writeBytesMax
	EndedMax            = endedMax
	ColumnsMax          = columnsMax
	RowsMax             = rowsMax
)
