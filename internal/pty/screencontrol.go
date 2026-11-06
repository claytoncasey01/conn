package pty

// The DEC private modes the screen acts on.
const (
	modeOrigin              = 6
	modeAutowrap            = 7
	modeAlternate           = 47
	modeAlternateClear      = 1047
	modeSaveCursor          = 1048
	modeAlternateSaveCursor = 1049
	// modeBracketedPaste is the program asking for pastes bracketed, so a line break in one is
	// not Enter.
	modeBracketedPaste = 2004
)

// The graphic renditions (SGR) the screen reads: concealment, and the colours whose parameters
// it must step over so that a colour of 8 is not taken for concealment.
const (
	renditionReset      = 0
	renditionConceal    = 8
	renditionReveal     = 28
	renditionForeground = 38
	renditionBackground = 48
	renditionUnderline  = 58
	colourIndexed       = 5
	colourIndexedLength = 2
	colourDirect        = 2
	colourDirectLength  = 4
)

// parameter is the index'th parameter, or fallback when it is absent or 0.
func (s *Screen) parameter(index, fallback int) int {
	if index >= min(s.parser.parameterCount, parametersMax) {
		return fallback
	}

	value := s.parser.parameters[index]
	if value == 0 {
		return fallback
	}

	return value
}

// controlDispatch acts on a whole control sequence: the queries the host answers are noted, the
// DEC private modes set, and the rest acted on if the screen knows them. A sequence with
// another private marker or with intermediates (DA2, XTVERSION, the kitty keyboard, DECRQM) is
// never acted on.
func (s *Screen) controlDispatch(final byte) {
	s.noteControlQuery(final)

	if s.parser.intermediateCount > 0 {
		return
	}

	switch s.parser.marker {
	case 0:
		s.plainControl(final)
	case '?':
		s.privateModes(final)
	}
}

// plainControl acts on a control sequence without a private marker, if the screen knows it.
func (s *Screen) plainControl(final byte) {
	if s.moveControl(final) {
		return
	}

	if s.placeControl(final) {
		return
	}

	if s.eraseControl(final) {
		return
	}

	s.lineControl(final)
}

// moveControl acts on the sequences that move the cursor by a count, and says whether final was
// one.
func (s *Screen) moveControl(final byte) bool {
	row, column := s.cursor.row, s.cursor.column
	count := s.parameter(0, 1)

	switch final {
	case 'A':
		s.moveTo(s.upLimit(row, count), column)
	case 'B', 'e':
		s.moveTo(s.downLimit(row, count), column)
	case 'C', 'a':
		s.moveTo(row, column+count)
	case 'D':
		s.moveTo(row, column-count)
	case 'E':
		s.moveTo(s.downLimit(row, count), 0)
	case 'F':
		s.moveTo(s.upLimit(row, count), 0)
	default:
		return false
	}

	return true
}

// placeControl acts on the sequences that put the cursor somewhere, and says whether final was
// one.
func (s *Screen) placeControl(final byte) bool {
	row, column := s.cursor.row, s.cursor.column
	count := s.parameter(0, 1)

	switch final {
	case 'G', '`':
		s.moveTo(row, count-1)
	case 'H', 'f':
		s.moveTo(s.originRow(count), s.parameter(1, 1)-1)
	case 'd':
		s.moveTo(s.originRow(count), column)
	case 'I':
		s.tabForward(count)
	case 'Z':
		s.moveTo(row, max((column-1)/tabColumns-count+1, 0)*tabColumns)
	case 's':
		s.shown().saved = s.cursor
	case 'u':
		s.restoreCursor()
	default:
		return false
	}

	return true
}

// eraseControl acts on the sequences that change characters, and says whether final was one.
func (s *Screen) eraseControl(final byte) bool {
	count := s.parameter(0, 1)

	switch final {
	case 'J':
		s.eraseDisplay(s.parameter(0, 0))
	case 'K':
		s.eraseLine(s.parameter(0, 0))
	case 'X':
		s.eraseCharacters(count)
	case '@':
		s.insertCharacters(count)
	case 'P':
		s.deleteCharacters(count)
	default:
		return false
	}

	return true
}

// lineControl acts on the sequences that change lines, the scroll region and what is drawn
// with.
func (s *Screen) lineControl(final byte) {
	count := s.parameter(0, 1)

	switch final {
	case 'L':
		s.insertLines(count)
	case 'M':
		s.deleteLines(count)
	case 'S':
		s.scrollUp(count)
	case 'T':
		s.scrollDown(count)
	case 'b':
		s.repeat(count)
	case 'm':
		s.graphicRendition()
	case 'r':
		s.setRegion(s.parameter(0, 1)-1, s.parameter(1, s.rows)-1)
	}
}

