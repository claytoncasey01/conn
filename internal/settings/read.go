package settings

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

const (
	// listedMax is the most Slack channels and Jira keys: both go into prompts and queries.
	listedMax = 20

	// branchUnitsMax and pathUnitsMax are the longest branch name and folder path, in UTF-16
	// code units as the page counts them.
	branchUnitsMax = 100
	pathUnitsMax   = 1024

	// baseBranchesMax is the most repositories with a base branch of their own.
	baseBranchesMax = 100

	// shownUnitsMax is the longest a value is shown in a message; a longer one is cut to
	// shownCutUnits and an ellipsis.
	shownUnitsMax = 60
	shownCutUnits = 57
)

const (
	channelRule = "# and lowercase letters, digits, dots, dashes or underscores"
	jiraKeyRule = "a Jira key like PROJ"
	branchRule  = "not a branch name (letters, digits and . _ - /, starting with a letter or " +
		"digit, no .., at most 100 characters)"
)

var (
	channelShape = regexp.MustCompile(`^#[a-z0-9._-]{1,80}$`)
	jiraKeyShape = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	branchShape  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// problems collects what could not be read, in the order it was found.
type problems struct {
	list []string
}

func (p *problems) addf(format string, args ...any) {
	p.list = append(p.list, fmt.Sprintf(format, args...))
}

// Loaded is the settings read from a file and what could not be read from it.
type Loaded struct {
	Settings Settings `json:"settings"`
	Dropped  []string `json:"dropped"`
	// Unreadable is true when the whole file was set aside for the defaults (not JSON, not an
	// object, another version, too big, unreadable): a save over it would lose what it held.
	Unreadable bool `json:"unreadable"`
}

func unreadable(reason string) Loaded {
	return Loaded{Settings: Defaults(), Dropped: []string{reason}, Unreadable: true}
}

// ParseDetailed reads every section leniently, with what was dropped or replaced. present is
// false for no value at all (undefined). Never fails.
func ParseDetailed(raw any, present bool) Loaded {
	if !present {
		return Loaded{Settings: Defaults(), Dropped: []string{}, Unreadable: false}
	}

	file, isObject := raw.(*js.Object)
	if !isObject {
		return unreadable(fmt.Sprintf(
			"settings.json is not a JSON object (%s): using the defaults", show(raw)))
	}

	version, hasVersion := file.Get("version")
	if hasVersion {
		if version != float64(fileVersion) {
			return unreadable(fmt.Sprintf("settings.json is version %s: this conn reads "+
				"version 1, so the defaults are used and the file is left as it is", show(version)))
		}
	}

	var found problems

	settings := readSections(file, &found)

	dropped := found.list
	if dropped == nil {
		dropped = []string{}
	}

	return Loaded{Settings: settings, Dropped: dropped, Unreadable: false}
}

// readSections reads each section, noting what it drops in the order the page reads them:
// repos, the github and reviews sections, github's switch, slack, atlassian, reviews' switches,
// then sessions.
func readSections(file *js.Object, found *problems) Settings {
	reposRaw, reposPresent := file.Get("repos")
	allowed := readRepos(reposRaw, reposPresent, found)

	githubRaw, githubPresent := file.Get("github")
	github := section(githubRaw, githubPresent, "github", found)

	reviewsRaw, reviewsPresent := file.Get("reviews")
	reviews := section(reviewsRaw, reviewsPresent, "reviews", found)

	githubEnabled := boolField(github, "enabled", true, "github", found)

	slackRaw, slackPresent := file.Get("slack")
	slack := readSlack(slackRaw, slackPresent, found)

	atlassianRaw, atlassianPresent := file.Get("atlassian")
	atlassian := readAtlassian(atlassianRaw, atlassianPresent, allowed.Allow, true, found)

	reviewsSection := Reviews{
		Requested: boolField(reviews, "requested", true, "reviews", found),
		AllOpen:   boolField(reviews, "allOpen", false, "reviews", found),
	}

	sessionsRaw, sessionsPresent := file.Get(SectionSessions)

	return Settings{
		Version:   fileVersion,
		Repos:     allowed,
		GitHub:    GitHub{Enabled: githubEnabled},
		Slack:     slack,
		Atlassian: atlassian,
		Reviews:   reviewsSection,
		Sessions:  readSessions(sessionsRaw, sessionsPresent, found),
	}
}

// Parse reads raw leniently: bad entries are dropped and an unreadable allow is null. Never
// fails.
func Parse(raw any, present bool) Settings { return ParseDetailed(raw, present).Settings }

// ParseBranch reads a git branch name Open Work may branch from and open its PR into: letters,
// digits and . _ - /, starting with a letter or digit (so it is never read as an option), at
// most 100 characters, with no .., //, /., @{, no .lock ending and no trailing / or .. It
// reaches git as one argument (never a shell) and a ref name, so this is the rule for what may
// be there. "" and false for anything else.
func ParseBranch(raw any) (string, bool) {
	name, isString := raw.(string)
	if !isString {
		return "", false
	}

	if name == "" {
		return "", false
	}

	if js.Len(name) > branchUnitsMax {
		return "", false
	}

	if !branchShape.MatchString(name) {
		return "", false
	}

	inside := func(part string) bool { return strings.Contains(name, part) }
	if slices.ContainsFunc([]string{"..", "//", "/.", "@{"}, inside) {
		return "", false
	}

	ending := func(part string) bool { return strings.HasSuffix(name, part) }
	if slices.ContainsFunc([]string{"/", ".", ".lock"}, ending) {
		return "", false
	}

	return name, true
}

// show is a value for a message, short, and safe to print whatever it is.
func show(value any) string {
	text := js.Stringify(value)
	if js.Len(text) > shownUnitsMax {
		return js.Slice(text, shownCutUnits) + "..."
	}

	return text
}

// absolutePath is an absolute path, cleaned of .. and a trailing slash; "" and false when value
// is not one.
func absolutePath(value any) (string, bool) {
	path, isString := value.(string)
	if !isString {
		return "", false
	}

	if path == "" {
		return "", false
	}

	if js.Len(path) > pathUnitsMax {
		return "", false
	}

	if strings.ContainsRune(path, 0) {
		return "", false
	}

	if !filepath.IsAbs(path) {
		return "", false
	}

	return filepath.Clean(path), true
}

// boolField is a boolean field: absent is the default without a word, a wrong type is the
// default with a problem.
func boolField(object *js.Object, key string, fallback bool, where string, found *problems) bool {
	value, present := object.Get(key)
	if !present {
		return fallback
	}

	typed, isBool := value.(bool)
	if isBool {
		return typed
	}

	found.addf("%s.%s is not true or false (%s): using %v", where, key, show(value), fallback)

	return fallback
}

// section is an object section: absent or not an object is empty, the latter with a problem.
func section(value any, present bool, where string, found *problems) *js.Object {
	if !present {
		return js.NewObject()
	}

	object, isObject := value.(*js.Object)
	if isObject {
		return object
	}

	found.addf("%s is not an object (%s): using its defaults", where, show(value))

	return js.NewObject()
}

func readRepos(raw any, present bool, found *problems) Repos {
	object := section(raw, present, "repos", found)

	return Repos{
		Allow:     readAllow(object, found),
		Clones:    readClones(object, found),
		CloneRoot: readCloneRoot(object, found),
	}
}

func readAllow(object *js.Object, found *problems) []string {
	raw, present := object.Get("allow")
	if !present {
		return nil
	}

	if raw == nil {
		return nil
	}

	list, isList := raw.([]any)
	if !isList {
		found.addf("repos.allow is not a list (%s): nothing is allowed until it is fixed",
			show(raw))

		return nil
	}

	allow := []string{}

	for _, entry := range list {
		parsed := repos.ParsePattern(entry)
		if !parsed.OK {
			found.addf("repos.allow %s dropped: %s", show(entry), parsed.Reason)

			continue
		}

		if !slices.Contains(allow, parsed.Pattern) {
			allow = append(allow, parsed.Pattern)
		}
	}

	if len(allow) > PatternsMax {
		found.addf("repos.allow holds %d patterns: only the first %d are kept",
			len(allow), PatternsMax)

		allow = allow[:PatternsMax]
	}

	return allow
}

func readClones(object *js.Object, found *problems) Clones {
	clones := Clones{keys: nil, paths: map[string]string{}}

	raw, present := object.Get("clones")
	if !present {
		return clones
	}

	entries, isObject := raw.(*js.Object)
	if !isObject {
		found.addf("repos.clones is not an object (%s): using none", show(raw))

		return clones
	}

	for _, key := range entries.Keys() {
		value, _ := entries.Get(key)
		if !repos.IsName(key) {
			found.addf("repos.clones %s dropped: not owner/repo", show(key))

			continue
		}

		path, isPath := absolutePath(value)
		if !isPath {
			found.addf("repos.clones %s dropped: %s is not an absolute folder path",
				show(key), show(value))

			continue
		}

		setDefault(&clones, strings.ToLower(key), path)
	}

	return clones
}

func readCloneRoot(object *js.Object, found *problems) *string {
	raw, present := object.Get("cloneRoot")
	if !present {
		return nil
	}

	if raw == nil {
		return nil
	}

	path, isPath := absolutePath(raw)
	if !isPath {
		found.addf("repos.cloneRoot %s dropped: not an absolute folder path", show(raw))

		return nil
	}

	return &path
}

// names is a list of strings that each match shape, repeats once, the bad ones reported.
func names(raw any, present bool, shape *regexp.Regexp, where, rule string,
	found *problems,
) []string {
	kept := []string{}
	if !present {
		return kept
	}

	list, isList := raw.([]any)
	if !isList {
		found.addf("%s is not a list (%s): using none", where, show(raw))

		return kept
	}

	for _, entry := range list {
		name, isName := matching(entry, shape)
		if !isName {
			found.addf("%s %s dropped: not %s", where, show(entry), rule)

			continue
		}

		if !slices.Contains(kept, name) {
			kept = append(kept, name)
		}
	}

	if len(kept) > listedMax {
		found.addf("%s holds %d entries: only the first %d are kept", where, len(kept),
			listedMax)

		kept = kept[:listedMax]
	}

	return kept
}

// matching is entry when it is a string shape matches.
func matching(entry any, shape *regexp.Regexp) (string, bool) {
	text, isString := entry.(string)
	if !isString {
		return "", false
	}

	return text, shape.MatchString(text)
}

func readSlack(raw any, present bool, found *problems) Slack {
	object := section(raw, present, "slack", found)

	channelsRaw, channelsPresent := object.Get("channels")
	channels := names(channelsRaw, channelsPresent, channelShape, "slack.channels", channelRule,
		found)
	draft := readDraftChannel(object, channels, found)

	return Slack{
		Enabled:      boolField(object, "enabled", false, "slack", found),
		Channels:     channels,
		DMs:          boolField(object, "dms", false, "slack", found),
		DraftChannel: draft,
	}
}

func readDraftChannel(object *js.Object, channels []string, found *problems) *string {
	raw, present := object.Get("draftChannel")
	if !present {
		return nil
	}

	if raw == nil {
		return nil
	}

	channel, isString := raw.(string)
	if isString {
		if slices.Contains(channels, channel) {
			return &channel
		}
	}

	found.addf("slack.draftChannel %s dropped: it must be one of the channels", show(raw))

	return nil
}

// readAtlassian reads the section; checkAllow says whether the default repository (buildRepo)
// must be on allow (a section checked alone has no list to ask).
func readAtlassian(raw any, present bool, allow []string, checkAllow bool,
	found *problems,
) Atlassian {
	object := section(raw, present, "atlassian", found)

	jiraRaw, jiraPresent := object.Get("jira")
	jira := section(jiraRaw, jiraPresent, "atlassian.jira", found)

	projectsRaw, projectsPresent := jira.Get("projects")
	projects := names(projectsRaw, projectsPresent, jiraKeyShape, "atlassian.jira.projects",
		jiraKeyRule, found)
	buildRepo := readBuildRepo(jira, allow, checkAllow, found)
	baseBranch := readBaseBranch(jira, found)

	return Atlassian{
		Enabled: boolField(object, "enabled", false, "atlassian", found),
		Jira: Jira{
			Projects: projects, BuildRepo: buildRepo, BaseBranch: baseBranch,
			BaseBranches: readBaseBranches(jira, allow, checkAllow, found),
			ReviewMove:   readChoice(jira, "reviewMove", reviewMoves(), ReviewAll, found),
			SessionLayout: readChoice(jira, "sessionLayout", sessionLayouts(), LayoutShared,
				found),
		},
	}
}

// reviewMoves are the values of atlassian.jira.reviewMove.
func reviewMoves() []string { return []string{ReviewFirst, ReviewAll, ReviewNever} }

// sessionLayouts are the values of atlassian.jira.sessionLayout.
func sessionLayouts() []string { return []string{LayoutShared, LayoutSplit} }

// choiceRule says which of values are allowed: "a", "b" or "c".
func choiceRule(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = strconv.Quote(value)
	}

	last := len(quoted) - 1

	return "it must be " + strings.Join(quoted[:last], ", ") + " or " + quoted[last]
}

