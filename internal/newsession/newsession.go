// Package newsession answers /api/sessions/new: the repositories a plain Claude session may start
// in, and the start itself, from the Sessions page.
//
// A start names a repository and, optionally, a name; never a folder, a program, an argument or a
// variable. The repository must be on the allowlist and have a local clone, as Open work resolves
// one, whose real path is a folder no one but the person and root may change (CheckClone),
// checked when it is chosen and again right before the start. The server builds the session
// itself: claude, as CONN_CLAUDE names it, on a conversation id Conn chooses (--session-id), with
// the name made safe (--name), in the clone's real path. It opens through the same opener every
// module uses, so it follows Settings' Where sessions open, and inside Conn it is recorded as a
// History session on that id: once claude has written a transcript, a full quit and the next
// launch resume it as History's own check allows.
package newsession

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// Path is the route: GET lists the choices, POST starts a session.
	Path = "/api/sessions/new"
	// startCooldownDefault is how long a second start in the same repository is refused: a
	// double press, or a press again before the first session shows up in the list.
	startCooldownDefault = 10 * time.Second
	// gitName is a clone's git folder, hooksName git's hooks folder in it, and claudeName the
	// clone's Claude Code settings folder.
	gitName    = ".git"
	hooksName  = "hooks"
	claudeName = ".claude"
)

// ErrNotAClone is a clone folder whose .git is not a git folder of its own.
var ErrNotAClone = errors.New("not a clone of its own")

// Deps are what the API reads and starts.
type Deps struct {
	// Settings are the settings now: the allowlist, the clones and where sessions open.
	Settings func() settings.Settings
	// Repos are the allowed repositories to offer under current (openwork.AllowedRepos over the
	// repositories found on this machine); each is offered only when a start there would work.
	Repos func(current settings.Settings) []string
	// CloneFor is the local clone of a repository per current, false when there is none.
	CloneFor func(repo string, current settings.Settings) (string, bool)
	// CheckClone refuses a clone's real path a session may not start in (CheckClone over
	// reviews.CheckSafeFolder).
	CheckClone func(folder string) error
	// OriginOf is the GitHub repository of a folder's origin, "" for none (origin.Reader.Of).
	OriginOf func(folder string) string
	// Resolve is a folder with every link followed (realpath.Resolve): the folder claude reports
	// as its own, which History's archive row holds.
	Resolve func(path string) string
	// Open starts the session where the settings say, recorded as a History session.
	Open launch.Opener
	// Claude is the claude program as configured (CONN_CLAUDE).
	Claude string
	// Dry starts nothing: a start says what it would run.
	Dry bool
	// Room is how many sessions the host runs or is starting now, and the most it runs
	// (pty.Host.Room).
	Room func() pty.Room
	// NewID is a fresh conversation id, a UUID (NewID).
	NewID func() (string, error)
	Now   func() time.Time
	// Cooldown refuses a second start in the same repository; 0 is startCooldownDefault.
	Cooldown time.Duration
}

// Reply is a handler's answer.
type Reply struct {
	Code int
	Body any
}

// Choices are what the page's form offers: the repositories a start may name, in allowlist order,
// where sessions open, and whether this run is dry.
type Choices struct {
	Repos []string `json:"repos"`
	Where string   `json:"where"`
	Dry   bool     `json:"dry"`
}

// Started is a start's answer, as History's resume answers: ok, or why not with the line to
// paste; a dry run's command; and the host's id of a session started inside Conn.
type Started struct {
	OK            bool    `json:"ok"`
	ID            *string `json:"id,omitempty"`
	Dry           *bool   `json:"dry,omitempty"`
	Command       *string `json:"command,omitempty"`
	Error         *string `json:"error,omitempty"`
	ManualCommand *string `json:"manualCommand,omitempty"`
}

type errorBody struct {
	Error string `json:"error"`
}

func refuse(code int, message string) *Reply {
	return &Reply{Code: code, Body: errorBody{Error: message}}
}

// API answers /api/sessions/new.
type API struct {
	deps  Deps
	mutex sync.Mutex
	// opening are the repositories a start is opening in now, by lowercase name.
	opening map[string]bool
	// openedAt is when a session last opened in each repository, within the cooldown.
	openedAt map[string]time.Time
}

// New is the API over deps.
func New(deps Deps) *API {
	if deps.Cooldown == 0 {
		deps.Cooldown = startCooldownDefault
	}

	return &API{
		deps: deps, mutex: sync.Mutex{}, opening: map[string]bool{},
		openedAt: map[string]time.Time{},
	}
}

