package pty

// The parts of the screen ED and EL erase.
const (
	eraseToEnd   = 0
	eraseToStart = 1
	eraseAll     = 2
)

// eraseDisplay is ED: from the cursor to the end, from the start to the cursor, or everything.
// ED 3 erases scrollback, which the screen has none of.
func (s *Screen) eraseDisplay(part int) {
	rows := s.shown().rows
	row, column := s.cursor.row, s.cursor.column

	switch part {
	case eraseToEnd:
		s.eraseCells(&rows[row], column, s.columns)

		for index := row + 1; index < len(rows); index++ {
			s.clearLine(&rows[index])
		}
	case eraseToStart:
		for index := range row {
			s.clearLine(&rows[index])
		}

		s.eraseCells(&rows[row], 0, column+1)
	case eraseAll:
		s.clearGrid(s.shown())
	}
}

// eraseLine is EL: the cursor's line from the cursor, up to the cursor, or all of it.
func (s *Screen) eraseLine(part int) {
	row := s.cursorLine()

	switch part {
	case eraseToEnd:
		s.eraseCells(row, s.cursor.column, s.columns)
	case eraseToStart:
		s.eraseCells(row, 0, s.cursor.column+1)
	case eraseAll:
		s.clearLine(row)
	}
}

// eraseCharacters is ECH: count cells from the cursor blanked, at most to the end of the line.
func (s *Screen) eraseCharacters(count int) {
	column := s.cursor.column
	s.eraseCells(s.cursorLine(), column, min(column+count, s.columns))
}

// insertCharacters is ICH: count blanks at the cursor, the rest of the line moving right and
// falling off its end.
func (s *Screen) insertCharacters(count int) {
	row := s.cursorLine()
	if !row.written {
		return
	}

	column := s.cursor.column
	count = min(count, s.columns-column)

	copy(row.cells[column+count:], row.cells[column:])
	clear(row.cells[column : column+count])

	s.changed = true
	s.repairAt(row.cells, column)
	s.repairAt(row.cells, column+count)
	s.repairAt(row.cells, s.columns)
}

// deleteCharacters is DCH: count cells at the cursor removed, the rest of the line moving left
// and blanks coming in at its end.
func (s *Screen) deleteCharacters(count int) {
	row := s.cursorLine()
	if !row.written {
		return
	}

	column := s.cursor.column
	count = min(count, s.columns-column)

	copy(row.cells[column:], row.cells[column+count:])
	clear(row.cells[s.columns-count:])
	s.changed = true
	s.repairAt(row.cells, column)
	s.repairAt(row.cells, s.columns-count)
}

// insertLines is IL: count blank lines at the cursor's row, inside the region only, the lines
// below moving down and falling off the region's bottom.
func (s *Screen) insertLines(count int) {
	if !s.inRegion() {
		return
	}

	s.rotateDown(s.shown().rows[s.cursor.row:s.bottom+1], count)
	s.moveTo(s.cursor.row, 0)
}

// deleteLines is DL: count lines at the cursor's row removed, inside the region only, blank
// lines coming in at the region's bottom.
func (s *Screen) deleteLines(count int) {
	if !s.inRegion() {
		return
	}

	s.rotateUp(s.shown().rows[s.cursor.row:s.bottom+1], count)
	s.moveTo(s.cursor.row, 0)
}

func (s *Screen) inRegion() bool {
	if s.cursor.row < s.top {
		return false
	}

	return s.cursor.row <= s.bottom
}

// repeat is REP: the last character printed, count more times, at most a line's worth of
// cells, so a huge count costs no more than one line of text. The cells are filled directly.
func (s *Screen) repeat(count int) {
	char := s.lastPrinted
	if char == cellEmpty {
		return
	}

	width := s.width(char)
	if width == wideWidth {
		if s.columns < wideWidth {
			return
		}
	}

	count = min(count, s.columns/width)

	for count > 0 {
		count = s.fillAhead(char, width, count)
	}
}

// fillAhead draws char, width cells wide, at most count times from the cursor to the end of its
// line, moves the cursor past them, and returns how many are left to draw on the next line. With
// autowrap off, the rest would all land on the line's last place, so none are left.
func (s *Screen) fillAhead(char rune, width, count int) int {
	if s.cursor.wrapPending {
		s.wrap()
	}

	if width == wideWidth {
		s.makeRoomForWide()
	}

	column := s.cursor.column
	drawn := min(count, (s.columns-column)/width)

	if width == wideWidth {
		s.fillWide(s.cursorLine(), column, drawn, char)
	} else {
		s.fill(s.cursorLine(), column, column+drawn, s.drawnAs(char))
	}

	s.advance(drawn * width)

	if !s.autowrap {
		return 0
	}

	return count - drawn
}

// drawnAs is char as the cursor draws it: a blank while text is concealed.
func (s *Screen) drawnAs(char rune) rune {
	if s.cursor.concealed {
		return ' '
	}

	return char
}

// fillWide draws count wide characters from start, as blanks while text is concealed.
func (s *Screen) fillWide(row *line, start, count int, char rune) {
	if s.cursor.concealed {
		s.fill(row, start, start+count*wideWidth, ' ')

		return
	}

	end := start + count*wideWidth
	for column := start; column < end; column += wideWidth {
		row.cells[column] = char
		row.cells[column+1] = cellWideTail
	}

	row.written = true
	s.changed = true
	s.repairAt(row.cells, start)
	s.repairAt(row.cells, end)
}
