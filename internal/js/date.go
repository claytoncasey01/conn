package js

import (
	"math"
	"strings"
	"time"
	"unicode/utf16"
)

// timeMax is the largest time a JavaScript Date holds, in ms either side of the epoch.
const timeMax = 8.64e15

// The parts of a date-time string, as the ECMAScript format writes them.
const (
	yearDigits      = 4
	yearDigitsLong  = 6
	fieldDigits     = 2
	fractionDigits  = 3
	monthMax        = 12
	dayMax          = 31
	hourMax         = 24
	minuteMax       = 59
	secondMax       = 59
	msPerSecond     = 1000
	offsetMinuteMax = 59
	minutesPerHour  = 60
	digitBase       = 10
)

// replacement is U+FFFD, what a cut through a surrogate pair leaves in Go.
const replacement = '�'

// DateParse is Date.parse(text) for the date-time strings Claude Code writes, in the local time
// zone. See DateParseIn.
func DateParse(text string) float64 {
	// The machine's own zone, as Bun reads a zone-less time.
	return DateParseIn(text, time.Now().Location())
}

// DateParseIn is Date.parse(text) with local times in loc: the ECMAScript date-time format
// (YYYY, YYYY-MM or YYYY-MM-DD, then optionally T and HH:mm, :ss and a fraction, then Z or an
// offset), with JavaScriptCore's leniencies: a lowercase t or z, a space for the T, an offset
// without its colon, any number of fraction digits (the first 3 count), 24:00 for the end of a
// day and a day past its month's end rolling over. A date alone is UTC; a date-time without a
// zone is local. Anything else is NaN: JavaScriptCore's guesses at other forms ("Nov 3 2026") are
// not followed.
func DateParseIn(text string, loc *time.Location) float64 {
	reader := dateReader{text: text, at: 0}

	parts, valid := reader.date()
	if !valid {
		return math.NaN()
	}

	if reader.done() {
		return utcMs(parts)
	}

	timed, zoned, valid := reader.clock(parts)
	if !valid {
		return math.NaN()
	}

	if !reader.done() {
		return math.NaN()
	}

	if zoned {
		offset := float64(timed.offsetMinutes) * float64(time.Minute/time.Millisecond)

		return inRange(utcMs(timed) - offset)
	}

	local := time.Date(timed.year, time.Month(timed.month), timed.day, timed.hour, timed.minute,
		timed.second, timed.ms*int(time.Millisecond), loc)

	return inRange(float64(local.UnixMilli()))
}

// Cut is the first units UTF-16 code units of text (all of it when shorter). Where JavaScript's
// slice would keep half of a surrogate pair, Cut keeps U+FFFD: a Go string holds no lone
// surrogate, and a page shows either as the same replacement mark.
func Cut(text string, units int) string {
	if units <= 0 {
		return ""
	}

	encoded := utf16.Encode([]rune(text))
	if units >= len(encoded) {
		return text
	}

	if isHighSurrogate(encoded[units-1]) {
		return string(utf16.Decode(encoded[:units-1])) + string(replacement)
	}

	return string(utf16.Decode(encoded[:units]))
}

// Clip is s.length > n ? s.slice(0, n - 1) + '…' : s, s being text with its whitespace collapsed
// and trimmed: the one-line preview Conn shows of a command, a path or a reply.
func Clip(text string, units int) string {
	one := Trim(CollapseSpace(text))
	if Len(one) <= units {
		return one
	}

	return Cut(one, units-1) + "…"
}

// Num is a number that goes into JSON as JSON.stringify writes it: NaN and the infinities as null.
type Num float64

// MarshalJSON writes the number as JavaScript prints it.
func (n Num) MarshalJSON() ([]byte, error) { return []byte(Number(float64(n))), nil }

type dateParts struct {
	year, month, day, hour, minute, second, ms int
	offsetMinutes                              int
}

type dateReader struct {
	text string
	at   int
}

func (r *dateReader) done() bool { return r.at == len(r.text) }

func (r *dateReader) peek() byte {
	if r.done() {
		return 0
	}

	return r.text[r.at]
}

// digits reads exactly count digits.
func (r *dateReader) digits(count int) (int, bool) {
	if r.at+count > len(r.text) {
		return 0, false
	}

	value := 0

	for _, char := range []byte(r.text[r.at : r.at+count]) {
		if !isDigit(char) {
			return 0, false
		}

		value = value*digitBase + int(char-'0')
	}

	r.at += count

	return value, true
}

// field reads a separator and a two-digit number from 0 to highest.
func (r *dateReader) field(separator byte, lowest, highest int) (int, bool) {
	if r.peek() != separator {
		return 0, false
	}

	r.at++

	value, valid := r.digits(fieldDigits)
	if !valid {
		return 0, false
	}

	if value < lowest {
		return 0, false
	}

	return value, value <= highest
}

