package sessions_test

import (
	"regexp"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/sessions"
)

func rules(t *testing.T) sessions.KindRules {
	t.Helper()

	cfg := config.FromEnv(func(string) (string, bool) { return "", false })

	return sessions.KindRules{
		ReviewName: cfg.ReviewNamePattern, SelfName: cfg.SelfNamePattern,
		ReviewsRoot: "/data/conn/reviews",
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()

	keys := map[string]bool{"fix-login": true, "add-search": true}
	inReviews := "/data/conn/reviews/pr-12-acme--widgets/widgets"
	elsewhere := "/Users/jane/projects/widgets"
	sibling := "/data/conn/reviews-other/pr-12"
	root := "/data/conn/reviews"
	tests := []struct {
		name string
		cwd  *string
		keys map[string]bool
		want string
	}{
		{"fix-login", nil, keys, sessions.KindBuild},
		{"scratch", nil, keys, sessions.KindAdhoc},
		{"fix-login", nil, map[string]bool{}, sessions.KindAdhoc},
		{"3f9a1c2e", nil, keys, sessions.KindAdhoc},
		{"pr-1234-acme--widgets", nil, keys, sessions.KindReview},
		{"pr-1234.2-acme--widgets", nil, keys, sessions.KindReview},
		{"pr-1234", nil, keys, sessions.KindReview},
		{"pr-1234-2", nil, keys, sessions.KindReview},
		{"pr-12-acme--widgets", &inReviews, keys, sessions.KindReview},
		{"pr-12-acme--widgets", &elsewhere, keys, sessions.KindAdhoc},
		{"pr-12", &sibling, keys, sessions.KindAdhoc},
		{"pr-12", &root, keys, sessions.KindAdhoc},
		{"pr-12ab", nil, keys, sessions.KindAdhoc},
		{"pr-", nil, keys, sessions.KindAdhoc},
		{"my-pr-1", nil, keys, sessions.KindAdhoc},
		{"pr-acme--widgets", nil, keys, sessions.KindAdhoc},
		{"pr-1-Acme--widgets", nil, keys, sessions.KindAdhoc},
		{"pr-1-a b", nil, keys, sessions.KindAdhoc},
		{"pr-1234", nil, map[string]bool{"pr-1234": true}, sessions.KindBuild},
		{"self-web-12", nil, keys, sessions.KindSelfReview},
		{"self-widgets.core-12-3", nil, keys, sessions.KindSelfReview},
		{"self-web", nil, keys, sessions.KindAdhoc},
		{"self-Web-12", nil, keys, sessions.KindAdhoc},
		{"myself-web-12", nil, keys, sessions.KindAdhoc},
		{"self-web-12", nil, map[string]bool{"self-web-12": true}, sessions.KindBuild},
	}

	for _, tt := range tests {
		got := sessions.KindOf(tt.name, tt.cwd, tt.keys, rules(t))
		if got != tt.want {
			t.Errorf("KindOf(%q) = %s, want %s", tt.name, got, tt.want)
		}
	}

	custom := rules(t)
	custom.ReviewName = regexp.MustCompile(`^rev-\d+$`)

	if sessions.KindOf("rev-9", nil, keys, custom) != sessions.KindReview {
		t.Error("a passed-in review pattern")
	}

	if sessions.KindOf("pr-9", nil, keys, custom) != sessions.KindAdhoc {
		t.Error("the default pattern still applied")
	}
}

func TestStartedByConn(t *testing.T) {
	t.Parallel()

	data := "/data/conn"
	run := "/anywhere/widgets-proj-1"
	tests := []struct {
		kind, cwd, run string
		want           bool
	}{
		{sessions.KindBuild, "/anywhere/widgets-proj-1", run, true},
		{sessions.KindBuild, "/anywhere/widgets-proj-1/apps/api", run, true},
		{sessions.KindBuild, "/Users/jane/work/widgets", run, false},
		{sessions.KindBuild, "/anywhere/widgets-proj-1-other", run, false},
		{sessions.KindBuild, "/anywhere/widgets-proj-1", "", false},
		{sessions.KindReview, "/data/conn/reviews/pr-5/widgets", "", true},
		{sessions.KindSelfReview, "/data/conn/myprs/acme-widgets-ab12cd34-7/widgets", "", true},
		{sessions.KindReview, "/Users/jane/work/widgets", "", false},
		{sessions.KindReview, "/data/conn", "", false},
		{sessions.KindReview, "/data/conn-other/pr-5", "", false},
		{sessions.KindReview, "/data/conn/../elsewhere", "", false},
		{sessions.KindAdhoc, "/data/conn/reviews/pr-5/widgets", "", false},
	}

	for _, tt := range tests {
		got := sessions.StartedByConn(tt.kind, tt.cwd, data, tt.run)
		if got != tt.want {
			t.Errorf("StartedByConn(%s, %s) = %v, want %v", tt.kind, tt.cwd, got, tt.want)
		}
	}
}
