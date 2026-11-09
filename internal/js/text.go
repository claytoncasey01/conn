package js

import (
	"math"
	"math/big"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// The UTF-8 decoder's byte ranges (WHATWG Encoding, "UTF-8 decoder").
const (
	asciiEnd        = 0x80
	minContinuation = 0x80
	maxContinuation = 0xbf
	minLeadTwo      = 0xc2
	maxLeadTwo      = 0xdf
	minLeadThree    = 0xe0
	maxLeadThree    = 0xef
	minLeadFour     = 0xf0
	maxLeadFour     = 0xf4

	// A three-byte lead of 0xe0 needs 0xa0 or more next (no overlong form); 0xed needs 0x9f or
	// less (no surrogate). A four-byte lead of 0xf0 needs 0x90 or more; 0xf4 needs 0x8f or less.
	minAfterE0 = 0xa0
	afterEDMax = 0x9f
	minAfterF0 = 0x90
	maxAfterF4 = 0x8f
	leadE0     = 0xe0
	leadED     = 0xed
	leadF0     = 0xf0
	leadF4     = 0xf4

	leadTwoBits      = 0x1f
	leadThreeBits    = 0x0f
	leadFourBits     = 0x07
	continuationBits = 0x3f
	continuationSize = 6

	// fixedLimit is where toFixed gives up on fixed notation and prints the number as it is.
	fixedLimit = 1e21
)

// utf8State is the WHATWG UTF-8 decoder's state between bytes.
type utf8State struct {
	codePoint rune
	needed    int
	seen      int
	lower     byte
	upper     byte
}

func newUTF8State() utf8State {
	return utf8State{
		codePoint: 0, needed: 0, seen: 0, lower: minContinuation, upper: maxContinuation,
	}
}

// DecodeUTF8 decodes bytes as UTF-8 the way TextDecoder and Buffer.toString do: each maximal
// invalid subpart becomes one U+FFFD. With stripBOM a leading byte order mark is dropped, as
// TextDecoder does by default (Buffer.toString keeps it).
func DecodeUTF8(data []byte, stripBOM bool) string {
	if stripBOM {
		data = trimBOM(data)
	}

	if utf8.Valid(data) {
		return string(data)
	}

	var builder strings.Builder

	builder.Grow(len(data))

	state := newUTF8State()

	for index := 0; index < len(data); {
		char, advance := state.step(data[index])
		if char >= 0 {
			builder.WriteRune(char)
		}

		if advance {
			index++
		}
	}

	if state.needed != 0 {
		builder.WriteRune(utf8.RuneError)
	}

	return builder.String()
}

func trimBOM(data []byte) []byte {
	bom := []byte{0xef, 0xbb, 0xbf}
	if len(data) < len(bom) {
		return data
	}

	if slices.Equal(data[:len(bom)], bom) {
		return data[len(bom):]
	}

	return data
}

// step takes one byte. It gives the character finished, or -1, and whether the byte was used
// (a byte that ends an invalid sequence is read again as the start of the next).
func (s *utf8State) step(current byte) (rune, bool) {
	if s.needed == 0 {
		return s.lead(current), true
	}

	if !within(current, s.lower, s.upper) {
		*s = newUTF8State()

		return utf8.RuneError, false
	}

	s.lower, s.upper = minContinuation, maxContinuation
	s.codePoint = s.codePoint<<continuationSize | rune(current&continuationBits)
	s.seen++

	if s.seen < s.needed {
		return -1, true
	}

	char := s.codePoint
	*s = newUTF8State()

	return char, true
}

// lead reads a byte that starts a character: an ASCII character, the start of a longer one
// (-1), or U+FFFD for a byte that can start nothing.
func (s *utf8State) lead(current byte) rune {
	switch {
	case current < asciiEnd:
		return rune(current)
	case within(current, minLeadTwo, maxLeadTwo):
		s.needed, s.codePoint = 1, rune(current&leadTwoBits)
	case within(current, minLeadThree, maxLeadThree):
		s.needed, s.codePoint = 2, rune(current&leadThreeBits)
		s.narrow(current)
	case within(current, minLeadFour, maxLeadFour):
		s.needed, s.codePoint = 3, rune(current&leadFourBits)
		s.narrow(current)
	default:
		return utf8.RuneError
	}

	return -1
}

func within(current, lowest, highest byte) bool {
	if current < lowest {
		return false
	}

	return current <= highest
}

// narrow sets the range of the byte after a lead that allows only part of it.
func (s *utf8State) narrow(lead byte) {
	switch lead {
	case leadE0:
		s.lower = minAfterE0
	case leadED:
		s.upper = afterEDMax
	case leadF0:
		s.lower = minAfterF0
	case leadF4:
		s.upper = maxAfterF4
	}
}

// QuoteClip is JSON.stringify(s.length > units ? s.slice(0, units) + "..." : s): the text cut
// to units UTF-16 code units, quoted. A cut through a surrogate pair leaves its first half,
// which JSON.stringify writes as a \udXXX escape.
func QuoteClip(text string, units int) string {
	encoded := utf16.Encode([]rune(text))
	if len(encoded) <= units {
		return Stringify(text)
	}

	cut := encoded[:units]
	if !isHighSurrogate(cut[len(cut)-1]) {
		return Stringify(string(utf16.Decode(cut)) + "...")
	}

	quoted := Stringify(string(utf16.Decode(cut[:len(cut)-1])) + "...")
	lone := `\u` + hex4(cut[len(cut)-1])

	// The escape goes where the cut was: before the "..." and the closing quote.
	return quoted[:len(quoted)-len(`..."`)] + lone + `..."`
}

func hex4(unit uint16) string {
	const (
		nibbles   = 4
		nibbleBit = 4
	)

	out := make([]byte, nibbles)
	for index := range nibbles {
		shift := (nibbles - 1 - index) * nibbleBit
		out[index] = hexDigits[(unit>>shift)&0xf]
	}

	return string(out)
}

// CompareUTF16 orders two strings by their UTF-16 code units, as JavaScript's default sort and
// its < do. It differs from Go's byte order only where a character beyond U+FFFF meets one
// from U+E000 to U+FFFF.
func CompareUTF16(left, right string) int {
	return slices.Compare(utf16.Encode([]rune(left)), utf16.Encode([]rune(right)))
}

// Fixed is value.toFixed(digits) for digits from 0 to 20: the exact binary value rounded to
// that many decimals, a tie going away from zero (strconv rounds a tie to even).
func Fixed(value float64, digits int) string {
	if math.IsNaN(value) {
		return "NaN"
	}

	if math.Abs(value) >= fixedLimit {
		return Number(value)
	}

	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}

	const decimalBase = 10

	half := big.NewRat(1, baseBinary)
	scale := new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(int64(digits)), nil)
	exact := new(big.Rat).SetFloat64(value)
	exact.Mul(exact, new(big.Rat).SetInt(scale))
	exact.Add(exact, half)

	rounded := new(big.Int).Quo(exact.Num(), exact.Denom())

	// A negative input keeps its sign even when it rounds to zero: (-0.001).toFixed(2) is
	// "-0.00".
	text := rounded.String()
	if digits == 0 {
		return sign + text
	}

	if len(text) <= digits {
		text = strings.Repeat("0", digits-len(text)+1) + text
	}

	return sign + text[:len(text)-digits] + "." + text[len(text)-digits:]
}

