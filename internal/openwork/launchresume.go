package openwork

import (
	"github.com/claytoncasey01/conn/internal/pty"
)

// ResumeAtLaunch is the session that resumes, when Conn launches again, the build session that
// ran in worktree for the run key at the last quit, and true; or false when this module would not
// open it now. Only the run's own record is read: the session is the one RESUME SESSION opens
// (ResumeSpec), and only when every check RESUME SESSION makes passes (Open work is on, the run
// is building or has its PR open and nothing else changes it, worktree is one of its session
// slots, launched and not cleaned up, the worktree exists, its repository is still allowed and
// cloned, and no session runs there), and the slot's own Claude session id is a UUID: at launch
// there is no falling back to the folder's last conversation. A session it gives starts the gap
// a reopen leaves, so a RESUME SESSION before the session shows up opens no second one.
func (w *Work) ResumeAtLaunch(key, worktree string) (pty.Spec, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.deps.Dry {
		return pty.Spec{}, false
	}

	if !w.on() {
		return pty.Spec{}, false
	}

	record, found := w.runs.Get(key)
	if !found {
		return pty.Spec{}, false
	}

	_, refused := w.resumeRefusal(record)
	if refused {
		return pty.Spec{}, false
	}

	slot, found := w.slotIn(record, worktree)
	if !found {
		return pty.Spec{}, false
	}

	if !w.slotResumable(record, slot) {
		return pty.Spec{}, false
	}

	spec, _ := ResumeSpec(w.deps.Claude, record, slot)
	w.resumedAt[slotKey(record, slot)] = w.now()

	return spec, true
}

// slotIn is the session slot of record whose worktree is worktree.
func (w *Work) slotIn(record *Record, worktree string) (*Leg, bool) {
	for _, slot := range w.sessionSlots(record) {
		if slot.Worktree == worktree {
			return slot, true
		}
	}

	return nil, false
}

// slotResumable is whether slot's session may be resumed at launch: launched and not cleaned
// up, refused by none of the checks a reopen makes, and with a session id that is a UUID.
func (w *Work) slotResumable(record *Record, slot *Leg) bool {
	if slot.cleaned() {
		return false
	}

	if !launched(record, slot) {
		return false
	}

	refusal, _ := w.slotLiveRefusal(record, slot)
	if refusal != "" {
		return false
	}

	return sessionUUID.MatchString(sessionText(slot.ClaudeSessionID))
}
