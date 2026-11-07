package openwork

import (
	"strings"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
)

// Research runs the research stage of a run, as the pipeline does, starting it from starting.
func Research(work *Work, key string) error {
	work.mu.Lock()
	defer work.mu.Unlock()

	record, _ := work.runs.Get(key)
	record.State = stateStarting

	return work.research(work.held(), record)
}

// PollPR reads a run's PR once, as the tick does.
func PollPR(work *Work, key string) error {
	work.mu.Lock()
	defer work.mu.Unlock()

	record, _ := work.runs.Get(key)

	return work.pollPR(work.held(), record, record.lead())
}

// DryStatus is the dry run's next PR status for a key.
func DryStatus(work *Work, key string) prstatus.Status {
	work.mu.Lock()
	defer work.mu.Unlock()

	return work.dryStatus(&Record{Key: key}, &Leg{})
}

// FixDry presses the dry run's FIX CHECKS for a key.
func FixDry(work *Work, key string) {
	work.mu.Lock()
	defer work.mu.Unlock()

	work.dryFixed[legKey(key, "")] = true
	work.dryPolls[legKey(key, "")] = 0
}

// TouchAll saves every run, as any change does.
func TouchAll(work *Work) {
	work.mu.Lock()
	defer work.mu.Unlock()

	for _, key := range work.runs.Keys() {
		record, _ := work.runs.Get(key)
		work.touch(record)
	}
}

// Snapshot are the runs as the page sees them (JSON), whether Open work is on or not.
func Snapshot(work *Work) []byte {
	work.mu.Lock()
	defer work.mu.Unlock()

	records := work.snapshot()
	parts := make([]string, 0, len(records))

	for _, record := range records {
		parts = append(parts, string(record.viewJSON()))
	}

	return []byte("[" + strings.Join(parts, ",") + "]")
}

// Running is whether a run's pipeline is still going.
func Running(work *Work, key string) bool {
	work.mu.Lock()
	defer work.mu.Unlock()

	return work.running[key]
}

// RecordOf is a copy of a run's record.
func RecordOf(work *Work, key string) Record {
	work.mu.Lock()
	defer work.mu.Unlock()

	record, _ := work.runs.Get(key)

	return *record
}

// MoveJira moves a run's ticket, as the pipeline and OPEN PR do.
func MoveJira(work *Work, key string, move jira.Move) {
	work.mu.Lock()
	defer work.mu.Unlock()

	record, _ := work.runs.Get(key)
	work.moveJira(work.held(), record, move)
}

// IsProtected is whether a run's branch, set to branch, is one Conn never pushes.
func IsProtected(work *Work, key, branch string) bool {
	work.mu.Lock()
	defer work.mu.Unlock()

	record, _ := work.runs.Get(key)
	copied := *record.lead()
	copied.Branch = branch

	return work.isProtected(&copied)
}

// RepoOf is the repository a run is in, as the pushes and sessions ask it.
func RepoOf(work *Work, key string) string {
	work.mu.Lock()
	defer work.mu.Unlock()

	record, _ := work.runs.Get(key)

	return work.repoOf(record.lead())
}

// BuildReposMax is the most repositories the list of places to build offers.
const BuildReposMax = buildReposMax

// LegFor is the leg of a record in repo, in any case.
func LegFor(record *Record, repo string) (*Leg, bool) { return record.legFor(repo) }

// DecodeRecord is the record a run's file holds, in either shape, before it is settled.
func DecodeRecord(data []byte) (*Record, bool) {
	parsed, err := js.Parse(data)
	if err != nil {
		return nil, false
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return nil, false
	}

	return decodeRecord(data, object)
}

// LoadedKeys are the keys of the runs in a runs folder, in the order they are loaded.
func LoadedKeys(runsDir string) []string {
	records := loadRecords(runsDir)
	keys := make([]string, 0, len(records))

	for _, record := range records {
		keys = append(keys, record.Key)
	}

	return keys
}

// ReviewDue is whether the ticket moves to review after a PR opens.
func ReviewDue(policy string, opened, withCommits int, moved bool) bool {
	return reviewDue(policy, opened, withCommits, moved)
}

// Merged is whether a run counts as merged.
func Merged(record *Record) bool { return merged(record) }

// RefusedAllOf is the refusal when every leg of record was refused, with none refused.
func RefusedAllOf(record *Record) string { return errorText(refusedAll(record, nil)) }

// LegRules are the per-leg rules the run page mirrors: whether OPEN PR may run, whether the run
// counts as merged, and for each leg whether its checks are failing (FIX FAILING CHECKS), its
// worktree may be removed (REMOVE WORKTREE) and it is kept for another leg's open PR.
func LegRules(record *Record) *js.Object {
	legs := make([]any, 0, len(record.Repos))

	for _, leg := range record.Repos {
		_, quiet := fixQuiet(record.Key, record.State, leg)
		_, settledNot := cleanupQuiet(record.State, leg)
		kept := openElsewhere(record, leg) != nil

		canClean := !settledNot
		if kept {
			canClean = false
		}

		rules := js.NewObject()
		rules.Set("checksFailing", !quiet)
		rules.Set("canClean", canClean)
		rules.Set("keptForOthers", kept)
		legs = append(legs, rules)
	}

	out := js.NewObject()
	out.Set("openable", openable(record))
	out.Set("merged", merged(record))
	out.Set("legs", legs)

	return out
}

// WriteBrief is writeBrief for a slot's own leg.
func WriteBrief(record *Record, slot *Leg) error { return writeBrief(record, slot, slot) }

// CheckPRBody is checkPRBody.
func CheckPRBody(path string, data []byte) error { return checkPRBody(path, data) }

// FallbackBody is fallbackBody.
func FallbackBody(subjects string) string { return fallbackBody(subjects) }

// PRBodyBytesMax is prBodyBytesMax.
const PRBodyBytesMax = prBodyBytesMax
