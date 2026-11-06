package pty

import (
	"slices"
	"unicode"
	"unicode/utf8"
)

// print draws char at the cursor and moves the cursor past it, wrapping first when the last
// character filled the line.
func (s *Screen) print(char rune) {
	char = s.mapped(char)

	width := s.width(char)
	if width == 0 {
		return
	}

	if width == wideWidth {
		if s.columns < wideWidth {
			char, width = utf8.RuneError, 1
		}
	}

	s.lastPrinted = char

	if s.cursor.wrapPending {
		s.wrap()
	}

	if width == wideWidth {
		s.makeRoomForWide()
	}

	s.draw(char, width)

	s.cursor.column += width
	if s.cursor.column >= s.columns {
		s.cursor.column = s.columns - 1
		s.cursor.wrapPending = s.autowrap
	}
}

// wrap moves the cursor to the start of the next line, scrolling at the bottom of the region.
func (s *Screen) wrap() {
	s.cursor.wrapPending = false
	s.cursor.column = 0
	s.lineFeed()
}

// makeRoomForWide makes sure a wide character fits: in the last column it wraps, or with
// autowrap off it is drawn one column to the left.
func (s *Screen) makeRoomForWide() {
	if s.cursor.column < s.columns-1 {
		return
	}

	if s.autowrap {
		s.wrap()

		return
	}

	s.cursor.column = s.columns - wideWidth
}

// draw puts char, width cells wide, at the cursor. Concealed text is drawn as blanks.
func (s *Screen) draw(char rune, width int) {
	row := s.shown().rows[s.cursor.row]
	column := s.cursor.column

	if s.cursor.concealed {
		for offset := range width {
			put(row, column+offset, ' ')
		}

		return
	}

	put(row, column, char)

	if width == wideWidth {
		put(row, column+1, cellWideTail)
	}
}

// put sets one cell, blanking the other half of any wide character it cuts in two.
func put(row []rune, column int, char rune) {
	if row[column] == cellWideTail {
		if column > 0 {
			row[column-1] = cellEmpty
		}
	}

	if column+1 < len(row) {
		if row[column+1] == cellWideTail {
			row[column+1] = cellEmpty
		}
	}

	row[column] = char
}

// repairRow blanks each half of a wide character whose other half an edit removed.
func (s *Screen) repairRow(row []rune) {
	for column, cell := range row {
		if cell == cellWideTail {
			if !s.wideHead(row, column-1) {
				row[column] = cellEmpty
			}

			continue
		}

		if s.width(cell) != wideWidth {
			continue
		}

		if column+1 >= len(row) {
			row[column] = cellEmpty

			continue
		}

		if row[column+1] != cellWideTail {
			row[column] = cellEmpty
		}
	}
}

// wideHead says whether the cell at column holds a wide character.
func (s *Screen) wideHead(row []rune, column int) bool {
	if column < 0 {
		return false
	}

	if row[column] == cellWideTail {
		return false
	}

	return s.width(row[column]) == wideWidth
}

// width is how many cells char takes: 0 for controls and characters drawn in no cell of their
// own (combining marks, joiners, format characters, line and paragraph separators), 2 for wide
// characters, else 1.
func (s *Screen) width(char rune) int {
	if char < ' ' {
		return 0
	}

	if char < asciiDelete {
		return 1
	}

	if char < c1End {
		return 0
	}

	if char < combiningStart {
		return 1
	}

	if unicode.In(char, unicode.Mn, unicode.Me, unicode.Cf, unicode.Zl, unicode.Zp) {
		return 0
	}

	_, wide := slices.BinarySearchFunc(s.wide, char, compareRange)
	if wide {
		return wideWidth
	}

	return 1
}

// mapped is char in the character set in use: DEC line drawing maps _ to ~.
func (s *Screen) mapped(char rune) rune {
	graphics := s.cursor.graphicsG0
	if s.cursor.shifted {
		graphics = s.cursor.graphicsG1
	}

	if !graphics {
		return char
	}

	if char < graphicsFirst {
		return char
	}

	if char >= graphicsFirst+graphicsCount {
		return char
	}

	return s.graphics[char-graphicsFirst]
}

// lineFeed moves the cursor down a line, scrolling the region when it is on the region's
// bottom row.
func (s *Screen) lineFeed() {
	s.cursor.wrapPending = false

	if s.cursor.row == s.bottom {
		s.scrollUp(1)

		return
	}

	if s.cursor.row < s.rows-1 {
		s.cursor.row++
	}
}

// reverseIndex moves the cursor up a line, scrolling the region down on its top row.
func (s *Screen) reverseIndex() {
	s.cursor.wrapPending = false

	if s.cursor.row == s.top {
		s.scrollDown(1)

		return
	}

	if s.cursor.row > 0 {
		s.cursor.row--
	}
}

// scrollUp moves the region's rows up count, blank rows coming in at its bottom.
func (s *Screen) scrollUp(count int) {
	s.rotateUp(s.shown().rows[s.top:s.bottom+1], count)
}

// scrollDown moves the region's rows down count, blank rows coming in at its top.
func (s *Screen) scrollDown(count int) {
	s.rotateDown(s.shown().rows[s.top:s.bottom+1], count)
}

// rotateUp moves rows up count and blanks the count rows that come in at the bottom.
func (s *Screen) rotateUp(rows [][]rune, count int) {
	count = min(count, len(rows))
	slices.Reverse(rows[:count])
	slices.Reverse(rows[count:])
	slices.Reverse(rows)

	for _, row := range rows[len(rows)-count:] {
		clear(row)
	}
}

// rotateDown moves rows down count and blanks the count rows that come in at the top.
func (s *Screen) rotateDown(rows [][]rune, count int) {
	count = min(count, len(rows))
	slices.Reverse(rows)
	slices.Reverse(rows[:count])
	slices.Reverse(rows[count:])

	for _, row := range rows[:count] {
		clear(row)
	}
}

// eraseCells blanks the cells of row from start up to end.
func (s *Screen) eraseCells(row []rune, start, end int) {
	clear(row[start:end])
	s.repairRow(row)
}