// Lower is String.prototype.toLowerCase: Unicode's lowercase mapping, with its one
// unconditional mapping to two characters (U+0130 to i and a combining dot above) and the final
// sigma, which lowercases to ς at the end of a word.
func Lower(text string) string {
	runes := []rune(text)

	var builder strings.Builder

	for index, char := range runes {
		switch char {
		case capitalIWithDot:
			builder.WriteString("i̇")
		case capitalSigma:
			if finalSigma(runes, index) {
				builder.WriteRune(finalSmallSigma)
			} else {
				builder.WriteRune(smallSigma)
			}
		default:
			builder.WriteRune(unicode.ToLower(char))
		}
	}

	return builder.String()
}

const (
	capitalIWithDot = 'İ'
	capitalSigma    = 'Σ'
	smallSigma      = 'σ'
	finalSmallSigma = 'ς'
)

// finalSigma is Unicode's Final_Sigma condition for the sigma at index: a cased letter before it
// (case-ignorable characters between) and none after it.
func finalSigma(runes []rune, index int) bool {
	before := false

	for at := index - 1; at >= 0; at-- {
		if caseIgnorable(runes[at]) {
			continue
		}

		before = cased(runes[at])

		break
	}

	if !before {
		return false
	}

	for at := index + 1; at < len(runes); at++ {
		if caseIgnorable(runes[at]) {
			continue
		}

		return !cased(runes[at])
	}

	return true
}

func cased(char rune) bool {
	if unicode.IsUpper(char) {
		return true
	}

	if unicode.IsLower(char) {
		return true
	}

	return unicode.IsTitle(char)
}

// caseIgnorable is Unicode's Case_Ignorable: marks, format characters, modifier letters and
// symbols, and the word-internal punctuation.
func caseIgnorable(char rune) bool {
	if unicode.In(char, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}

	return strings.ContainsRune("'.:^`·‘’․‧", char)
}
