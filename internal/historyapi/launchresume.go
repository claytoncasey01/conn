package historyapi

import (
	"context"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/repos"
)

// ResumeAtLaunch is the session that resumes, when Conn launches again, the History session
// sessionID that ran in folder at the last quit, and true; or false when it may not be resumed.
// The archive's own row is what is resumed: the session is the one RESUME opens (ResumeSpec over
// cmux.ResumeArgs, in the row's folder, under the row's name), and only when every check RESUME
// makes passes, the id is a UUID, the row's folder is folder, and that folder's repository is on
// the allowlist. A dry run resumes none.
func (a *API) ResumeAtLaunch(ctx context.Context, sessionID, folder string) (pty.Spec, bool) {
	if a.deps.Dry() {
		return pty.Spec{}, false
	}

	if !cmux.IsSessionID(sessionID) {
		return pty.Spec{}, false
	}

	info, found, err := a.deps.DB.ResumeInfo(ctx, sessionID)
	if err != nil {
		return pty.Spec{}, false
	}

	if a.resumable(ctx, sessionID, info, found) != nil {
		return pty.Spec{}, false
	}

	if info.Cwd != folder {
		return pty.Spec{}, false
	}

	if !a.allowedFolder(info.Cwd) {
		return pty.Spec{}, false
	}

	var name *string
	if info.Named {
		name = &info.Name
	}

	args, err := cmux.ResumeArgs(sessionID, name)
	if err != nil {
		return pty.Spec{}, false
	}

	return ResumeSpec(a.deps.Claude, args, info.Cwd, cmux.WorkspaceName(info.Name, sessionID)),
		true
}

// allowedFolder is whether folder's repository is on the allowlist now.
func (a *API) allowedFolder(folder string) bool {
	if a.deps.RepoOf == nil {
		return false
	}

	if a.deps.Allow == nil {
		return false
	}

	return repos.Allowed(a.deps.RepoOf(folder), a.deps.Allow())
}
