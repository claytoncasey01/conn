package server

import (
	"encoding/json/v2"
	"errors"
	"strconv"
)

// The frames a terminal's page sends are text, one JSON object each, in one of two shapes:
//
//	{"t":"input","d":"<what the person typed, as text>"}
//	{"t":"resize","cols":<columns>,"rows":<rows>}
//
// The server sends the program's output as binary frames, and three text frames:
//
//	{"t":"replay-end"}          after the scrollback, before live output
//	{"t":"paste-closed"}        input refused: a paste cut short was closed in its place
//	{"t":"exit","code":<code>}  once the program has ended, before a normal close
const (
	// typeInput and typeResize are the two values of a client frame's "t".
	typeInput  = "input"
	typeResize = "resize"

	// replayEndText is the server's frame that ends a viewer's replay.
	replayEndText = `{"t":"replay-end"}`
	// pasteClosedText tells the page its input was refused: a paste cut short was closed.
	pasteClosedText = `{"t":"paste-closed"}`
	// exitTextPrefix and exitTextSuffix wrap the exit code in the server's exit frame.
	exitTextPrefix = `{"t":"exit","code":`
	exitTextSuffix = `}`
)

// errMalformedFrame is a client frame that is not one of the two shapes. It never says what the
// frame held, which is keystrokes.
var errMalformedFrame = errors.New("malformed terminal frame")

// frameKind is which of the two shapes a client frame has.
type frameKind int

const (
	frameInput frameKind = iota + 1
	frameResize
)

// clientFrame is a client frame parsed: input's data, or a resize's size.
type clientFrame struct {
	kind    frameKind
	data    []byte
	columns uint16
	rows    uint16
}

// wireFrame is a client frame as JSON. A member that is absent or null is nil.
type wireFrame struct {
	Type    *string `json:"t"`
	Data    *string `json:"d"`
	Columns *uint16 `json:"cols"`
	Rows    *uint16 `json:"rows"`
}

// parseClientFrame parses one client frame, strictly: one JSON object of exactly one of the two
// shapes, with no other member, no member twice, names as spelled here, valid UTF-8 and nothing
// after it. Input carries 1 to maxInputFrameBytes bytes. A frame longer than maxFrameBytes is
// refused before it is decoded. The size is not checked here: the host bounds it.
func parseClientFrame(text []byte) (clientFrame, error) {
	if len(text) > maxFrameBytes {
		return clientFrame{}, errMalformedFrame
	}

	var wire wireFrame

	// encoding/json/v2 refuses duplicate names and invalid UTF-8, and matches names exactly, by
	// default; unknown members are refused here.
	err := json.Unmarshal(text, &wire, json.RejectUnknownMembers(true))
	if err != nil {
		return clientFrame{}, errMalformedFrame
	}

	if wire.Type == nil {
		return clientFrame{}, errMalformedFrame
	}

	switch *wire.Type {
	case typeInput:
		return inputFrame(wire)
	case typeResize:
		return resizeFrame(wire)
	default:
		return clientFrame{}, errMalformedFrame
	}
}

// inputFrame is wire as input: data and nothing else.
func inputFrame(wire wireFrame) (clientFrame, error) {
	if wire.Data == nil {
		return clientFrame{}, errMalformedFrame
	}

	if wire.Columns != nil {
		return clientFrame{}, errMalformedFrame
	}

	if wire.Rows != nil {
		return clientFrame{}, errMalformedFrame
	}

	if len(*wire.Data) == 0 {
		return clientFrame{}, errMalformedFrame
	}

	if len(*wire.Data) > maxInputFrameBytes {
		return clientFrame{}, errMalformedFrame
	}

	return clientFrame{kind: frameInput, data: []byte(*wire.Data), columns: 0, rows: 0}, nil
}

// resizeFrame is wire as a resize: both sizes and nothing else.
func resizeFrame(wire wireFrame) (clientFrame, error) {
	if wire.Data != nil {
		return clientFrame{}, errMalformedFrame
	}

	if wire.Columns == nil {
		return clientFrame{}, errMalformedFrame
	}

	if wire.Rows == nil {
		return clientFrame{}, errMalformedFrame
	}

	return clientFrame{
		kind: frameResize, data: nil, columns: *wire.Columns, rows: *wire.Rows,
	}, nil
}

// cost is what the frame spends of its session's input budget: its bytes of input.
func (frame clientFrame) cost() inputCost {
	return inputCost{bytes: len(frame.data), resize: frame.kind == frameResize}
}

// exitText is the server's frame that reports the program's exit code.
func exitText(code int) []byte {
	return []byte(exitTextPrefix + strconv.Itoa(code) + exitTextSuffix)
}
