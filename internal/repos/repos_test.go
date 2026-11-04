package repos_test

import (
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/answers"
	"github.com/claytoncasey01/conn/internal/repos"
)

// Lookalikes: a Cyrillic e and a, and the Kelvin sign, which lowercases to a plain k.
const (
	cyrillicE  = "\u0435"
	cyrillicA  = "\u0430"
	kelvinSign = "\u212a"
)

func TestIsNameTakesOwnerRepoOfGitHubNameCharactersOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{"acme/widgets", true},
		{"Acme/Widgets", true},
		{"a/b", true},
		{"acme-labs/my.repo_2", true},
		{"acme/.github", true},
		{"a/b...c", true},
		{"", false},
		{"acme", false},
		{"acme/", false},
		{"/widgets", false},
		{"a/b/c", false},
		{"../x", false},
		{"acme/..", false},
		{"./x", false},
		{"acme/.", false},
		{".../x", false},
		{"acme/...", false},
		{"acme/wid gets", false},
		{"acme/wid?gets", false},
		{"acme/wid#gets", false},
		{"acme/wid\ngets", false},
		{"acme/widgets\n", false},
		{"acme/*", false},
		{"acme/wid" + cyrillicE + "gets", false},
		{"-a/b", false},
		{"--help/x", false},
		{"acme/-x", false},
		{"acme/--force", false},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.name), func(t *testing.T) {
			t.Parallel()

			if got := repos.IsName(tt.name); got != tt.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestParsePatternAcceptsOwnerRepoAndOwnerStar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw, want string
	}{
		{"acme/widgets", "acme/widgets"},
		{"ACME/Widgets", "acme/widgets"},
		{"  acme/widgets \t", "acme/widgets"},
		{"acme/*", "acme/*"},
		{" ACME/* ", "acme/*"},
		{"acme-labs/my.repo_2", "acme-labs/my.repo_2"},
		{"a-b/c-d", "a-b/c-d"},
		{"acme/.github", "acme/.github"},
		{"acme/_x", "acme/_x"},
		{"acme/.x", "acme/.x"},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.raw), func(t *testing.T) {
			t.Parallel()

			parsed := repos.ParsePattern(tt.raw)
			if !parsed.OK {
				t.Fatalf("refused: %s", parsed.Reason)
			}

			if parsed.Pattern != tt.want {
				t.Fatalf("got %q", parsed.Pattern)
			}
		})
	}
}