// Choices are the repositories a start may name now: each offered one passes every check a start
// makes of its repository.
func (a *API) Choices() Reply {
	current := a.deps.Settings()
	offered := []string{}

	for _, repo := range a.deps.Repos(current) {
		_, refused := a.folderOf(repo, current)
		if refused == nil {
			offered = append(offered, repo)
		}
	}

	return Reply{Code: http.StatusOK, Body: Choices{
		Repos: offered, Where: current.Sessions.Where, Dry: a.deps.Dry,
	}}
}

// Start starts a session from body; an unexpected failure is the error, for a 500.
func (a *API) Start(ctx context.Context, body *js.Object) (Reply, error) {
	request, problems := ParseRequest(body)
	if len(problems) > 0 {
		return *refuse(http.StatusBadRequest, strings.Join(problems, " ")), nil
	}

	current := a.deps.Settings()

	folder, refused := a.folderOf(request.Repo, current)
	if refused != nil {
		return *refused, nil
	}

	sessionID, err := a.deps.NewID()
	if err != nil {
		return Reply{}, fmt.Errorf("choosing the conversation id: %w", err)
	}

	spec := Spec(a.deps.Claude, sessionID, folder, request)

	if a.deps.Dry {
		dry := true
		command := launch.PasteLine(spec)

		return Reply{Code: http.StatusOK, Body: Started{
			OK: true, ID: nil, Dry: &dry, Command: &command, Error: nil, ManualCommand: nil,
		}}, nil
	}

	key := js.Lower(request.Repo)
	if refused := a.claim(key, request.Repo, current.Sessions.Where); refused != nil {
		return *refused, nil
	}

	// Checked again right before the start, so a change since the first check starts nothing.
	// From here to the program's chdir the path cannot change: no one but the person and root
	// may change any folder on it (CheckClone).
	if refused := a.cloneProblem(request.Repo, folder); refused != nil {
		a.release(key, false)

		return *refused, nil
	}

	opened := a.deps.Open(ctx, spec)
	a.release(key, opened.OK)

	return Reply{Code: http.StatusOK, Body: startedOf(opened)}, nil
}

// startedOf is how an open went, as the page reads it.
func startedOf(opened launch.Opened) Started {
	if !opened.OK {
		// A start with no line to paste (one holding a character that is not printable ASCII)
		// leaves manualCommand out rather than sending an empty one.
		var manual *string
		if opened.ManualCommand != "" {
			manual = &opened.ManualCommand
		}

		return Started{
			OK: false, ID: nil, Dry: nil, Command: nil, Error: &opened.Error,
			ManualCommand: manual,
		}
	}

	var hostedID *string

	if opened.HostedID != "" {
		id := string(opened.HostedID)
		hostedID = &id
	}

	return Started{
		OK: true, ID: hostedID, Dry: nil, Command: nil, Error: nil, ManualCommand: nil,
	}
}

// folderOf is the folder a session in repo runs in, or why there is none: repo is allowed and
// has a clone, and the clone's real path (every link resolved first) is a folder a session may
// start in (cloneProblem).
func (a *API) folderOf(repo string, current settings.Settings) (string, *Reply) {
	if !repos.Allowed(repo, current.Repos.Allow) {
		return "", refuse(http.StatusBadRequest,
			repo+" is not an allowed repository: add it in Settings.")
	}

	clone, found := a.deps.CloneFor(repo, current)
	if !found {
		return "", refuse(http.StatusConflict,
			"No local clone of "+repo+": set a folder in Settings.")
	}

	// Resolve makes the path absolute, which can fail in an odd environment (no working folder):
	// that is the environment's, so it is a refusal, not a panic.
	folder := a.deps.Resolve(clone)
	if !filepath.IsAbs(folder) {
		return "", refuse(http.StatusConflict, "The clone of "+repo+" cannot be used: "+clone+
			" could not be made an absolute path.")
	}

	if refused := a.cloneProblem(repo, folder); refused != nil {
		return "", refused
	}

	return folder, nil
}

// cloneProblem is why a session may not start in folder, the clone of repo, or nil: it passes
// CheckClone, and its origin is repo.
// Its origin is read last, once no one else can change the folder: the repository the allowlist
// allowed must be the one the session starts in, whatever CloneFor's path pointed at before it
// was resolved.
func (a *API) cloneProblem(repo, folder string) *Reply {
	err := a.deps.CheckClone(folder)
	if err != nil {
		return refuse(http.StatusConflict,
			"The clone of "+repo+" cannot be used: "+err.Error()+".")
	}

	origin := a.deps.OriginOf(folder)
	if js.Lower(origin) != js.Lower(repo) {
		return refuse(http.StatusConflict, "The clone of "+repo+" cannot be used: "+folder+
			" is a clone of "+originOrNone(origin)+", not of "+repo+": set its folder in Settings.")
	}

	return nil
}

