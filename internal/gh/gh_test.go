package gh_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/run"
)

func TestMessage(t *testing.T) {
	t.Parallel()

	failed := &run.Error{Message: "Command failed: gh api user", Code: 1, Spawn: "", Killed: false}

	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name: "the first line gh printed", stderr: "\n gh: Not Found (HTTP 404)\nmore\n",
			want: "gh: Not Found (HTTP 404)",
		},
		{
			name: "nothing printed is how the run ended", stderr: " \n",
			want: "Command failed: gh api user",
		},
		{
			name: "a long line is cut", stderr: strings.Repeat("x", 500),
			want: strings.Repeat("x", 400),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := gh.Message(run.Result{Stderr: tt.stderr, Err: failed})
			if got != tt.want {
				t.Errorf("Message() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorUnwrapsToTheRun(t *testing.T) {
	t.Parallel()

	failed := &run.Error{Message: "Command failed: gh", Code: 1, Spawn: "", Killed: false}

	var err error = &gh.Error{Message: "gh: no", Err: failed}

	got, isRun := errors.AsType[*run.Error](err)
	if !isRun {
		t.Fatal("errors.AsType[*run.Error] found nothing")
	}

	if got.Code != 1 {
		t.Errorf("Code = %d, want 1", got.Code)
	}
}
