package pty

import (
	"fmt"
	"slices"
	"strings"
)

// A Screen is a model of what a session's terminal shows, built from the program's output alone.
// It is passive: it never writes to the program. It notes the queries the host answers
// (Queries) and leaves the answering to the host.
//
// What it keeps is the live screen and nothing else: the main screen and the alternate one a
// full-screen program draws on, each exactly rows by columns cells, with no scrollback. A line
// that scrolls off the top is gone, so text printed above the live screen can never be read back
// as if it were on it. Memory is fixed by the size, at most maxCells cells a screen, whatever
// the output: every count, move and repeat in it is clamped to the screen.
//
// Cost: feeding allocates nothing (only NewScreen and Resize do), and no sequence costs more
// than one line of cells plus one pass over the rows: each row knows whether anything is drawn
// in it, so clearing a screen touches only the rows that hold text, scrolling moves rows rather
// than cells, a repeat fills cells directly, and an edit repairs a split wide character only at
// its two ends. On the largest screen, 1000 by 262, one chunk of output (readBufferBytes) of the
// worst sequences costs at most about 6 ms; internal/pty/costtest holds every chunk to 20 ms.
//
// It reads what Claude Code and programs like it send: UTF-8 text, wide characters, the C0
// controls, C1 controls written as UTF-8 (each acts as ESC and a byte, as in xterm.js), cursor
// addressing and movement, erasing, inserting and deleting characters and lines, scrolling and
// scroll regions, saving the cursor, origin mode, autowrap, the alternate screen (cleared when the
// program leaves it, as in xterm.js), the DEC line-drawing character set and concealed text.
// Colours and other attributes are not kept: only the text matters to the readers of Lines.
// Operating system commands, device control and application strings are consumed and never
// drawn. Every sequence it does not know is skipped whole. Known differences from xterm.js: no tab
// stops other than every eighth column, no insert mode, no left and right margins, combining marks
// and joiners are dropped rather than joined to the character before them, and the width table is
// a short one.
type Screen struct {
	// size is the screen's size; columns and rows are it as ints, set with it.
	size    Size
	columns int
	rows    int
	// main and alternate are the two screens; onAlternate says which is shown.
	main        grid
	alternate   grid
	onAlternate bool
	cursor      cursorState
	// top and bottom are the scroll region's rows, inclusive.
	top    int
	bottom int
	// autowrap and origin are DEC modes 7 and 6, bracketedPaste mode 2004, focusReports 1004.
	autowrap       bool
	origin         bool
	bracketedPaste bool
	focusReports   bool
	// cursorKeys is DECCKM (mode 1), keypad the keypad's application mode, mouseTracking the
	// mouse mode in force (0, 9, 1000, 1002 or 1003) and mouseEncoding its encoding (0, 1006 or
	// 1016): screenmodes.go.
	cursorKeys    bool
	keypad        bool
	mouseTracking int
	mouseEncoding int
	// fed counts the bytes of output read; sequenceStart is the offset the newest control
	// sequence began at, and alternateFrom that of the newest switch to the alternate screen.
	fed           uint64
	sequenceStart uint64
	alternateFrom uint64
	// pasteResets counts the times the program turned bracketed paste off, or reset.
	pasteResets uint64
	// lastPrinted is the character a repeat (REP) repeats.
	lastPrinted rune
	parser      parser
	// queries are the queries the host answers, oldest first, at most maxQueriesPending.
	queries []Query
	// generation counts the Feeds and Resizes that changed what is shown; changed says the Feed
	// in progress has.
	generation uint64
	changed    bool
	// The tables a screen reads, built once when it is made.
	wide     []runeRange
	graphics [graphicsCount]rune
	answered []queryRow
}

// grid is one screen: its rows, and the cursor DECSC saved on it.
type grid struct {
	rows  []line
	saved cursorState
}

// line is one row of cells, a slice of the grid's one backing array, so scrolling moves rows, not
// cells. A cell is the character drawn in it, cellEmpty or cellWideTail. written is false only
// while every cell is empty, so clearing a blank row costs nothing.
type line struct {
	cells   []rune
	written bool
}

