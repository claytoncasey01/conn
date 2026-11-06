package sessions

import "context"

// KeptRows are the ids of the hosted sessions whose last row the session list keeps.
func KeptRows(ctx context.Context, loop *Loop) []string {
	var kept []string

	_ = loop.do(ctx, func(state *store) {
		for sessionID := range state.hosts.lastRows {
			kept = append(kept, string(sessionID))
		}
	})

	return kept
}

// EnvironmentPart is environmentPart.
func EnvironmentPart(withEnvironment, command string) string {
	return environmentPart(withEnvironment, command)
}
