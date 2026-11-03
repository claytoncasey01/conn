// Package settings is settings.json in $CONN_DATA: the repository allowlist, the modules and the
// review sources, validated field by field. The file is user-editable, so a bad one never stops
// the server and never widens access: an entry that cannot be read is dropped (and reported), a
// section that cannot be read is its default, and an allow that cannot be read is null, which is
// setup (nothing is read). The Store is the one place every reader asks; it is the only code
// that knows the store is a file.
package settings

import (
	"maps"
	"slices"

	"github.com/claytoncasey01/conn/internal/js"
)

// Module names.
const (
	ModuleGitHub    = "github"
	ModuleSlack     = "slack"
	ModuleAtlassian = "atlassian"
)

// PatternsMax is the most patterns the allowlist holds: My PRs makes one search per pattern.
const PatternsMax = 25

// fileVersion is the only settings.json version this Conn reads and writes.
const fileVersion = 1

// Clones maps a lowercase owner/repo to the absolute folder of its clone, keeping the order
// entries were written in.
type Clones struct {
	keys  []string
	paths map[string]string
}

// NewClones builds clones from repo, path pairs in order.
func NewClones(pairs ...string) Clones {
	clones := Clones{keys: nil, paths: map[string]string{}}
	for index := 0; index+1 < len(pairs); index += 2 {
		setDefault(&clones, pairs[index], pairs[index+1])
	}

	return clones
}

// Get is the folder for repo, or "".
func (c Clones) Get(repo string) string { return c.paths[repo] }

// Keys are the repos in order.
func (c Clones) Keys() []string { return slices.Clone(c.keys) }

// Len is how many clones there are.
func (c Clones) Len() int { return len(c.keys) }

// MarshalJSON writes the clones in order.
func (c Clones) MarshalJSON() ([]byte, error) {
	object := js.NewObject()
	for _, key := range c.keys {
		object.Set(key, c.paths[key])
	}

	return []byte(js.Stringify(object)), nil
}

// setDefault adds repo to clones when it is not there yet (the first entry for a repo wins).
func setDefault(clones *Clones, repo, path string) {
	if clones.paths == nil {
		clones.paths = map[string]string{}
	}

	_, found := clones.paths[repo]
	if found {
		return
	}

	clones.keys = append(clones.keys, repo)
	clones.paths[repo] = path
}

func (c Clones) clone() Clones {
	paths := maps.Clone(c.paths)
	if paths == nil {
		paths = map[string]string{}
	}

	return Clones{keys: slices.Clone(c.keys), paths: paths}
}

// Repos is the allowlist and where clones are. Allow is nil while there is no list (setup).
type Repos struct {
	Allow     []string `json:"allow"`
	Clones    Clones   `json:"clones"`
	CloneRoot *string  `json:"cloneRoot"`
}

// GitHub is the GitHub module's switch.
type GitHub struct {
	Enabled bool `json:"enabled"`
}

// Slack is the Slack module: its switch, the channels Reviews scans, whether DMs are scanned, and
// the channel drafts go to.
type Slack struct {
	Enabled      bool     `json:"enabled"`
	Channels     []string `json:"channels"`
	DMs          bool     `json:"dms"`
	DraftChannel *string  `json:"draftChannel"`
}

// Jira is the Open Work projects, the repo builds happen in and the branch they start from.
type Jira struct {
	Projects   []string `json:"projects"`
	BuildRepo  *string  `json:"buildRepo"`
	BaseBranch *string  `json:"baseBranch"`
}

// Atlassian is the Atlassian module.
type Atlassian struct {
	Enabled bool `json:"enabled"`
	Jira    Jira `json:"jira"`
}

// Reviews is which pull requests Reviews lists.
type Reviews struct {
	Requested bool `json:"requested"`
	AllOpen   bool `json:"allOpen"`
}

// Settings is the whole file.
type Settings struct {
	Version   int       `json:"version"`
	Repos     Repos     `json:"repos"`
	GitHub    GitHub    `json:"github"`
	Slack     Slack     `json:"slack"`
	Atlassian Atlassian `json:"atlassian"`
	Reviews   Reviews   `json:"reviews"`
}

// Defaults are the settings of a fresh install: no allowlist, GitHub on, Slack and Atlassian off,
// requested on, allOpen off.
func Defaults() Settings {
	return Settings{
		Version: fileVersion,
		Repos: Repos{
			Allow:     nil,
			Clones:    Clones{keys: nil, paths: map[string]string{}},
			CloneRoot: nil,
		},
		GitHub: GitHub{Enabled: true},
		Slack:  Slack{Enabled: false, Channels: []string{}, DMs: false, DraftChannel: nil},
		Atlassian: Atlassian{
			Enabled: false,
			Jira:    Jira{Projects: []string{}, BuildRepo: nil, BaseBranch: nil},
		},
		Reviews: Reviews{Requested: true, AllOpen: false},
	}
}

// Clone is a deep copy.
func (s Settings) Clone() Settings {
	copied := s
	if s.Repos.Allow != nil {
		copied.Repos.Allow = slices.Clone(s.Repos.Allow)
	}

	copied.Repos.Clones = s.Repos.Clones.clone()
	copied.Repos.CloneRoot = clonePointer(s.Repos.CloneRoot)
	copied.Slack.Channels = slices.Clone(s.Slack.Channels)
	copied.Slack.DraftChannel = clonePointer(s.Slack.DraftChannel)
	copied.Atlassian.Jira.Projects = slices.Clone(s.Atlassian.Jira.Projects)
	copied.Atlassian.Jira.BuildRepo = clonePointer(s.Atlassian.Jira.BuildRepo)
	copied.Atlassian.Jira.BaseBranch = clonePointer(s.Atlassian.Jira.BaseBranch)

	return copied
}

func clonePointer(text *string) *string {
	if text == nil {
		return nil
	}

	copied := *text

	return &copied
}
