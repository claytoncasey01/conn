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
		s.eraseCells(rows[row], column, s.columns)

		for _, below := range rows[row+1:] {
			clear(below)
		}
	case eraseToStart:
		for _, above := range rows[:row] {
			clear(above)
		}

		s.eraseCells(rows[row], 0, column+1)
	case eraseAll:
		clearGrid(*s.shown())
	}
}

// eraseLine is EL: the cursor's line from the cursor, up to the cursor, or all of it.
func (s *Screen) eraseLine(part int) {
	row := s.shown().rows[s.cursor.row]

	switch part {
	case eraseToEnd:
		s.eraseCells(row, s.cursor.column, s.columns)
	case eraseToStart:
		s.eraseCells(row, 0, s.cursor.column+1)
	case eraseAll:
		clear(row)
	}
}

// eraseCharacters is ECH: count cells from the cursor blanked, at most to the end of the line.
func (s *Screen) eraseCharacters(count int) {
	column := s.cursor.column
	s.eraseCells(s.shown().rows[s.cursor.row], column, min(column+count, s.columns))
}

// insertCharacters is ICH: count blanks at the cursor, the rest of the line moving right and
// falling off its end.
func (s *Screen) insertCharacters(count int) {
	row := s.shown().rows[s.cursor.row]
	column := s.cursor.column
	count = min(count, s.columns-column)

	copy(row[column+count:], row[column:])
	s.eraseCells(row, column, column+count)
}

// deleteCharacters is DCH: count cells at the cursor removed, the rest of the line moving left
// and blanks coming in at its end.
func (s *Screen) deleteCharacters(count int) {
	row := s.shown().rows[s.cursor.row]
	column := s.cursor.column
	count = min(count, s.columns-column)

	copy(row[column:], row[column+count:])
	s.eraseCells(row, s.columns-count, s.columns)
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

// repeat is REP: the last character printed, count more times. At most a line's worth, so a
// huge count costs no more than one line of text.
func (s *Screen) repeat(count int) {
	if s.lastPrinted == cellEmpty {
		return
	}

	for range min(count, s.columns) {
		s.print(s.lastPrinted)
	}
}
