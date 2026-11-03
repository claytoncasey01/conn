// Package js gives Go the few JavaScript behaviours that decide what Conn's API and files hold:
// JSON whose objects keep their keys in the order written, JSON text as JSON.stringify writes it,
// whitespace as String.prototype.trim sees it, and string lengths counted in UTF-16 code units.
// Settings and request bodies are read with these, so a value means the same thing wherever it
// is read.
package js

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// depthMax bounds how deeply JSON may nest. Conn's files and request bodies nest a few levels;
// text nesting deeper is refused rather than followed.
const depthMax = 512

// SpaceClass is JavaScript's \s as the inside of a Go regexp character class: Go's own \s is
// ASCII only.
const SpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}` +
	`\x{3000}\x{feff}`

const (
	// indexDigitsMax is how many digits the largest array index, 2^32 - 2, has.
	indexDigitsMax = 10

	// controlEnd is the first character JSON.stringify writes as it is.
	controlEnd = 0x20

	// surrogateHighFirst and surrogateLowFirst start the two halves of the UTF-16 surrogate range.
	surrogateHighFirst = 0xd800
	surrogateLowFirst  = 0xdc00

	// The fixed notation range of Number.prototype.toString; outside it, exponent notation.
	fixedMin = 1e-6
	fixedEnd = 1e21

	hexDigits = "0123456789abcdef"
)

// JavaScript's whitespace beyond ASCII.
const (
	noBreakSpace       = 0xa0
	oghamSpace         = 0x1680
	enQuad             = 0x2000 // The first of the spaces U+2000 to U+200A.
	hairSpace          = 0x200a // The last of them.
	lineSeparator      = 0x2028
	paragraphSeparator = 0x2029
	narrowNoBreakSpace = 0x202f
	mathSpace          = 0x205f
	ideographicSpace   = 0x3000
	byteOrderMark      = 0xfeff
)

const (
	baseBinary = 2
	baseOctal  = 8
	baseHex    = 16
)

// nullText is how JavaScript writes null.
const nullText = "null"

// ErrSyntax is the one error Parse gives for text that is not JSON.
var ErrSyntax = errors.New("not valid JSON")

// decimal is the decimal literal Number() accepts, after trimming.
var decimal = regexp.MustCompile(`^[+-]?(?:` +
	`[0-9]+\.?[0-9]*(?:[eE][+-]?[0-9]+)?` +
	`|\.[0-9]+(?:[eE][+-]?[0-9]+)?` +
	`)$`)

// Object is a JSON object that keeps its keys in the order they were first written. A repeated
// key keeps its first position and takes its last value, as JSON.parse does.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject makes an empty object.
func NewObject() *Object { return &Object{keys: nil, values: map[string]any{}} }

// Get is the value at key and whether the key is there (a key holding null is there; an absent
// key is undefined).
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}

	value, ok := o.values[key]

	return value, ok
}

// Set puts value at key, appending the key when it is new.
func (o *Object) Set(key string, value any) {
	_, ok := o.values[key]
	if !ok {
		o.keys = append(o.keys, key)
	}

	o.values[key] = value
}

// Delete removes key.
func (o *Object) Delete(key string) {
	_, ok := o.values[key]
	if !ok {
		return
	}

	delete(o.values, key)

	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// Keys are the object's keys in order.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}

	return slices.Clone(o.keys)
}

// Len is how many keys the object holds.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}

	return len(o.keys)
}

// MarshalJSON writes the object in key order.
func (o *Object) MarshalJSON() ([]byte, error) { return []byte(Stringify(o)), nil }

// indexKey is a key that is an array index, with its number.
type indexKey struct {
	key    string
	number uint64
}

// ordered is the keys in JavaScript's own order: keys that are array indexes first in numeric
// order, then the rest as written. JSON.parse builds objects through the same property
// machinery, so `{"b":1,"2":2,"a":3,"1":4}` lists 1, 2, b, a.
func (o *Object) ordered() []string {
	var (
		indexes []indexKey
		rest    []string
	)

	for _, key := range o.keys {
		number, isIndex := arrayIndex(key)
		if isIndex {
			indexes = append(indexes, indexKey{key: key, number: number})

			continue
		}

		rest = append(rest, key)
	}

	slices.SortStableFunc(indexes, func(first, second indexKey) int {
		return cmp.Compare(first.number, second.number)
	})

	keys := make([]string, 0, len(o.keys))
	for _, index := range indexes {
		keys = append(keys, index.key)
	}

	return append(keys, rest...)
}

// arrayIndex reads a canonical array index: 0 to 2^32 - 2, written without leading zeros.
func arrayIndex(key string) (uint64, bool) {
	if key == "" {
		return 0, false
	}

	if len(key) > indexDigitsMax {
		return 0, false
	}

	if len(key) > 1 {
		if key[0] == '0' {
			return 0, false
		}
	}

	for index := range len(key) {
		if !isDigit(key[index]) {
			return 0, false
		}
	}

	number, err := strconv.ParseUint(key, 10, 64)
	if err != nil {
		return 0, false
	}

	return number, number < math.MaxUint32
}

func isDigit(char byte) bool {
	if char < '0' {
		return false
	}

	return char <= '9'
}

// Parse reads JSON text the way JSON.parse does: objects as *Object (key order kept), arrays as
// []any, numbers as float64, strings, booleans and nil. Anything else, including trailing text,
// is ErrSyntax.
func Parse(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	parsed, err := readValue(decoder, 0)
	if err != nil {
		return nil, ErrSyntax
	}

	_, err = decoder.Token()
	if !errors.Is(err, io.EOF) {
		return nil, ErrSyntax
	}

	return parsed, nil
}

// readValue reads the next value; every failure is ErrSyntax.
func readValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > depthMax {
		return nil, ErrSyntax
	}

	token, err := decoder.Token()
	if err != nil {
		return nil, ErrSyntax
	}

	switch typed := token.(type) {
	case json.Delim:
		return readContainer(decoder, typed, depth)
	case json.Number:
		return readNumber(typed)
	}

	// A string, a boolean or nil.
	return token, nil
}

func readContainer(decoder *json.Decoder, opening json.Delim, depth int) (any, error) {
	switch opening {
	case '{':
		return readObject(decoder, depth)
	case '[':
		return readArray(decoder, depth)
	}

	return nil, ErrSyntax
}

func readObject(decoder *json.Decoder, depth int) (any, error) {
	object := NewObject()

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrSyntax
		}

		key, ok := token.(string)
		if !ok {
			return nil, ErrSyntax
		}

		value, err := readValue(decoder, depth+1)
		if err != nil {
			return nil, err
		}

		object.Set(key, value)
	}

	_, err := decoder.Token()
	if err != nil {
		return nil, ErrSyntax
	}

	object.keys = object.ordered()

	return object, nil
}

func readArray(decoder *json.Decoder, depth int) (any, error) {
	array := []any{}

	for decoder.More() {
		value, err := readValue(decoder, depth+1)
		if err != nil {
			return nil, err
		}

		array = append(array, value)
	}

	_, err := decoder.Token()
	if err != nil {
		return nil, ErrSyntax
	}

	return array, nil
}

// readNumber reads a number. One too large for a float64 is ±Infinity, as JSON.parse gives.
func readNumber(number json.Number) (any, error) {
	value, err := strconv.ParseFloat(string(number), 64)
	if err == nil {
		return value, nil
	}

	if errors.Is(err, strconv.ErrRange) {
		return value, nil
	}

	return nil, ErrSyntax
}

// Stringify writes v as JSON.stringify(v) does, compact: *Object in key order, maps with sorted
// keys, structs through encoding/json (field order), numbers as JavaScript prints them, and no
// escaping of <, > or &. A non-finite number is null.
func Stringify(value any) string {
	var builder strings.Builder

	write(&builder, value, 0)

	return builder.String()
}

// StringifyIndent writes v as JSON.stringify(v, null, indent) does.
func StringifyIndent(value any, indent string) string {
	compact := Stringify(value)

	var out bytes.Buffer

	err := json.Indent(&out, []byte(compact), "", indent)
	if err != nil {
		return compact
	}

	return out.String()
}

// write appends value's JSON. A value nesting deeper than depthMax is a program error (only a
// cycle gets there), so it panics rather than writing something wrong.
func write(builder *strings.Builder, value any, depth int) {
	if depth > depthMax {
		panic("js: a value nests deeper than depthMax; is it a cycle?")
	}

	switch typed := value.(type) {
	case nil:
		builder.WriteString(nullText)
	case *Object:
		writeObject(builder, typed, depth)
	case []any:
		writeArray(builder, typed, depth)
	case []string:
		writeStrings(builder, typed)
	default:
		writeScalar(builder, value)
	}
}

// writeScalar appends the JSON of a value that holds no other value.
func writeScalar(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case string:
		writeString(builder, typed)
	case bool:
		builder.WriteString(strconv.FormatBool(typed))
	case float64:
		builder.WriteString(Number(typed))
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	default:
		writeEncoded(builder, value)
	}
}

func writeObject(builder *strings.Builder, object *Object, depth int) {
	if object == nil {
		builder.WriteString(nullText)

		return
	}

	builder.WriteByte('{')

	for index, key := range object.keys {
		if index > 0 {
			builder.WriteByte(',')
		}

		writeString(builder, key)
		builder.WriteByte(':')
		write(builder, object.values[key], depth+1)
	}

	builder.WriteByte('}')
}

func writeArray(builder *strings.Builder, array []any, depth int) {
	builder.WriteByte('[')

	for index, element := range array {
		if index > 0 {
			builder.WriteByte(',')
		}

		write(builder, element, depth+1)
	}

	builder.WriteByte(']')
}

func writeStrings(builder *strings.Builder, array []string) {
	builder.WriteByte('[')

	for index, element := range array {
		if index > 0 {
			builder.WriteByte(',')
		}

		writeString(builder, element)
	}

	builder.WriteByte(']')
}

// writeEncoded writes any other value (a struct, a map) through encoding/json.
func writeEncoded(builder *strings.Builder, value any) {
	var buffer bytes.Buffer

	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)

	err := encoder.Encode(value)
	if err != nil {
		builder.WriteString(nullText)

		return
	}

	builder.WriteString(strings.TrimSuffix(buffer.String(), "\n"))
}

// writeString quotes s as JSON.stringify does: ", \ and control characters escaped (\b \f \n \r
// \t by name, the rest as \u00XX), and everything else as it is.
func writeString(builder *strings.Builder, text string) {
	builder.WriteByte('"')

	for index := 0; index < len(text); {
		char, size := utf8.DecodeRuneInString(text[index:])
		escaped := escape(char)

		switch {
		case escaped != "":
			builder.WriteString(escaped)
		case char < controlEnd:
			builder.WriteString(`\u00`)
			builder.WriteByte(hexDigits[char>>4])
			builder.WriteByte(hexDigits[char&0xf])
		default:
			builder.WriteString(text[index : index+size])
		}

		index += size
	}

	builder.WriteByte('"')
}

// escape is the named escape JSON.stringify writes for char, or "".
func escape(char rune) string {
	switch char {
	case '"':
		return `\"`
	case '\\':
		return `\\`
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	}

	return ""
}

