package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
)

func TestLinksAtStart(t *testing.T) {
	t.Parallel()

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	realDir := filepath.Join(home, ".config", "conn")
	link := filepath.Join(home, "link-to-real")

	err = os.MkdirAll(realDir, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(realDir, link)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data string
		dry  bool
		want bool
	}{
		{"the real data folder", realDir, false, true},
		{"a link to the real data folder", link, false, true},
		{"the dev server's folder", filepath.Join(home, ".config", "conn-dev"), false, false},
		{"a scratch folder", t.TempDir(), false, false},
		{"the real folder in a dry run", realDir, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Config{DataDir: tt.data, DefaultDataDir: realDir}
			cfg.Dry.Work = tt.dry

			got := linksAtStart(cfg)
			if got != tt.want {
				t.Errorf("linksAtStart = %v, want %v", got, tt.want)
			}
		})
	}
}
