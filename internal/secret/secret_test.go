package secret_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/secret"
)

func TestNewIs256RandomBits(t *testing.T) {
	t.Parallel()

	first, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	second, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(first.String())
	if err != nil {
		t.Fatalf("String is not unpadded base64url: %v", err)
	}

	if len(raw) != 32 {
		t.Fatalf("%d bytes, want 32", len(raw))
	}

	if first.String() == second.String() {
		t.Fatal("two secrets are the same")
	}
}

func TestMatches(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	value := made.String()

	tests := []struct {
		name      string
		secret    secret.Secret
		presented string
		want      bool
	}{
		{"the secret", made, value, true},
		{"empty", made, "", false},
		{"one character off", made, value[:len(value)-1] + flip(value[len(value)-1]), false},
		{"a prefix", made, value[:10], false},
		{"longer", made, value + "A", false},
		{"with the scheme", made, "Bearer " + value, false},
		{"the zero secret and empty", secret.Secret{}, "", false},
		{"the zero secret and a value", secret.Secret{}, value, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.secret.Matches(tt.presented); got != tt.want {
				t.Fatalf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
}

func flip(char byte) string {
	if char == 'A' {
		return "B"
	}

	return "A"
}

func TestParse(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		text    string
		wantErr error
	}{
		{"a secret", made.String(), nil},
		{"empty", "", secret.ErrNotSecret},
		{"too short", made.String()[:42], secret.ErrNotSecret},
		{"too long", made.String() + "A", secret.ErrNotSecret},
		{"padded", base64.URLEncoding.EncodeToString(make([]byte, 32)), secret.ErrNotSecret},
		{"standard alphabet", strings.Repeat("+", 43), secret.ErrNotSecret},
		{"spaces", strings.Repeat(" ", 43), secret.ErrNotSecret},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := secret.Parse(tt.text)
			if tt.wantErr != nil {
				assertRefused(t, err, tt.wantErr, tt.text)

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if !parsed.Matches(tt.text) {
				t.Fatal("the parsed secret does not match its text")
			}
		})
	}
}

func assertRefused(t *testing.T, err, want error, text string) {
	t.Helper()

	if err == nil {
		t.Fatal("no error")
	}

	if err.Error() != want.Error() {
		t.Fatalf("error %q, want %q", err, want)
	}

	if text != "" {
		if strings.Contains(err.Error(), text) {
			t.Fatal("the error holds the text")
		}
	}
}

// The secret only leaves as itself through String: every way a value is printed or logged shows
// a mark instead.
func TestPrintingDoesNotShowIt(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	holder := struct{ Key secret.Secret }{Key: made}

	var logged strings.Builder

	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	logger.Info("started", "key", made, "holder", holder)
	slog.New(slog.NewTextHandler(&logged, nil)).Info("started", "key", made)

	printed := map[string]string{
		"%v":   fmt.Sprintf("%v", made),
		"%s":   fmt.Sprintf("%s", made),
		"%+v":  fmt.Sprintf("%+v", holder),
		"%#v":  fmt.Sprintf("%#v", holder),
		"%q":   fmt.Sprintf("%q", made),
		"%x":   fmt.Sprintf("%x", made),
		"slog": logged.String(),
	}

	for verb, text := range printed {
		if strings.Contains(text, made.String()) {
			t.Errorf("%s shows the secret: %s", verb, text)
		}
	}
}

func TestJSONDoesNotShowIt(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(struct {
		Key secret.Secret `json:"key"`
	}{Key: made})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), made.String()) {
		t.Fatalf("JSON shows the secret: %s", data)
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	if made.Authorization() != "Bearer "+made.String() {
		t.Fatalf("Authorization = %q", made.Authorization())
	}

	if secret.Header != "Authorization" {
		t.Fatalf("Header = %q", secret.Header)
	}
}

// fmt cannot call Format on a field it cannot export, so a Secret held in an unexported field
// prints as itself. This is the limit of the mark: keep a Secret out of any value that is printed
// whole, or hold it in an exported field.
func TestAnUnexportedFieldIsPrintedAsItself(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	holder := struct{ key secret.Secret }{key: made}

	if !strings.Contains(fmt.Sprintf("%+v", holder), made.String()) {
		t.Fatal("fmt now marks an unexported Secret: update the warning on Secret")
	}
}
