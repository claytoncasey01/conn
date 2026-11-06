package settings

import (
	"log"
	"maps"
	"slices"
	"sync"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// UpdateResult is what Update answers.
type UpdateResult struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	SaveFailed bool   `json:"saveFailed,omitempty"`
}

// listener is one OnChange function, with the id that removes it.
type listener struct {
	id     int
	notify func(Settings)
}

// Store holds the settings every reader asks. The server calls Reload at boot; tests call
// Reload(tempDir).
type Store struct {
	mu            sync.Mutex
	current       Settings
	dropped       []string
	backupPending bool
	backedUpTo    string
	dir           string
	loaded        bool
	listeners     []listener
	nextID        int
	logger        *log.Logger
	// version counts the times current was replaced (a saved update, a reload), so a reader can
	// tell whether the settings it read are still the ones in force.
	version uint64
}

// NewStore is a store with the defaults and no folder: nothing is written until Reload names
// one. A listener that panics is reported to logger.
func NewStore(logger *log.Logger) *Store {
	return &Store{
		mu:            sync.Mutex{},
		current:       Defaults(),
		dropped:       []string{},
		backupPending: false,
		backedUpTo:    "",
		dir:           "",
		loaded:        false,
		listeners:     nil,
		nextID:        0,
		logger:        logger,
		version:       0,
	}
}

// Get is the current settings, a copy a reader may keep: editing it cannot widen what the next
// reader sees.
func (s *Store) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.current.Clone()
}

// Current is the current settings, as Get, with their version: the version moves each time a
// saved update or a reload replaces them, so a reader that later sees another version knows the
// settings it read are no longer the ones in force.
func (s *Store) Current() (Settings, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.current.Clone(), s.version
}

// Setup is true while there is no allowlist (or an empty one): nothing is read until one is
// saved.
func (s *Store) Setup() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.current.Repos.Allow) == 0
}

// Allow is the allowlist, nil in setup.
func (s *Store) Allow() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.current.Repos.Allow)
}

// Dropped is what the last load could not read from the file (nothing once a save has rewritten
// it).
func (s *Store) Dropped() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.dropped)
}

// BackedUp is where the first save kept the unreadable file it replaced (settings.json.bak), or
// "" when none was needed or one was already there.
func (s *Store) BackedUp() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.backedUpTo
}

// ModuleEnabled reports whether a module may be used: false for every module while Setup, else
// its own switch.
func (s *Store) ModuleEnabled(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.current.Repos.Allow) == 0 {
		return false
	}

	return s.current.Switch(name)
}

// Update replaces one section, whole: validated, saved atomically, then every listener told. A
// refused or failed update changes nothing.
func (s *Store) Update(name string, body any) UpdateResult {
	s.mu.Lock()

	next, refusal := s.next(name, body)
	if refusal != "" {
		s.mu.Unlock()

		return UpdateResult{OK: false, Error: refusal, SaveFailed: false}
	}

	err := s.persist(next)
	if err != nil {
		s.mu.Unlock()

		return UpdateResult{
			OK: false, Error: "settings could not be saved: " + NodeError(err),
			SaveFailed: true,
		}
	}

	s.backupPending = false
	s.current = next
	s.version++
	s.dropped = []string{}
	listeners := slices.Clone(s.listeners)

	s.mu.Unlock()

	for _, entry := range listeners {
		s.notify(entry, next.Clone())
	}

	return UpdateResult{OK: true, Error: "", SaveFailed: false}
}

// buildRepoRefusal refuses a default repository (buildRepo) the allowlist does not cover, when
// there is a list.
func buildRepoRefusal(atlassian Atlassian, allow []string) string {
	buildRepo := atlassian.Jira.BuildRepo
	if buildRepo == nil {
		return ""
	}

	if !outsideList(*buildRepo, allow, true) {
		return ""
	}

	return "atlassian.jira.buildRepo " + show(*buildRepo) + ": it is not an allowed repository"
}

