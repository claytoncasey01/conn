// Package ghbudget is what Conn asks of GitHub's GraphQL budget, in one place. My PRs and Reviews
// both poll GitHub with the one signed-in account, so the hour's 5000 points are theirs together:
// each reads what its own poll cost from the answer (`rateLimit { cost remaining resetAt }`),
// they share the part of the budget Conn spends, and a rate limit that either one runs into
// pauses both. Nothing here calls GitHub.
package ghbudget

import (
	"math"
	"regexp"
	"sync"

	"github.com/claytoncasey01/conn/internal/js"
)

// The budget and the limits on how often a poller asks, in milliseconds.
const (
	// GraphQLHourly is GitHub's GraphQL budget per hour, in points.
	GraphQLHourly = 5000
	// PollCapMs is the longest a poller waits between polls, however much a poll costs.
	PollCapMs = 15 * 60_000
	// RatePauseMs is how long polling waits after a rate limit that said nothing about its
	// reset.
	RatePauseMs = 10 * 60_000
	// activeMs is how long a poller that was answered still counts as spending the budget.
	activeMs = 3_600_000
	// hourMs is an hour.
	hourMs = 3_600_000
	// half is the share of a budget the pollers spend: the other half is left for everything
	// else the account does.
	half = 2
)

// RateLimitText is how gh or GitHub says the rate limit is used up.
var RateLimitText = regexp.MustCompile(`(?i)RATE_LIMITED|rate limit`)

// RateInfo is what one GitHub answer (or several, summed) says about the rate limit: the cost in
// points, what is left of the hour's budget, and when it resets (ms).
type RateInfo struct {
	Cost      float64 `json:"cost"`
	Remaining float64 `json:"remaining"`
	ResetAt   float64 `json:"resetAt"`
}

// RateLimitError is GitHub refusing for the rate limit: nothing more is asked until ResetAt (ms;
// nil when GitHub did not say).
type RateLimitError struct {
	ResetAt *float64
}

// Error is the message a rate limit reads as.
func (e *RateLimitError) Error() string { return "GitHub rate limit used up" }

// ParseRate is `data.rateLimit { cost remaining resetAt }` of an answer's JSON; nil when it
// carries none or it is malformed.
func ParseRate(answer any) *RateInfo {
	data, _ := field(answer, "data").(*js.Object)
	if data == nil {
		return nil
	}

	limit, _ := field(data, "rateLimit").(*js.Object)
	if limit == nil {
		return nil
	}

	cost, costOK := field(limit, "cost").(float64)
	remaining, remainingOK := field(limit, "remaining").(float64)

	reset := math.NaN()
	if text, isText := field(limit, "resetAt").(string); isText {
		reset = js.DateParse(text)
	}

	if !costOK {
		return nil
	}

	if !remainingOK {
		return nil
	}

	if math.IsNaN(reset) {
		return nil
	}

	if !finite(cost) {
		return nil
	}

	if !finite(remaining) {
		return nil
	}

	return &RateInfo{Cost: cost, Remaining: remaining, ResetAt: reset}
}

// AddRate adds up what several answers cost: the lowest remaining budget and the latest reset.
func AddRate(first, second *RateInfo) *RateInfo {
	if first == nil {
		return second
	}

	if second == nil {
		return first
	}

	return &RateInfo{
		Cost:      first.Cost + second.Cost,
		Remaining: math.Min(first.Remaining, second.Remaining),
		ResetAt:   math.Max(first.ResetAt, second.ResetAt),
	}
}

// PollInterval is how long a poller waits between polls (ms), from what its last poll cost as
// GitHub measured it: often enough that an hour of its polls spends at most its share of half
// the hour's budget, and at most its share of half of what is left before the reset; never more
// often than base, never less often than every 15 minutes. parties is how many pollers share
// the budget (each gets 1/parties of that half). With nothing measured yet it is base.
func PollInterval(rate *RateInfo, now, base float64, parties int) float64 {
	if rate == nil {
		return base
	}

	if !(rate.Cost > 0) {
		return base
	}

	count := float64(max(1, parties))
	hourly := float64(rate.Cost*hourMs) / (GraphQLHourly / half / count)
	untilReset := rate.ResetAt - now

	beforeReset := float64(PollCapMs)

	switch {
	case untilReset <= 0:
		beforeReset = 0
	case rate.Remaining > 0:
		beforeReset = float64(rate.Cost*untilReset) / (rate.Remaining / half / count)
	}

	return jsMin(PollCapMs, jsMax(jsMax(base, hourly), beforeReset))
}

// Budget is what the pollers of one Conn share: who has been spending the budget, and a rate
// limit that is in force. Safe for use from several goroutines.
type Budget struct {
	mutex    sync.Mutex
	pausedTo *float64
	answered *js.Map[float64]
}

// New is a budget no poller has spent yet.
func New() *Budget {
	return &Budget{mutex: sync.Mutex{}, pausedTo: nil, answered: js.NewMap[float64]()}
}

// Note says a poll of who was answered by GitHub at now: it counts as a spender for the next
// hour.
func (b *Budget) Note(who string, now float64) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.answered.Set(who, now)
}

// Leave says who has stopped asking GitHub (its module went off): it no longer takes a share.
func (b *Budget) Leave(who string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.answered.Delete(who)
}

// Parties is how many pollers spend the budget: who, and every other one that was answered
// within the hour.
func (b *Budget) Parties(who string, now float64) int {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.parties(who, now)
}

// Every is how long who waits between polls (see PollInterval), its own last cost against the
// budget it shares with the others.
func (b *Budget) Every(who string, rate *RateInfo, now, base float64) float64 {
	b.mutex.Lock()
	parties := b.parties(who, now)
	b.mutex.Unlock()

	return PollInterval(rate, now, base, parties)
}

// Pause says GitHub's rate limit is used up: nothing is asked of it by anyone before until (the
// later of two limits stands).
func (b *Budget) Pause(until float64) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.pausedTo == nil {
		b.pausedTo = &until

		return
	}

	if until > *b.pausedTo {
		b.pausedTo = &until
	}
}

// PausedUntil is when the limit in force ends (ms), or nil when none is.
func (b *Budget) PausedUntil(now float64) *float64 {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.pausedTo == nil {
		return nil
	}

	if *b.pausedTo > now {
		until := *b.pausedTo

		return &until
	}

	return nil
}

// Resume says a call was answered: the limit is not in force.
func (b *Budget) Resume() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.pausedTo = nil
}

func (b *Budget) parties(who string, now float64) int {
	count := 1

	for _, other := range b.answered.Keys() {
		if other == who {
			continue
		}

		at, _ := b.answered.Get(other)
		if now-at < activeMs {
			count++
		}
	}

	return count
}

func field(value any, key string) any {
	object, _ := value.(*js.Object)
	found, _ := object.Get(key)

	return found
}

func finite(value float64) bool {
	if math.IsNaN(value) {
		return false
	}

	return !math.IsInf(value, 0)
}

// jsMax is Math.max of two numbers: NaN when either is.
func jsMax(first, second float64) float64 {
	if math.IsNaN(first) {
		return first
	}

	if math.IsNaN(second) {
		return second
	}

	return math.Max(first, second)
}

// jsMin is Math.min of two numbers: NaN when either is.
func jsMin(first, second float64) float64 {
	if math.IsNaN(first) {
		return first
	}

	if math.IsNaN(second) {
		return second
	}

	return math.Min(first, second)
}
