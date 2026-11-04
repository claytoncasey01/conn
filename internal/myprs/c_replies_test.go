package myprs_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The session's proposed replies (myPrs.test.ts, "The session's proposed replies").

// cReplyThreadIDs are the thread ids of proposals.
func cReplyThreadIDs(proposals []*myprs.Proposal) []string {
	out := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		out = append(out, proposal.ThreadID)
	}

	return out
}

// cRowThreads are a row's threads as `id outdated=… text=… state=…`, null for no proposal.
func cRowThreads(row *myprs.Row) []string {
	out := make([]string, 0, len(row.Threads))

	for _, one := range row.Threads {
		text, state := "null", "null"
		if one.Proposal != nil {
			text, state = one.Proposal.Text, one.Proposal.State
		}

		out = append(out, one.ID+" outdated="+strconv.FormatBool(one.Outdated)+" text="+text+
			" state="+state)
	}

	return out
}

func cBodyText(entries ...cReplyEntry) *string { return new(cBody(entries...)) }

func TestCNewestLaunchsFileBecomesProposals(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{file: cBodyText(
		cReplyEntry{ThreadID: "t1", Text: "Fixed."}, cReplyEntry{ThreadID: "t2", Text: ""},
	)})
	round.poll(0)
	cSame(t, "proposals", round.proposals(),
		[]*myprs.Proposal{cProposed("t1", "Fixed."), cProposed("t2", "")})
	cSame(t, "reads", round.readCalls(), []string{cRepliesPath})
}

func TestCDisarmedRoundWithAnOpenSessionHasItsRepliesTaken(t *testing.T) {
	t.Parallel()

	reply := "Not acted on: the input is checked a level up."
	round := cReplying(t, cReplyOpts{
		pull: []func(pull *myprs.PR){cHead("old")},
		file: cBodyText(cReplyEntry{ThreadID: "t1", Text: reply}),
	})
	round.setSessions(cSession("self-repo-7-2", cCloneWorktree, "idle"))
	// First seen idle.
	round.poll(0)
	round.world.Advance(cMinuteMs)
	tick(round.myPrs)
	// Quiet a minute after its replies file, the head unchanged: disarmed.
	round.poll(0)
	cSame(t, "armed", round.saved()[0].Armed, false)
	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{cProposed("t1", reply)})
	cSame(t, "row threads", cRowThreads(round.row()),
		[]string{
			"t1 outdated=false text=" + reply + " state=proposed",
			"t2 outdated=false text=null state=null",
		})
	cSame(t, "status", round.status(), "idle")
	cSame(t, "asks", len(round.asks()), 0)
}

func TestCFileNotNewerThanTheLaunchIsNeverRead(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{at: cAt(worldT0 - hourMs)})
	round.poll(0)
	cSame(t, "reads", len(round.readCalls()), 0)
	cSame(t, "proposals", len(round.proposals()), 0)

	newer := cReplying(t, cReplyOpts{
		at: cAt(worldT0 - hourMs + 1),
		launch: []func(launch *myprs.Launch){func(launch *myprs.Launch) {
			launch.At = worldT0 - hourMs
		}},
	})
	newer.poll(0)
	cSame(t, "reads of a newer file", len(newer.readCalls()), 1)
}

func TestCFileWithNoMtimeIsNeverRead(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{})
	round.world.Change(func(w *World) { delete(w.Replies, cCloneWorktree) })
	round.poll(0)
	cSame(t, "reads", len(round.readCalls()), 0)
}

func TestCWorktreeOutsideTheDataFolderIsNeverRead(t *testing.T) {
	t.Parallel()

	const elsewhere = "/wt/elsewhere"

	round := cReplying(t, cReplyOpts{launch: []func(launch *myprs.Launch){
		func(launch *myprs.Launch) { launch.Worktree = elsewhere },
	}})
	round.world.Change(func(w *World) {
		w.Replies[elsewhere] = worldT0
		w.Files[elsewhere+"/.conn/replies.json"] = cBody(cReplyEntry{ThreadID: "t1", Text: "x"})
	})
	round.poll(0)
	cSame(t, "reads", len(round.readCalls()), 0)
	cSame(t, "proposals", len(round.proposals()), 0)
}