// hasJiraKey reports whether a PUT body's jira object carries key (a page that does not know the
// key leaves it out).
func hasJiraKey(body any, key string) bool {
	object, isObject := body.(*js.Object)
	if !isObject {
		return false
	}

	jira, isObject := objectAt(object, "jira")
	if !isObject {
		return false
	}

	_, present := jira.Get(key)

	return present
}

// keepAbsent is saved with every key the body left out taken from current, whole: null clears
// baseBranch and an empty object clears baseBranches, but a key not sent is not changed.
func keepAbsent(body any, saved, current Jira) Jira {
	if !hasJiraKey(body, "baseBranch") {
		saved.BaseBranch = clonePointer(current.BaseBranch)
	}

	if !hasJiraKey(body, "baseBranches") {
		saved.BaseBranches = maps.Clone(current.BaseBranches)
	}

	if !hasJiraKey(body, "reviewMove") {
		saved.ReviewMove = current.ReviewMove
	}

	if !hasJiraKey(body, "sessionLayout") {
		saved.SessionLayout = current.SessionLayout
	}

	return saved
}

// consistent clears what depends on another section: a default repository and base branches the
// list no longer covers and a draft channel the channels no longer hold.
func consistent(settings *Settings) {
	buildRepo := settings.Atlassian.Jira.BuildRepo
	if buildRepo != nil {
		if !repos.Allowed(*buildRepo, settings.Repos.Allow) {
			settings.Atlassian.Jira.BuildRepo = nil
		}
	}

	maps.DeleteFunc(settings.Atlassian.Jira.BaseBranches, func(repo, _ string) bool {
		return !repos.Allowed(repo, settings.Repos.Allow)
	})

	draft := settings.Slack.DraftChannel
	if draft != nil {
		if !slices.Contains(settings.Slack.Channels, *draft) {
			settings.Slack.DraftChannel = nil
		}
	}
}

// OnChange calls notify after every saved update, in the order listeners were added; the
// returned function removes it.
func (s *Store) OnChange(notify func(Settings)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()

	listenerID := s.nextID
	s.nextID++
	s.listeners = append(s.listeners, listener{id: listenerID, notify: notify})

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.listeners = slices.DeleteFunc(s.listeners, func(entry listener) bool {
			return entry.id == listenerID
		})
	}
}

// Reload reads the file again from dir and makes that folder the one saves go to. Listeners are
// not told: this is boot and tests.
func (s *Store) Reload(dir string) {
	loaded := LoadDetailed(dir)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.dir = dir
	s.loaded = true
	s.current = loaded.Settings
	s.version++
	s.dropped = loaded.Dropped
	s.backupPending = loaded.Unreadable
	s.backedUpTo = ""
}

// next is the settings with one section replaced, or why that is refused. The caller holds mu.
func (s *Store) next(name string, body any) (Settings, string) {
	if !s.loaded {
		return Settings{}, "settings are not loaded"
	}

	checked := ValidateSection(name, body)
	if !checked.OK {
		return Settings{}, checked.Error
	}

	next := s.current.Clone()

	switch value := checked.Value.(type) {
	case Repos:
		next.Repos = value
	case GitHub:
		next.GitHub = value
	case Slack:
		next.Slack = value
	case Atlassian:
		refusal := buildRepoRefusal(value, s.current.Repos.Allow)
		if refusal != "" {
			return Settings{}, refusal
		}

		value.Jira = keepAbsent(body, value.Jira, s.current.Atlassian.Jira)

		next.Atlassian = value
	case Reviews:
		next.Reviews = value
	}

	consistent(&next)

	return next, ""
}

// persist saves next. When the file was set aside for the defaults, its bytes are kept first,
// or the save does not happen. The caller holds mu.
func (s *Store) persist(next Settings) error {
	if s.backupPending {
		backup, err := backUp(s.dir)
		if err != nil {
			return err
		}

		if backup != "" {
			s.backedUpTo = backup
		}
	}

	return Save(s.dir, next, nil)
}

// notify calls one listener; one that panics is reported and the others are still told.
func (s *Store) notify(entry listener, settings Settings) {
	defer func() {
		recovered := recover()
		if recovered != nil {
			s.logger.Printf("settings listener failed: %v", recovered)
		}
	}()

	entry.notify(settings)
}
