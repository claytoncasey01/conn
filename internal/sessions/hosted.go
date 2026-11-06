package sessions

import (
	"context"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/snapshot"
)

// Hosted sessions: the programs Conn runs on terminals of its own (Deps.Host). Each is a row of
// the session list under the id the host issued, running or ended, for as long as the host
// keeps it. A hosted claude registers like any other; its registry entry is matched to it by the
// process id the host started, an exact lookup in the host's own record, and from then on the
// entry's fields, transcripts and asks are the row's. Nothing a process prints or holds in its
// environment can make a session hosted, or a hosted one a cmux one.

// hostedState is what the store knows of the host's sessions, as the last tick read them.
type hostedState struct {
	// live is every session the host holds, running or ended, oldest first.
	live []pty.Info
	// byPID is the running ones by process id.
	byPID map[float64]pty.ID
	// seen is what live said last, so a change moves the snapshot's version.
	seen string
	// lastRows is the row each registered hosted session last showed, so the row stays as it
	// was, ended, once the session's registry entry has gone. Only sessions the host still
	// holds are kept: at most its own bound of ended sessions.
	lastRows map[pty.ID]snapshot.Detail
}

func newHostedState() hostedState {
	return hostedState{
		live: nil, byPID: map[float64]pty.ID{}, seen: "",
		lastRows: map[pty.ID]snapshot.Detail{},
	}
}

// hostedRow is one hosted session as the routes act on it: what the host says, and the registry
// session it is, when it has registered and still runs.
type hostedRow struct {
	info pty.Info
	// key is the session's registry key (its pid), "" with no session.
	key     string
	session *Session
}

// refreshHosted reads the host's sessions, and is true when they changed: one started, ended or
// was forgotten.
func (s *store) refreshHosted() bool {
	if s.deps.Host == nil {
		return false
	}

	live := s.deps.Host.Live()
	present := map[pty.ID]bool{}
	byPID := map[float64]pty.ID{}

	var seen strings.Builder

	for _, info := range live {
		present[info.ID] = true
		seen.WriteString(string(info.ID) + exitText(info.ExitCode) + nul)

		if info.ExitCode == nil {
			byPID[float64(info.PID)] = info.ID
		}
	}

	for id := range s.hosts.lastRows {
		if !present[id] {
			delete(s.hosts.lastRows, id)
		}
	}

	changed := seen.String() != s.hosts.seen
	s.hosts.live = live
	s.hosts.byPID = byPID
	s.hosts.seen = seen.String()

	return changed
}

func exitText(code *int) string {
	if code == nil {
		return "running"
	}

	return strconv.Itoa(*code)
}

// markHosted marks each registry session whose process is a running hosted program as that
// hosted session. It is never looked up as a cmux one, and shares no cmux workspace.
func (s *store) markHosted() {
	for _, key := range s.order {
		session := s.sessions[key]
		if session.hosted != "" {
			continue
		}

		hostedID, hosted := s.hosts.byPID[session.reg.PID]
		if !hosted {
			continue
		}

		session.hosted = hostedID
	}
}

// rowID is the id the page names a registry session by: the host's for a hosted one.
func (s *store) rowID(key string) string {
	hostedID := s.sessions[key].hosted
	if hostedID != "" {
		return string(hostedID)
	}

	return key
}

// hostedRow is the hosted session the host issued hostedID for, and false when it holds none.
func (s *store) hostedRow(hostedID pty.ID) (hostedRow, bool) {
	for _, info := range s.hosts.live {
		if info.ID == hostedID {
			return s.rowOf(info), true
		}
	}

	return hostedRow{info: pty.Info{}, key: "", session: nil}, false
}

// rowOf is a hosted session with its registry session, while it runs and has registered.
func (s *store) rowOf(info pty.Info) hostedRow {
	row := hostedRow{info: info, key: "", session: nil}
	if info.ExitCode != nil {
		return row
	}

	key := js.Number(float64(info.PID))

	session, found := s.sessions[key]
	if !found {
		return row
	}

	if session.hosted != info.ID {
		return row
	}

	row.key = key
	row.session = session

	return row
}

// hostedDetail is a hosted session's row: its registry session's while it runs, else what the
// host says, and once it has ended the row it last showed, which can no longer be replied to.
// Conn started it, so it is never hidden for being in a repository off the allowlist.
func (s *store) hostedDetail(
	ctx context.Context, info pty.Info, now float64, runKeys map[string]bool,
) snapshot.Detail {
	row := s.rowOf(info)
	if row.session != nil {
		detail := s.detail(ctx, row.session, now, runKeys, rowFacts{
			id: string(info.ID), canReply: true, ask: s.askOf(row.key, row.session),
			hosted: true, exitCode: nil,
		})
		s.hosts.lastRows[info.ID] = detail

		return detail
	}

	if info.ExitCode == nil {
		return s.unregisteredDetail(ctx, info, now, runKeys)
	}

	last, had := s.hosts.lastRows[info.ID]
	if !had {
		last = s.unregisteredDetail(ctx, info, now, runKeys)
	}

	return endedDetail(last, *info.ExitCode)
}

// unregisteredDetail is the row of a hosted session with no registry entry (yet): what the host
// says of it, idle, with nothing read from a transcript or a screen.
func (s *store) unregisteredDetail(
	ctx context.Context, info pty.Info, now float64, runKeys map[string]bool,
) snapshot.Detail {
	name := info.Name
	started := float64(info.StartedAt.UnixMilli())
	session := newSession(Reg{
		PID: float64(info.PID), SessionID: "", Cwd: info.Dir, Name: &name, Status: regIdle,
		WaitingFor: nil, StartedAt: &started, UpdatedAt: &started, StatusUpdatedAt: nil,
	})
	session.hosted = info.ID

	return s.detail(ctx, session, now, runKeys, rowFacts{
		id: string(info.ID), canReply: true, ask: nil, hosted: true, exitCode: nil,
	})
}

// endedDetail is a row as an ended hosted session shows it: idle, asking nothing, with no reply
// possible, and its exit code.
func endedDetail(last snapshot.Detail, exitCode int) snapshot.Detail {
	ended := last
	ended.Status = snapshot.StatusIdle
	ended.WaitingFor = nil
	ended.Ask = nil
	ended.CanReply = false
	ended.ExitCode = &exitCode

	return ended
}

// readHostedAsks reads the screen of every waiting hosted session from the host's model of its
// terminal, and keeps what it asks, as a cmux screen read does. The model is in memory, so it is
// read on the loop, every time.
func (s *store) readHostedAsks(now float64) {
	if s.deps.Host == nil {
		return
	}

	for _, key := range s.order {
		session := s.sessions[key]
		if session.hosted == "" {
			continue
		}

		if session.reg.Status != regWaiting {
			continue
		}

		lines, _, err := s.deps.Host.ScreenLines(session.hosted, askLines)
		if err != nil {
			continue
		}

		stamp := statusStamp(session.reg)
		s.record(key, session, stamp, s.screenAsk(key, session, ask.ScreenText(lines), stamp),
			now)
	}
}