func originOrNone(origin string) string {
	if origin == "" {
		return "no GitHub repository"
	}

	return origin
}

// CheckClone returns every reason a session may not start in folder, a clone's real path, or
// nil. The folder and every folder above it pass checkFolder (reviews.CheckSafeFolder: no link,
// no folder another user may change), and so does its .git: a real folder, not a link, and not a
// file pointing at a git folder elsewhere (a worktree's or a --separate-git-dir clone's), since
// either would let the folder take its origin, and so its place on the allowlist, from
// somewhere else. The session trusts what the clone's .claude and .git/hooks hold, so each that
// exists passes checkFolder too. What is in them, and in .mcp.json or CLAUDE.md, is the
// repository's own and is not checked.
func CheckClone(folder string, checkFolder func(folder string) error) error {
	problems := []error{checkFolder(folder)}

	gitFolder := filepath.Join(folder, gitName)

	info, err := os.Lstat(gitFolder)

	switch {
	case err != nil:
		problems = append(problems, fmt.Errorf("%w: %s cannot be read: %w", ErrNotAClone,
			gitFolder, err))
	case info.Mode().IsRegular():
		problems = append(problems, fmt.Errorf("%w: %s is a file that points at a git folder "+
			"elsewhere (a worktree or a --separate-git-dir clone): set the main clone's folder in "+
			"Settings", ErrNotAClone, gitFolder))
	default:
		problems = append(problems, checkFolder(gitFolder))
		problems = append(problems, checkIfThere(filepath.Join(gitFolder, hooksName), checkFolder))
	}

	problems = append(problems, checkIfThere(filepath.Join(folder, claudeName), checkFolder))

	return joinOnce(problems)
}

// checkIfThere is checkFolder of path when something is at path, and nil when nothing is.
func checkIfThere(path string, checkFolder func(folder string) error) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return checkFolder(path)
}

// joinOnce joins problems, each said once: the folders above a clone's own folders are checked
// with each of them, and a problem there is one problem.
func joinOnce(problems []error) error {
	said := map[string]bool{}
	kept := []error{}

	for _, problem := range problems {
		for _, each := range flatten(problem) {
			if said[each.Error()] {
				continue
			}

			said[each.Error()] = true
			kept = append(kept, each)
		}
	}

	return errors.Join(kept...)
}

// flatten is the problems an errors.Join holds, or err alone, or none for nil.
func flatten(err error) []error {
	if err == nil {
		return nil
	}

	joined, isJoined := err.(interface{ Unwrap() []error })
	if !isJoined {
		return []error{err}
	}

	return joined.Unwrap()
}

// claim marks a start in the repository keyed key as opening, or refuses it: one is opening or
// opened within the cooldown, or, for a session inside Conn, the host runs (or is starting) as
// many as it may. The check and the mark are one step, so two presses at once open one session.
// Two starts in different repositories at once can still both pass the host's count: the host's
// own Start then refuses the one past its limit, and the reply says so.
func (a *API) claim(key, repo, where string) *Reply {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	now := a.deps.Now()
	maps.DeleteFunc(a.openedAt, func(_ string, at time.Time) bool {
		return now.Sub(at) >= a.deps.Cooldown
	})

	if a.opening[key] {
		return refuse(http.StatusConflict, justStarted(repo))
	}

	if _, recent := a.openedAt[key]; recent {
		return refuse(http.StatusConflict, justStarted(repo))
	}

	if where == settings.WhereInside {
		room := a.deps.Room()
		if room.Running >= room.SessionsMax {
			return refuse(http.StatusConflict, fmt.Sprintf("Conn already runs %d sessions, "+
				"the most it runs at once: stop one, then start a new one.", room.SessionsMax))
		}
	}

	a.opening[key] = true

	return nil
}

// release ends the start in the repository keyed key; one that opened starts the cooldown.
func (a *API) release(key string, opened bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	delete(a.opening, key)

	if opened {
		a.openedAt[key] = a.deps.Now()
	}
}

func justStarted(repo string) string {
	return "A session was just started in " + repo + ": give it a few seconds to appear."
}