// cursorState is where the cursor is and what is drawn with it: what DECSC saves.
type cursorState struct {
	row    int
	column int
	// wrapPending is set when a character filled the last column with autowrap on: the next
	// character goes to the start of the next line.
	wrapPending bool
	concealed   bool
	// graphicsG0 and graphicsG1 say which sets are DEC line drawing; shifted is SO (G1 in use).
	graphicsG0 bool
	graphicsG1 bool
	shifted    bool
}

const (
	// cellEmpty is a cell nothing was drawn in.
	cellEmpty rune = 0
	// cellWideTail is the right half of a wide character drawn in the cell before it.
	cellWideTail rune = -1
	// tabColumns is the distance between tab stops.
	tabColumns = 8
)

// NewScreen is a blank screen of size, or ErrInvalidSize for a size the host refuses.
func NewScreen(size Size) (*Screen, error) {
	err := checkSize(size)
	if err != nil {
		return nil, err
	}

	columns, rows := int(size.Columns), int(size.Rows)
	screen := &Screen{
		size: size, columns: columns, rows: rows,
		main: newGrid(columns, rows), alternate: newGrid(columns, rows), onAlternate: false,
		cursor: cursorState{}, top: 0, bottom: rows - 1, autowrap: true, origin: false,
		bracketedPaste: false, focusReports: false, cursorKeys: false, keypad: false,
		mouseTracking: mouseOff, mouseEncoding: defaultMouseEncoding,
		fed: 0, sequenceStart: 0, alternateFrom: 0, pasteResets: 0,
		lastPrinted: cellEmpty, parser: parser{}, queries: make([]Query, 0, maxQueriesPending),
		generation: 0, changed: false,
		wide: wideRanges(), graphics: lineDrawing(), answered: answeredQueries(),
	}

	return screen, nil
}

// checkSize refuses a size the host does not run a terminal at. Host.Resize and the screen use
// it alike, so the program and its model always have the same size.
func checkSize(size Size) error {
	if size.Columns == 0 {
		return fmt.Errorf("%w: no columns", ErrInvalidSize)
	}

	if size.Rows == 0 {
		return fmt.Errorf("%w: no rows", ErrInvalidSize)
	}

	if size.Columns > maxColumns {
		return fmt.Errorf("%w: %d columns, at most %d", ErrInvalidSize, size.Columns, maxColumns)
	}

	if size.Rows > maxRows {
		return fmt.Errorf("%w: %d rows, at most %d", ErrInvalidSize, size.Rows, maxRows)
	}

	cells := int(size.Columns) * int(size.Rows)
	if cells > maxCells {
		return fmt.Errorf("%w: %d cells, at most %d", ErrInvalidSize, cells, maxCells)
	}

	return nil
}

func newGrid(columns, rows int) grid {
	cells := make([]rune, columns*rows)
	lines := make([]line, rows)

	for index := range lines {
		start := index * columns
		lines[index] = line{cells: cells[start : start+columns : start+columns], written: false}
	}

	return grid{rows: lines, saved: cursorState{}}
}

// Lines is the bottom count lines of the screen shown, as text: a wide character once, an empty
// cell as a space, each line without its trailing spaces and the whole without its trailing
// empty lines. This is what `cmux read-screen` gives, so ask.Parse reads it alike. Never more
// lines than the screen has rows; never anything from above the screen.
func (s *Screen) Lines(count int) []string {
	if count <= 0 {
		return []string{}
	}

	shown := s.shown().rows
	first := max(len(shown)-count, 0)
	lines := make([]string, 0, len(shown)-first)

	var builder strings.Builder

	builder.Grow(s.columns)

	for _, row := range shown[first:] {
		lines = append(lines, rowText(&builder, row))
	}

	end := len(lines)
	for end > 0 {
		if lines[end-1] != "" {
			break
		}

		end--
	}

	return lines[:end]
}

// rowText is one row as Lines gives it, built in builder.
func rowText(builder *strings.Builder, row line) string {
	if !row.written {
		return ""
	}

	builder.Reset()

	for _, cell := range row.cells {
		switch cell {
		case cellWideTail:
		case cellEmpty:
			builder.WriteByte(' ')
		default:
			builder.WriteRune(cell)
		}
	}

	return strings.TrimRight(builder.String(), " ")
}

