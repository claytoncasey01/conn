package pty

import "bytes"

// The markers of a bracketed paste. A program that asked for them (mode 2004) takes everything
// between them as pasted text: a key, Enter too, does nothing until the end marker comes.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// pasteClosingAfter is what would close the paste a program is left inside once it has read the
// first written bytes of attempted, having needed closing before (nil: inside none). The markers
// it read whole decide; a write cut inside a marker leaves the rest of that marker to finish:
// the rest of an end marker, or the rest of a start marker and then a whole end marker. A marker
// is never repeated whole after a part of it, which a parser reading the part as a sequence of
// its own would not recover from.
func pasteClosingAfter(closing, attempted []byte, written int) []byte {
	state := closing
	from := 0

	for from < len(attempted) {
		start, marker := nextMarker(attempted[from:])
		if start < 0 {
			break
		}

		start += from
		if start >= written {
			break
		}

		end := start + len(marker)
		if end > written {
			return markerRest(marker, written-start)
		}

		state = nil
		if marker == pasteStart {
			state = []byte(pasteEnd)
		}

		from = end
	}

	return state
}

// nextMarker is where the first paste marker in data begins, and which one, or -1.
func nextMarker(data []byte) (int, string) {
	started := bytes.Index(data, []byte(pasteStart))
	ended := bytes.Index(data, []byte(pasteEnd))

	if started < 0 {
		return ended, pasteEnd
	}

	if ended < 0 {
		return started, pasteStart
	}

	if started < ended {
		return started, pasteStart
	}

	return ended, pasteEnd
}

// markerRest is what finishes marker, done bytes of it already read, and closes the paste.
func markerRest(marker string, done int) []byte {
	if marker == pasteEnd {
		return []byte(pasteEnd[done:])
	}

	return []byte(pasteStart[done:] + pasteEnd)
}
