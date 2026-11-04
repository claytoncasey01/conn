package provided_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/claytoncasey01/conn/internal/provided"
)

func TestReadCatalog(t *testing.T) {
	t.Parallel()

	agents := fstest.MapFS{
		"search.md": {Data: []byte("search")},
		"notes.txt": {Data: []byte("not an agent")},
	}
	skills := fstest.MapFS{
		"README.md":                  {Data: []byte("not a skill")},
		"deep-review/SKILL.md":       {Data: []byte("skill")},
		"deep-review/refs/one.md":    {Data: []byte("ref")},
		"no-skill-file/other.md":     {Data: []byte("a folder without SKILL.md")},
		"another-one/SKILL.md":       {Data: []byte("another")},
		"another-one/scripts/run.sh": {Data: []byte("#!/bin/sh\n")},
	}

	items, err := provided.ReadCatalog(agents, skills)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, string(item.Kind)+"/"+item.Name)
	}

	want := "agents/search skills/another-one skills/deep-review"
	if strings.Join(got, " ") != want {
		t.Errorf("items = %v, want %s", got, want)
	}

	skill, _ := provided.Find(items, provided.KindSkill, "deep-review")
	if len(skill.Files) != 2 {
		t.Errorf("deep-review has %d files, want 2", len(skill.Files))
	}
}

func TestReadCatalogRefusesWhatIsNotUsable(t *testing.T) {
	t.Parallel()

	tooMany := fstest.MapFS{"big/SKILL.md": {Data: []byte("x")}}
	for index := range 70 {
		tooMany["big/f"+strings.Repeat("a", index+1)+".md"] = &fstest.MapFile{Data: []byte("x")}
	}

	tests := []struct {
		name           string
		agents, skills fstest.MapFS
	}{
		{"an agent with a bad name", fstest.MapFS{"Bad_Name.md": {Data: []byte("x")}}, nil},
		{"a skill with a bad name", nil, fstest.MapFS{"Bad Name/SKILL.md": {Data: []byte("x")}}},
		{
			"a file too large", fstest.MapFS{"big.md": {Data: make([]byte, 300<<10)}}, nil,
		},
		{"a skill with too many files", nil, tooMany},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := provided.ReadCatalog(tt.agents, tt.skills)
			if err == nil {
				t.Fatal("ReadCatalog succeeded, want an error")
			}
		})
	}
}
