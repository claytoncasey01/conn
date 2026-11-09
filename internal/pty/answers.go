package pty

import "fmt"

// Who answers a program's terminal queries: the host, never the page. A session runs with no
// viewer at all (started by Conn, or resumed at launch), so only the host is always there; up to
// maxViewers viewers would each answer the same query; and a viewer that joins late replays the
// scrollback, whose old queries a page's terminal would answer again, typing stale replies into
// the program's input. So the host answers each query once, as the output is read, and the
// page's terminal answers nothing.
//
// What it answers is fixed, below: device attributes and the background colour, nothing else. A
// reply is fixed bytes and the background's red, green and blue as hex digits, never text from
// the program's output. Every other query, the cursor position, the terminal's version, the
// kitty keyboard flags, title and window reports, setting and mode reports (DECRQSS, DECRQM), the
// clipboard (OSC 52) and the other colours among them, gets no answer, as from a terminal that
// does not support it. Device attributes are what Claude Code waits for: their answer tells it,
// at once, that the queries it sent before them are not supported. It never asks for the cursor
// position (captured start-ups), and a cursor position reply is two numbers the program chooses
// by where it puts the cursor, which can spell a key: ESC [ 1 ; 5 R is Ctrl+F3.

const (
	// maxQueriesPending bounds the queries a screen keeps between two Queries calls: the host
	// takes them after every chunk of output, so more in one chunk are dropped.
	maxQueriesPending = 8
	// maxAnswersQueued bounds the answers waiting to be typed into one session; more are
	// dropped, so a program that floods queries and reads no input never holds up its reader.
	maxAnswersQueued = 8
	// deviceAttributesReply says VT220 with ANSI colour, and nothing else: no sixel graphics, no
	// other extension a program would then use.
	deviceAttributesReply = "\x1b[?62;22c"
	// backgroundQuery is OSC 11 asking for the background colour.
	backgroundQuery = "11;?"
	// stringTerminator ends an OSC reply, whatever ended the query.
	stringTerminator = "\x1b\\"
)

// QueryKind is a terminal query the host answers.
type QueryKind int

// The queries the host answers.
const (
	// QueryDeviceAttributes is primary device attributes (DA1, CSI c).
	QueryDeviceAttributes QueryKind = iota + 1
	// QueryBackground is the background colour (OSC 11 ; ?).
	QueryBackground
)

// Query is one query a program sent.
type Query struct {
	Kind QueryKind
}

// RGB is a colour, 0 to 255 a channel.
type RGB struct {
	Red   uint8
	Green uint8
	Blue  uint8
}

// Answers is what the host's replies may vary by: the background colour, the theme's when Conn
// knows it. The zero value answers black.
type Answers struct {
	Background RGB
}

// queryRow is one query the host answers, as a program spells it. A control sequence row
// matches a sequence with no private marker, no intermediates and at most one parameter, which
// is parameter (0 matching none); an OSC row matches a command whose text is exactly osc.
type queryRow struct {
	kind      QueryKind
	final     byte
	parameter int
	osc       string
}

// answeredQueries is the table of the queries the host answers.
func answeredQueries() []queryRow {
	return []queryRow{
		{kind: QueryDeviceAttributes, final: 'c', parameter: 0, osc: ""},
		{kind: QueryBackground, final: 0, parameter: 0, osc: backgroundQuery},
	}
}

// Reply is the bytes that answer query. A query kind outside the table is a mistake in the
// code, so it panics.
func Reply(answers Answers, query Query) []byte {
	switch query.Kind {
	case QueryDeviceAttributes:
		return []byte(deviceAttributesReply)
	case QueryBackground:
		colour := answers.Background
		reply := fmt.Sprintf("\x1b]11;rgb:%02x%02x/%02x%02x/%02x%02x"+stringTerminator,
			colour.Red, colour.Red, colour.Green, colour.Green, colour.Blue, colour.Blue)

		return []byte(reply)
	}

	panic(fmt.Sprintf("pty: no reply for query kind %d", query.Kind))
}

// noteControlQuery notes a control sequence that is a query the host answers.
func (s *Screen) noteControlQuery(final byte) {
	if s.parser.marker != 0 {
		return
	}

	if s.parser.intermediateCount > 0 {
		return
	}

	if s.parser.parameterCount > 1 {
		return
	}

	if s.parser.subparameters {
		return
	}

	for _, row := range s.answered {
		if row.final != final {
			continue
		}

		if row.parameter == s.parser.parameters[0] {
			s.note(row.kind)
		}
	}
}

// oscDispatch notes an OSC that is a query the host answers. Nothing else in an OSC is acted on:
// titles, colours, hyperlinks and clipboard writes are for the page's terminal, under its rules.
func (s *Screen) oscDispatch() {
	if s.parser.oscOverflow {
		return
	}

	text := s.parser.osc[:s.parser.oscCount]

	for _, row := range s.answered {
		if row.osc == "" {
			continue
		}

		if string(text) == row.osc {
			s.note(row.kind)
		}
	}
}

// note keeps a query of kind, unless maxQueriesPending are already waiting.
func (s *Screen) note(kind QueryKind) {
	if len(s.queries) == maxQueriesPending {
		return
	}

	s.queries = append(s.queries, Query{Kind: kind})
}
