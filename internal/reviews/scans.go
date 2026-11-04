package reviews

import (
	"errors"
	"math"
	"slices"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

// scanPlan is what one scan asks.
type scanPlan struct {
	// now is when the scan started: the schedule is read at this moment, so a scan that comes
	// late covers every step already due.
	now float64
	// current is the settings it was planned with.
	current settings.Settings
	// unmatched are PRs (keys) with no post yet whose scan is due.
	unmatched []string
	threads   []Thread
}

type unmatchedPR struct {
	key         string
	firstSeenAt float64
}

// plan is what the next scan would ask, or nil when nothing is due. It also restarts a PR's
// thread schedule when its head moved.
func (r *Reviews) plan(now float64, current settings.Settings) *scanPlan {
	if r.file == nil {
		return nil
	}

	if r.me == nil {
		return nil
	}

	unmatched, threads, changed := r.planCandidates(now, &current)

	if changed {
		r.save()
	}

	// The newest first, so a long list asks about the PRs most likely to have a fresh post.
	js.Sort(unmatched, func(first, second unmatchedPR) float64 {
		return second.firstSeenAt - first.firstSeenAt
	})

	keys := []string{}
	for _, item := range unmatched[:min(len(unmatched), unmatchedMax)] {
		keys = append(keys, item.key)
	}

	// With nothing to look for, a scan still runs now and then: a post is how a PR nobody
	// requested of me is found.
	if all(len(keys) == 0, len(threads) == 0, now < r.nextDiscovery) {
		return nil
	}

	r.nextDiscovery = now + r.deps.Config.ChannelScanMs

	return &scanPlan{now: now, current: current, unmatched: keys, threads: threads}
}

// planCandidates are the PRs a scan may ask about: those with no post yet whose scan is due,
// and the threads to read; and whether a thread schedule was restarted.
func (r *Reviews) planCandidates(
	now float64, current *settings.Settings,
) ([]unmatchedPR, []Thread, bool) {
	var (
		unmatched []unmatchedPR
		threads   []Thread
		changed   bool
	)

	for _, key := range r.prs.Keys() {
		pull, _ := r.prs.Get(key)
		if anyOf(pull.IsDraft, same(pull.Author, *r.me),
			!repos.Allowed(pull.Repo, current.Repos.Allow)) {
			continue
		}

		entry, tracked := r.file.PRs.Get(key)
		if !tracked {
			continue
		}

		if entry.Slack == nil {
			if ScanDue(entry.Scan, entry.FirstSeenAt, now) {
				unmatched = append(unmatched, unmatchedPR{
					key: key, firstSeenAt: entry.FirstSeenAt,
				})
			}

			continue
		}

		thread, moved := r.threadDue(key, pull, entry, now, current)
		changed = anyOf(changed, moved)

		if thread != nil {
			threads = append(threads, *thread)
		}
	}

	return unmatched, threads, changed
}

// threadDue is the thread to read for a PR I reviewed whose head then moved, with no ask yet,
// and whether its schedule was restarted. The schedule is measured from when the head moved, so
// an author who pushes and never asks cannot keep a scan firing. A post kept from before channels
// were chosen names no channel: it is taken to be the first chosen one.
func (r *Reviews) threadDue(
	key string, pull *PR, entry *Entry, now float64, current *settings.Settings,
) (*Thread, bool) {
	row := r.rowFor(pull, now, nil, SourcesOf(current))
	latest := mine(pull, *r.me)

	channel := ""
	if entry.Slack.Channel != nil {
		channel = *entry.Slack.Channel
	} else if len(current.Slack.Channels) > 0 {
		channel = current.Slack.Channels[0]
	}

	if anyOf(row == nil, latest == nil, channel == "") {
		return nil, false
	}

	if anyOf(row.State != StateWaiting, !row.Updated,
		!slices.Contains(current.Slack.Channels, channel)) {
		return nil, false
	}

	moved := false

	if entry.ThreadScan.HeadSha != pull.HeadSha {
		entry.ThreadScan = ThreadScan{Tries: 0, LastAt: now, HeadSha: pull.HeadSha}
		moved = true
	}

	// lastAt is that moment; a try only counts up.
	scan := Scan{Tries: entry.ThreadScan.Tries, LastAt: entry.ThreadScan.LastAt}
	if !ScanDue(scan, entry.ThreadScan.LastAt, now) {
		return nil, moved
	}

	return &Thread{
		PR: key, Channel: channel, TS: entry.Slack.TS, SinceMs: latest.SubmittedAt,
	}, moved
}

// scanSince is where a channel's scan reads from. Its cursor, except that when PRs still have no
// post it is pulled back to 15 minutes before the older of the cursor and the earliest of those
// PRs' first-seen times. Never later than the cursor, and never further back than the first-scan
// window. No cursor is the window.
func (r *Reviews) scanSince(plan *scanPlan, channel string) *string {
	if r.file == nil {
		return nil
	}

	cursor, found := r.file.Cursors.Get(channel)
	if !found {
		return nil
	}

	floor := plan.now - float64(r.deps.Config.FirstWindowDays*dayMs)
	first := math.Inf(1)

	for _, key := range plan.unmatched {
		entry, tracked := r.file.PRs.Get(key)
		if !tracked {
			continue
		}

		if entry.Slack == nil {
			first = math.Min(first, entry.FirstSeenAt)
		}
	}

	when := TSMs(cursor)
	if when < floor {
		since := MsTS(floor)

		return &since
	}

	if math.IsInf(first, 0) {
		return &cursor
	}

	from := math.Max(math.Min(when, first)-scanOverlapMs, floor)
	if from < when {
		since := MsTS(from)

		return &since
	}

	return &cursor
}

func (r *Reviews) scanInput(plan *scanPlan) ScanInput {
	channels := make([]ChannelRead, 0, len(plan.current.Slack.Channels))
	for _, name := range plan.current.Slack.Channels {
		channels = append(channels, ChannelRead{Name: name, SinceTS: r.scanSince(plan, name)})
	}

	allow := plan.current.Repos.Allow
	if allow == nil {
		allow = []string{}
	}

	return ScanInput{
		Channels: channels, Allow: allow, Unmatched: plan.unmatched, Threads: plan.threads,
	}
}

func (r *Reviews) scanOnce(plan *scanPlan, input ScanInput) {
	var (
		result ScanResult
		err    error
	)

	r.outside(func() { result, err = r.deps.Scan(r.ctx, input) })

	defer func() {
		r.scanning = false
		r.settle()
		r.bump()
	}()

	// Slack (or GitHub, or every channel) switched off while the run was out: what it read is
	// dropped, and nothing is recorded or moved for a module that is off now.
	current, _ := r.settings()
	if !r.channelsOn(&current) {
		return
	}

	if err != nil {
		r.scanFailed(plan, err)

		return
	}

	r.finishScan(plan, result)
	r.readFound()
}

// readFound reads from GitHub up to FetchPerScan of the PRs the scans named that are not known
// yet, the oldest finds first; the rest wait for the next scan. One counts only when GitHub has
// it open in an allowed repo: a message cannot invent a PR. A find whose read failed waits too;
// one GitHub has not got, or has closed, is dropped, and so is a draft or a PR of my own. The
// settings are read now, not as the scan found them.
func (r *Reviews) readFound() {
	file := r.file
	current, _ := r.settings()

	if anyOf(file == nil, !r.githubOn(&current), !current.Slack.Enabled, r.pausedUntil != nil) {
		return
	}

	allow := current.Repos.Allow
	keys := r.leadsToRead(file, &current)

	if len(keys) == 0 {
		return
	}

	parts := make([]KeyParts, 0, len(keys))

	for _, key := range keys {
		r.reading[key] = true
		found, _ := ParseKey(key)
		parts = append(parts, found)
	}

	var (
		read *js.Map[*PR]
		err  error
	)

	r.outside(func() { read, err = r.deps.Gh.Fetch(r.ctx, parts, nil) })

	for _, key := range keys {
		delete(r.reading, key)
	}

	if err != nil {
		if limit, isLimit := errors.AsType[*ghbudget.RateLimitError](err); isLimit {
			// The same pause a poll takes: nothing more is asked of GitHub until the reset.
			r.pauseFor(limit)

			return
		}

		r.logf("reviews: the PRs a scan found could not be read %s", message(err))
		r.record(moduleGitHub, false, "Reviews could not read the PRs a Slack scan found")

		return
	}

	r.record(moduleGitHub, true, "Reviews read the PRs a Slack scan found")
	r.trackFound(file, keys, read, allow)
	r.save()
}

// leadsToRead are the finds to read now: a lead counts only while the source that found it is
// on (a DM while the DM scan is, a post while its channel is chosen).
func (r *Reviews) leadsToRead(file *File, current *settings.Settings) []string {
	type lead struct {
		key string
		at  float64
	}

	var leads []lead

	for _, key := range file.Found.Keys() {
		found, _ := file.Found.Get(key)
		if r.reading[key] {
			continue
		}

		parts, _ := ParseKey(key)
		if !repos.Allowed(parts.Repo, current.Repos.Allow) {
			continue
		}

		if !r.sourceOn(found, current) {
			continue
		}

		leads = append(leads, lead{key: key, at: found.At})
	}

	js.Sort(leads, func(first, second lead) float64 { return first.at - second.at })

	chosen := leads[:min(len(leads), FetchPerScan)]

	keys := make([]string, 0, len(chosen))
	for _, item := range chosen {
		keys = append(keys, item.key)
	}

	return keys
}

func (r *Reviews) sourceOn(found *Found, current *settings.Settings) bool {
	if found.DM != nil {
		if r.dmsOn(current) {
			return true
		}
	}

	if found.Slack != nil {
		return slices.Contains(current.Slack.Channels, found.Slack.Channel)
	}

	return false
}

func (r *Reviews) trackFound(file *File, keys []string, read *js.Map[*PR], allow []string) {
	now := r.now()

	for _, key := range keys {
		pull, answered := read.Get(key)
		if !answered {
			continue
		}

		found, had := file.Found.Get(key)
		file.Found.Delete(key)

		if anyOf(!had, pull == nil) {
			continue
		}

		if anyOf(pull.Ended != nil, !repos.Allowed(pull.Repo, allow), pull.IsDraft) {
			continue
		}

		if r.me != nil {
			if same(pull.Author, *r.me) {
				continue
			}
		}

		r.track(key, pull, found, now)
	}
}

// track lists a PR GitHub has open, with what a scan found about it, from now on.
func (r *Reviews) track(key string, pull *PR, found *Found, now float64) {
	entry := TrackOf(r.file, key, now)
	entry.SeenAt = now
	entry.Ended = nil

	if found.Slack != nil {
		if entry.Slack == nil {
			channel := found.Slack.Channel
			entry.Slack = &SlackPost{
				TS: found.Slack.TS, Permalink: found.Slack.Permalink,
				PostedAt: TSMs(found.Slack.TS), Channel: &channel,
			}
		}
	}

	if found.DM != nil {
		if entry.DM == nil {
			entry.DM = &DMPost{
				TS: found.DM.FirstTS, Permalink: found.DM.Permalink,
				PostedAt: TSMs(found.DM.FirstTS),
			}
		}

		last := math.Max(valueOr(entry.DMLastAt, 0), TSMs(found.DM.LastTS))
		entry.DMLastAt = &last
	}

	if r.me != nil {
		if requestsMe(pull, *r.me) {
			entry.Requested = true
		}
	}

	r.prs.Set(key, pull)
	r.endedPrs.Delete(key)

	if r.polling {
		r.addedDuringPoll.Set(key, true)
	}
}

// remember keeps a find for a later read: a post and a DM about the same PR are kept together,
// the earliest of each.
func (r *Reviews) remember(key string, now float64, slack *FoundSlack, direct *FoundDM) {
	have, found := r.file.Found.Get(key)
	if !found {
		if r.file.Found.Len() >= FoundMax {
			return
		}

		r.file.Found.Set(key, &Found{At: now, Slack: slack, DM: direct})

		return
	}

	if slack != nil {
		if have.Slack == nil {
			have.Slack = slack
		} else if CompareTS(slack.TS, have.Slack.TS) < 0 {
			have.Slack = slack
		}
	}

	if direct == nil {
		return
	}

	if have.DM == nil {
		have.DM = direct

		return
	}

	if CompareTS(direct.FirstTS, have.DM.FirstTS) < 0 {
		have.DM = &FoundDM{
			FirstTS: direct.FirstTS, Permalink: direct.Permalink, LastTS: have.DM.LastTS,
		}
	}

	if CompareTS(direct.LastTS, have.DM.LastTS) > 0 {
		have.DM = &FoundDM{
			FirstTS: have.DM.FirstTS, Permalink: have.DM.Permalink, LastTS: direct.LastTS,
		}
	}
}

// dmReady is whether a DM scan could start, the schedule aside: switched on, a dashboard open,
// the reviewer known and the file read.
func (r *Reviews) dmReady(current *settings.Settings) bool {
	return all(r.dmsOn(current), r.deps.HasClients(), r.me != nil, r.file != nil)
}

func (r *Reviews) dmDue(now float64, current *settings.Settings) bool {
	if now < r.nextDMScanAt {
		return false
	}

	return r.dmReady(current)
}

// dmSince is where the DM scan reads from: the cursor less its overlap, never further back than
// the first-scan window (which is also where the first run starts).
func (r *Reviews) dmSince(startedAt float64) string {
	floor := startedAt - float64(r.deps.Config.FirstWindowDays*dayMs)

	if r.file == nil {
		return MsTS(floor)
	}

	if r.file.DMCursorTS == nil {
		return MsTS(floor)
	}

	return MsTS(math.Max(TSMs(*r.file.DMCursorTS)-dmOverlapMs, floor))
}

func (r *Reviews) startDMScan(startedAt float64, current settings.Settings) {
	since := r.dmSince(startedAt)

	allow := current.Repos.Allow
	if allow == nil {
		allow = []string{}
	}

	r.spawn(func() {
		var (
			posts []DMFind
			err   error
		)

		r.outside(func() { posts, err = r.deps.DMScan(r.ctx, allow, since) })

		defer func() {
			r.dmScanning = false
			r.settle()
			r.bump()
		}()

		// The DM scan (or Slack, or GitHub) switched off while the run was out: what it read is
		// dropped, and nothing is recorded or moved.
		now, _ := r.settings()
		if !r.dmsOn(&now) {
			return
		}

		if err != nil {
			r.dmScanFailed(err)

			return
		}

		r.finishDMScan(startedAt, posts)
		r.readFound()
	})
}

// dmScanFailed: a signed-out Slack is shared with the channel scan, and the DM scan backs off
// with it. Any other failure is the DM scan's own and leaves the Slack state alone.
func (r *Reviews) dmScanFailed(err error) {
	if r.file == nil {
		return
	}

	now := r.now()

	if signedOut(err) {
		r.record(moduleSlack, false, signedOutDetail)
		r.dmError = nil
		r.signedOut(now)
		r.nextDMScanAt = math.Max(r.nextDMScanAt, r.nextScanAt)

		return
	}

	r.record(moduleSlack, false, "Reviews could not scan your Slack DMs")

	why := message(err)
	r.dmError = &why
}

func signedOut(err error) bool {
	scanErr, isScan := errors.AsType[*ScanError](err)
	if !isScan {
		return false
	}

	return len(scanErr.NeedsAuth) > 0
}

// finishDMScan applies a DM scan that read. It clears a signed-out Slack (a DM scan that read
// proves the sign-in is back), never an error of the channel scan. The cursor moves only on a
// success.
func (r *Reviews) finishDMScan(startedAt float64, posts []DMFind) {
	file := r.file
	if file == nil {
		return
	}

	now := r.now()
	r.record(moduleSlack, true, "Reviews scanned your Slack DMs")
	r.dmError = nil

	if r.slack == slackNeedsAuth {
		r.slack = slackOK
		r.slackError = nil
		r.signedOutDelay = r.deps.Config.PollMs
	}

	for _, post := range posts {
		entry, tracked := file.PRs.Get(post.Key)
		if anyOf(!tracked, !r.prs.Has(post.Key)) {
			r.remember(post.Key, now, nil, &FoundDM{
				FirstTS: post.FirstTS, Permalink: post.Permalink, LastTS: post.LastTS,
			})

			continue
		}

		if entry.DM == nil {
			entry.DM = &DMPost{
				TS: post.FirstTS, Permalink: post.Permalink, PostedAt: TSMs(post.FirstTS),
			}
		}

		last := math.Max(valueOr(entry.DMLastAt, 0), TSMs(post.LastTS))
		entry.DMLastAt = &last
	}

	cursor := MsTS(startedAt - dmOverlapMs)
	if file.DMCursorTS == nil {
		file.DMCursorTS = &cursor
	} else if CompareTS(cursor, *file.DMCursorTS) > 0 {
		file.DMCursorTS = &cursor
	}

	r.save()
}

func (r *Reviews) signedOut(now float64) {
	r.slack = slackNeedsAuth
	r.slackError = nil
	r.nextScanAt = now + r.signedOutDelay
	r.signedOutDelay = math.Min(float64(r.signedOutDelay*signedOutBackoff), signedOutDelayMaxMs)
}

// burn spends one try for each PR the scan asked about, so a scan that fails is not repeated
// forever. A scan that comes late also spends every step of the schedule that had already come
// due when it started: one catch-up scan, not one per step.
func (r *Reviews) burn(plan *scanPlan, now float64) {
	elapsed := func(since float64) float64 {
		count := 0

		for _, minutes := range ScanBackoffMin() {
			if plan.now-since >= float64(minutes*minuteMs) {
				count++
			}
		}

		return float64(count)
	}

	for _, key := range plan.unmatched {
		entry, tracked := r.file.PRs.Get(key)
		if !tracked {
			continue
		}

		if entry.Slack == nil {
			entry.Scan = Scan{
				Tries: math.Max(entry.Scan.Tries+1, elapsed(entry.FirstSeenAt)), LastAt: now,
			}
		}
	}

	for _, thread := range plan.threads {
		entry, tracked := r.file.PRs.Get(thread.PR)
		if !tracked {
			continue
		}

		tries := math.Max(entry.ThreadScan.Tries+1, elapsed(entry.ThreadScan.LastAt))
		entry.ThreadScan.Tries = tries
	}
}

// scanFailed: a signed-out Slack is not a scan that failed, so no try is spent on it.
func (r *Reviews) scanFailed(plan *scanPlan, err error) {
	if r.file == nil {
		return
	}

	now := r.now()

	if signedOut(err) {
		r.record(moduleSlack, false, signedOutDetail)
		r.signedOut(now)

		return
	}

	r.record(moduleSlack, false, "Reviews could not scan the Slack channels")
	r.slack = slackError
	why := message(err)
	r.slackError = &why
	r.burn(plan, now)
	r.save()
}

func (r *Reviews) finishScan(plan *scanPlan, result ScanResult) {
	file := r.file
	if file == nil {
		return
	}

	now := r.now()
	at := now
	r.lastScanAt = &at

	if all(len(result.Posts) == 0, len(result.Asks) == 0, len(result.NeedsAuth) > 0) {
		r.record(moduleSlack, false, signedOutDetail)
		r.signedOut(now)

		return
	}

	r.record(moduleSlack, true, "Reviews scanned the Slack channels")
	r.slack = slackOK
	r.slackError = nil
	r.signedOutDelay = r.deps.Config.PollMs

	r.takePosts(file, result.Posts, now)

	for _, ask := range result.Asks {
		entry, tracked := file.PRs.Get(ask.Key)
		if tracked {
			askedAt := math.Max(valueOr(entry.SlackAskAt, 0), ask.AskedAt)
			entry.SlackAskAt = &askedAt
		}
	}

	// The run read every channel up to when it started: each cursor moves to that moment less the
	// overlap, so a quiet channel is not read from the first-scan window again on every run. A run
	// that was partly signed out may not have read every channel: the cursors stay.
	if len(result.NeedsAuth) == 0 {
		read := MsTS(plan.now - scanOverlapMs)
		for _, channel := range plan.current.Slack.Channels {
			moveCursor(file, channel, read)
		}
	}

	r.burn(plan, now)
	r.save()
}

func (r *Reviews) takePosts(file *File, posts []Post, now float64) {
	for _, post := range posts {
		entry, tracked := file.PRs.Get(post.Key)
		if all(tracked, r.prs.Has(post.Key)) {
			if entry.Slack == nil {
				channel := post.Channel
				entry.Slack = &SlackPost{
					TS: post.TS, Permalink: post.Permalink, PostedAt: TSMs(post.TS),
					Channel: &channel,
				}
			}
		} else {
			r.remember(post.Key, now, &FoundSlack{
				TS: post.TS, Permalink: post.Permalink, Channel: post.Channel,
			}, nil)
		}

		moveCursor(file, post.Channel, post.TS)
	}
}

// moveCursor moves a channel's cursor to ts when that is later.
func moveCursor(file *File, channel, stamp string) {
	cursor, found := file.Cursors.Get(channel)
	if !found {
		file.Cursors.Set(channel, stamp)

		return
	}

	if CompareTS(stamp, cursor) > 0 {
		file.Cursors.Set(channel, stamp)
	}
}
