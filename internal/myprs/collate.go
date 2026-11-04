package myprs

import (
	"github.com/claytoncasey01/conn/internal/js"
)

// Collation weights: String.prototype.localeCompare as Bun's ICU orders the characters a repo
// name holds. Punctuation comes before digits, digits before letters, and a letter's case is a
// later level: it decides only between names that are otherwise the same.
const (
	weightUnderscore = 1
	weightHyphen     = 2
	weightDot        = 3
	weightSlash      = 4
	weightDigits     = 10
	weightLetters    = 20
	// weightOther puts a character outside the repo alphabet after every letter, by its code
	// point (a repo name never holds one: it is checked by the grammar).
	weightOther = 1000
)

// Collate orders two repo names as localeCompare does: by the characters' primary weights, then
// lowercase before uppercase at the first place they differ in case only.
func Collate(left, right string) int {
	first := []rune(left)
	second := []rune(right)

	for index := range min(len(first), len(second)) {
		if order := primary(first[index]) - primary(second[index]); order != 0 {
			return sign(order)
		}
	}

	if len(first) != len(second) {
		return sign(len(first) - len(second))
	}

	for index := range first {
		if order := caseOf(first[index]) - caseOf(second[index]); order != 0 {
			return sign(order)
		}
	}

	return js.CompareUTF16(left, right)
}

func primary(char rune) int {
	switch char {
	case '_':
		return weightUnderscore
	case '-':
		return weightHyphen
	case '.':
		return weightDot
	case '/':
		return weightSlash
	}

	return alphanumeric(char)
}

func alphanumeric(char rune) int {
	if '0' <= char {
		if char <= '9' {
			return weightDigits + int(char-'0')
		}
	}

	if 'a' <= char {
		if char <= 'z' {
			return weightLetters + int(char-'a')
		}
	}

	if 'A' <= char {
		if char <= 'Z' {
			return weightLetters + int(char-'A')
		}
	}

	return weightOther + int(char)
}

// caseOf is 1 for an uppercase letter, else 0: lowercase sorts first.
func caseOf(char rune) int {
	if 'A' <= char {
		if char <= 'Z' {
			return 1
		}
	}

	return 0
}

func sign(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	}

	return 0
}
