package pty

import "strconv"

// MaxPreambleBytes bounds a mode preamble: every mode written once is 47 bytes. A longer one is a
// mistake in this file.
const MaxPreambleBytes = 64

// MaxReplayBytes bounds a viewer's replay with the default limits: the preamble and a full
// scrollback.
const MaxReplayBytes = maxScrollbackBytes + MaxPreambleBytes

// The modes that decide what a viewer's terminal sends the program, beyond those screencontrol.go
// acts on: the cursor keys, the keypad, and the mouse. The screen keeps them so that a viewer
// who joins late is put in the modes the program asked for, even when the sequences that asked
// have left the scrollback (ModePreamble). Every one is off on a new screen and after a reset,
// and a mode the screen could not read counts as off.
const (
	// modeCursorKeys is DECCKM: the arrow keys send ESC O A rather than ESC [ A.
	modeCursorKeys = 1
	// modeKeypad is DECNKM, the keypad in application mode, as ESC = sets it.
	modeKeypad = 66
	// The mouse tracking modes; setting one replaces the one before, resetting any ends it.
	modeMouseX10    = 9
	modeMouseNormal = 1000
	modeMouseButton = 1002
	modeMouseAny    = 1003
	// The mouse report encodings; resetting either goes back to the default.
	modeMouseSGR       = 1006
	modeMouseSGRPixels = 1016
	// mouseOff is no mouse tracking, and defaultMouseEncoding the X10 encoding.
	mouseOff             = 0
	defaultMouseEncoding = 0
	// escapeByte and c1LeadByte begin a control sequence: ESC, and the first byte of a C1
	// control written as UTF-8 (U+0080 to U+009F all start with it).
	escapeByte = 0x1b
	c1LeadByte = 0xc2
	// decimal is the base a mode's number is written in.
	decimal = 10
)

// inputMode sets (or resets) one of the modes this file keeps, and says whether mode was one.
func (s *Screen) inputMode(mode int, set bool) bool {
	switch mode {
	case modeCursorKeys:
		s.cursorKeys = set
	case modeKeypad:
		s.keypad = set
	case modeMouseX10, modeMouseNormal, modeMouseButton, modeMouseAny:
		s.mouseTracking = mouseOff
		if set {
			s.mouseTracking = mode
		}
	case modeMouseSGR, modeMouseSGRPixels:
		s.mouseEncoding = defaultMouseEncoding
		if set {
			s.mouseEncoding = mode
		}
	default:
		return false
	}

	return true
}

// resetInputModes turns off every mode a viewer's terminal sends input by: the program's own
// reset, or modes the screen could not read, which count as off.
func (s *Screen) resetInputModes() {
	s.pasteResets++
	s.cursorKeys = false
	s.keypad = false
	s.bracketedPaste = false
	s.focusReports = false
	s.mouseTracking = mouseOff
	s.mouseEncoding = defaultMouseEncoding
}

// softReset is DECSTR (CSI ! p), as xterm.js does it: the modes reset, the mouse kept.
func (s *Screen) softReset() {
	s.pasteResets++
	s.cursorKeys = false
	s.keypad = false
	s.bracketedPaste = false
	s.focusReports = false
	s.origin = false
	s.autowrap = true
	s.top, s.bottom = 0, s.rows-1
}

// ModePreamble is what puts a terminal just reset into the modes the program's screen is in, for
// a viewer whose replay starts at output offset windowStart (the offset of the first byte of
// output still in the scrollback). The input modes that are on are written: the cursor keys, the
// keypad, bracketed paste, focus reports and the mouse, so a viewer has them however much output
// has passed since they were set; the replay's own later switches still apply after them. The
// alternate screen is written only when the switch to it began before windowStart: a switch the
// replay holds is made by the replay itself, after the main screen's output before it, which
// then stays on the main screen.
func (s *Screen) ModePreamble(windowStart uint64) []byte {
	out := make([]byte, 0, MaxPreambleBytes)

	if s.onAlternate {
		if s.alternateFrom < windowStart {
			out = appendMode(out, modeAlternateSaveCursor)
		}
	}

	if s.cursorKeys {
		out = appendMode(out, modeCursorKeys)
	}

	if s.keypad {
		out = append(out, "\x1b="...)
	}

	if s.bracketedPaste {
		out = appendMode(out, modeBracketedPaste)
	}

	if s.focusReports {
		out = appendMode(out, modeFocusReports)
	}

	if s.mouseTracking != mouseOff {
		out = appendMode(out, s.mouseTracking)
	}

	if s.mouseEncoding != defaultMouseEncoding {
		out = appendMode(out, s.mouseEncoding)
	}

	if len(out) > MaxPreambleBytes {
		panic("pty: a mode preamble is longer than PreambleBytesMax")
	}

	return out
}

// noteSequenceStart keeps where the newest control sequence began, for a switch of screens to
// say where it was in the output: every sequence begins with ESC or a C1 control.
func (s *Screen) noteSequenceStart(current byte) {
	switch current {
	case escapeByte, c1LeadByte:
		s.sequenceStart = s.fed
	}
}

// appendMode is out with the sequence that sets DEC private mode.
func appendMode(out []byte, mode int) []byte {
	out = append(out, "\x1b[?"...)
	out = strconv.AppendInt(out, int64(mode), decimal)

	return append(out, 'h')
}
