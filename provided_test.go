package conn_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/provided"
)

func TestShippedSkills(t *testing.T) {
	t.Parallel()

	items, err := provided.ReadCatalog(conn.Agents(), conn.Skills())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		skill string
		files []string
	}{
		{"deep-code-review", "deep-code-review", []string{"SKILL.md", "scripts/post_review.py"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item, found := provided.Find(items, provided.KindSkill, tt.skill)
			if !found {
				t.Fatalf("the binary does not ship the %s skill", tt.skill)
			}

			paths := make([]string, 0, len(item.Files))
			for _, file := range item.Files {
				paths = append(paths, file.Path)
			}

			for _, want := range tt.files {
				if !slices.Contains(paths, want) {
					t.Errorf("%s: no %s in %v", tt.skill, want, paths)
				}
			}

			for _, file := range item.Files {
				if strings.Contains(string(file.Data), "references/") {
					t.Errorf("%s: %s points at a references/ file it does not ship", tt.skill,
						file.Path)
				}
			}
		})
	}
}