// moveTo puts the cursor at row and column, clamped to the screen.
func (s *Screen) moveTo(row, column int) {
	s.cursor.wrapPending = false
	s.cursor.row = min(max(row, 0), s.rows-1)
	s.cursor.column = min(max(column, 0), s.columns-1)
}

// upLimit is count rows up from row, stopping at the region's top when inside it.
func (s *Screen) upLimit(row, count int) int {
	if row >= s.top {
		return max(row-count, s.top)
	}

	return max(row-count, 0)
}

// downLimit is count rows down from row, stopping at the region's bottom when inside it.
func (s *Screen) downLimit(row, count int) int {
	if row <= s.bottom {
		return min(row+count, s.bottom)
	}

	return min(row+count, s.rows-1)
}

// originRow is the screen row of the one-based row a program addressed: in origin mode it
// counts from the region's top and stays inside the region.
func (s *Screen) originRow(addressed int) int {
	if !s.origin {
		return addressed - 1
	}

	return min(s.top+addressed-1, s.bottom)
}

func (s *Screen) tabForward(count int) {
	stop := (s.cursor.column/tabColumns + min(count, s.columns)) * tabColumns
	s.moveTo(s.cursor.row, stop)
}

// restoreCursor is DECRC: the cursor DECSC saved on the screen shown, or home.
func (s *Screen) restoreCursor() {
	s.cursor = s.shown().saved
	s.moveTo(s.cursor.row, s.cursor.column)
}

// setRegion is DECSTBM: rows top to bottom scroll, if that is two rows or more. The cursor goes
// home.
func (s *Screen) setRegion(top, bottom int) {
	bottom = min(bottom, s.rows-1)
	if top >= bottom {
		return
	}

	s.top, s.bottom = top, bottom
	s.moveTo(s.originRow(1), 0)
}

// privateModes sets (h) or resets (l) the DEC private modes listed.
func (s *Screen) privateModes(final byte) {
	var set bool

	switch final {
	case 'h':
		set = true
	case 'l':
		set = false
	default:
		return
	}

	for index := range min(s.parser.parameterCount, parametersMax) {
		s.privateMode(s.parser.parameters[index], set)
	}
}

func (s *Screen) privateMode(mode int, set bool) {
	switch mode {
	case modeOrigin:
		s.origin = set
		s.moveTo(s.originRow(1), 0)
	case modeAutowrap:
		s.autowrap = set
		s.cursor.wrapPending = false
	case modeAlternate, modeAlternateClear:
		s.show(set)
	case modeSaveCursor:
		s.saveOrRestore(set)
	case modeAlternateSaveCursor:
		s.alternateWithCursor(set)
	case modeBracketedPaste:
		s.bracketedPaste = set
	}
}

func (s *Screen) saveOrRestore(save bool) {
	if save {
		s.shown().saved = s.cursor

		return
	}

	s.restoreCursor()
}

// alternateWithCursor is mode 1049: save the cursor and show a cleared alternate screen, or go
// back to the main screen and its saved cursor.
func (s *Screen) alternateWithCursor(set bool) {
	if !set {
		s.show(false)
		s.restoreCursor()

		return
	}

	s.main.saved = s.cursor
	s.clearGrid(&s.alternate)
	s.show(true)
}

// graphicRendition reads SGR for concealment only.
func (s *Screen) graphicRendition() {
	count := min(s.parser.parameterCount, parametersMax)
	if count == 0 {
		s.cursor.concealed = false

		return
	}

	for index := 0; index < count; index++ {
		switch s.parser.parameters[index] {
		case renditionReset, renditionReveal:
			s.cursor.concealed = false
		case renditionConceal:
			s.cursor.concealed = true
		case renditionForeground, renditionBackground, renditionUnderline:
			index += s.colourLength(index + 1)
		}
	}
}

// colourLength is how many parameters from index belong to an extended colour.
func (s *Screen) colourLength(index int) int {
	if index >= min(s.parser.parameterCount, parametersMax) {
		return 0
	}

	switch s.parser.parameters[index] {
	case colourIndexed:
		return colourIndexedLength
	case colourDirect:
		return colourDirectLength
	}

	return 0
}
