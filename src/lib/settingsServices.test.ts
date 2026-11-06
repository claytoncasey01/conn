// The Services half of the Settings page's pure parts: channel and key grammar (held to the server's), the card drafts, the build-repo
// choices, the status line and the "Lists: ..." sentence for every combination of switches.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseBranch, validateSection } from './serverAnswers.ts'
import {
  MAX_LISTED,
  addName,
  agoText,
  atlassianSection,
  baseBranchProblems,
  baseBranchRepos,
  buildRepoOptions,
  draftChannelOptions,
  listsSentence,
  nameCheck,
  parseBaseBranch,
  parseChannel,
  parseJiraKey,
  sameAtlassian,
  sameSlack,
  slackReads,
  slackSection,
  stateWord,
  statusLine,
  wroteAtlassian,
  wroteGithub,
  wroteReviews,
  wroteSlack,
  type SourcesInput,
} from './settingsModel.ts'

const serverChannel = (c: string) => validateSection('slack', { enabled: false, channels: [c] }).ok
const serverKey = (k: string) => validateSection('atlassian', { enabled: false, jira: { projects: [k] } }).ok

const CHANNELS = ['#code-review', 'code-review', '  #Code-Review  ', '#a', '#a.b_c-d', '#' + 'x'.repeat(80), '#' + 'x'.repeat(81), '#', '', '   ', '# code', '#co de', '##code', '#code/review', '#code?x', '#<b>', '#../x', '#cödé', '#K', '#a\nb', '#a‮b', '#a\0b', 'x'.repeat(81)]
const KEYS = ['PROJ', 'proj', ' Proj ', 'AB', 'A1', 'ABCDEFGHIJ', 'ABCDEFGHIJK', 'A', '1AB', 'AB-1', 'A B', '', 'PRÖJ', 'PRO_J', 'AK', 'AB\n', 'AB‮', '<b>']

