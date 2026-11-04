package myprs

import "math"

// draftRounds starts the Slack drafts that are due. It never waits: a draft can take as long as
// the helper's timeout, and the poll must not.
func (m *MyPrs) draftRounds() {
	if m.file == nil {
		return
	}

	if m.dry() {
		return
	}

	// The draft goes to the Slack module's draft channel: no channel, or Slack off, and no draft
	// is made.
	channel := m.draftChannel()
	if channel == "" {
		return
	}

	for _, listed := range m.listed() {
		if m.staleKeys[listed.key] {
			continue
		}

		entry, found := m.file.PRs.Get(listed.key)
		if !found {
			continue
		}

		if !m.permitted(listed.pull.Repo) {
			continue
		}

		if m.draftDue(listed.key, listed.pull, entry, channel) {
			m.draftOne(listed.key, listed.pull, entry, channel)
		}
	}
}

// draftChannel is the channel a draft is saved in now, "" when none is to be made: the Slack
// module is off (or no allowlist), or it has no channel.
func (m *MyPrs) draftChannel() string {
	current := m.scope()
	if offOf(current) != "" {
		return ""
	}

	if !current.Slack {
		return ""
	}

	if current.DraftChannel == nil {
		return ""
	}

	return *current.DraftChannel
}

// draftDue is whether a Slack draft is to be saved for this head: only for a PR a Self-Review was
// started on (the launch that asked for the head decides), once that launch is ready for it. One
// draft per head: a saved one is final, a failed one is tried again for the same head only after
// DraftRetryMs and at most DraftTriesMax times in all, and a new head is drafted at once.
func (m *MyPrs) draftDue(key string, pull *PR, entry *Entry, channel string) bool {
	if m.drafting[key] {
		return false
	}

	cfg := m.deps.Config
	if !ReadyFor(pull, AskedLaunch(entry.Launches, pull.HeadSha), cfg.FindingBots,
		cfg.GreptileBot) {
		return false
	}

	draft := entry.Draft
	if draft == nil {
		return true
	}

	if draft.Sha != pull.HeadSha {
		return true
	}

	if draft.State != DraftFailed {
		return false
	}

	// A draft that failed in another channel (a refusal because one was waiting there, say) is
	// tried afresh in the channel chosen since.
	if draft.Channel != "" {
		if draft.Channel != channel {
			return true
		}
	}

	if draft.Tries >= DraftTriesMax {
		return false
	}

	return m.now()-draft.At >= DraftRetryMs
}

// draftOne saves the draft for pull's current head and stores the outcome, saved or failed, with
// the text either way. A final failure (the channel has a draft already) uses up the attempts
// for this head at once. The helper runs on a goroutine of its own.
func (m *MyPrs) draftOne(key string, pull *PR, entry *Entry, channel string) {
	m.drafting[key] = true
	sha := pull.HeadSha
	// The count is per head and channel: a new channel starts again at one.
	tries := 1

	if previous := entry.Draft; previous != nil {
		if previous.Sha == sha {
			switch previous.Channel {
			case "", channel:
				tries = previous.Tries + 1
			}
		}
	}

	round := 0.0
	for _, launch := range entry.Launches {
		round = math.Max(round, launch.Round)
	}

	body, err := DraftText(pull.Title, pull.URL, round)
	if err != nil {
		why := message(err)
		if why == "" {
			why = textDraftFailed
		}

		m.draftDone(key, entry, &SlackDraft{
			Sha: sha, Text: "", State: DraftFailed, Error: &why, At: 0, Tries: tries,
			Channel: channel,
		})

		return
	}

	m.spawn(func() {
		var result DraftResult

		m.outside(func() { result = m.deps.Draft(m.ctx, channel, body) })
		m.draftDone(key, entry, m.draftOutcome(result, &SlackDraft{
			Sha: sha, Text: body, State: DraftFailed, Error: nil, At: 0, Tries: tries,
			Channel: channel,
		}))
	})
}

// draftOutcome is the draft to store for the helper's result. A draft refused because one is
// already waiting (or for a channel that is no channel) says nothing about Slack.
func (m *MyPrs) draftOutcome(result DraftResult, draft *SlackDraft) *SlackDraft {
	if result.OK {
		m.record(moduleSlack, true, "My PRs saved a Slack draft")

		draft.State = DraftSaved

		return draft
	}

	if !result.Final {
		m.record(moduleSlack, false, "My PRs could not save a Slack draft")
	}

	why := Clean(result.Error, errorUnitsMax)
	if why == "" {
		why = textDraftFailed
	}

	draft.Error = &why

	if result.Final {
		draft.Tries = max(draft.Tries, DraftTriesMax)
	}

	return draft
}

// draftDone stores the outcome, unless the PR was pruned from the file while the helper ran:
// there is nothing left to store it on.
func (m *MyPrs) draftDone(key string, entry *Entry, draft *SlackDraft) {
	defer func() {
		delete(m.drafting, key)
		m.bump()
	}()

	if m.file == nil {
		return
	}

	if current, found := m.file.PRs.Get(key); !found {
		return
	} else if current != entry {
		return
	}

	draft.At = m.now()
	entry.Draft = draft

	m.save()
}
