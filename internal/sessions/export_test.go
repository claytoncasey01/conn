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

// ProcArgsEnvironment is procArgsEnvironment.
func ProcArgsEnvironment(raw []byte) ([]string, bool) { return procArgsEnvironment(raw) }

// CmuxIDOf is cmuxIDOf.
func CmuxIDOf(environment []string, name string) (string, bool) {
	return cmuxIDOf(environment, name)
}

// CloseFailure is closeFailure.
func CloseFailure(err error) Reply { return closeFailure(err) }