func TestCOnlyTheNewestLaunchsFileIsRead(t *testing.T) {
	t.Parallel()

	const old = "/data/myprs/org-repo-7/self-repo-7-old"

	round := cReplying(t, cReplyOpts{extra: []func(launch *myprs.Launch){
		func(launch *myprs.Launch) { launch.Worktree = old },
	}})
	round.world.Change(func(w *World) {
		w.Replies[old] = worldT0
		w.Files[old+"/.conn/replies.json"] = cBody(
			cReplyEntry{ThreadID: "t2", Text: "from the old round"})
	})
	round.poll(0)
	cSame(t, "reads", round.readCalls(), []string{cRepliesPath})
	cSame(t, "proposals", cReplyThreadIDs(round.proposals()), []string{"t1"})
}

func TestCRepliesFileOver256KBIsIgnoredWhole(t *testing.T) {
	t.Parallel()

	const fileBytesMax = 256 * 1024

	just := cBody(cReplyEntry{ThreadID: "t1", Text: "ok"})
	tests := []struct {
		name string
		file string
		want []string
	}{
		{name: "over the cap", file: just + strings.Repeat(" ", fileBytesMax), want: []string{}},
		{
			name: "exactly at the cap", file: just + strings.Repeat(" ", fileBytesMax-len(just)),
			want: []string{"t1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			round := cReplying(t, cReplyOpts{file: new(tt.file)})
			round.poll(0)
			cSame(t, "proposals", cReplyThreadIDs(round.proposals()), tt.want)
		})
	}
}

func TestCBadOrMissingRepliesFileChangesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		file   string
		noFile bool
	}{
		{name: "not JSON", file: "{nope", noFile: false},
		{name: "an array", file: "[]", noFile: false},
		{name: "a string", file: `"x"`, noFile: false},
		{name: "null", file: "null", noFile: false},
		{name: "threads not a list", file: `{"threads":"x"}`, noFile: false},
		{name: "no file", file: "", noFile: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kept := []*myprs.Proposal{cProposed("t2", "kept")}
			round := cReplying(t, cReplyOpts{
				file: new(tt.file), noFile: tt.noFile,
				proposals: []*myprs.Proposal{cProposed("t2", "kept")},
			})
			round.poll(0)
			cSame(t, "proposals", round.proposals(), kept)
			cGone(t, round.file)
			round.poll(pollMs)
			cSame(t, "written", cExists(round.file), false)
			cSame(t, "error", round.myPrs.Snapshot().Error, (*string)(nil))
		})
	}
}

func TestCEditedProposalKeepsItsText(t *testing.T) {
	t.Parallel()

	edited := cProp("t1", "edited by Jane", "skipped", worldT0-hourMs)
	round := cReplying(t, cReplyOpts{
		file:      cBodyText(cReplyEntry{ThreadID: "t1", Text: "from the file"}),
		proposals: []*myprs.Proposal{cProp("t1", "edited by Jane", "skipped", worldT0-hourMs)},
	})
	round.poll(0)
	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{edited})
	// A second read after a new proposal was added keeps both.
	round.world.Change(func(w *World) {
		w.Files[cRepliesPath] = cBody(cReplyEntry{ThreadID: "t1", Text: "from the file"},
			cReplyEntry{ThreadID: "t2", Text: "new"})
	})
	round.poll(pollMs)
	cSame(t, "proposals after", round.proposals(),
		[]*myprs.Proposal{edited, cProposed("t2", "new")})
}

