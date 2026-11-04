package ghbudget_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
)

// The pure parts (parseRate, addRate, pollInterval and the budget's op sequences) replay the
// recorded answers in internal/reviews/answers_test.go; these are the rest of the budget's tests.

const budgetT0 = 1_800_000_000_000

// TestRateLimitedIsToldApartAndKeepsItsReset: the error is found by errors.As through a wrap, is
// an error, and keeps the reset it was given, nil included.
func TestRateLimitedIsToldApartAndKeepsItsReset(t *testing.T) {
	t.Parallel()

	reset := float64(budgetT0)
	tests := []struct {
		name    string
		resetAt *float64
	}{
		{"with a reset", &reset},
		{"with none", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wrapped := fmt.Errorf("polling: %w", &ghbudget.RateLimitError{ResetAt: tt.resetAt})

			var limit *ghbudget.RateLimitError
			if !errors.As(wrapped, &limit) {
				t.Fatalf("errors.As did not find RateLimitError in %v", wrapped)
			}

			if limit.ResetAt != tt.resetAt {
				t.Errorf("ResetAt = %v, want %v", limit.ResetAt, tt.resetAt)
			}

			if limit.Error() != "GitHub rate limit used up" {
				t.Errorf("Error() = %q", limit.Error())
			}
		})
	}
}
