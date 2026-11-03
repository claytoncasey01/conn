package js

// mergeRuns merges items[left:leftEnd] and items[right:rightEnd] into work, as WebKit's
// mergePowersortRuns does: trim what is already in place at either end, merge one by one until
// a side wins minGallop times in a row, then gallop.
func (s *powersort[T]) mergeRuns(left, leftEnd, right, rightEnd int) {
	src, dst := s.items, s.work

	if left == leftEnd {
		copy(dst[left:rightEnd], src[left:rightEnd])

		return
	}

	if right == rightEnd {
		copy(dst[left:rightEnd], src[left:rightEnd])

		return
	}

	skip := s.gallopRight(src[right], src[left:leftEnd], 0)
	copy(dst[left:left+skip], src[left:left+skip])
	left += skip

	if left == leftEnd {
		copy(dst[right:rightEnd], src[right:rightEnd])

		return
	}

	length := rightEnd - right
	skipRight := length - s.gallopLeft(src[leftEnd-1], src[right:rightEnd], length-1)
	copy(dst[rightEnd-skipRight:rightEnd], src[rightEnd-skipRight:rightEnd])
	rightEnd -= skipRight

	if right == rightEnd {
		copy(dst[left:leftEnd], src[left:leftEnd])

		return
	}

	cursor := mergeCursor{left: left, leftEnd: leftEnd, right: right, rightEnd: rightEnd, out: left}
	s.gallopMerge(&cursor)

	copy(dst[cursor.out:], src[cursor.left:cursor.leftEnd])
	cursor.out += cursor.leftEnd - cursor.left
	copy(dst[cursor.out:], src[cursor.right:cursor.rightEnd])
}

type mergeCursor struct {
	left, leftEnd, right, rightEnd, out int
}

func (c *mergeCursor) exhausted() bool {
	if c.left >= c.leftEnd {
		return true
	}

	return c.right >= c.rightEnd
}

// gallopMerge merges until one side runs out, leaving the rest for the caller to copy.
func (s *powersort[T]) gallopMerge(cursor *mergeCursor) {
	for {
		if !s.linearMerge(cursor) {
			return
		}

		s.minGallop++

		for {
			if s.minGallop > 1 {
				s.minGallop--
			}

			leftWins, rightWins, done := s.gallopStep(cursor)
			if done {
				return
			}

			if leftWins < minGallopStart {
				if rightWins < minGallopStart {
					break
				}
			}
		}

		s.minGallop++
	}
}

// linearMerge takes items one by one until a side wins minGallop times in a row (true) or a side
// runs out (false).
func (s *powersort[T]) linearMerge(cursor *mergeCursor) bool {
	src, dst := s.items, s.work
	leftWins, rightWins := 0, 0

	for leftWins < s.minGallop {
		if rightWins >= s.minGallop {
			break
		}

		if cursor.exhausted() {
			return false
		}

		if s.less(src[cursor.right], src[cursor.left]) {
			dst[cursor.out] = src[cursor.right]
			cursor.right++
			rightWins++
			leftWins = 0
		} else {
			dst[cursor.out] = src[cursor.left]
			cursor.left++
			leftWins++
			rightWins = 0
		}

		cursor.out++
	}

	return true
}

// gallopStep gallops once each way; done is set when a side has run out.
func (s *powersort[T]) gallopStep(cursor *mergeCursor) (int, int, bool) {
	src, dst := s.items, s.work

	if cursor.exhausted() {
		return 0, 0, true
	}

	leftWins := s.gallopRight(src[cursor.right], src[cursor.left:cursor.leftEnd], 0)
	copy(dst[cursor.out:], src[cursor.left:cursor.left+leftWins])
	cursor.out += leftWins
	cursor.left += leftWins
	dst[cursor.out] = src[cursor.right]
	cursor.out++
	cursor.right++

	if cursor.exhausted() {
		return leftWins, 0, true
	}

	rightWins := s.gallopLeft(src[cursor.left], src[cursor.right:cursor.rightEnd], 0)
	copy(dst[cursor.out:], src[cursor.right:cursor.right+rightWins])
	cursor.out += rightWins
	cursor.right += rightWins
	dst[cursor.out] = src[cursor.left]
	cursor.out++
	cursor.left++

	return leftWins, rightWins, cursor.exhausted()
}

// nextOffset is the next gallop step, capped at limit.
func nextOffset(offset, limit int) int {
	return min(offset*2+1, limit)
}

// gallopLeft is the leftmost place for key in base (before equal items), searched from hint.
func (s *powersort[T]) gallopLeft(key T, base []T, hint int) int {
	lastOffset, offset := 0, 1

	if s.less(base[hint], key) {
		limit := len(base) - hint
		for offset < limit {
			if !s.less(base[hint+offset], key) {
				break
			}

			lastOffset = offset
			offset = nextOffset(offset, limit)
		}

		lastOffset += hint
		offset += hint
	} else {
		limit := hint + 1
		for offset < limit {
			if s.less(base[hint-offset], key) {
				break
			}

			lastOffset = offset
			offset = nextOffset(offset, limit)
		}

		lastOffset, offset = hint-offset, hint-lastOffset
	}

	lastOffset++
	for lastOffset < offset {
		middle := lastOffset + (offset-lastOffset)>>1
		if s.less(base[middle], key) {
			lastOffset = middle + 1
		} else {
			offset = middle
		}
	}

	return offset
}

// gallopRight is the rightmost place for key in base (after equal items), searched from hint.
func (s *powersort[T]) gallopRight(key T, base []T, hint int) int {
	lastOffset, offset := 0, 1

	if s.less(key, base[hint]) {
		limit := hint + 1
		for offset < limit {
			if !s.less(key, base[hint-offset]) {
				break
			}

			lastOffset = offset
			offset = nextOffset(offset, limit)
		}

		lastOffset, offset = hint-offset, hint-lastOffset
	} else {
		limit := len(base) - hint
		for offset < limit {
			if s.less(key, base[hint+offset]) {
				break
			}

			lastOffset = offset
			offset = nextOffset(offset, limit)
		}

		lastOffset += hint
		offset += hint
	}

	lastOffset++
	for lastOffset < offset {
		middle := lastOffset + (offset-lastOffset)>>1
		if s.less(key, base[middle]) {
			offset = middle
		} else {
			lastOffset = middle + 1
		}
	}

	return offset
}