func TestCNewerRoundsFileReplacesOnlyUnpostedReplies(t *testing.T) {
	t.Parallel()

	const old = worldT0 - 3*hourMs

	failed := cProp("t2", "old 2", "failed", old)
	failed.Error = new("gh: HTTP 502")
	failed.TriedAt = cAt(old + cMinuteMs)
	ids := []string{"t1", "t2", "t3", "t4", "t5"}
	entries := make([]cReplyEntry, 0, len(ids))

	for _, id := range ids {
		entries = append(entries, cReplyEntry{ThreadID: id, Text: "new " + id})
	}

	round := cReplying(t, cReplyOpts{
		threads: ids, file: cBodyText(entries...),
		proposals: []*myprs.Proposal{
			cProp("t1", "old 1", "proposed", old), failed, cProp("t3", "old 3", "skipped", old),
			cProp("t4", "old 4", "replied", old), cProp("t5", "old 5", "posted", old),
		},
	})
	round.poll(0)
	// A failed one keeps the time of its try: that reply may be on GitHub, and Jane may still
	// post its text.
	retried := cProposed("t2", "new t2")
	retried.TriedAt = cAt(old + cMinuteMs)
	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{
		cProposed("t1", "new t1"), retried, cProposed("t3", "new t3"),
		cProp("t4", "old 4", "replied", old), cProp("t5", "old 5", "posted", old),
	})
	// And the same round's file read again changes nothing.
	cGone(t, round.file)
	round.poll(pollMs)
	cSame(t, "written", cExists(round.file), false)
}

func TestCFailedResolveOnlyClickDoesNotBlockTheRoundsReply(t *testing.T) {
	t.Parallel()

	click := cProp("t1", "", "failed", 0)
	click.Error = new("gh: HTTP 502")
	round := cReplying(t, cReplyOpts{
		proposals: []*myprs.Proposal{click},
		file:      cBodyText(cReplyEntry{ThreadID: "t1", Text: "Fixed."}),
	})
	round.poll(0)
	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{cProposed("t1", "Fixed.")})
}

func TestCThreadResolvedElsewhereDropsItsProposal(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{
		noFile: true, threads: []string{"t1", "t2", "t3", "t4"},
		proposals: []*myprs.Proposal{
			cProposed("t1", "a"), cProp("t2", "b", "replied", worldT0-hourMs),
			cProp("t3", "c", "failed", worldT0-hourMs), cProp("t4", "d", "posted", worldT0-hourMs),
		},
		pull: []func(pull *myprs.PR){func(pull *myprs.PR) {
			pull.Threads = []myprs.Thread{
				thread(func(one *myprs.Thread) { one.ID, one.Resolved = "t1", true }),
				thread(func(one *myprs.Thread) { one.ID, one.Outdated = "t2", true }),
				thread(func(one *myprs.Thread) { one.ID = "t3" }),
				thread(func(one *myprs.Thread) { one.ID = "t4" }),
			}
		}},
	})
	round.poll(0)
	cSame(t, "proposals", cReplyThreadIDs(round.proposals()), []string{"t2", "t3", "t4"})
}

func cThreadReply(t *testing.T, round *cRound, threadID, body string) {
	t.Helper()

	answer := round.myPrs.Thread(context.Background(), cRepo, cNumber, threadID, body)
	cSame(t, "code", answer.Code, 200)
	cSame(t, "body", js.Stringify(answer.Body), `{"ok":true}`)
}