// Number prints value as JavaScript's Number.prototype.toString does.
func Number(value float64) string {
	if math.IsNaN(value) {
		return nullText
	}

	if math.IsInf(value, 0) {
		return nullText
	}

	if value == 0 {
		return "0"
	}

	if fixedNotation(math.Abs(value)) {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}

	// Go writes 1e+21 and 1.5e-07; JavaScript writes 1e+21 and 1.5e-7.
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(value, 'e', -1, 64), "e")

	return mantissa + "e" + exponent[:1] + strings.TrimLeft(exponent[1:], "0")
}

func fixedNotation(magnitude float64) bool {
	if magnitude < fixedMin {
		return false
	}

	return magnitude < fixedEnd
}

// Trim removes leading and trailing whitespace as String.prototype.trim does: the Unicode space
// separators, tab, vertical tab, form feed, no-break space, the byte order mark and the line
// terminators (LF, CR, U+2028, U+2029).
func Trim(text string) string { return strings.TrimFunc(text, IsSpace) }

// IsSpace is JavaScript's WhiteSpace or LineTerminator.
func IsSpace(char rune) bool {
	switch char {
	case '\t', '\n', '\v', '\f', '\r', ' ', noBreakSpace, oghamSpace, lineSeparator,
		paragraphSeparator, narrowNoBreakSpace, mathSpace, ideographicSpace, byteOrderMark:
		return true
	}

	if char < enQuad {
		return false
	}

	return char <= hairSpace
}

