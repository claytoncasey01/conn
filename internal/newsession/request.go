package newsession

import (
	"fmt"
	"strings"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/repos"
)

const (
	// keyRepo and keyName are the only keys a start's body may hold.
	keyRepo = "repo"
	keyName = "name"
	// maxNameBytes bounds the name a request carries, before it is made safe: a name is a few
	// words, and cmux.WorkspaceName keeps at most 64 characters of it.
	maxNameBytes = 256
	// flagSessionID and flagName are claude's options for the conversation's id and its name.
	flagSessionID = "--session-id"
	flagName      = "--name"
)

// startRequest is what a start asks for once it is read: an allowed-looking repository name,
// and the session's name made safe (cmux.WorkspaceName), "" for none.
type startRequest struct {
	Repo string
	Name string
}

// parseRequest reads a start's body: an object with a string "repo" and, optionally, a string
// "name". It is the request and every problem with the body, one sentence each; a request with a
// problem is not used. Nothing in the body is a path, a flag or a command: the repository is only
// a key to the allowlist, and the name reaches claude only as safeName makes it.
func parseRequest(body *js.Object) (startRequest, []string) {
	problems := []string{}

	for _, key := range body.Keys() {
		if key == keyRepo {
			continue
		}

		if key == keyName {
			continue
		}

		problems = append(problems, fmt.Sprintf("%q is not a field of a new session: send only "+
			"repo and name.", js.Cut(key, maxNameBytes)))
	}

	repo, repoProblems := repoOf(body)
	problems = append(problems, repoProblems...)

	name, nameProblems := nameOf(body)
	problems = append(problems, nameProblems...)

	return startRequest{Repo: repo, Name: name}, problems
}

// repoOf is the body's repository name, or the problem with it.
func repoOf(body *js.Object) (string, []string) {
	raw, present := body.Get(keyRepo)
	if !present {
		return "", []string{"Choose a repository."}
	}

	repo, isText := raw.(string)
	if !isText {
		return "", []string{"The repository must be a name such as owner/name."}
	}

	if !repos.IsName(repo) {
		return "", []string{"The repository must be a name such as owner/name."}
	}

	return repo, nil
}

// nameOf is the body's name made safe, "" when it has none (absent, null or empty), or the
// problem with it.
func nameOf(body *js.Object) (string, []string) {
	raw, present := body.Get(keyName)
	if !present {
		return "", nil
	}

	if raw == nil {
		return "", nil
	}

	name, isText := raw.(string)
	if !isText {
		return "", []string{"The name must be text."}
	}

	if name == "" {
		return "", nil
	}

	safe, err := safeName(name)
	if err != nil {
		return "", []string{err.Error()}
	}

	return safe, nil
}

// safeName is name as claude's --name and the session list get it: checked by launch's rule for
// a session's name (no control, bidi or invisible character, UTF-8), at most maxNameBytes bytes,
// then reduced to [A-Za-z0-9._-] by cmux.WorkspaceName. A name with nothing left once reduced is
// refused, rather than replaced by something the person did not type.
func safeName(name string) (string, error) {
	if len(name) > maxNameBytes {
		return "", fmt.Errorf("%w: the name is %d bytes, more than %d", launch.ErrInvalidSession,
			len(name), maxNameBytes)
	}

	err := launch.CheckName(name)
	if err != nil {
		return "", fmt.Errorf("checking the name: %w", err)
	}

	safe := cmux.WorkspaceName(name, "")
	if safe == "" {
		return "", fmt.Errorf("%w: the name must hold a letter or a digit (A-Z, a-z, 0-9)",
			launch.ErrInvalidSession)
	}

	return safe, nil
}

// specOf is the session a start opens: claude, as CONN_CLAUDE names it, on the conversation
// sessionID (a UUID Conn chose), named when the request names it, in folder (the clone's real
// path), under the request's name or else the repository's. Nothing else from the request is in
// it. sessionID must be a UUID: anything else is a program error.
func specOf(claude, sessionID, folder string, request startRequest) pty.Spec {
	if !cmux.IsSessionID(sessionID) {
		panic(fmt.Sprintf("newsession: the conversation id %q is not a UUID", sessionID))
	}

	argv := []string{claude, flagSessionID, sessionID}
	name := request.Name

	if name != "" {
		argv = append(argv, flagName, name)
	}

	if name == "" {
		_, repoName, _ := strings.Cut(request.Repo, "/")
		name = cmux.WorkspaceName(repoName, sessionID)
	}

	return pty.Spec{Argv: argv, Dir: folder, Env: nil, Name: name}
}