test('a channel the page accepts, the server accepts as stored; and one the server takes as typed, the page takes', () => {
  for (const raw of CHANNELS) {
    const r = parseChannel(raw)
    if (r.ok) {
      assert.ok(serverChannel(r.name), `${JSON.stringify(raw)} -> ${r.name}`)
      assert.match(r.name, /^#[a-z0-9._-]+$/)
    }
    // the server's own form is never refused by the page
    if (serverChannel(raw)) assert.deepEqual(parseChannel(raw), { ok: true, name: raw }, JSON.stringify(raw))
  }
  assert.deepEqual(parseChannel('  Code-Review '), { ok: true, name: '#code-review' })
  assert.deepEqual(parseChannel('#code-review'), { ok: true, name: '#code-review' })
})

test('channel names that are not names get a reason, and a lookalike is not folded into one', () => {
  for (const bad of ['', '   ', '#', '# code', '#co de', '##code', '#code/review', '#code?x', '#<b>', '#../x', '#cödé', '#K', '#a\nb', '#a‮b', '#a\0b', '#' + 'x'.repeat(81)]) {
    const r = parseChannel(bad)
    assert.equal(r.ok, false, JSON.stringify(bad))
    assert.ok(!r.ok && r.reason.length > 0)
  }
})

test('a Jira key the page accepts, the server accepts as stored; and one the server takes as typed, the page takes', () => {
  for (const raw of KEYS) {
    const r = parseJiraKey(raw)
    if (r.ok) assert.ok(serverKey(r.name), `${JSON.stringify(raw)} -> ${r.name}`)
    if (serverKey(raw)) assert.deepEqual(parseJiraKey(raw), { ok: true, name: raw }, JSON.stringify(raw))
  }
  assert.deepEqual(parseJiraKey(' proj '), { ok: true, name: 'PROJ' })
  for (const bad of ['', 'A', '1AB', 'AB-1', 'A B', 'PRÖJ', 'AK', 'ABCDEFGHIJK', '<b>']) assert.equal(parseJiraKey(bad).ok, false, JSON.stringify(bad))
})

// The server's own base-branch cases and a few typed forms
const BRANCHES = ['main', 'trunk', 'release/2.1', 'feature/a_b-c.d', 'v1', 'A', 'x'.repeat(100), ' main ', 'Main', '', '   ', '-x', '--upload-pack=x', '/x', '.x', 'a..b', 'a//b', 'a/.b', 'a b', 'a\tb', 'a\nb', 'a/', 'a.', 'x.lock', 'a@{1}', 'a~1', 'a^', 'a:b', 'a?', 'a*', 'a[0]', 'a\\b', 'é', 'x'.repeat(101), 'a‮b']
const serverBase = (b: string | null) => validateSection('atlassian', { enabled: false, jira: { projects: [], baseBranch: b } }).ok

test('a base branch the page accepts, the server accepts as stored; one the server takes, the page takes; empty is none', () => {
  for (const raw of BRANCHES) {
    const r = parseBaseBranch(raw)
    const trimmed = raw.trim()
    // the page and the server agree on every name, trimmed as the page trims it
    assert.equal(r.ok, trimmed === '' || parseBranch(trimmed) !== null, JSON.stringify(raw))
    if (r.ok) assert.ok(serverBase(r.branch), `${JSON.stringify(raw)} -> ${r.branch}`)
    else assert.ok(r.reason.length > 0)
    if (parseBranch(raw) !== null) assert.deepEqual(r, { ok: true, branch: raw }, JSON.stringify(raw))
  }
  assert.deepEqual(parseBaseBranch('  '), { ok: true, branch: null })
  assert.deepEqual(parseBaseBranch(' release/2.1 '), { ok: true, branch: 'release/2.1' })
})

test('the Atlassian save sends the base branch: trimmed, none when empty, and the server keeps what was sent', () => {
  const a = { enabled: true, jira: { projects: ['PROJ'], buildRepo: null as string | null, baseBranch: ' develop ' as string | null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }
  assert.deepEqual(atlassianSection(a).jira, { projects: ['PROJ'], buildRepo: null, baseBranch: 'develop', baseBranches: {}, reviewMove: 'all', sessionLayout: 'shared' })
  assert.equal(atlassianSection({ ...a, jira: { ...a.jira, baseBranch: '' } }).jira.baseBranch, null)
  assert.equal(atlassianSection({ enabled: true, jira: { projects: [], buildRepo: null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }).jira.baseBranch, null)
  const sent = validateSection('atlassian', atlassianSection(a))
  assert.ok(sent.ok)
  assert.equal((sent.value as { jira: { baseBranch: string | null } }).jira.baseBranch, 'develop')
  // a changed base branch is a change of the card; none and a missing field are the same
  const saved = { enabled: true, jira: { projects: ['PROJ'], buildRepo: null, baseBranch: null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }
  assert.ok(sameAtlassian(saved, { enabled: true, jira: { projects: ['PROJ'], buildRepo: null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }))
  assert.ok(!sameAtlassian(saved, { ...saved, jira: { ...saved.jira, baseBranch: 'develop' } }))
  assert.equal(wroteAtlassian({ enabled: true, jira: { projects: ['PROJ'], buildRepo: 'acme/app', baseBranch: 'develop', baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }), 'Saved: Atlassian is on, 1 project (PROJ), default repository acme/app, base branch develop')
})

test('adding a channel or a key: validated, never a repeat, never past the limit, the list given is not changed', () => {
  const list = ['#a']
  const r = addName(list, 'B', parseChannel)
  assert.deepEqual(r, { ok: true, list: ['#a', '#b'], name: '#b' })
  assert.deepEqual(list, ['#a'])
  assert.deepEqual(addName(list, '#A', parseChannel), { ok: false, reason: '#a is already in the list' })
  assert.equal(addName(list, 'no way', parseChannel).ok, false)
  const full = Array.from({ length: MAX_LISTED }, (_, i) => `#c${i}`)
  const over = addName(full, '#more', parseChannel)
  assert.ok(!over.ok && /At most 20/.test(over.reason))
  assert.equal(MAX_LISTED, 20)
  assert.deepEqual(addName(['PROJ'], 'abc', parseJiraKey), { ok: true, list: ['PROJ', 'ABC'], name: 'ABC' })
})

test('the add box says nothing while empty, what will be added when it is good, and why not when it is not', () => {
  assert.deepEqual(nameCheck('', [], parseChannel), { state: 'empty' })
  assert.deepEqual(nameCheck('  ', [], parseChannel), { state: 'empty' })
  assert.deepEqual(nameCheck('Eng', ['#a'], parseChannel), { state: 'ok', name: '#eng' })
  assert.equal(nameCheck('#a', ['#a'], parseChannel).state, 'error')
  assert.equal(nameCheck('a b', [], parseChannel).state, 'error')
})

test('a save never sends a draft channel that is not one of the channels (removing it clears it)', () => {
  const s = { enabled: true, channels: ['#a', '#b'], dms: false, draftChannel: '#b' }
  assert.deepEqual(slackSection({ ...s, channels: ['#a'] }), { enabled: true, channels: ['#a'], dms: false, draftChannel: null })
  assert.deepEqual(slackSection({ ...s, channels: ['#b'] }), { enabled: true, channels: ['#b'], dms: false, draftChannel: '#b' })
  assert.deepEqual(s.channels, ['#a', '#b'])
  assert.equal(slackSection({ ...s, draftChannel: '#gone' }).draftChannel, null)
  assert.equal(slackSection(s).draftChannel, '#b')
  // what a save sends is accepted by the server
  assert.ok(validateSection('slack', slackSection({ ...s, draftChannel: '#gone' })).ok)
  assert.ok(validateSection('atlassian', atlassianSection({ enabled: true, jira: { projects: ['PROJ'], buildRepo: null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } })).ok)
})

test('a card is changed when any of its fields differs from what was saved', () => {
  const s = { enabled: true, channels: ['#a'], dms: false, draftChannel: null }
  assert.ok(sameSlack(s, { ...s, channels: ['#a'] }))
  for (const o of [{ enabled: false }, { channels: ['#a', '#b'] }, { channels: [] }, { dms: true }, { draftChannel: '#a' }]) assert.ok(!sameSlack(s, { ...s, ...o }), JSON.stringify(o))
  const a = { enabled: true, jira: { projects: ['PROJ'], buildRepo: 'acme/app' as string | null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }
  assert.ok(sameAtlassian(a, atlassianSection(a)))
  assert.ok(!sameAtlassian(a, { ...a, enabled: false }))
  assert.ok(!sameAtlassian(a, { ...a, jira: { ...a.jira, projects: [] } }))
  assert.ok(!sameAtlassian(a, { ...a, jira: { ...a.jira, buildRepo: null } }))
  assert.ok(!sameAtlassian(a, { ...a, jira: { ...a.jira, baseBranches: { 'acme/app': 'develop' } } }))
  assert.ok(!sameAtlassian(a, { ...a, jira: { ...a.jira, reviewMove: 'never' } }))
  assert.ok(!sameAtlassian(a, { ...a, jira: { ...a.jira, sessionLayout: 'split' } }))
  // a repository box left empty is the same as none
  assert.ok(sameAtlassian(a, { ...a, jira: { ...a.jira, baseBranches: { 'acme/app': '  ' } } }))
})

test('draft channel choices: none without channels, else the first channel (stored as none) and each channel', () => {
  assert.deepEqual(draftChannelOptions([]), [])
  assert.deepEqual(draftChannelOptions(['#a', '#b']), [
    { value: null, label: 'First channel (#a)' },
    { value: '#a', label: '#a' },
    { value: '#b', label: '#b' },
  ])
})

test('build repo choices are concrete allowed repos: exact patterns, the clone rows and the saved one; never a wildcard, never a repo the list does not cover', () => {
  const rows = [{ repo: 'acme/infra' }, { repo: 'Acme/App' }, { repo: 'other/thing' }, { repo: 'jane/widgets' }]
  assert.deepEqual(buildRepoOptions(['acme/*', 'jane/widgets'], rows, null), ['Acme/App', 'acme/infra', 'jane/widgets'])
  assert.deepEqual(buildRepoOptions(['acme/*', 'acme/lib'], [], 'acme/app'), ['acme/app', 'acme/lib'])
  assert.deepEqual(buildRepoOptions(['acme/*'], [], 'other/thing'), [], 'a saved repo the list no longer covers is not offered')
  assert.deepEqual(buildRepoOptions(null, rows, 'acme/app'), [])
  assert.deepEqual(buildRepoOptions(['acme/*'], [{ repo: 'acme/*' }, { repo: '../x' }, { repo: 'a b/c' }], null), [])
})

test('how long ago: just now, minutes, hours, days; nothing for a status with no time', () => {
  const now = 1_000_000_000
  assert.equal(agoText(null, now), '')
  assert.equal(agoText(now - 5_000, now), 'just now')
  assert.equal(agoText(now + 5_000, now), 'just now', 'a clock a little behind is not "in the future"')
  assert.equal(agoText(now - 3 * 60_000, now), '3 min ago')
  assert.equal(agoText(now - 2 * 3_600_000, now), '2 h ago')
  assert.equal(agoText(now - 3 * 86_400_000, now), '3 d ago')
  assert.equal(statusLine({ state: 'ok', detail: 'Signed in as jane', at: now - 3 * 60_000 }, now), 'Working: Signed in as jane · 3 min ago')
  assert.equal(statusLine({ state: 'failing', detail: 'Slack did not answer', at: now - 1000 }, now), 'Failing: Slack did not answer · just now')
  assert.equal(statusLine({ state: 'unchecked', detail: 'Not checked yet', at: null }, now), 'Not checked yet')
  assert.equal(statusLine({ state: 'off', detail: 'Off: conn makes no calls', at: null }, now), 'Off: conn makes no calls')
  assert.equal(stateWord('ok'), 'Working')
})

// ---- "Lists: ..." ----

const base: SourcesInput = { github: true, slackOn: false, channels: [], dms: false, requested: true, allOpen: false }

test('the sentence for the default: PRs that request you, and a Slack read as well when Slack is on with a channel', () => {
  assert.equal(listsSentence(base), 'Lists: PRs that request your review. Reviews makes no Slack call.')
  assert.equal(
    listsSentence({ ...base, slackOn: true, channels: ['#code-review'], dms: true }),
    'Lists: PRs that request your review, PRs posted in #code-review and PRs sent to you in a DM. A PR that is only ready for review, with no message about it, is left out.',
  )
})

test('the sentence names every channel and joins them with "or"', () => {
  assert.equal(
    listsSentence({ ...base, requested: false, slackOn: true, channels: ['#a', '#b'] }),
    'Lists: PRs posted in #a or #b. A PR that is only ready for review, with no message about it, is left out.',
  )
  assert.match(listsSentence({ ...base, slackOn: true, channels: ['#a', '#b', '#c'] }), /posted in #a, #b or #c/)
})

test('every open PR: stated as such, with the tag for the rest only when Slack is read', () => {
  assert.equal(listsSentence({ ...base, allOpen: true }), 'Lists: every open, non-draft PR in your allowed repositories that you did not write.')
  assert.equal(
    listsSentence({ ...base, allOpen: true, slackOn: true, channels: ['#a'] }),
    'Lists: every open, non-draft PR in your allowed repositories that you did not write. PRs posted in #a show their Slack links; the rest are tagged "not in a channel".',
  )
  assert.match(listsSentence({ ...base, allOpen: true, slackOn: true, dms: true }), /sent to you in a DM show their Slack links/)
})

test('nothing on, and GitHub off, say so instead of going quiet', () => {
  assert.match(listsSentence({ ...base, requested: false }), /^Lists: nothing\. Turn on a source/)
  assert.match(listsSentence({ ...base, requested: false, slackOn: true }), /^Lists: nothing\./, 'Slack on with no channel and no DM scans nothing')
  assert.match(listsSentence({ ...base, github: false }), /^Lists: nothing\. GitHub is off/)
})

test('Slack on with no channel and no DM scan reads nothing from Slack, and the sentence does not pretend it does', () => {
  assert.deepEqual(slackReads({ ...base, slackOn: true }), { channel: false, dm: false })
  assert.equal(listsSentence({ ...base, slackOn: true }), 'Lists: PRs that request your review. Reviews makes no Slack call.')
  assert.deepEqual(slackReads({ ...base, slackOn: false, channels: ['#a'], dms: true }), { channel: false, dm: false }, 'Slack off reads nothing, whatever is configured')
})

test('every combination of the switches: a sentence that matches the rules', () => {
  let n = 0
  for (const github of [true, false])
    for (const requested of [true, false])
      for (const allOpen of [true, false])
        for (const slackOn of [true, false])
          for (const channels of [[], ['#a'], ['#a', '#b']])
            for (const dms of [true, false]) {
              n++
              const i: SourcesInput = { github, slackOn, channels, dms, requested, allOpen }
              const s = listsSentence(i)
              const at = JSON.stringify(i)
              const r = slackReads(i)
              assert.ok(s.startsWith('Lists: '), at)
              assert.ok(!/\n/.test(s) && s.endsWith('.'), at)
              if (!github) {
                assert.match(s, /^Lists: nothing\. GitHub is off/, at)
                continue
              }
              // a channel is named exactly when it is read (a channel on a Slack that is off is never named)
              for (const c of channels) assert.equal(s.includes(c), r.channel, `${c} in ${at}`)
              // a DM is mentioned exactly when it is scanned
              assert.equal(/\bDM\b/.test(s), r.dm, at)
              // "Slack" is said only to say that a read happens, or that none is made
              if (!r.channel && !r.dm) assert.ok(!/Slack links|"not in a channel"|only ready for review/.test(s), at)
              // the left-out rule is stated exactly when a message is needed to list a ready-for-review PR: a Slack read on and not every open PR
              assert.equal(/only ready for review/.test(s), (r.channel || r.dm) && !allOpen, at)
              // every open PR is stated exactly when it is on
              assert.equal(/every open, non-draft PR/.test(s), allOpen, at)
              // the request source is stated exactly when it is on and not folded into every open PR
              assert.equal(/request your review/.test(s), requested && !allOpen, at)
              // no Slack read and no every-open: the sentence says Reviews makes no Slack call (or that nothing is on)
              if (!r.channel && !r.dm && !allOpen) assert.ok(/makes no Slack call|^Lists: nothing\./.test(s), at)
              if (!requested && !allOpen && !r.channel && !r.dm) assert.match(s, /^Lists: nothing\./, at)
            }
  assert.equal(n, 96)
})

test('what a save says it wrote, from what was sent', () => {
  assert.equal(wroteGithub(true), 'Saved: GitHub is on')
  assert.equal(wroteSlack({ enabled: true, channels: ['#a', '#b'], dms: true, draftChannel: '#b' }), 'Saved: Slack is on, 2 channels (#a, #b), DM scan on, drafts in #b')
  assert.equal(wroteSlack({ enabled: true, channels: ['#a'], dms: false, draftChannel: null }), 'Saved: Slack is on, 1 channel (#a), DM scan off, drafts in #a')
  assert.equal(wroteSlack({ enabled: false, channels: [], dms: false, draftChannel: null }), 'Saved: Slack is off, no channels, DM scan off, no drafts')
  assert.equal(wroteAtlassian({ enabled: true, jira: { projects: ['PROJ'], buildRepo: 'acme/app', baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }), 'Saved: Atlassian is on, 1 project (PROJ), default repository acme/app')
  assert.equal(wroteAtlassian({ enabled: false, jira: { projects: [], buildRepo: null, baseBranches: {}, reviewMove: 'all' as const, sessionLayout: 'shared' as const } }), 'Saved: Atlassian is off, no projects')
  assert.equal(wroteReviews({ requested: true, allOpen: false }), 'Saved: "Requests me" is on, "Every open non-draft PR" is off')
})

test('the Atlassian save sends the per-repository branches, the review move and the layout, and the server keeps them as sent', () => {
  const a = {
    enabled: true,
    jira: { projects: ['PROJ'], buildRepo: null, baseBranch: null, baseBranches: { 'acme/web': '  ', 'acme/api': ' develop ' }, reviewMove: 'first' as const, sessionLayout: 'split' as const },
  }
  const sent = atlassianSection(a)
  // an empty box is no entry, the others are trimmed
  assert.deepEqual(sent.jira, { projects: ['PROJ'], buildRepo: null, baseBranch: null, baseBranches: { 'acme/api': 'develop' }, reviewMove: 'first', sessionLayout: 'split' })
  const checked = validateSection('atlassian', sent)
  assert.ok(checked.ok)
  assert.deepEqual(checked.value, sent)
  // a typed branch the server would refuse is said by repository, and is not sent
  const bad = { ...a, jira: { ...a.jira, baseBranches: { 'acme/api': 'a..b', 'acme/web': 'main' } } }
  assert.deepEqual(Object.keys(baseBranchProblems(bad.jira.baseBranches)), ['acme/api'])
  assert.deepEqual(atlassianSection(bad).jira.baseBranches, { 'acme/web': 'main' })
})

test('each allowed repository gets a base branch box: the concrete allowed ones, and any that already has a saved branch', () => {
  const rows = [{ repo: 'acme/app' }, { repo: 'acme/infra' }, { repo: 'other/thing' }]
  assert.deepEqual(baseBranchRepos(['acme/*'], rows, {}), ['acme/app', 'acme/infra'])
  assert.deepEqual(baseBranchRepos(['acme/*'], rows, { 'acme/lib': 'develop', 'other/gone': 'main' }), ['acme/app', 'acme/infra', 'acme/lib'])
  assert.deepEqual(baseBranchRepos(null, rows, {}), [])
})
