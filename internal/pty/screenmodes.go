package pty

import "strconv"

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
	// mouseOff is no mouse tracking, and mouseEncodingDefault the X10 encoding.
	mouseOff             = 0
	mouseEncodingDefault = 0
	// preambleBytesMax bounds the longest preamble: every mode written once.
	preambleBytesMax = 64
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
		s.mouseEncoding = mouseEncodingDefault
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
	s.cursorKeys = false
	s.keypad = false
	s.bracketedPaste = false
	s.focusReports = false
	s.mouseTracking = mouseOff
	s.mouseEncoding = mouseEncodingDefault
}

// softReset is DECSTR (CSI ! p), as xterm.js does it: the modes reset, the mouse kept.
func (s *Screen) softReset() {
	s.cursorKeys = false
	s.keypad = false
	s.bracketedPaste = false
	s.focusReports = false
	s.origin = false
	s.autowrap = true
	s.top, s.bottom = 0, s.rows-1
}

// ModePreamble is what puts a terminal just reset into the modes the program's screen is in: the
// alternate screen, the cursor keys, the keypad, bracketed paste, focus reports and the mouse.
// Only modes that are on are written, so a program that set none gives nothing. A viewer's replay
// starts with it, so its modes are the program's however much output has passed since they were
// set; the replay's own mode switches, being later, still apply after it.
func (s *Screen) ModePreamble() []byte {
	out := make([]byte, 0, preambleBytesMax)

	if s.onAlternate {
		out = appendMode(out, modeAlternateSaveCursor)
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

	if s.mouseEncoding != mouseEncodingDefault {
		out = appendMode(out, s.mouseEncoding)
	}

	return out
}

// appendMode is out with the sequence that sets DEC private mode.
func appendMode(out []byte, mode int) []byte {
	out = append(out, "\x1b[?"...)
	out = strconv.AppendInt(out, int64(mode), decimal)

	return append(out, 'h')
}
