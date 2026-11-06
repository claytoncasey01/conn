package pty

// ring is a session's scrollback: the newest limit bytes of its output. It grows as output comes,
// up to limit, and then overwrites its oldest bytes, so it never holds more than limit.
type ring struct {
	data  []byte
	limit int
	// start is where the oldest byte is once data is full; before that it is 0.
	start int
}

// ringCapacityFirst is what a ring allocates for its first output, so a quiet session does not
// cost the whole limit.
const ringCapacityFirst = 64 << 10

// ringGrowth is how many times its capacity a ring grows by when it needs room.
const ringGrowth = 2

func newRing(limit int) ring {
	if limit <= 0 {
		panic("pty: a scrollback ring needs a limit above zero")
	}

	return ring{data: nil, limit: limit, start: 0}
}

// write adds chunk as the newest bytes, dropping the oldest past the limit.
func (r *ring) write(chunk []byte) {
	if len(chunk) >= r.limit {
		r.reserve(r.limit)
		r.data = append(r.data[:0], chunk[len(chunk)-r.limit:]...)
		r.start = 0

		return
	}

	room := r.limit - len(r.data)
	if room > 0 {
		taken := min(room, len(chunk))
		r.reserve(len(r.data) + taken)
		r.data = append(r.data, chunk[:taken]...)
		chunk = chunk[taken:]
	}

	// The chunk is shorter than the limit, so this wraps at most once.
	for len(chunk) > 0 {
		copied := copy(r.data[r.start:], chunk)
		chunk = chunk[copied:]
		r.start = (r.start + copied) % r.limit
	}
}

// reserve makes room for size bytes without ever allocating more than the limit.
func (r *ring) reserve(size int) {
	if size <= cap(r.data) {
		return
	}

	capacity := max(size, min(r.limit, max(ringCapacityFirst, ringGrowth*cap(r.data))))
	grown := make([]byte, len(r.data), capacity)
	copy(grown, r.data)
	r.data = grown
}

// contents is a copy of the scrollback, oldest byte first.
func (r *ring) contents() []byte {
	out := make([]byte, 0, len(r.data))
	out = append(out, r.data[r.start:]...)

	return append(out, r.data[:r.start]...)
}
