package reviews

import "math"

// The decisions about a finished review session.
const (
	DecisionClose = "close"
	DecisionWait  = "wait"
)

// CloseFacts are what the close decision is made on.
type CloseFacts struct {
	// IdleForMs is how long the session has sat idle.
	IdleForMs float64 `json:"idleForMs"`
	// InputEmpty is that its input box is visibly empty (a draft, or a screen that cannot be
	// read, is not).
	InputEmpty          bool    `json:"inputEmpty"`
	ReviewedSinceLaunch bool    `json:"reviewedSinceLaunch"`
	AfterMs             float64 `json:"afterMs"`
	CapMs               float64 `json:"capMs"`
}

// CloseDecision is whether a review session that Conn launched should close itself now. A
// session closes only once the review has landed and it has been idle AfterMs, or, with no
// review seen, once it has been idle CapMs; anything a person might still be typing into is left
// alone.
func CloseDecision(facts CloseFacts) string {
	if !facts.InputEmpty {
		return DecisionWait
	}

	if math.IsNaN(facts.IdleForMs) {
		return DecisionWait
	}

	if math.IsInf(facts.IdleForMs, 0) {
		return DecisionWait
	}

	if facts.IdleForMs < 0 {
		return DecisionWait
	}

	if facts.ReviewedSinceLaunch {
		if facts.IdleForMs >= facts.AfterMs {
			return DecisionClose
		}
	}

	if facts.IdleForMs >= facts.CapMs {
		return DecisionClose
	}

	return DecisionWait
}