// CollapseSpace replaces every run of JavaScript whitespace with one space, as
// text.replace(/\s+/g, ' ') does.
func CollapseSpace(text string) string {
	var builder strings.Builder

	inRun := false

	for _, char := range text {
		if IsSpace(char) {
			inRun = true

			continue
		}

		if inRun {
			builder.WriteByte(' ')
		}

		inRun = false

		builder.WriteRune(char)
	}

	if inRun {
		builder.WriteByte(' ')
	}

	return builder.String()
}

// Truthy reports whether a parsed JSON value is truthy in JavaScript: everything but null, false,
// 0, NaN and the empty string.
func Truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		if math.IsNaN(typed) {
			return false
		}

		return typed != 0
	case string:
		return typed != ""
	}

	return true
}

// String converts a parsed JSON value as JavaScript's String(value) does: a number as
// Number.prototype.toString, an object as "[object Object]", an array as its items joined by
// commas (null as nothing).
func String(value any) string {
	return stringAt(value, 0)
}

func stringAt(value any, depth int) string {
	if depth > depthMax {
		panic("js.String: nesting deeper than depthMax, so the value has a cycle")
	}

	switch typed := value.(type) {
	case nil:
		if depth > 0 {
			return ""
		}

		return nullText
	case string:
		return typed
	case float64:
		return Number(typed)
	case bool:
		return strconv.FormatBool(typed)
	case *Object:
		return "[object Object]"
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, stringAt(item, depth+1))
		}

		return strings.Join(items, ",")
	}

	panic(fmt.Sprintf("js.String: %T is not a parsed JSON value", value))
}