// readChoice reads atlassian.jira.<key>, one of allowed; absent, null or anything else is
// fallback (and anything else is reported).
func readChoice(jira *js.Object, key string, allowed []string, fallback string,
	found *problems,
) string {
	raw, present := jira.Get(key)
	if !present {
		return fallback
	}

	if raw == nil {
		return fallback
	}

	text, isString := raw.(string)
	if isString {
		if slices.Contains(allowed, text) {
			return text
		}
	}

	found.addf("atlassian.jira.%s %s dropped: %s", key, show(raw), choiceRule(allowed))

	return fallback
}

// readBaseBranches reads atlassian.jira.baseBranches: repository (lowercased, first spelling
// wins) to branch. Every entry that cannot be used is reported and left out, as are the ones
// past baseBranchesMax; a null branch is no entry. The map is never nil.
func readBaseBranches(jira *js.Object, allow []string, checkAllow bool,
	found *problems,
) map[string]string {
	kept := map[string]string{}

	raw, present := jira.Get("baseBranches")
	if !present {
		return kept
	}

	if raw == nil {
		return kept
	}

	object, isObject := raw.(*js.Object)
	if !isObject {
		found.addf("atlassian.jira.baseBranches %s dropped: not an object of repository to "+
			"branch", show(raw))

		return kept
	}

	repoNames := object.Keys()
	if len(repoNames) > baseBranchesMax {
		found.addf("atlassian.jira.baseBranches holds %d entries: only the first %d are read",
			len(repoNames), baseBranchesMax)

		repoNames = repoNames[:baseBranchesMax]
	}

	for _, repo := range repoNames {
		value, _ := object.Get(repo)
		readBaseBranchEntry(kept, repo, value, allow, checkAllow, found)
	}

	return kept
}

