package myprs_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/myprs"
)

// cGhFake is one fake gh for the list, the login and the ask: what the ask posts is what the
// next list shows, by the same login.
type cGhFake struct {
	mu       sync.Mutex
	world    *World
	comments []map[string]any
	posted   [][]string
}

func cISO(at float64) string {
	return time.UnixMilli(int64(at)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func (g *cGhFake) node() map[string]any {
	comments := append([]map[string]any{}, g.comments...)

	return map[string]any{
		"number": 7, "title": "Add a thing", "url": "https://github.com/org/repo/pull/7",
		"isDraft": false, "createdAt": cISO(worldT0 - 5*hourMs), "updatedAt": cISO(worldT0),
		"headRefName": "feature/a", "headRefOid": "new", "baseRefName": "main",
		"repository": map[string]any{
			"nameWithOwner": cRepo, "defaultBranchRef": map[string]any{"name": "main"},
		},
		"headRepository": map[string]any{"nameWithOwner": cRepo},
		"commits": map[string]any{"nodes": []any{
			map[string]any{"commit": map[string]any{"committedDate": cISO(worldT0 - hourMs)}},
		}},
		"reviewThreads": map[string]any{
			"pageInfo": map[string]any{"hasPreviousPage": false}, "nodes": []any{},
		},
		"reviews":       map[string]any{"nodes": []any{}},
		"comments":      map[string]any{"nodes": comments},
		"firstComments": map[string]any{"nodes": comments[:min(5, len(comments))]},
	}
}

func (g *cGhFake) exec(_ context.Context, args []string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch {
	case slices.Equal(args[:2], []string{"api", "user"}):
		return "jane\n", nil
	case slices.Equal(args[:2], []string{"api", "graphql"}):
		answer := map[string]any{"data": map[string]any{"search": map[string]any{
			"nodes": []any{g.node()},
		}}}

		data, err := json.Marshal(answer)
		if err != nil {
			return "", errWorld
		}

		return string(data), nil
	case slices.Equal(args[:2], []string{"pr", "comment"}):
		g.posted = append(g.posted, args)
		g.comments = append(g.comments, map[string]any{
			"id": "IC_a" + strconv.Itoa(len(g.posted)), "author": map[string]any{"login": "jane"},
			"createdAt": cISO(g.world.now()), "lastEditedAt": nil,
			"body": args[slices.Index(args, "--body")+1],
		})

		return "", nil
	default:
		return "", errWorld
	}
}

func TestCOwnGreptileAskIsNotTheLastComment(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "myprs.json")
	cSeed(t, file, cRoundOpts{})

	world := newWorld(t)
	fake := &cGhFake{
		mu: sync.Mutex{}, world: world, posted: [][]string{},
		comments: []map[string]any{{
			"id": "IC_h", "author": map[string]any{"login": "sam"},
			"createdAt": cISO(worldT0 - 2*hourMs), "lastEditedAt": nil, "body": "Looks close",
		}},
	}
	trigger := defaults().GreptileTrigger
	deps := world.Deps(file)
	deps.Config.Me = ""
	deps.Gh = myprs.Gh{Exec: fake.exec, Trigger: trigger}
	deps.Writes = myprs.Writes{Exec: fake.exec, Trigger: trigger}
	myPrs := myprs.New(context.Background(), deps)
	poll := func(advanceMs float64) {
		world.Advance(advanceMs)
		myPrs.Refresh()
		tick(myPrs)
	}

	// The round's session never showed up: it is over, and its push is asked for.
	poll(6 * cMinuteMs)
	fake.mu.Lock()
	cSame(t, "posted", len(fake.posted), 1)

	if len(fake.posted) == 1 {
		args := fake.posted[0]
		cSame(t, "the ask's body", args[slices.Index(args, "--body")+1], trigger)
	}
	fake.mu.Unlock()

	poll(cMinuteMs)
	fake.mu.Lock()
	cSame(t, "comments", len(fake.comments), 2)
	fake.mu.Unlock()

	rows := myPrs.Snapshot().Rows
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}

	cSame(t, "status", rows[0].Status, "waiting")
	cSame(t, "lastCommentAt", rows[0].LastCommentAt, float64(worldT0-2*hourMs))
}
