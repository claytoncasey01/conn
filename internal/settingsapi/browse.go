package settingsapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/run"
)

// Browse's limits: the owners it lists (the signed-in user and their organizations), the
// repositories listed for each, and how many owners are listed at once.
const (
	BrowseOwnersMax = 30
	BrowseReposMax  = 100
	browseParallel  = 4

	// reasonShownMax is the most of a gh failure an answer shows, in UTF-16 units.
	reasonShownMax = 300
	// loginShownMax is the most of an unusable login an answer quotes, in UTF-16 units.
	loginShownMax = 60

	jqFlag = "--jq"
)

// login is a GitHub login (an Enterprise Managed User's has an underscore). It goes to gh as an
// argument, so it may not start with a dash.
var login = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,38}$`)

var errNoLogin = errors.New("gh did not report a login")

// Owner is one owner browse lists, with its repositories as GitHub spells them.
type Owner struct {
	Owner string   `json:"owner"`
	Repos []string `json:"repos"`
}

// browseBody is browse's answer: Failed names the owners that could not be listed.
type browseBody struct {
	Owners []Owner   `json:"owners"`
	Failed *[]string `json:"failed,omitempty"`
}

func (a *API) browse(ctx context.Context) Reply {
	if !a.deps.Settings.Get().GitHub.Enabled {
		return refuse(http.StatusConflict,
			"GitHub is off: turn it on before browsing its repositories")
	}

	owners, err := a.owners(ctx)
	if err != nil {
		return refuse(http.StatusBadGateway, reason(err))
	}

	listed, failures := a.listAll(ctx, owners)

	good := []Owner{}
	failed := []string{}

	for index, owner := range listed {
		if owner == nil {
			failed = append(failed, owners[index])

			continue
		}

		good = append(good, *owner)
	}

	if len(good) == 0 {
		if len(failures) > 0 {
			return refuse(http.StatusBadGateway, reason(failures[0]))
		}
	}

	body := browseBody{Owners: good, Failed: nil}
	if len(failed) > 0 {
		body.Failed = &failed
	}

	return Reply{Code: http.StatusOK, Body: body}
}

// owners are the signed-in login, then its organizations, each a valid login, once, at most
// BrowseOwnersMax.
func (a *API) owners(ctx context.Context) ([]string, error) {
	signedIn, err := a.deps.Gh(ctx, []string{"api", "user", jqFlag, ".login"})
	if err != nil {
		return nil, err
	}

	organizations, err := a.deps.Gh(ctx, []string{
		"api", "user/orgs?per_page=100", jqFlag, ".[].login",
	})
	if err != nil {
		return nil, err
	}

	signedIn = js.Trim(signedIn)
	if signedIn == "" {
		return nil, errNoLogin
	}

	if !login.MatchString(signedIn) {
		return nil, unusableLoginError(signedIn)
	}

	owners := []string{}

	for _, name := range append([]string{signedIn}, lines(organizations)...) {
		if !login.MatchString(name) {
			continue
		}

		if slices.Contains(owners, name) {
			continue
		}

		owners = append(owners, name)
	}

	return owners[:min(len(owners), BrowseOwnersMax)], nil
}

// unusableLoginError quotes the start of a login gh reported that Conn cannot pass on.
type unusableLoginError string

func (u unusableLoginError) Error() string {
	quoted := js.Stringify(js.Slice(string(u), loginShownMax))

	return "gh reported a login conn cannot use: " + quoted
}

// listAll lists every owner, browseParallel at a time. An owner that could not be listed is nil,
// and its failure is in failures, in the order they happened.
func (a *API) listAll(ctx context.Context, owners []string) ([]*Owner, []error) {
	listed := make([]*Owner, len(owners))

	var (
		guard    sync.Mutex
		failures []error
		next     int
		workers  sync.WaitGroup
	)

	take := func() (int, bool) {
		guard.Lock()
		defer guard.Unlock()

		index := next
		next++

		return index, index < len(owners)
	}

	for range min(browseParallel, len(owners)) {
		workers.Go(func() {
			for index, more := take(); more; index, more = take() {
				repositories, err := a.listOwner(ctx, owners[index])
				if err != nil {
					guard.Lock()

					failures = append(failures, err)

					guard.Unlock()

					continue
				}

				listed[index] = &Owner{Owner: owners[index], Repos: repositories}
			}
		})
	}

	workers.Wait()

	return listed, failures
}

// listOwner lists an owner's repositories: only a valid name that belongs to the owner, as
// GitHub spelled it.
func (a *API) listOwner(ctx context.Context, owner string) ([]string, error) {
	out, err := a.deps.Gh(ctx, []string{
		"repo", "list", owner, "--limit", strconv.Itoa(BrowseReposMax), "--json", "nameWithOwner",
		jqFlag, ".[].nameWithOwner",
	})
	if err != nil {
		return nil, err
	}

	found := []string{}

	for _, name := range lines(out) {
		if !repos.IsName(name) {
			continue
		}

		nameOwner, _, _ := strings.Cut(name, "/")
		if !strings.EqualFold(nameOwner, owner) {
			continue
		}

		found = append(found, name)
	}

	return found[:min(len(found), BrowseReposMax)], nil
}

// lines are the non-blank lines of out, trimmed.
func lines(out string) []string {
	found := []string{}

	for line := range strings.SplitSeq(out, "\n") {
		line = js.Trim(line)
		if line != "" {
			found = append(found, line)
		}
	}

	return found
}

// reason is the first line of an error, cut to reasonShownMax.
func reason(err error) string {
	return js.Slice(run.FirstLine(err.Error()), reasonShownMax)
}
