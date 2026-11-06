package argv_test

import (
	"bytes"
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// shellTimeout bounds one shell run of a round trip.
const shellTimeout = 10 * time.Second

// randomCases and randomWordsMax bound the generated round trips: each case is up to
// randomWordsMax words of up to randomUnitsMax characters.
const (
	randomCases     = 300
	randomWordsMax  = 6
	randomUnitsMax  = 40
	randomSeedFirst = 7
	randomSeedLast  = 11
)

// hostileWords are values a ticket title, a branch, a folder or a session name could hold: each
// must come out of a shell as the one word it went in as.
func hostileWords() []string {
	return []string{
		"", " ", "it's", `say "hi"`, "$(whoami)", "`id`", "${HOME}", "$HOME", "a;rm -rf ~",
		"a && b", "a|b", "a\nb", "line one\nline two\n", "\ttab", "--", "-rf",
		"--dangerously-skip-permissions", "~", "~root", "*", "?", "[a]", "{a,b}", "!", "!!",
		"#comment", "a#b", "=cmd", "%1", "\\", `a\'b`, `\\'`, "'", "''", `'\''`, "\x1b[31mred",
		"\r", "\u00e9\u65e5\u672c", "\u202eevil", "a\x7fb", "../../etc/passwd", "/tmp/it's here",
		strings.Repeat("long ", 2000), "\xff\xfe", "(a)", "<in", ">out", "a&", "a^b",
	}
}

// TestShellLineKeepsPlainWordsBare: a word of the plain alphabet is written as it is, so the
// line a person reads looks like what they would type.
func TestShellLineKeepsPlainWordsBare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		words []string
		want  string
	}{
		{"a command", []string{"claude", "--name", "PROJ-1893"}, "claude --name PROJ-1893"},
		{"a path", []string{"cd", "/tmp/a.b_c"}, "cd /tmp/a.b_c"},
		{"an empty word", []string{"printf", ""}, "printf ''"},
		{"a quote", []string{"cd", "/tmp/it's here"}, `cd '/tmp/it'\''s here'`},
		{"a backslash", []string{`a\b`}, `'a'\\'b'`},
		{"a dollar", []string{"$(id)"}, "'$(id)'"},
		{"nothing", []string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := argv.ShellLine(tt.words)
			if got != tt.want {
				t.Errorf("ShellLine(%q) = %q, want %q", tt.words, got, tt.want)
			}
		})
	}
}

// TestShellLineRoundTripsThroughSh: every hostile word, alone and among others, comes back out
// of /bin/sh as exactly the words that went in, so nothing in a value runs or splits.
func TestShellLineRoundTripsThroughSh(t *testing.T) {
	t.Parallel()

	for _, word := range hostileWords() {
		words := []string{"before", word, "after"}

		got := throughShell(t, "/bin/sh", words)
		if !slices.Equal(got, words) {
			t.Errorf("%q came back as %q", words, got)
		}
	}
}

// TestShellLineRoundTripsRandomWords: words drawn from an alphabet of every character a shell
// treats specially come back whole, through sh, dash, bash and zsh.
func TestShellLineRoundTripsRandomWords(t *testing.T) {
	t.Parallel()

	shells := []string{"/bin/sh", "/bin/dash", "/bin/bash", "/bin/zsh"}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			_, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not on this machine", shell)
			}

			for seed := uint64(randomSeedFirst); seed <= randomSeedLast; seed++ {
				words := randomCase(&sequence{state: seed})

				got := throughShell(t, shell, words)
				if !slices.Equal(got, words) {
					t.Errorf("seed %d: %q came back as %q", seed, words, got)
				}
			}
		})
	}
}

// TestShellLineRoundTripsThroughFish: the person's own shell may be fish, whose single quotes
// treat a backslash differently; the hostile words still come back whole.
func TestShellLineRoundTripsThroughFish(t *testing.T) {
	t.Parallel()

	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not on this machine")
	}

	for _, word := range hostileWords() {
		words := []string{"before", word, "after"}

		got := throughShell(t, fish, words)
		if !slices.Equal(got, words) {
			t.Errorf("%q came back as %q", words, got)
		}
	}
}

// TestShellLineIsOneTokenPerWord: many random words, checked against the line alone, never
// against a shell: each word gives exactly one token, so the count of words a shell would see
// is the count given.
func TestShellLineIsOneTokenPerWord(t *testing.T) {
	t.Parallel()

	random := &sequence{state: 1}

	for range randomCases {
		words := randomCase(random)

		got := throughShell(t, "/bin/sh", words)
		if len(got) != len(words) {
			t.Fatalf("%q gave %d words, want %d", words, len(got), len(words))
		}
	}
}

// sequence is a fixed series of numbers from a seed (a linear congruential generator), so a
// generated case is the same on every run and a failure names it again.
type sequence struct {
	state uint64
}

