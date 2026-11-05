package update_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/update"
)

// longTag is a release tag of the version shape that is exactly size bytes long.
func longTag(size int) string {
	const prefix = "v1.2.3-"

	return prefix + strings.Repeat("a", size-len(prefix))
}

func TestNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"minor compared as a number", "v1.9.0", "v1.10.0", true},
		{"same version", "v1.2.3", "v1.2.3", false},
		{"older latest", "v1.2.3", "v1.2.2", false},
		{"next major", "v1.2.3", "v2.0.0", true},
		{"pre-release latest of the same numbers", "v1.2.3", "v1.2.3-rc1", false},
		{"release after a pre-release", "v1.2.3-rc1", "v1.2.3", true},
		{"one pre-release after another", "v1.2.3-rc1", "v1.2.3-rc2", false},
		{"build metadata is ignored", "v1.2.3+7", "v1.2.3", false},
		{"dev current", "dev", "v1.0.0", false},
		{"dev latest", "v1.0.0", "dev", false},
		{"empty latest", "v1.0.0", "", false},
		{"no leading v", "v1.0.0", "1.0.1", false},
		{"two numbers", "v1.0.0", "v1.0", false},
		{"four numbers", "v1.0.0", "v1.0.1.2", false},
		{"too many digits", "v1.0.0", "v1.0.9999999999", false},
		{"trailing space", "v1.0.0", "v1.0.1 ", false},
		{"trailing newline", "v1.0.0", "v1.2.4\n", false},
		{"uppercase v", "v1.0.0", "V1.2.3", false},
		{"empty pre-release", "v1.0.0", "v1.2.3-", false},
		{"different build metadata", "v1.2.3+a", "v1.2.3+b", false},
		{"leading zeros are equal", "v1.2.3", "v01.2.3", false},
		{"leading zeros are not older", "v01.2.3", "v1.2.3", false},
		{"leading zeros compare as numbers", "v1.2.3", "v01.2.4", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := update.Newer(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("Newer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

var errFetch = errors.New("network down")

func TestEvaluate(t *testing.T) {
	t.Parallel()

	atLimit := longTag(update.TagBytesMax)
	overLimit := longTag(update.TagBytesMax + 1)

	tests := []struct {
		name       string
		current    string
		latest     string
		fetchErr   error
		wantKind   update.Kind
		wantLatest string
		wantError  error
	}{
		{"dev never checks", "dev", "v9.0.0", nil, update.Unchecked, "", nil},
		{"fetch error", "v1.0.0", "", errFetch, update.Failed, "", errFetch},
		{"garbage latest", "v1.0.0", "garbage", nil, update.Failed, "", nil},
		{"tag at the limit", "v1.0.0", atLimit, nil, update.Available, atLimit, nil},
		{"tag over the limit", "v1.0.0", overLimit, nil, update.Failed, "", nil},
		{"newer", "v1.0.0", "v1.1.0", nil, update.Available, "v1.1.0", nil},
		{"equal", "v1.1.0", "v1.1.0", nil, update.UpToDate, "v1.1.0", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := update.Evaluate(tt.current, tt.latest, tt.fetchErr)
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, tt.wantKind)
			}

			if got.Latest != tt.wantLatest {
				t.Errorf("Latest = %q, want %q", got.Latest, tt.wantLatest)
			}

			if tt.wantError != nil {
				if !errors.Is(got.Err, tt.wantError) {
					t.Errorf("Err = %v, want %v", got.Err, tt.wantError)
				}
			}

			if tt.wantKind == update.Failed {
				if got.Err == nil {
					t.Error("Err = nil, want an error for a failed check")
				}
			}
		})
	}
}

func TestEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want bool
	}{
		{"", true},
		{"on", true},
		{"yes", true},
		{"1", true},
		{"garbage", true},
		{"off", false},
		{" OFF ", false},
		{"false", false},
		{"0", false},
		{"no", false},
		{"none", false},
		{"Disabled", false},
		// The whitespace config's tests wrap its off values in.
		{" off\n", false},
		{" 0 ", false},
		{"\tdisabled\n", false},
		{"   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()

			got := update.Enabled(tt.raw)
			if got != tt.want {
				t.Errorf("Enabled(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// The off values are config.ThemeIndexOff's, so one list of words turns off every switch. The
// package keeps its own copy because it imports only the standard library.
func TestEnabledOffValuesAreConfigs(t *testing.T) {
	t.Parallel()

	for _, word := range config.ThemeIndexOff() {
		if update.Enabled(word) {
			t.Errorf("Enabled(%q) = true, want false: config turns the gallery off by it", word)
		}
	}

	// The reverse: every word this package turns the check off by is config's.
	for _, word := range update.OffValues() {
		if !slices.Contains(config.ThemeIndexOff(), word) {
			t.Errorf("%q turns the check off here and is not one of config's off values", word)
		}
	}
}