func TestParsePatternRefusesAllElse(t *testing.T) {
	t.Parallel()

	tests := []any{
		"*", "*/*", "*/widgets", "acme/re*", "acme/*x", "acme/**", "a/b/c", "../x", "acme/..",
		"acme/.", "", "   ", "acme", "acme/", "/widgets", "acme/wid gets", "ac me/widgets",
		"acme/wid?gets", "acme/wid#gets", "acme/wid\ngets", "https://github.com/acme/widgets",
		"acme/widgets/", ".../*", "-a/b", "--help/x", "acme/-x", "acme/--force", "-a/*", "--x/*",
		// A lookalike letter is not a name character, and the Kelvin sign must not slip through
		// by lowercasing to a plain k.
		"ac" + kelvinSign + "e/widgets", "acme/wid" + cyrillicE + "gets", cyrillicA + "cme/*",
		nil, 5.0, true,
		[]any{"acme/widgets"},
	}
	for index, raw := range tests {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()

			parsed := repos.ParsePattern(raw)
			if parsed.OK {
				t.Fatalf("%q should be refused", raw)
			}

			if parsed.Reason == "" {
				t.Fatal("a refusal says why")
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		repo string
		list []string
		want bool
	}{
		{"acme/widgets", []string{"acme/widgets"}, true},
		{"Acme/Widgets", []string{"acme/widgets"}, true},
		{"acme/widgets", []string{"ACME/WIDGETS"}, true},
		{"acme/widgets", []string{"acme/*"}, true},
		{"ACME/anything.else", []string{"acme/*"}, true},
		{"acme-labs/widgets", []string{"acme/*"}, false},
		{"acme2/widgets", []string{"acme/*"}, false},
		{"acm/widgets", []string{"acme/*"}, false},
		{"acme/widgets", []string{"acme-labs/*"}, false},
		{"xacme/widgets", []string{"acme/*"}, false},
		{"jane/widgets", []string{"acme/*", "jane/widgets"}, true},
		{"jane/other", []string{"acme/*", "jane/widgets"}, false},
		{"acme/x", []string{"acme/*", "jane/widgets"}, true},
		{"jane/widgets2", []string{"acme/*", "jane/widgets"}, false},
		{"jane/widget", []string{"acme/*", "jane/widgets"}, false},
		{"acme/my.repo", []string{"acme/my.repo"}, true},
		{"acme/myXrepo", []string{"acme/my.repo"}, false},
		{"acme/my-repo", []string{"acme/my-repo"}, true},
		{"acme/.github", []string{"acme/*"}, true},
		{"acme/widgets", nil, false},
		{"acme/widgets", []string{}, false},
		{"-a/b", []string{"acme/*", "-a/b", "acme/-x"}, false},
		{"acme/-x", []string{"acme/*", "-a/b", "acme/-x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			t.Parallel()

			if got := repos.Allowed(tt.repo, tt.list); got != tt.want {
				t.Fatalf("in %q: got %v", tt.list, got)
			}
		})
	}
}

func TestAMalformedRepoIsNeverAllowed(t *testing.T) {
	t.Parallel()

	allow := []string{"acme/*", "acme/widgets", "ack/*"}

	tests := []string{
		"", "acme", "acme/", "/widgets", "a/b/c", "../x", "acme/..", "acme/*", "acme/wid gets",
		"acme/widgets?x=1", "acme/widgets#1", "acme/widgets\n", "acme/wid" + cyrillicE + "gets",
		"ac" + kelvinSign + "e/widgets", " acme/widgets",
	}
	for _, repo := range tests {
		t.Run(strconv.Quote(repo), func(t *testing.T) {
			t.Parallel()

			if repos.Allowed(repo, allow) {
				t.Fatal("allowed")
			}
		})
	}
}

func TestFromRemoteReadsGitHubRemotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url, want string
	}{
		{"https://github.com/Acme/Widgets", "acme/widgets"},
		{"https://github.com/acme/widgets.git", "acme/widgets"},
		{"https://github.com/acme/widgets/", "acme/widgets"},
		{"https://github.com/acme/widgets.git/", "acme/widgets"},
		{"https://x-access-token:tok@github.com/acme/widgets.git", "acme/widgets"},
		{"git@github.com:acme/widgets.git", "acme/widgets"},
		{"git@github.com:Acme/widgets", "acme/widgets"},
		{"ssh://git@github.com/acme/widgets", "acme/widgets"},
		{"ssh://git@github.com/acme/widgets.git", "acme/widgets"},
		{"  https://github.com/acme/my.repo_2.git  ", "acme/my.repo_2"},
		{"https://gitlab.com/acme/widgets.git", ""},
		{"git@gitlab.com:acme/widgets.git", ""},
		{"https://github.com.evil.example/acme/widgets", ""},
		{"https://evilgithub.com/acme/widgets", ""},
		{"git@github.com.evil.example:acme/widgets.git", ""},
		{"https://github.com/acme", ""},
		{"https://github.com/acme/widgets/extra", ""},
		{"https://github.com/acme/widgets?x=1", ""},
		{"https://github.com/acme/widgets#frag", ""},
		{"https://github.com/acme/..", ""},
		{"https://github.com/../widgets", ""},
		{"git@github.com:acme/wid gets.git", ""},
		{"git@github.com:acme/widgets/extra.git", ""},
		{"/Users/jane/widgets", ""},
		{"file:///srv/widgets.git", ""},
		{"../widgets", ""},
		{"", ""},
		{"garbage", ""},
		{"\n", ""},
		{"https://a b@github.com/acme/widgets", ""},
		{"https://github.com/acme/wid\u3000gets", ""},
		{"https://github.com/acme/-x", ""},
		{"git@github.com:-a/b.git", ""},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.url), func(t *testing.T) {
			t.Parallel()

			if got := repos.FromRemote(tt.url); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The answers the page's own checks are held to (testdata/answers/repomatch.json).
func TestRecordedParsePatternAnswers(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "repomatch.json", "parsePattern") {
		answers.Check(t, recorded, repos.ParsePattern(recorded.Args[0]))
	}
}

func TestRecordedAllowedAnswers(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "repomatch.json", "allowed") {
		repo := answers.String(t, recorded, 0)
		answers.Check(t, recorded, repos.Allowed(repo, answers.Strings(t, recorded, 1)))
	}
}
