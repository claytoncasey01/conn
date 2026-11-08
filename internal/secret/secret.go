// Package secret is the per-launch secret every /api request must carry: 256 random bits made
// when a Conn starts, held in memory and in the data folder's lock file (mode 0600), and sent by
// the page in an Authorization header. It keeps the API from another user of the machine, who can
// reach the port but not the lock file. The secret prints as a mark, never as itself: only String
// gives its text, so a log line or an error that formats one shows nothing, and JSON shows an
// empty object.
package secret

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
)

const (
	// Header is the request header the secret travels in.
	Header = "Authorization"
	// Scheme is the Authorization scheme: the header is "Bearer <secret>".
	Scheme = "Bearer"

	// bytesCount is the secret's size: 256 bits.
	bytesCount = 32
	// textLength is the length of its text: unpadded base64url of bytesCount bytes.
	textLength = 43
	// mark is what a printed or logged secret shows instead of itself.
	mark = "[secret]"
)

// ErrNotSecret is text that is not a secret's: the wrong length or not unpadded base64url. It
// never says what the text was.
var ErrNotSecret = errors.New("not a conn secret")

// Secret is one launch's secret. The zero Secret is none, and matches nothing.
//
// The mark has a limit: fmt does not call Format on a field it cannot export, so a Secret in an
// unexported struct field prints as itself when the struct is printed whole (%v, %+v). Never
// print or log a value that holds one; a test pins this limit.
type Secret struct {
	text string
}

// New is a fresh secret from crypto/rand.
func New() (Secret, error) {
	raw := make([]byte, bytesCount)

	_, err := rand.Read(raw)
	if err != nil {
		return Secret{}, fmt.Errorf("making the secret: %w", err)
	}

	return Secret{text: base64.RawURLEncoding.EncodeToString(raw)}, nil
}

// Parse is the secret text names: exactly textLength characters of unpadded base64url that
// decode to bytesCount bytes.
func Parse(text string) (Secret, error) {
	if len(text) != textLength {
		return Secret{}, ErrNotSecret
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil {
		return Secret{}, ErrNotSecret
	}

	if len(raw) != bytesCount {
		return Secret{}, ErrNotSecret
	}

	return Secret{text: text}, nil
}

// String is the secret's text, for the one place that hands it on: the lock file, the page's
// address fragment, a request's header.
func (s Secret) String() string { return s.text }

// IsZero says whether s is no secret.
func (s Secret) IsZero() bool { return s.text == "" }

// Matches says, in time that does not depend on where they differ, whether presented is the
// secret. The zero Secret matches nothing, the empty string included.
func (s Secret) Matches(presented string) bool {
	if s.IsZero() {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.text)) == 1
}

// Authorization is the value of the Header that carries s.
func (s Secret) Authorization() string { return Scheme + " " + s.text }

// Format prints the mark for every verb, so fmt never shows the secret.
func (s Secret) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(mark))
}

// LogValue logs the mark, so slog never shows the secret.
func (s Secret) LogValue() slog.Value { return slog.StringValue(mark) }