func (r *dateReader) year() (int, bool) {
	sign := r.peek()
	if sign != '+' {
		if sign != '-' {
			return r.digits(yearDigits)
		}
	}

	r.at++

	value, valid := r.digits(yearDigitsLong)
	if sign == '-' {
		value = -value
	}

	return value, valid
}

func (r *dateReader) date() (dateParts, bool) {
	parts := dateParts{
		year: 0, month: 1, day: 1, hour: 0, minute: 0, second: 0, ms: 0,
		offsetMinutes: 0,
	}

	year, valid := r.year()
	if !valid {
		return parts, false
	}

	parts.year = year

	if r.peek() != '-' {
		return parts, true
	}

	parts.month, valid = r.field('-', 1, monthMax)
	if !valid {
		return parts, false
	}

	if r.peek() != '-' {
		return parts, true
	}

	parts.day, valid = r.field('-', 1, dayMax)

	return parts, valid
}

// clock reads the time after a date: its separator, HH:mm, optional :ss and fraction, and an
// optional zone. It says whether a zone was given.
func (r *dateReader) clock(parts dateParts) (dateParts, bool, bool) {
	switch r.peek() {
	case 'T', 't', ' ':
		r.at++
	default:
		return parts, false, false
	}

	hour, valid := r.digits(fieldDigits)
	if !valid {
		return parts, false, false
	}

	parts.hour = hour

	parts.minute, valid = r.field(':', 0, minuteMax)
	if !valid {
		return parts, false, false
	}

	if r.peek() == ':' {
		parts.second, valid = r.field(':', 0, secondMax)
		if !valid {
			return parts, false, false
		}

		parts.ms = r.fraction()
	}

	if !validHour(parts) {
		return parts, false, false
	}

	return r.zone(parts)
}

// validHour allows 24 only as 24:00:00.000, the end of the day.
func validHour(parts dateParts) bool {
	if parts.hour < hourMax {
		return true
	}

	if parts.hour > hourMax {
		return false
	}

	return parts.minute+parts.second+parts.ms == 0
}

// fraction reads a '.' and its digits, keeping the first three as ms.
func (r *dateReader) fraction() int {
	if r.peek() != '.' {
		return 0
	}

	start := r.at + 1
	end := start

	for end < len(r.text) {
		if !isDigit(r.text[end]) {
			break
		}

		end++
	}

	if end == start {
		return 0
	}

	r.at = end

	kept := r.text[start:min(end, start+fractionDigits)]
	kept += strings.Repeat("0", fractionDigits-len(kept))

	value := 0
	for _, char := range []byte(kept) {
		value = value*digitBase + int(char-'0')
	}

	return value
}

func (r *dateReader) zone(parts dateParts) (dateParts, bool, bool) {
	sign := r.peek()

	switch sign {
	case 'Z', 'z':
		r.at++

		return parts, true, true
	case '+', '-':
		r.at++
	default:
		return parts, false, true
	}

	hours, valid := r.digits(fieldDigits)
	if !valid {
		return parts, false, false
	}

	if r.peek() == ':' {
		r.at++
	}

	minutes, valid := r.digits(fieldDigits)
	if !valid {
		return parts, false, false
	}

	if hours >= hourMax {
		return parts, false, false
	}

	if minutes > offsetMinuteMax {
		return parts, false, false
	}

	parts.offsetMinutes = hours*minutesPerHour + minutes
	if sign == '-' {
		parts.offsetMinutes = -parts.offsetMinutes
	}

	return parts, true, true
}

// utcMs is the time the parts name in UTC, a day past the month's end rolling over.
func utcMs(parts dateParts) float64 {
	moment := time.Date(parts.year, time.Month(parts.month), parts.day, parts.hour, parts.minute,
		parts.second, parts.ms*int(time.Millisecond), time.UTC)

	return inRange(float64(moment.UnixMilli()))
}

func inRange(ms float64) float64 {
	if math.Abs(ms) > timeMax {
		return math.NaN()
	}

	return ms
}

// CutFrom is text.slice(start) for start >= 0, in UTF-16 units. Where JavaScript would keep half
// of a surrogate pair at the start, CutFrom keeps U+FFFD.
func CutFrom(text string, start int) string {
	encoded := utf16.Encode([]rune(text))
	if start >= len(encoded) {
		return ""
	}

	if start <= 0 {
		return text
	}

	if encoded[start] >= surrogateLowFirst {
		if encoded[start] < surrogateLowFirst+surrogateLowFirst-surrogateHighFirst {
			return string(replacement) + string(utf16.Decode(encoded[start+1:]))
		}
	}

	return string(utf16.Decode(encoded[start:]))
}
