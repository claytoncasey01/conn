package pty

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every entry of an environment table is listed in its function's doc comment, and every name
// that doc comment lists is in the table: the documentation cannot drift.
func TestEnvironmentTablesMatchTheirDocumentation(t *testing.T) {
	t.Parallel()

	docs := functionDocs(t, "env.go")
	listed := regexp.MustCompile(`_*[A-Z][A-Z0-9_]*[A-Z0-9_](=[a-z0-9-]+)?`)

	tables := map[string][]string{
		"replacedVariables":  replacedVariables(),
		"terminalNames":      terminalNames(),
		"multiplexerNames":   multiplexerNames(),
		"parentSessionNames": parentSessionNames(),
		"parentPolicyNames":  parentPolicyNames(),
		"parentTokenNames":   parentTokenNames(),
		"deniedPrefixes":     deniedPrefixes(),
		"keptNames":          keptNames(),
	}

	for name, table := range tables {
		doc, found := docs[name]
		if !found {
			t.Fatalf("%s has no doc comment", name)
		}

		_, list, _ := strings.Cut(doc, "Lists:")
		if name == "deniedPrefixes" {
			list = doc
		}

		for _, entry := range table {
			if !slices.Contains(listed.FindAllString(list, -1), entry) {
				t.Errorf("%s holds %s, which its doc comment does not list", name, entry)
			}
		}

		if name == "deniedPrefixes" {
			continue
		}

		for _, entry := range listed.FindAllString(list, -1) {
			if !slices.Contains(table, entry) {
				t.Errorf("%s's doc comment lists %s, which the table does not hold", name, entry)
			}
		}
	}
}

// functionDocs is the doc comment of each function in file, by name.
func functionDocs(t *testing.T, file string) map[string]string {
	t.Helper()

	parsed, err := goparser.ParseFile(token.NewFileSet(), file, nil, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	docs := map[string]string{}

	for _, declaration := range parsed.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction {
			continue
		}

		if function.Doc == nil {
			continue
		}

		docs[function.Name.Name] = function.Doc.Text()
	}

	return docs
}