func TestCFixedThreadsReplyIsTakenListedOutdatedAndPosted(t *testing.T) {
	t.Parallel()

	const (
		fixed = "PRRT_fixed00"
		wont  = "PRRT_wontfix"
	)

	fixedText, wontText := "Fixed in abc1234.", "Not acted on: checked a level up."
	round := cReplying(t, cReplyOpts{
		pull: []func(pull *myprs.PR){func(pull *myprs.PR) {
			pull.Threads = []myprs.Thread{
				thread(func(one *myprs.Thread) {
					one.ID, one.By, one.Outdated = fixed, greptileBot, true
					one.At = worldT0 - 2*hourMs
				}),
				thread(func(one *myprs.Thread) {
					one.ID, one.By, one.At = wont, greptileBot, worldT0-hourMs
				}),
			}
		}},
		file: cBodyText(cReplyEntry{ThreadID: fixed, Text: fixedText},
			cReplyEntry{ThreadID: wont, Text: wontText}),
	})
	round.poll(0)
	cSame(t, "proposals", round.proposals(),
		[]*myprs.Proposal{cProposed(fixed, fixedText), cProposed(wont, wontText)})

	row := round.row()
	// The current thread first, then the outdated one, each with its proposal.
	cSame(t, "row threads", cRowThreads(row), []string{
		wont + " outdated=false text=" + wontText + " state=proposed",
		fixed + " outdated=true text=" + fixedText + " state=proposed",
	})
	// The open-findings count still leaves the outdated thread out.
	cSame(t, "open bot", row.OpenBot, 1)
	cSame(t, "outdated", row.Outdated, 1)

	writes := round.threadSide()
	cThreadReply(t, round, fixed, fixedText)
	cSame(t, "writes", writes(), []string{"reply " + fixed + " " + fixedText, "resolve " + fixed})

	state := ""

	for _, proposal := range round.proposals() {
		if proposal.ThreadID == fixed {
			state = proposal.State
		}
	}

	cSame(t, "state", state, "posted")
}

func TestCRepliedProposalWhoseResolveFailedCanStillBeResolved(t *testing.T) {
	t.Parallel()

	const threadID = "PRRT_replied1"

	replied := cProp(threadID, "Fixed in abc1234.", "replied", worldT0-hourMs)
	replied.Error = new("the reply is posted, but the thread was not resolved: gh: HTTP 502")
	round := cReplying(t, cReplyOpts{
		noFile: true, proposals: []*myprs.Proposal{replied},
		pull: []func(pull *myprs.PR){func(pull *myprs.PR) {
			pull.Threads = []myprs.Thread{
				thread(func(one *myprs.Thread) { one.ID, one.Outdated = threadID, true }),
			}
		}},
	})
	round.poll(0)
	cSame(t, "state", round.proposals()[0].State, "replied")
	cSame(t, "row threads", cRowThreads(round.row()),
		[]string{threadID + " outdated=true text=Fixed in abc1234. state=replied"})

	writes := round.threadSide()
	cThreadReply(t, round, threadID, "Fixed in abc1234.")
	cSame(t, "writes", writes(), []string{"resolve " + threadID})
}

func TestCThreadGoneFromAWholeListDropsItsProposal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		partial bool
		want    []string
	}{
		{name: "a whole list", partial: false, want: []string{}},
		{name: "a cut-off list", partial: true, want: []string{"gone"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			round := cReplying(t, cReplyOpts{
				noFile: true, proposals: []*myprs.Proposal{cProposed("gone", "x")},
				threads: []string{"t1"},
				pull: []func(pull *myprs.PR){
					func(pull *myprs.PR) { pull.Partial = tt.partial },
				},
			})
			round.poll(0)
			cSame(t, "proposals", cReplyThreadIDs(round.proposals()), tt.want)
		})
	}
}

func TestCPostedProposalOfAnOpenThreadIsKept(t *testing.T) {
	t.Parallel()

	kept := cProp("t1", "posted text", "posted", worldT0-hourMs)
	round := cReplying(t, cReplyOpts{
		file:      cBodyText(cReplyEntry{ThreadID: "t1", Text: "again"}),
		proposals: []*myprs.Proposal{cProp("t1", "posted text", "posted", worldT0-hourMs)},
	})
	round.poll(0)
	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{kept})
}

func TestCIDThatIsNotAnOpenThreadIsNotTaken(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{
		file: cBodyText(cReplyEntry{ThreadID: "zzz", Text: "x"},
			cReplyEntry{ThreadID: "t1", Text: "y"}),
		pull: []func(pull *myprs.PR){func(pull *myprs.PR) {
			pull.Threads = []myprs.Thread{
				thread(func(one *myprs.Thread) { one.ID = "t1" }),
				thread(func(one *myprs.Thread) { one.ID, one.Resolved = "t2", true }),
			}
		}},
	})
	round.poll(0)
	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{cProposed("t1", "y")})
}

