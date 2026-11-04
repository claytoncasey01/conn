// Package transcript reads Claude Code's transcripts: JSONL files that grow while a session
// runs, read by byte offset so only the lines appended since the last read are ever parsed.
package transcript

import (
	"bytes"
	"io"
	"math"
	"os"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
)

// chunkBytes is the most one read call takes from the file.
const chunkBytes = 4 << 20

// Tail reads a growing JSONL file by byte offset.
type Tail struct {
	path   string
	offset int64
	// rest is a partial last line, held until it is completed.
	rest []byte
	// missing is set when the file could not be opened at the last read: nothing more can be
	// consumed, so a replay loop must end.
	missing bool

	// Size and ModTimeMs are the file's size and modification time (Unix ms) at the last read.
	Size      int64
	ModTimeMs float64
}

// NewTail reads the file at path from its start.
func NewTail(path string) *Tail {
	return &Tail{path: path, offset: 0, rest: nil, missing: false, Size: 0, ModTimeMs: 0}
}

// Path is the file the tail reads.
func (t *Tail) Path() string { return t.path }

// AtEnd is whether everything up to the size seen at the last read has been consumed. A held
// partial last line is not pending work.
func (t *Tail) AtEnd() bool {
	if t.missing {
		return true
	}

	return t.offset >= t.Size
}

// ReadAll is every complete line appended since the last read, parsed (js.Parse). A bad or
// partial line is skipped.
func (t *Tail) ReadAll() []any { return t.Read(math.MaxInt64) }

// Read is as ReadAll, but stops after about bytesMax bytes, so a caller can yield between calls
// and loop until AtEnd.
func (t *Tail) Read(bytesMax int64) []any {
	file, err := os.Open(t.path)
	if err != nil {
		t.missing = true

		return []any{}
	}

	defer func() { _ = file.Close() }()

	t.missing = false

	info, err := file.Stat()
	if err != nil {
		return []any{}
	}

	t.Size = info.Size()
	t.ModTimeMs = float64(info.ModTime().UnixNano()) / float64(time.Millisecond)

	if t.Size < t.offset {
		t.offset, t.rest = 0, nil
	}

	return t.consume(file, bytesMax)
}

func (t *Tail) consume(file io.ReaderAt, bytesMax int64) []any {
	lines := []any{}
	budget := bytesMax

	for t.offset < t.Size {
		if budget <= 0 {
			break
		}

		want := min(chunkBytes, t.Size-t.offset, max(1, budget))
		buffer := make([]byte, want)

		got, err := file.ReadAt(buffer, t.offset)
		if got <= 0 {
			break
		}

		t.offset += int64(got)
		budget -= int64(got)

		lines = append(lines, t.split(buffer[:got])...)

		if err != nil {
			if err != io.EOF {
				break
			}
		}
	}

	return lines
}

// split adds data to the held partial line and parses every complete line in it.
func (t *Tail) split(data []byte) []any {
	if len(t.rest) > 0 {
		data = append(t.rest, data...)
	}

	lines := []any{}
	start := 0

	for {
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			break
		}

		end += start
		if end > start {
			parsed, err := js.Parse([]byte(js.DecodeUTF8(data[start:end], false)))
			if err == nil {
				lines = append(lines, parsed)
			}
		}

		start = end + 1
	}

	t.rest = bytes.Clone(data[start:])

	return lines
}
