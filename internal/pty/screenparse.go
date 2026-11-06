package pty

import "unicode/utf8"

// The parser follows the DEC VT500 state machine that xterm and xterm.js follow, with every part
// of a sequence it keeps bounded: at most parametersMax parameters, each at most
// parameterValueMax, intermediatesMax intermediates, and the first oscBytesMax bytes of an
// operating system command, which is all that telling a query apart needs. Anything past a bound
// is dropped or makes the sequence one that is skipped; nothing grows with the input.
const (
	parametersMax     = 16
	parameterValueMax = 65535
	intermediatesMax  = 2
	oscBytesMax       = 16
	// decimalBase is what each digit of a parameter multiplies the ones before it by.
	decimalBase = 10
)

// The UTF-8 bytes that start or continue a character: continuation bytes are below
// continuationEnd, and a character of more than one byte starts with leadingFirst to leadingLast.
const (
	continuationEnd = 0xc0
	leadingFirst    = 0xc2
	leadingLast     = 0xf4
)

// The C0 controls the screen acts on.
const (
	controlBell           = 0x07
	controlBackspace      = 0x08
	controlTab            = 0x09
	controlLineFeed       = 0x0a
	controlVerticalTab    = 0x0b
	controlFormFeed       = 0x0c
	controlCarriageReturn = 0x0d
	controlShiftOut       = 0x0e
	controlShiftIn        = 0x0f
	controlCancel         = 0x18
	controlSubstitute     = 0x1a
	controlEscape         = 0x1b
)

// parseState is where the parser is in a sequence.
type parseState int

const (
	stateGround parseState = iota
	stateEscape
	stateEscapeIntermediate
	stateCSI
	stateOSC
	// stateString is a device control, start-of-string, privacy or application string: all
	// consumed, none acted on.
	stateString
)

// parser is what the parser holds between bytes, so a sequence or a character may be split
// across Feeds anywhere.
type parser struct {
	state parseState
	// utf8 holds the first bytes of a character still arriving.
	utf8      [utf8.UTFMax]byte
	utf8Count int
	// A control sequence's private marker (< = > ?), intermediates and parameters. A parameter
	// with sub-parameters (38:5:1) keeps its first part; subparameters says one was seen.
	marker            byte
	intermediates     [intermediatesMax]byte
	intermediateCount int
	parameters        [parametersMax]int
	parameterCount    int
	inSubparameter    bool
	subparameters     bool
	// ignoring is a sequence past a bound or malformed: it is consumed and not acted on.
	ignoring bool
	// escapeIntermediate is the intermediate of an escape sequence (the ( of ESC ( 0).
	escapeIntermediate byte
	// osc is the start of an operating system command; oscOverflow says there was more.
	osc         [oscBytesMax]byte
	oscCount    int
	oscOverflow bool
}

// Feed reads the program's output. It never fails and never writes anything back: bytes it does
// not understand are skipped.
func (s *Screen) Feed(data []byte) {
	for _, current := range data {
		s.step(current)
	}
}

// step reads one byte.
func (s *Screen) step(current byte) {
	switch s.parser.state {
	case stateGround:
		s.ground(current)
	case stateEscape:
		s.escape(current)
	case stateEscapeIntermediate:
		s.escapeIntermediate(current)
	case stateCSI:
		s.controlSequence(current)
	case stateOSC:
		s.operatingSystemCommand(current)
	case stateString:
		s.ignoredString(current)
	}
}

// ground reads text and C0 controls.
func (s *Screen) ground(current byte) {
	if s.parser.utf8Count > 0 {
		if continuation(current) {
			s.continueCharacter(current)

			return
		}

		// A character cut short by something else is a replacement character.
		s.parser.utf8Count = 0
		s.print(utf8.RuneError)
	}

	switch {
	case current < ' ':
		s.execute(current)
	case current < asciiDelete:
		s.print(rune(current))
	case current == asciiDelete:
	case leading(current):
		s.parser.utf8[0] = current
		s.parser.utf8Count = 1
	default:
		s.print(utf8.RuneError)
	}
}

// continueCharacter adds a continuation byte to the character arriving, printing it once whole.
func (s *Screen) continueCharacter(current byte) {
	s.parser.utf8[s.parser.utf8Count] = current
	s.parser.utf8Count++

	pending := s.parser.utf8[:s.parser.utf8Count]
	if !utf8.FullRune(pending) {
		return
	}

	char, _ := utf8.DecodeRune(pending)
	s.parser.utf8Count = 0
	s.print(char)
}

// continuation says whether a byte continues a UTF-8 character.
func continuation(current byte) bool {
	if current < utf8.RuneSelf {
		return false
	}

	return current < continuationEnd
}

// leading says whether a byte starts a UTF-8 character of more than one byte.
func leading(current byte) bool {
	if current < leadingFirst {
		return false
	}

	return current <= leadingLast
}

// execute acts on a C0 control, in any state but a string.
func (s *Screen) execute(control byte) {
	switch control {
	case controlBackspace:
		s.cursor.wrapPending = false
		s.cursor.column = max(s.cursor.column-1, 0)
	case controlTab:
		s.tabForward(1)
	case controlLineFeed, controlVerticalTab, controlFormFeed:
		s.lineFeed()
	case controlCarriageReturn:
		s.cursor.wrapPending = false
		s.cursor.column = 0
	case controlShiftOut:
		s.cursor.shifted = true
	case controlShiftIn:
		s.cursor.shifted = false
	case controlCancel, controlSubstitute:
		s.parser.state = stateGround
	case controlEscape:
		s.beginEscape()
	}
}

// beginEscape starts an escape sequence, abandoning any character or sequence in progress.
func (s *Screen) beginEscape() {
	s.parser.utf8Count = 0
	s.parser.state = stateEscape
	s.parser.escapeIntermediate = 0
}

