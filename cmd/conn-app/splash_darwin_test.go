package main

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/desk"
)

// tracedExport is what a shell traced with set -x prints for an export: it must never be shown.
const tracedExport = "+ export PROFILE_VALUE=do-not-show-this"

func TestSplashPage(t *testing.T) {
	t.Parallel()

	// maxPageBytes bounds the page whatever the reason: the template and a capped notice.
	maxPageBytes := len(splashPage(splashFile, desk.SplashText(""))) + 1024

	tests := []struct {
		name       string
		reason     string
		wantHas    string
		wantHasNot string
	}{
		{
			name:       "the text is escaped",
			reason:     `<script>alert("x")</script>`,
			wantHas:    "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;",
			wantHasNot: "<script>",
		},
		{
			name:       "what follows the first line of a reason is not shown",
			reason:     "Command failed: /bin/zsh -l -i -c\n" + tracedExport + "\n",
			wantHas:    "Command failed: /bin/zsh -l -i -c",
			wantHasNot: "do-not-show-this",
		},
		{
			name:       "a long reason is cut",
			reason:     "timed out " + strings.Repeat("x", 100_000),
			wantHas:    "timed out x",
			wantHasNot: strings.Repeat("x", desk.MaxReasonRunes+1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			page := splashPage(splashFile, desk.SplashText(tt.reason))

			if !strings.Contains(page, tt.wantHas) {
				t.Errorf("the splash page does not hold %q", tt.wantHas)
			}

			if strings.Contains(page, tt.wantHasNot) {
				t.Errorf("the splash page holds %q", tt.wantHasNot)
			}

			if len(page) > maxPageBytes {
				t.Errorf("the splash page is %d bytes, want at most %d", len(page), maxPageBytes)
			}
		})
	}
}
