package guard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// compoundConditions is every `file:line: &&` (or ||) in the Go source. Conditions are one fact
// each: split a compound one into nested ifs or a switch, so each branch is visible on its own.
func compoundConditions(name string, source []byte) ([]string, error) {
	files := token.NewFileSet()

	parsed, err := parser.ParseFile(files, name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", name, err)
	}

	var found []string

	ast.Inspect(parsed, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}

		if binary.Op == token.LAND {
			found = append(found, fmt.Sprintf("%s:%d: &&", name, files.Position(binary.OpPos).Line))
		}

		if binary.Op == token.LOR {
			found = append(found, fmt.Sprintf("%s:%d: ||", name, files.Position(binary.OpPos).Line))
		}

		return true
	})

	return found, nil
}

func TestNoCompoundConditions(t *testing.T) {
	t.Parallel()

	dir := root(t)

	var found []string

	for _, name := range tracked(t, dir) {
		if filepath.Ext(name) != ".go" {
			continue
		}

		text := fileText(dir, name)
		if text == "" {
			continue
		}

		conditions, err := compoundConditions(name, []byte(text))
		if err != nil {
			t.Fatal(err)
		}

		found = append(found, conditions...)
	}

	if len(found) > 0 {
		t.Fatalf("compound conditions (split each into nested ifs or a switch):\n%s",
			strings.Join(found, "\n"))
	}
}

func TestCompoundConditionsAreFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   int
	}{
		{"and", "package p\nfunc f(a, b bool) bool { return a && b }\n", 1},
		{"or in an if", "package p\nfunc f(a, b bool) { if a || b {} }\n", 1},
		{"nested ifs", "package p\nfunc f(a, b bool) { if a { if b {} } }\n", 0},
		{"in a string", "package p\nconst s = \"a && b || c\"\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, err := compoundConditions("p.go", []byte(tt.source))
			if err != nil {
				t.Fatal(err)
			}

			if len(found) != tt.want {
				t.Fatalf("found %q", found)
			}
		})
	}
}