// readBaseBranchEntry adds one repository's base branch to kept, or reports why it cannot.
func readBaseBranchEntry(kept map[string]string, repo string, value any, allow []string,
	checkAllow bool, found *problems,
) {
	if !repos.IsName(repo) {
		found.addf("atlassian.jira.baseBranches %s dropped: not owner/repo", show(repo))

		return
	}

	if outsideList(repo, allow, checkAllow) {
		found.addf("atlassian.jira.baseBranches %s dropped: it is not an allowed repository",
			show(repo))

		return
	}

	if value == nil {
		return
	}

	branch, isBranch := ParseBranch(value)
	if !isBranch {
		found.addf("atlassian.jira.baseBranches[%s] %s dropped: %s", show(repo), show(value),
			branchRule)

		return
	}

	_, repeated := kept[strings.ToLower(repo)]
	if !repeated {
		kept[strings.ToLower(repo)] = branch
	}
}

func readBuildRepo(jira *js.Object, allow []string, checkAllow bool, found *problems) *string {
	raw, present := jira.Get("buildRepo")
	if !present {
		return nil
	}

	if raw == nil {
		return nil
	}

	name, isString := raw.(string)
	if !isString {
		found.addf("atlassian.jira.buildRepo %s dropped: not owner/repo", show(raw))

		return nil
	}

	if !repos.IsName(name) {
		found.addf("atlassian.jira.buildRepo %s dropped: not owner/repo", show(raw))

		return nil
	}

	if outsideList(name, allow, checkAllow) {
		found.addf("atlassian.jira.buildRepo %s dropped: it is not an allowed repository",
			show(raw))

		return nil
	}

	lower := strings.ToLower(name)

	return &lower
}

// outsideList reports whether a repository is refused by the allowlist: only when the list is
// asked and holds something.
func outsideList(name string, allow []string, checkAllow bool) bool {
	if !checkAllow {
		return false
	}

	if len(allow) == 0 {
		return false
	}

	return !repos.Allowed(name, allow)
}

func readBaseBranch(jira *js.Object, found *problems) *string {
	raw, present := jira.Get("baseBranch")
	if !present {
		return nil
	}

	if raw == nil {
		return nil
	}

	branch, isBranch := ParseBranch(raw)
	if !isBranch {
		found.addf("atlassian.jira.baseBranch %s dropped: %s", show(raw), branchRule)

		return nil
	}

	return &branch
}
