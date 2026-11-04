package js

import "math/bits"

// Sort sorts items as JavaScriptCore's Array.prototype.sort(compare) does: Powersort over
// natural runs (an insertion sort below 8 items, short runs topped up to 64 by insertion,
// galloping merges), after WebKit's StableSort.h. It is stable, and an item goes before another
// only when compare(item, other) is below 0, so a comparator that gives NaN (a time that did not
// parse) leaves items exactly where JavaScriptCore's sort leaves them, not only where a correct
// sort would.
func Sort[T any](items []T, compare func(left, right T) float64) {
	sorter := powersort[T]{
		items:     items,
		work:      make([]T, len(items)),
		less:      func(left, right T) bool { return compare(left, right) < 0 },
		minGallop: minGallopStart,
	}
	sorter.sort()
}

const (
	minGallopStart  = 7
	extendRunCutoff = 8
	forceRunLength  = 64
	powerShift      = 62
	wordBits        = 64
)

// run is a sorted stretch of items, end inclusive.
type run struct{ begin, end int }

type powerRun struct {
	run   run
	power int
}

type powersort[T any] struct {
	items     []T
	work      []T
	less      func(left, right T) bool
	minGallop int
}

func (s *powersort[T]) sort() {
	count := len(s.items)
	if count == 0 {
		return
	}

	if count < extendRunCutoff {
		s.insertion(0, count, 0)

		return
	}

	stack := []powerRun{}
	first := s.nextRun(0)

	for first.end+1 < count {
		second := s.nextRun(first.end + 1)
		power := runPower(first.begin, second.begin, second.end, count)

		for len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.power <= power {
				break
			}

			stack = stack[:len(stack)-1]

			s.merge(top.run, first)
			first.begin = top.run.begin
		}

		stack = append(stack, powerRun{run: first, power: power})
		first = second
	}

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		s.merge(top.run, first)
		first.begin = top.run.begin
	}
}

// nextRun finds the natural run at begin, tops a short one up by insertion and extends it.
func (s *powersort[T]) nextRun(begin int) run {
	count := len(s.items)
	found := run{begin: begin, end: s.extendRun(begin)}

	if found.end-found.begin < extendRunCutoff {
		size := min(forceRunLength, count-begin)
		s.insertion(begin, begin+size, found.end-found.begin)
		found.end = begin + size - 1
	}

	for found.end+1 < count {
		if s.less(s.items[found.end+1], s.items[found.end]) {
			break
		}

		found.end++
	}

	return found
}

// insertion is a binary insertion sort of items[from:to], whose first sorted+1 items are in
// order already.
func (s *powersort[T]) insertion(from, to, sorted int) {
	span := s.items[from:to]

	for index := sorted + 1; index < len(span); index++ {
		value := span[index]
		left, right := 0, index

		for left < right {
			middle := left + (right-left)>>1
			if s.less(value, span[middle]) {
				right = middle
			} else {
				left = middle + 1
			}
		}

		copy(span[left+1:index+1], span[left:index])
		span[left] = value
	}
}

// extendRun is the inclusive end of the run at begin: ascending, or strictly descending and then
// reversed.
func (s *powersort[T]) extendRun(begin int) int {
	end := begin
	count := len(s.items)

	if end+1 >= count {
		return end
	}

	descending := s.less(s.items[end+1], s.items[end])
	end++

	for end+1 < count {
		if s.less(s.items[end+1], s.items[end]) != descending {
			break
		}

		end++
	}

	if descending {
		for low, high := begin, end; low < high; low, high = low+1, high-1 {
			s.items[low], s.items[high] = s.items[high], s.items[low]
		}
	}

	return end
}

// runPower is Powersort's power of the boundary between [left, middle-1] and [middle, right]:
// where the two runs' midpoints, scaled by 2^62/count, first differ.
func runPower(left, middle, right, count int) int {
	// Every index is at least 0, so each converts to uint64 exactly.
	first := unsigned(middle - left)
	second := unsigned(right - middle + 1)
	quotient := func(twice uint64) uint64 {
		value, _ := bits.Div64(twice>>(wordBits-powerShift), twice<<powerShift, unsigned(count))

		return value
	}

	differing := quotient(unsigned(left)<<1+first) ^ quotient(unsigned(middle)<<1+second)

	return bits.LeadingZeros64(differing)
}

// unsigned is a non-negative int as a uint64.
func unsigned(value int) uint64 {
	if value < 0 {
		panic("js.Sort: a negative index")
	}

	return uint64(value)
}

// merge merges the adjacent runs left and right of items, through the work buffer.
func (s *powersort[T]) merge(left, right run) {
	s.mergeRuns(left.begin, left.end+1, right.begin, right.end+1)
	copy(s.items[left.begin:right.end+1], s.work[left.begin:right.end+1])
}
