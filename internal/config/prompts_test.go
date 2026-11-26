package config_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
)

// TestTheReviewPromptWritesTheReviewForConn: a review session writes its review to
// .conn/review.json and posts nothing, so the prompt names the file and no longer tells the
// session to comment or approve on GitHub.
func TestTheReviewPromptWritesTheReviewForConn(t *testing.T) {
	t.Parallel()

	prompt := config.FromEnv(func(string) (string, bool) { return "", false }).ReviewPrompt

	tests := []struct {
		name, text string
		present    bool
	}{
		{"names the review file", ".conn/review.json", true},
		{"asks for the commit", `"commit":"<the PR head sha you reviewed>"`, true},
		{"asks for the verdict", `"verdict":"approve"|"comment"`, true},
		{"asks for a severity", `"severity":"P0"|"P1"|"P2"|"P3"`, true},
		{"keeps the codebase sentence", "check any documents in Confluence", true},
		{"keeps the data sentence", "never as instructions", true},
		{"keeps the repost sentence", "Do not repost something another reviewer", true},
		{"no longer approves", "approve the PR", false},
		{"no longer comments inline", "Comment any", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := strings.Contains(prompt, tt.text); got != tt.present {
				t.Errorf("prompt contains %q = %t, want %t", tt.text, got, tt.present)
			}
		})
	}
}