// Len is s.length: the number of UTF-16 code units.
func Len(text string) int {
	count := 0
	for _, char := range text {
		count += utf16.RuneLen(char)
	}

	return count
}

// Slice is s.slice(0, n) for n >= 0: the first n UTF-16 code units. A surrogate pair the cut
// would split is left out whole.
func Slice(text string, units int) string {
	if units <= 0 {
		return ""
	}

	encoded := utf16.Encode([]rune(text))
	if units >= len(encoded) {
		return text
	}

	if isHighSurrogate(encoded[units-1]) {
		units--
	}

	return string(utf16.Decode(encoded[:units]))
}

func isHighSurrogate(unit uint16) bool {
	if unit < surrogateHighFirst {
		return false
	}

	return unit < surrogateLowFirst
}

// ToNumber converts text as JavaScript's Number(text) does: surrounding whitespace ignored, empty
// is 0, decimal and 0x/0o/0b integer literals, Infinity with an optional sign, and NaN for
// anything else.
func ToNumber(text string) float64 {
	text = Trim(text)

	switch text {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}

	value, isRadix := radixNumber(text)
	if isRadix {
		return value
	}

	if !decimal.MatchString(text) {
		return math.NaN()
	}

	value, err := strconv.ParseFloat(text, 64)
	if err == nil {
		return value
	}

	if errors.Is(err, strconv.ErrRange) {
		return value
	}

	return math.NaN()
}

// radixNumber reads a 0x, 0o or 0b integer literal; isRadix is false when text is not one.
func radixNumber(text string) (float64, bool) {
	if len(text) <= len("0x") {
		return 0, false
	}

	if text[0] != '0' {
		return 0, false
	}

	base := radixBase(text[1])
	if base == 0 {
		return 0, false
	}

	digits := text[2:]
	if strings.ContainsAny(digits, "_+-") {
		return math.NaN(), true
	}

	integer, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return math.NaN(), true
	}

	value, _ := new(big.Float).SetInt(integer).Float64()

	return value, true
}

func radixBase(letter byte) int {
	switch letter {
	case 'x', 'X':
		return baseHex
	case 'o', 'O':
		return baseOctal
	case 'b', 'B':
		return baseBinary
	}

	return 0
}
