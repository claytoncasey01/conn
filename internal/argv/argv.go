// Package argv writes a program's arguments as one shell line, for the two places a person's
// shell runs a session Conn would otherwise start itself: a terminal app's command (cmux, or the
// person's own) and the command a person pastes. ShellLine is the only code in Conn that quotes
// for a shell; everything else passes arguments to a program directly, with no shell between.
package argv

import "strings"

// plainCharacters are the characters a word may hold and still be written bare: none of them
// means anything to sh, bash, zsh or fish anywhere in a word. A tilde (home), an equals sign
// (zsh's =command), a percent sign (old fish's process expansion) and every other mark are
// quoted, so a bare word is always plain text.
const plainCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@+,-"

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
