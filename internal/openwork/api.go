package openwork

import (
	"regexp"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
)

// ListPath is GET /api/work, the sprint list; it is not there while Open work is off.
const ListPath = "/api/work"

// The actions of the POST routes.
const (
	ActionRefresh   = "refresh"
	ActionStart     = "start"
	ActionAnswers   = "answers"
	ActionPR        = "pr"
	ActionCancel    = "cancel"
	ActionRetry     = "retry"
	ActionFixChecks = "fix-checks"
	ActionJira      = "jira"
	ActionCleanup   = "cleanup"
	ActionResume    = "resume"
)

var (
	// refreshRoute, startRoute and runRoute are Open work's POST routes. The key is any Jira
	// key's shape here; whether it is a listed project's is the handler's to say.
	refreshRoute = regexp.MustCompile(`^/api/work/refresh$`)
	startRoute   = regexp.MustCompile(`^/api/work/([A-Z][A-Z0-9]{1,9}-[0-9]{1,9})/start$`)
	runRoute     = regexp.MustCompile(`^/api/runs/([A-Z][A-Z0-9]{1,9}-[0-9]{1,9})/` +
		`(answers|pr|cancel|retry|fix-checks|jira|cleanup|resume)$`)
)

// Route is one of Open work's POST routes: the action and the run's key.
type Route struct {
	Action string
	Key    string
}

// RouteOf is the POST route a path names, false for a path that is none of them.
func RouteOf(urlPath string) (Route, bool) {
	if refreshRoute.MatchString(urlPath) {
		return Route{Action: ActionRefresh, Key: ""}, true
	}

	if match := startRoute.FindStringSubmatch(urlPath); match != nil {
		return Route{Action: ActionStart, Key: match[1]}, true
	}

	if match := runRoute.FindStringSubmatch(urlPath); match != nil {
		return Route{Action: match[2], Key: match[1]}, true
	}

	return Route{Action: "", Key: ""}, false
}

// On is whether Open work is on: the Atlassian module is enabled (never while setup) and at
// least one project is listed. While it is off its routes are not there.
func (w *Work) On() bool { return w.on() }

// Handle answers one POST route with its body (a JSON object, {} for anything else).
func (w *Work) Handle(route Route, body *js.Object) (Reply, error) {
	if route.Action == ActionFixChecks {
		return w.FixChecks(route.Key)
	}

	handle, known := w.handlers()[route.Action]
	if !known {
		return notFound(jira.Off), nil
	}

	return handle(route.Key, body), nil
}

// handlers are the POST routes' answers, by action.
func (w *Work) handlers() map[string]func(key string, body *js.Object) Reply {
	return map[string]func(key string, body *js.Object) Reply{
		ActionRefresh: func(string, *js.Object) Reply {
			w.Refresh()

			return ok()
		},
		ActionStart: w.Start,
		ActionAnswers: func(key string, body *js.Object) Reply {
			return w.Answer(key, field(body, "answers"))
		},
		ActionPR:     func(key string, _ *js.Object) Reply { return w.OpenPR(key) },
		ActionCancel: func(key string, _ *js.Object) Reply { return w.Cancel(key) },
		ActionJira: func(key string, body *js.Object) Reply {
			return w.MoveTicket(key, field(body, "status"))
		},
		ActionCleanup: func(key string, _ *js.Object) Reply { return w.Cleanup(key) },
		ActionResume: func(key string, body *js.Object) Reply {
			repo, named := resumeRepo(body)
			if !named {
				return badRequest(repoNotName)
			}

			return w.Resume(key, repo)
		},
		ActionRetry: func(key string, _ *js.Object) Reply { return w.Retry(key) },
	}
}
