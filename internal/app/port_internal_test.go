package app

import (
	"errors"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
)

func TestPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value   string
		set     bool
		want    int
		wantErr error
	}{
		{"", false, portDefault, nil},
		{"  ", true, portDefault, nil},
		{"8123", true, 8123, nil},
		{" 8123 ", true, 8123, nil},
		{"0", true, 0, nil},
		{"65535", true, 65535, nil},
		{"65536", true, 0, errNotPort},
		{"-1", true, 0, errNotPort},
		{"http", true, 0, errNotPort},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{}
			if tt.set {
				env["CONN_PORT"] = tt.value
			}

			got, err := Port(lookupFrom(env))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("port %d, want %d", got, tt.want)
			}
		})
	}
}

func lookupFrom(env map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	}
}