// escape reads the byte after ESC.
func (s *Screen) escape(current byte) {
	switch {
	case current < ' ':
		s.execute(current)
	case current == '[':
		s.beginControlSequence()
	case current == ']':
		s.parser.state = stateOSC
		s.parser.oscCount = 0
		s.parser.oscOverflow = false
	case current == 'P', current == 'X', current == '^', current == '_':
		s.parser.state = stateString
	case current < '0':
		s.parser.escapeIntermediate = current
		s.parser.state = stateEscapeIntermediate
	case current < asciiDelete:
		s.parser.state = stateGround
		s.escapeDispatch(current)
	default:
		s.parser.state = stateGround
	}
}

// escapeIntermediate reads the byte after an escape sequence's intermediate.
func (s *Screen) escapeIntermediate(current byte) {
	switch {
	case current < ' ':
		s.execute(current)
	case current < '0':
		// A second intermediate: no sequence the screen knows has one.
		s.parser.escapeIntermediate = 0
	case current < asciiDelete:
		s.parser.state = stateGround
		s.designate(s.parser.escapeIntermediate, current)
	default:
		s.parser.state = stateGround
	}
}

// designate is ESC ( and ESC ): which set G0 or G1 is, line drawing (0) or not.
func (s *Screen) designate(intermediate, final byte) {
	switch intermediate {
	case '(':
		s.cursor.graphicsG0 = final == '0'
	case ')':
		s.cursor.graphicsG1 = final == '0'
	}
}

// escapeDispatch acts on a whole escape sequence without an intermediate.
func (s *Screen) escapeDispatch(final byte) {
	switch final {
	case '7':
		s.shown().saved = s.cursor
	case '8':
		s.restoreCursor()
	case 'D':
		s.lineFeed()
	case 'E':
		s.cursor.column = 0
		s.lineFeed()
	case 'M':
		s.reverseIndex()
	case 'c':
		s.reset()
	}
}

// beginControlSequence starts a control sequence (CSI) with nothing collected.
func (s *Screen) beginControlSequence() {
	s.parser.state = stateCSI
	s.parser.marker = 0
	s.parser.intermediateCount = 0
	s.parser.parameters = [parametersMax]int{}
	s.parser.parameterCount = 0
	s.parser.inSubparameter = false
	s.parser.subparameters = false
	s.parser.ignoring = false
}

// controlSequence reads a byte of a control sequence.
func (s *Screen) controlSequence(current byte) {
	switch {
	case current < ' ':
		s.execute(current)
	case current < '0':
		s.collectIntermediate(current)
	case current <= '9':
		s.collectDigit(current)
	case current < '<':
		s.separate(current)
	case current < '@':
		s.collectMarker(current)
	case current < asciiDelete:
		s.parser.state = stateGround
		if !s.parser.ignoring {
			s.controlDispatch(current)
		}
	case current == asciiDelete:
	default:
		s.parser.ignoring = true
	}
}

func (s *Screen) collectIntermediate(current byte) {
	if s.parser.intermediateCount == intermediatesMax {
		s.parser.ignoring = true

		return
	}

	s.parser.intermediates[s.parser.intermediateCount] = current
	s.parser.intermediateCount++
}

func (s *Screen) collectDigit(current byte) {
	if s.parser.intermediateCount > 0 {
		s.parser.ignoring = true

		return
	}

	if s.parser.inSubparameter {
		return
	}

	if s.parser.parameterCount == 0 {
		s.parser.parameterCount = 1
	}

	index := s.parser.parameterCount - 1
	if index >= parametersMax {
		return
	}

	value := s.parser.parameters[index]*decimalBase + int(current-'0')
	s.parser.parameters[index] = min(value, parameterValueMax)
}

// separate reads : (a sub-parameter follows) or ; (the next parameter follows).
func (s *Screen) separate(current byte) {
	if current == ':' {
		s.parser.inSubparameter = true
		s.parser.subparameters = true

		return
	}

	s.nextParameter()
}

func (s *Screen) nextParameter() {
	if s.parser.intermediateCount > 0 {
		s.parser.ignoring = true

		return
	}

	s.parser.inSubparameter = false

	if s.parser.parameterCount == 0 {
		s.parser.parameterCount = 1
	}

	// One past the bound is enough to know there were too many.
	s.parser.parameterCount = min(s.parser.parameterCount+1, parametersMax+1)
}

func (s *Screen) collectMarker(current byte) {
	if s.parser.parameterCount > 0 {
		s.parser.ignoring = true

		return
	}

	if s.parser.intermediateCount > 0 {
		s.parser.ignoring = true

		return
	}

	if s.parser.marker != 0 {
		s.parser.ignoring = true

		return
	}

	s.parser.marker = current
}

// operatingSystemCommand reads a byte of an OSC, keeping only its start.
func (s *Screen) operatingSystemCommand(current byte) {
	switch current {
	case controlBell:
		s.parser.state = stateGround
		s.oscDispatch()
	case controlEscape:
		// ESC ends the command; with \ after it, that is its terminator (ST).
		s.oscDispatch()
		s.beginEscape()
	case controlCancel, controlSubstitute:
		s.parser.state = stateGround
	default:
		if s.parser.oscCount == oscBytesMax {
			s.parser.oscOverflow = true

			return
		}

		s.parser.osc[s.parser.oscCount] = current
		s.parser.oscCount++
	}
}

// ignoredString consumes a string sequence up to its end: ST, BEL, CAN or SUB.
func (s *Screen) ignoredString(current byte) {
	switch current {
	case controlBell, controlCancel, controlSubstitute:
		s.parser.state = stateGround
	case controlEscape:
		s.beginEscape()
	}
}
