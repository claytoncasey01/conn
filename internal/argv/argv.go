// Package argv writes a program's arguments as one shell line, for the two places a person's
// shell runs a session Conn would otherwise start itself: a terminal app's command (cmux, or the
// person's own) and the command a person pastes. ShellLine is the only code in Conn that quotes
// for a shell; everything else passes arguments to a program directly, with no shell between.
package argv

import (
	"strings"
	"unicode/utf8"
)

// plainCharacters are the characters a word may hold and still be written bare: none of them
// means anything to sh, bash, zsh or fish anywhere in a word. A tilde (home), an equals sign
// (zsh's =command), a percent sign (old fish's process expansion) and every other mark are
// quoted, so a bare word is always plain text.
const plainCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@+,-"

// The control characters Control finds.
const (
	// c0End is the first character after C0.
	c0End = 0x20
	// del is DEL.
	del = 0x7f
	// c1First and c1Last bound C1.
	c1First = 0x80
	c1Last  = 0x9f
	// bidiControls are the characters that change the direction text is shown in: the Arabic
	// letter mark, the left-to-right and right-to-left marks, the embeddings and overrides
	// (U+202A to U+202E) and the isolates (U+2066 to U+2069).
	bidiControls = "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
)

// ShellLine is words as one line a shell reads back as exactly those words, in order: a word of
// plain characters as it is, and any other in single quotes, with each single quote and each
// backslash written outside them, escaped (fish, unlike sh, reads a backslash inside single
// quotes). So no character of a word, a quote, a newline, $( or a leading dash included, can end
// it, start another, or run anything. An empty word is ”.
//
// A NUL cannot be passed to a program at all, so a caller refuses one before it gets here; a
// NUL given anyway is quoted like any other character, and the shell or the system refuses it.
func ShellLine(words []string) string {
	quoted := make([]string, 0, len(words))

	for _, word := range words {
		quoted = append(quoted, shellWord(word))
	}

	return strings.Join(quoted, " ")
}

// shellWord is one word as ShellLine writes it.
func shellWord(word string) string {
	if word == "" {
		return "''"
	}

	if plain(word) {
		return word
	}

	var line strings.Builder

	line.WriteByte('\'')

	// Bytes, not runes: a word that is not valid UTF-8 keeps its bytes, and neither mark is
	// ever part of a longer character.
	for index := range len(word) {
		switch word[index] {
		case '\'':
			line.WriteString(`'\''`)
		case '\\':
			line.WriteString(`'\\'`)
		default:
			line.WriteByte(word[index])
		}
	}

	line.WriteByte('\'')

	return line.String()
}

// plain is whether every character of word is a plain character.
func plain(word string) bool {
	for index := range len(word) {
		if strings.IndexByte(plainCharacters, word[index]) < 0 {
			return false
		}
	}

	return true
}

// Pastable is whether text may be part of a line a person pastes into a shell: valid UTF-8 with
// no control character (Control). Quoting cannot make a control character safe there: an
// interactive shell's line editor acts on the bytes (Ctrl-U erases the line, Ctrl-J ends it)
// before the shell reads any quote, and a bidirectional control makes the line shown read
// differently from the line that runs.
func Pastable(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}

	for _, char := range text {
		if Control(char) {
			return false
		}
	}

	return true
}

// Control is whether char is a control character no pasted or drawn text may hold: C0 (NUL to
// U+001F), DEL, C1 (U+0080 to U+009F) and the bidirectional controls (bidiControls).
func Control(char rune) bool {
	if char < c0End {
		return true
	}

	if char == del {
		return true
	}

	if char >= c1First {
		if char <= c1Last {
			return true
		}
	}

	return strings.ContainsRune(bidiControls, char)
}
