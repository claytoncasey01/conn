package openwork

import (
	"strings"

	"github.com/claytoncasey01/conn/internal/jira"
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

	return work.pollPR(work.held(), record)
}

// DryStatus is the dry run's next PR status for a key.
func DryStatus(work *Work, key string) prstatus.Status {
	work.mu.Lock()
	defer work.mu.Unlock()

	return work.dryStatus(&Record{Key: key})
}

// FixDry presses the dry run's FIX CHECKS for a key.
func FixDry(work *Work, key string) {
	work.mu.Lock()
	defer work.mu.Unlock()

	work.dryFixed[key] = true
	work.dryPolls[key] = 0
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
	copied := *record
	copied.Branch = branch

	return work.isProtected(&copied)
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
