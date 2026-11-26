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

	var err error = &gh.Error{Message: "gh: no", Status: 0, Overflowed: false, Err: failed}

	got, isRun := errors.AsType[*run.Error](err)
	if !isRun {
		t.Fatal("errors.AsType[*run.Error] found nothing")
	}

	if got.Code != 1 {
		t.Errorf("Code = %d, want 1", got.Code)
	}
}

func TestErrorOfReadsWhatGhSaidOnce(t *testing.T) {
	t.Parallel()

	failed := &run.Error{Message: "Command failed: gh api", Code: 1, Spawn: "", Killed: false}

	tests := []struct {
		name           string
		result         run.Result
		wantStatus     int
		wantClient     bool
		wantOverflowed bool
	}{
		{
			name:       "a client error",
			result:     run.Result{Stderr: "gh: Validation Failed (HTTP 422)\n", Err: failed},
			wantStatus: 422, wantClient: true, wantOverflowed: false,
		},
		{
			name:       "a server error",
			result:     run.Result{Stderr: "gh: Bad Gateway (HTTP 502)\n", Err: failed},
			wantStatus: 502, wantClient: false, wantOverflowed: false,
		},
		{
			name:       "no status named",
			result:     run.Result{Stderr: "connection reset\n", Err: failed},
			wantStatus: 0, wantClient: false, wantOverflowed: false,
		},
		{
			name:       "the output passed its cap",
			result:     run.Result{Stderr: "", Err: failed, Overflowed: "stdout"},
			wantStatus: 0, wantClient: false, wantOverflowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := gh.ErrorOf(tt.result)

			if got.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", got.Status, tt.wantStatus)
			}

			if got.ClientError() != tt.wantClient {
				t.Errorf("ClientError() = %v, want %v", got.ClientError(), tt.wantClient)
			}

			if got.Overflowed != tt.wantOverflowed {
				t.Errorf("Overflowed = %v, want %v", got.Overflowed, tt.wantOverflowed)
			}
		})
	}
}