// Cursor is the cursor's row and column, from 0 at the top left.
func (s *Screen) Cursor() (int, int) { return s.cursor.row, s.cursor.column }

// Size is the screen's size.
func (s *Screen) Size() Size { return s.size }

// Generation counts the changes to what the screen shows: it goes up with every Feed that
// changes a cell or which screen is shown, and with every Resize, and never otherwise. Moving
// the cursor, a query or a title is no change. Two reads with the same generation read the same
// lines.
func (s *Screen) Generation() uint64 { return s.generation }

// FocusReports is whether the program has asked to be told of the terminal's focus (mode 1004).
func (s *Screen) FocusReports() bool { return s.focusReports }

// PasteResets is how many times the program has turned bracketed paste off, or reset its
// terminal, since the screen began.
func (s *Screen) PasteResets() uint64 { return s.pasteResets }

// BracketedPaste is whether the program has asked for pastes bracketed (mode 2004), so a paste
// it is sent can hold line breaks that are not Enter.
func (s *Screen) BracketedPaste() bool { return s.bracketedPaste }

// Queries takes the queries noted since the last call, oldest first.
func (s *Screen) Queries() []Query {
	if len(s.queries) == 0 {
		return nil
	}

	taken := slices.Clone(s.queries)
	s.queries = s.queries[:0]

	return taken
}

// Resize makes the screen size, as the program's terminal now is. Rows go from the top while the
// cursor's row would fall off the bottom, so the cursor stays on the text it was on; lines are
// cut or padded on the right, never rewrapped. The scroll region becomes the whole screen.
func (s *Screen) Resize(size Size) error {
	err := checkSize(size)
	if err != nil {
		return err
	}

	columns, rows := int(size.Columns), int(size.Rows)
	shift := max(s.cursor.row-(rows-1), 0)

	s.main = s.resized(s.main, columns, rows, shift)
	s.alternate = s.resized(s.alternate, columns, rows, shift)
	s.size, s.columns, s.rows = size, columns, rows
	s.top, s.bottom = 0, rows-1
	s.cursor = s.clamped(s.cursor, shift)
	s.main.saved = s.clamped(s.main.saved, shift)
	s.alternate.saved = s.clamped(s.alternate.saved, shift)
	s.generation++

	return nil
}

// resized is old at columns by rows, its rows from shift down, a wide character cut in two at
// the right edge blanked.
func (s *Screen) resized(old grid, columns, rows, shift int) grid {
	fresh := newGrid(columns, rows)
	fresh.saved = old.saved

	for index := range fresh.rows {
		source := index + shift
		if source >= len(old.rows) {
			continue
		}

		row := &fresh.rows[index]
		copy(row.cells, old.rows[source].cells)
		row.written = old.rows[source].written
		s.repairAt(row.cells, columns)
	}

	return fresh
}

// clamped is cursor on the resized screen, shift rows having gone from the top.
func (s *Screen) clamped(cursor cursorState, shift int) cursorState {
	cursor.row = min(max(cursor.row-shift, 0), s.rows-1)
	cursor.column = min(cursor.column, s.columns-1)
	cursor.wrapPending = false

	return cursor
}

// shown is the screen the terminal shows.
func (s *Screen) shown() *grid {
	if s.onAlternate {
		return &s.alternate
	}

	return &s.main
}

// reset is RIS: everything as a new screen has it, cleared in place, the queries still waiting
// kept.
func (s *Screen) reset() {
	s.clearGrid(&s.main)
	s.clearGrid(&s.alternate)
	s.main.saved = cursorState{}
	s.alternate.saved = cursorState{}
	s.show(false)
	s.cursor = cursorState{}
	s.top, s.bottom = 0, s.rows-1
	s.autowrap = true
	s.origin = false
	s.resetInputModes()
	s.lastPrinted = cellEmpty
}

// show shows the alternate screen or the main one. Leaving the alternate screen clears it, as
// xterm.js does.
func (s *Screen) show(alternate bool) {
	if s.onAlternate == alternate {
		return
	}

	// Only a real switch: one made while already on the alternate screen draws nothing new.
	if alternate {
		s.alternateFrom = s.sequenceStart
	}

	if !alternate {
		s.clearGrid(&s.alternate)
	}

	s.onAlternate = alternate
	s.changed = true
}
