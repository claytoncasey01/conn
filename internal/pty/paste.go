package pty

import "bytes"

// The markers of a bracketed paste. A program that asked for them (mode 2004) takes everything
// between them as pasted text: a key, Enter too, does nothing until the end marker comes.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// pasteOpenAfter says whether a program is inside a paste once it has read written, having been
// inside one before it when open: the newest whole marker decides, and with none it is as it was.
// A marker cut short is no marker: a start cut short opened nothing, an end cut short closed
// nothing.
func pasteOpenAfter(open bool, written []byte) bool {
	started := bytes.LastIndex(written, []byte(pasteStart))
	ended := bytes.LastIndex(written, []byte(pasteEnd))

	if started > ended {
		return true
	}

	if ended > started {
		return false
	}

	return open
}