// next is the next number of the series.
func (s *sequence) next() uint64 {
	s.state = s.state*6364136223846793005 + 1442695040888963407

	return s.state >> 33
}

// randomCase is up to randomWordsMax words, each up to randomUnitsMax characters drawn from
// the characters shells treat specially, letters and a few beyond ASCII.
func randomCase(random *sequence) []string {
	alphabet := [...]rune{
		' ', '\t', '\n', '\'', '"', '\\', '$', '`', '!', '#', '&', ';', '|', '<', '>', '(', ')',
		'{', '}', '[', ']', '*', '?', '~', '=', '%', '^', ',', ':', '@', '+', '-', '.', '/',
		'_', 'a', 'Z', '0', '9', '\u00e9', '\u65e5', '\u202e', '\x1b',
	}
	words := make([]string, 1+random.next()%randomWordsMax)

	for index := range words {
		runes := make([]rune, random.next()%randomUnitsMax)
		for at := range runes {
			runes[at] = alphabet[random.next()%uint64(len(alphabet))]
		}

		words[index] = string(runes)
	}

	return words
}

// throughShell runs printf in shell over ShellLine(words) and answers the words printf saw,
// each ended by a NUL.
func throughShell(t *testing.T, shell string, words []string) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(shellTimeout))
	defer cancel()

	script := `printf '%s\0' ` + argv.ShellLine(words)
	if strings.HasSuffix(shell, "fish") {
		script = `printf '%s\x00' ` + argv.ShellLine(words)
	}

	command := exec.CommandContext(ctx, shell, "-c", script)

	var stdout, stderr bytes.Buffer

	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if err != nil {
		t.Fatalf("%s: %v: %s", shell, err, stderr.String())
	}

	out := stdout.String()

	if len(words) == 0 {
		return []string{}
	}

	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
}

// TestPastableRefusesWhatALineEditorActsOn: a word holding a control character (C0, DEL, C1), a
// bidirectional control or bytes that are not UTF-8 is not pastable: a line editor acts on such
// bytes before any shell reads the quotes, and a bidi control makes the shown line read
// differently from the one that runs.
func TestPastableRefusesWhatALineEditorActsOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		pastable bool
	}{
		{"plain", "/work/it's here $(id)", true},
		{"beyond ASCII", "/work/\u00e9\u65e5", true},
		{"empty", "", true},
		{"Ctrl-U", "a\x15b", false},
		{"newline", "a\nb", false},
		{"escape", "a\x1b[2Jb", false},
		{"tab", "a\tb", false},
		{"NUL", "a\x00b", false},
		{"DEL", "a\x7fb", false},
		{"C1 next line", "a\u0085b", false},
		{"C1 CSI", "a\u009bb", false},
		{"right-to-left override", "a\u202eb", false},
		{"left-to-right isolate", "a\u2066b", false},
		{"pop directional isolate", "a\u2069b", false},
		{"right-to-left mark", "a\u200fb", false},
		{"Arabic letter mark", "a\u061cb", false},
		{"not UTF-8", "a\xffb", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := argv.Pastable(tt.text); got != tt.pastable {
				t.Errorf("Pastable(%q) = %v, want %v", tt.text, got, tt.pastable)
			}
		})
	}
}

// TestAPastableLineIsPrintableASCIIOnly: a line is offered to paste only when every byte is
// printable ASCII. Under a C or POSIX locale, bash's readline reads each byte of 0x80 or more as a
// Meta key (U+0112 is ESC D then ESC ^R, revert-line), so any other byte can rewrite the line
// before a quote is read.
func TestAPastableLineIsPrintableASCIIOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		line     string
		pastable bool
	}{
		{"plain", `cd '/work/it'\''s here' && claude --name PROJ-1`, true},
		{"every printable byte", " !~", true},
		{"empty", "", true},
		{"a letter beyond ASCII", "cd /work/x\u0112touch pwned", false},
		{"a line separator", "cd /work/x\u2028y", false},
		{"a tab", "cd /work/x\ty", false},
		{"DEL", "cd /work/x\x7f", false},
		{"a byte that is not UTF-8", "cd /work/x\xff", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := argv.PastableLine(tt.line); got != tt.pastable {
				t.Errorf("PastableLine(%q) = %v, want %v", tt.line, got, tt.pastable)
			}
		})
	}
}

// TestInvisibleCharactersAreFound: the zero-width and tag characters, which make a name shown
// differ from the name used.
func TestInvisibleCharactersAreFound(t *testing.T) {
	t.Parallel()

	for _, char := range []rune{0x200b, 0x200c, 0x200d, 0x2060, 0xfeff, 0xe0000, 0xe0041, 0xe007f} {
		if !argv.Invisible(char) {
			t.Errorf("U+%04X is not found", char)
		}
	}

	for _, char := range []rune{'a', 0x00e9, 0x200a, 0x2061, 0xe0080, 0xfefe} {
		if argv.Invisible(char) {
			t.Errorf("U+%04X is found", char)
		}
	}
}
