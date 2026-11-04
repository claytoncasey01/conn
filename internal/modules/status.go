// Package modules is what Conn knows about each module (GitHub, Slack, Atlassian): a state and
// one line saying why, kept in memory. A status is never fetched on its own: Get only reads what
// was recorded. A helper run that used the module records how it went, and the CHECK button runs
// exactly one probe on press, because a connector probe is a metered claude run. The probes are
// given to NewStatuses, so a test never starts anything.
package modules

import (
	"context"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/flight"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/settings"
)

// State is where a module stands.
type State string

// The states a module can be in.
const (
	StateUnchecked State = "unchecked"
	StateOK        State = "ok"
	StateFailing   State = "failing"
	StateOff       State = "off"
)

// The lines shown for a module that is off, and one nobody has checked.
const (
	OffText       = "Off: conn makes no calls"
	UncheckedText = "Not checked yet"
)

const (
	// detailMax is the longest status line, in UTF-16 units.
	detailMax = 200

	ellipsis = "..."
)

// unsafeCharacters are control characters and the bidi marks that can make text read as
// something else.
var unsafeCharacters = regexp.MustCompile(
	`[\x00-\x1f\x7f\x{061c}\x{200e}\x{200f}\x{202a}-\x{202e}\x{2066}-\x{2069}]`)

// Status is one module's state, as the Settings page shows it.
type Status struct {
	State State `json:"state"`
	// Detail is one short line of plain text.
	Detail string `json:"detail"`
	// At is when it was recorded or checked, in milliseconds since 1970; nil for unchecked and
	// off.
	At *int64 `json:"at"`
}

// All is every module's status, in the order the page lists them.
type All struct {
	GitHub    Status `json:"github"`
	Slack     Status `json:"slack"`
	Atlassian Status `json:"atlassian"`
}

// ProbeResult is what one look at a module found.
type ProbeResult struct {
	OK     bool
	Detail string
}

// Probe looks at a module once. An error is a failure with the error's first line.
type Probe func(ctx context.Context) (ProbeResult, error)

// Names are the modules, in the order the page lists them.
func Names() []string {
	return []string{settings.ModuleGitHub, settings.ModuleSlack, settings.ModuleAtlassian}
}

// IsModule reports whether name is a module.
func IsModule(name string) bool { return slices.Contains(Names(), name) }

// CleanDetail is text as one line of plain text: control characters and bidi marks become
// spaces, runs of space become one, and it is cut to unitsMax UTF-16 units with "..." at the end.
func CleanDetail(text string, unitsMax int) string {
	clean := js.Trim(js.CollapseSpace(unsafeCharacters.ReplaceAllLiteralString(text, " ")))
	if js.Len(clean) <= unitsMax {
		return clean
	}

	return js.Slice(clean, unitsMax-len(ellipsis)) + ellipsis
}

// Deps are what Statuses reads.
type Deps struct {
	// Enabled is whether a module's own switch is on. Not settings.Store.ModuleEnabled, which is
	// false for every module in setup: the card for a module that is on says so then too.
	Enabled func(name string) bool
	// Probes has one probe per module.
	Probes map[string]Probe
	Now    func() time.Time
}

// Statuses holds every module's status.
type Statuses struct {
	mu      sync.Mutex
	known   map[string]Status
	running flight.Group[string, Status]
	deps    Deps
}

// NewStatuses holds nothing yet: every module that is on is unchecked. deps must name a probe
// for every module.
func NewStatuses(deps Deps) *Statuses {
	for _, name := range Names() {
		if deps.Probes[name] == nil {
			panic("modules.NewStatuses: no probe for " + name)
		}
	}

	return &Statuses{
		mu:      sync.Mutex{},
		known:   map[string]Status{},
		running: flight.Group[string, Status]{},
		deps:    deps,
	}
}

// Get is a module's status; off when its switch is.
func (s *Statuses) Get(name string) Status {
	if !s.deps.Enabled(name) {
		return Status{State: StateOff, Detail: OffText, At: nil}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	status, found := s.known[name]
	if !found {
		return Status{State: StateUnchecked, Detail: UncheckedText, At: nil}
	}

	return status
}

// All is every module's status, as GET /api/settings sends it. It runs nothing.
func (s *Statuses) All() All {
	return All{
		GitHub:    s.Get(settings.ModuleGitHub),
		Slack:     s.Get(settings.ModuleSlack),
		Atlassian: s.Get(settings.ModuleAtlassian),
	}
}

// Record keeps how a run that used the module went: the last one decides.
func (s *Statuses) Record(name string, worked bool, detail string) {
	if !IsModule(name) {
		return
	}

	line := CleanDetail(detail, detailMax)
	if line == "" {
		line = "Failed"
		if worked {
			line = "Worked"
		}
	}

	state := StateFailing
	if worked {
		state = StateOK
	}

	at := s.deps.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.known[name] = Status{State: state, Detail: line, At: &at}
}

// Check runs the module's one probe, now, records the answer and returns the module's status.
// A module whose switch is off is not called. Two presses at once (a double click, two tabs)
// share one probe, and the next press runs again. Only a press reaches this, never a timer.
//
// The probe outlives a press whose page went away, so its answer is still recorded: it runs
// without ctx's cancellation, bounded by its own time limit.
func (s *Statuses) Check(ctx context.Context, name string) Status {
	if !IsModule(name) {
		return Status{State: StateOff, Detail: OffText, At: nil}
	}

	if !s.deps.Enabled(name) {
		return Status{State: StateOff, Detail: OffText, At: nil}
	}

	status, answered := s.running.Do(ctx, name, func(ctx context.Context) Status {
		return s.probe(ctx, name)
	})
	if !answered {
		return s.Get(name)
	}

	return status
}

// probe runs the module's probe, records what it found and returns the module's status.
func (s *Statuses) probe(ctx context.Context, name string) Status {
	result, err := s.deps.Probes[name](ctx)
	if err != nil {
		result = ProbeResult{OK: false, Detail: run.FirstLine(err.Error())}
	}

	s.Record(name, result.OK, result.Detail)

	// Read after recording: a switch turned off while the probe ran makes the module off.
	return s.Get(name)
}