func TestCEntriesNotTakenAreCountedOnTheRow(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{
		threads: []string{"t1", "t2", "t3"},
		file: cBodyText(
			cReplyEntry{ThreadID: "t1", Text: "Fixed."}, cReplyEntry{ThreadID: "zzz", Text: "x"},
			cReplyEntry{ThreadID: "t1", Text: "again"},
			cReplyEntry{ThreadID: "t2", Text: strings.Repeat("y", 2001)},
			cReplyEntry{ThreadID: "t3", Text: "Fine."},
		),
	})
	round.poll(0)
	cSame(t, "proposals", cReplyThreadIDs(round.proposals()), []string{"t1", "t3"})
	cSame(t, "ignored", round.row().IgnoredReplies, 3)
	// Jane posts and resolves t1: the next read of the same file finds it resolved, and that is
	// not one more ignored entry.
	next := *round.cur
	next.Threads = []myprs.Thread{
		thread(func(one *myprs.Thread) { one.ID, one.Resolved = "t1", true }),
		thread(func(one *myprs.Thread) { one.ID = "t2" }),
		thread(func(one *myprs.Thread) { one.ID = "t3" }),
	}
	round.setPR(&next)
	round.poll(pollMs)
	cSame(t, "ignored after a resolve", round.row().IgnoredReplies, 3)
	// A file that is no longer the newest round's (older than its launch) is not read, and its
	// count goes with it.
	round.world.Change(func(w *World) { w.Replies[cCloneWorktree] = worldT0 - 2*hourMs })
	round.poll(pollMs)
	cSame(t, "ignored of an older file", round.row().IgnoredReplies, 0)
}

func TestCFileWhoseEveryEntryWasTakenShowsNoIgnoredCount(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{})
	round.poll(0)
	cSame(t, "ignored", round.row().IgnoredReplies, 0)
}

func TestCNothingIsSavedWhenTheProposalsDidNotChange(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{})
	round.poll(0)
	cSame(t, "proposals", len(round.proposals()), 1)
	cGone(t, round.file)
	round.poll(pollMs)
	cSame(t, "written", cExists(round.file), false)
}

func TestCPRNotInTheOpenListIsNotRead(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{proposals: []*myprs.Proposal{cProposed("t1", "x")}})
	round.setPR(pr(func(pull *myprs.PR) {
		pull.Number = 8
		pull.URL = "https://github.com/org/repo/pull/8"
	}))
	round.world.Change(func(w *World) { w.ReadCalls = nil })
	round.poll(pollMs)
	cSame(t, "reads", len(round.readCalls()), 0)
	cSame(t, "proposals", cReplyThreadIDs(round.proposals()), []string{"t1"})
}

func TestCPRWithNoLaunchIsNotRead(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr(cThreads("t1")))
	myPrs, _ := world.New()
	myPrs.Refresh()
	tick(myPrs)
	world.Change(func(w *World) { cSame(t, "reads", len(w.ReadCalls), 0) })
}

func TestCRowJoinsItsOpenThreadsWithTheStoredProposals(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "myprs.json")
	seeded := myprs.LoadFile(file)
	myprs.TrackOf(seeded, myprs.EntryKey(cRepo, cNumber), worldT0).Proposals = []*myprs.Proposal{
		cProp("t1", "Fixed.", "failed", worldT0-hourMs), cProposed("t9", "stale"),
	}
	cSave(t, file, seeded)

	_, myPrs := cRestart(t, file, pr(func(pull *myprs.PR) {
		pull.Threads = []myprs.Thread{
			thread(func(one *myprs.Thread) { one.ID, one.At = "t2", worldT0 }),
			thread(func(one *myprs.Thread) { one.ID, one.At = "t1", worldT0-hourMs }),
			thread(func(one *myprs.Thread) { one.ID, one.Resolved = "t3", true }),
		}
	}))
	tick(myPrs)
	cSame(t, "row threads", cRowThreads(myPrs.Snapshot().Rows[0]),
		[]string{
			"t1 outdated=false text=Fixed. state=failed",
			"t2 outdated=false text=null state=null",
		})
}
