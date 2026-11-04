import assert from 'node:assert/strict'
import { test } from 'node:test'
import { attention, diffAlerts, newSeen, oneLine } from './alerts.ts'
import type { MyPrDraft, MyPrRow, Snapshot } from './types.ts'

const sess = (id: string, status: string) => ({ id, name: 'n' + id, status, waitingFor: status === 'waiting' ? 'permission prompt' : null, activity: 'did a thing' })
const run = (key: string, state: string, extra = {}) => ({ key, state, questions: [{ answer: null }, { answer: 'x' }], error: 'boom', ...extra })
const snap = (sessions: object[], runs: object[] = []) => ({ at: 0, sessions, runs }) as unknown as Snapshot

test('the first snapshot only seeds, and an unchanged one raises nothing', () => {
  const seen = newSeen()
  const now = snap([sess('1', 'waiting'), sess('2', 'idle'), sess('3', 'busy')], [run('PROJ-1', 'asking')])
  assert.equal(diffAlerts(seen, now).length, 0)
  assert.equal(diffAlerts(seen, now).length, 0)
})

test('busy to waiting alerts, waiting or busy to idle alerts, a new idle session and an unchanged waiting one do not', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([sess('1', 'waiting'), sess('2', 'idle'), sess('3', 'busy')], [run('PROJ-1', 'asking')]))
  let a = diffAlerts(seen, snap([sess('1', 'waiting'), sess('2', 'idle'), sess('3', 'waiting')], [run('PROJ-1', 'asking')]))
  assert.deepEqual(a.map(x => x.title), ['n3 needs input'])
  a = diffAlerts(seen, snap([sess('1', 'idle'), sess('2', 'idle'), sess('3', 'waiting')], [run('PROJ-1', 'building')]))
  assert.deepEqual(a.map(x => x.title), ['n1 finished'])
  a = diffAlerts(seen, snap([sess('4', 'idle')], [run('PROJ-2', 'failed')]))
  assert.deepEqual(a.map(x => x.title), ['PROJ-2 failed'])
})

test('an open PR going red, green, merged or closed alerts once', () => {
  const seen = newSeen()
  const pr = (state: string, checks: string) => run('PROJ-5', 'pr_open', { pr: { number: 9, status: { state, checks: { state: checks, failed: ['Backend tests'] } } } })
  diffAlerts(seen, snap([], [pr('open', 'pending')]))
  assert.deepEqual(diffAlerts(seen, snap([], [pr('open', 'failing')])).map(x => x.title), ['PROJ-5 checks failed'])
  assert.equal(diffAlerts(seen, snap([], [pr('open', 'failing')])).length, 0)
  assert.deepEqual(diffAlerts(seen, snap([], [pr('merged', 'passing')])).map(x => x.title), ['PROJ-5 PR merged'])
})

test('the tab count is sessions on a prompt plus runs with questions', () => {
  assert.equal(attention(snap([sess('1', 'waiting'), sess('2', 'busy')], [run('PROJ-1', 'asking'), run('PROJ-3', 'building')])), 2)
})

test('the tab count also has the My PRs rows that need me, the same count as the tab badge', () => {
  const d = (o: Partial<MyPrDraft> = {}): MyPrDraft => ({ sha: 'a', text: 't', state: 'saved', error: null, at: 0, tries: 1, ...o })
  const rows = [
    { repo: 'org/repo', number: 1, status: 'ready', slackDraft: d() },
    { repo: 'org/repo', number: 2, status: 'ready', slackDraft: null },
    { repo: 'org/repo', number: 3, status: 'draft-failed', slackDraft: d({ state: 'failed', tries: 3 }) },
    { repo: 'org/repo', number: 4, status: 'draft-failed', slackDraft: d({ state: 'failed', tries: 1 }) },
  ] as MyPrRow[]
  const s = { ...snap([sess('1', 'waiting')]), myPrs: { at: 0, loaded: true, draftMaxTries: 3, rows } } as unknown as Snapshot
  assert.equal(attention(s), 3)
  // No My PRs view yet
  assert.equal(attention(snap([sess('1', 'waiting')])), 1)
})

// ---- what an alert says about the prompt ----

// Shaped as the parser returns it: a question carries what it asks in `title` (its `text` is empty), any other kind in `text`.
// `text` here is what the prompt says, whichever field that lands in.
const ask = (over: { text?: string } & Record<string, unknown> = {}) => {
  const { text = 'Which database should the migration use?', ...rest } = over
  const question = (rest.kind ?? 'question') === 'question'
  return { kind: 'question', id: 'q1', title: question ? text : '', text: question ? '' : text, options: [{ key: '1', label: 'pg', role: 'pick' }], since: 5, ...rest }
}
const withAsk = (id: string, status: string, a: object | null, extra: object = {}) => ({ ...sess(id, status), ask: a, ...extra })

test('waiting without an ask alerts with the reason and the agent page; the ask arriving raises a second alert with the same tag; then nothing', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const first = diffAlerts(seen, snap([withAsk('1', 'waiting', null)]))
  assert.equal(first.length, 1)
  assert.equal(first[0].body, 'permission prompt')
  assert.equal(first[0].href, '#/s/1/a/lead')
  const second = diffAlerts(seen, snap([withAsk('1', 'waiting', ask())]))
  assert.equal(second.length, 1)
  assert.equal(second[0].tag, first[0].tag)
  assert.equal(second[0].body, 'Question: Which database should the migration use?')
  assert.equal(second[0].href, '#/s/1/a/lead')
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask())])).length, 0)
})

test('an ask known when the session starts waiting is in the first alert and raises no second', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const a = diffAlerts(seen, snap([withAsk('1', 'waiting', ask())]))
  assert.equal(a.length, 1)
  assert.match(a[0].body, /^Question: /)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask())])).length, 0)
})

test('the ask changing from one prompt to another in one waiting spell raises one more alert with the same tag; a gap in the ask does not', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const a = diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ id: 'A', text: 'first' }))]))
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', null)])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ id: 'A', text: 'first' }))])).length, 0)
  const b = diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ id: 'B', text: 'second' }))]))
  assert.equal(b.length, 1)
  assert.equal(b[0].tag, a[0].tag)
  assert.equal(b[0].body, 'Question: second')
})

test('waiting, then idle, then waiting again is a new spell with its own alert', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  diffAlerts(seen, snap([withAsk('1', 'waiting', ask())]))
  assert.deepEqual(diffAlerts(seen, snap([withAsk('1', 'idle', null)])).map(x => x.title), ['n1 finished'])
  const again = diffAlerts(seen, snap([withAsk('1', 'waiting', ask())]))
  assert.equal(again.length, 1)
  assert.equal(again[0].title, 'n1 needs input')
})

test('a permission prompt\'s alert states what is asked and never offers to answer from the popup', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const perm = ask({ kind: 'permission', id: 'p', text: 'rm -rf build', options: [{ key: '1', label: 'Yes', role: 'allow' }, { key: '4', label: 'No', role: 'deny' }] })
  const [a] = diffAlerts(seen, snap([withAsk('1', 'waiting', perm)]))
  assert.equal(a.body, 'Permission needed: rm -rf build')
  assert.doesNotMatch(a.title + a.body, /allow|approve|click|deny/i)
})

test('a teammate\'s alert names the agent and opens its page under the lead\'s session', () => {
  const lead = { ...sess('10', 'busy'), name: 'plan', sessionId: 'lead-uuid', agents: [{ id: 'mate-uuid', type: 'teammate', description: 'dev' }] }
  const mate = (status: string, a: object | null) => ({ ...withAsk('11', status, a), name: 'the-team', sessionId: 'mate-uuid', team: { name: 'the-team', leadSessionId: 'lead-uuid', leadId: '10' } })
  const seen = newSeen()
  diffAlerts(seen, snap([lead, mate('busy', null)]))
  const [a] = diffAlerts(seen, snap([lead, mate('waiting', ask())]))
  assert.equal(a.title, 'the-team · dev needs input')
  assert.equal(a.href, '#/s/10/a/mate-uuid')
})

test('a teammate whose lead is not live falls back to its own page', () => {
  const mate = (status: string) => ({ ...withAsk('11', status, null), name: 'the-team', sessionId: 'mate-uuid', team: { name: 'the-team', leadSessionId: 'lead-uuid', leadId: null } })
  const seen = newSeen()
  diffAlerts(seen, snap([mate('busy')]))
  const [a] = diffAlerts(seen, snap([mate('waiting')]))
  assert.equal(a.title, 'the-team needs input')
  assert.equal(a.href, '#/s/11/a/lead')
})

test('what comes off the screen is text: bidi controls are escaped, control characters and newlines flattened, and the cut comes after', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const evil = ask({ text: 'echo hi\n\u202Edrowssap\u0007\u0000' + 'x'.repeat(300) })
  const [a] = diffAlerts(seen, snap([{ ...withAsk('1', 'waiting', evil), name: 'we\u202Eird\nname' }]))
  assert.equal(a.title, 'we<U+202E>ird name needs input')
  assert.match(a.body, /^Question: echo hi <U\+202E>drowssap /)
  assert.doesNotMatch(a.body, /[\u0000-\u001f\u202E]/)
  // invisible characters are shown, not dropped
  assert.equal(oneLine('rm\u200B -rf\u2028x', 80), 'rm<U+200B> -rf<U+2028>x')
  assert.ok(a.body.length <= 'Question: '.length + 121)
})

test('oneLine never cuts inside an escape or a surrogate pair', () => {
  // the cut lands right after the 4 hex digits of an escape, before its `>`
  assert.equal(oneLine('aaaaa\u202Ezz', 12), 'aaaaa…')
  // after `<U+`, `<U` and `<`
  assert.equal(oneLine('aaaaa\u202Ezz', 8), 'aaaaa…')
  assert.equal(oneLine('aaaaa\u202Ezz', 6), 'aaaaa…')
  assert.equal(oneLine('aaaaa\u202Ezz', 5), 'aaaaa…')
  // a whole escape that fits is kept
  assert.equal(oneLine('aaaaa\u202Ezz', 13), 'aaaaa<U+202E>…')
  // inside the 5-digit escape of a tag character
  assert.equal(oneLine('aaaaa\u{E0041}zz', 12), 'aaaaa…')
  assert.equal(oneLine('aaaaa\u{E0041}zz', 14), 'aaaaa<U+E0041>…')
  // between the two halves of an emoji
  assert.equal(oneLine('ab\u{1F600}cd', 3), 'ab…')
  assert.equal(oneLine('ab\u{1F600}cd', 4), 'ab\u{1F600}…')
  // short enough: untouched
  assert.equal(oneLine('short', 80), 'short')
  // and through a title
  const name = 'n'.repeat(75) + '\u202E' + 'z'.repeat(30)
  const [t] = diffAlerts(newSeenAfterBusy(), snap([{ ...withAsk('1', 'waiting', null), name }]))
  assert.equal(t.title, 'n'.repeat(75) + '… needs input')
})

test('the body shows at most 120 characters of the ask, then an ellipsis', () => {
  const [a] = diffAlerts(newSeenAfterBusy(), snap([withAsk('1', 'waiting', ask({ text: 'y'.repeat(300) }))]))
  assert.equal(a.body, 'Question: ' + 'y'.repeat(120) + '…')
  const [b] = diffAlerts(newSeenAfterBusy(), snap([withAsk('1', 'waiting', ask({ text: 'z'.repeat(120) }))]))
  assert.equal(b.body, 'Question: ' + 'z'.repeat(120))
})

test('the tag is `s` and the session id: one per session, kept by a later spell so its alert replaces the earlier one still on screen', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null), withAsk('2', 'busy', null)]))
  const both = diffAlerts(seen, snap([withAsk('1', 'waiting', ask()), withAsk('2', 'waiting', ask({ id: 'q2', text: 'other' }))]))
  assert.deepEqual(both.map(x => x.tag), ['s1', 's2'])
  assert.deepEqual(both.map(x => x.href), ['#/s/1/a/lead', '#/s/2/a/lead'])
  assert.deepEqual(both.map(x => x.title), ['n1 needs input', 'n2 needs input'])
  // each session keeps its own record: a new ask for one raises one alert, for that one
  const next = diffAlerts(seen, snap([withAsk('1', 'waiting', ask()), withAsk('2', 'waiting', ask({ id: 'q3', text: 'third' }))]))
  assert.deepEqual(next.map(x => [x.tag, x.body]), [['s2', 'Question: third']])
  // a new spell after idle is a fresh alert with the same tag
  diffAlerts(seen, snap([withAsk('1', 'idle', null), withAsk('2', 'waiting', ask({ id: 'q3', text: 'third' }))]))
  const again = diffAlerts(seen, snap([withAsk('1', 'waiting', ask()), withAsk('2', 'waiting', ask({ id: 'q3', text: 'third' }))]))
  assert.deepEqual(again.map(x => [x.tag, x.title]), [['s1', 'n1 needs input']])
})

test('a session that vanishes mid-spell raises nothing, and one that comes back waiting is a new spell', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  diffAlerts(seen, snap([withAsk('1', 'waiting', ask())]))
  assert.deepEqual(diffAlerts(seen, snap([])), [])
  const back = diffAlerts(seen, snap([withAsk('1', 'waiting', ask())]))
  assert.equal(back.length, 1)
  assert.equal(back[0].tag, 's1')
})

function newSeenAfterBusy() {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  return seen
}

test('an old row without an ask field is waiting without a question', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([sess('1', 'busy')]))
  const [a] = diffAlerts(seen, snap([sess('1', 'waiting')]))
  assert.equal(a.body, 'permission prompt')
})

test('a question\'s alert says the question itself, as the parser returns it (the words in the title, an empty text)', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const [a] = diffAlerts(seen, snap([withAsk('1', 'waiting', { kind: 'question', id: 'q', title: 'Red or blue?', text: '', options: [], since: 1 })]))
  assert.equal(a.body, 'Question: Red or blue?')
})

test('an untitled unknown (screen text) that changes under its own feet alerts once; a titled one (a downgraded dialog) alerts when its words are new', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const text = (id: string, t: string) => ask({ kind: 'unknown', id, title: '', text: t, options: [] })
  const dialog = (id: string, t: string) => ask({ kind: 'unknown', id, title: 'Bash command', text: t, options: [] })
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', null)])).length, 1)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', text('u1', 'a'))])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', text('u2', 'b'))])).length, 0)
  // a real dialog, downgraded: worth an alert; the same words under another id are not; a new dialog is
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', dialog('d1', 'ls'))])).length, 1)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', dialog('d2', 'ls'))])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', dialog('d3', 'rm -rf build'))])).length, 1)
})

test('one spell is at most the status flip and the prompt: a passing frame read as untitled unknown text raises nothing', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', null)])).length, 1)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ kind: 'unknown', id: 'u1', text: 'Running…', options: [] }))])).length, 0)
  const perm = ask({ kind: 'permission', id: 'p', text: 'rm -rf build', options: [{ key: '1', label: 'Yes', role: 'allow' }, { key: '4', label: 'No', role: 'deny' }] })
  assert.deepEqual(diffAlerts(seen, snap([withAsk('1', 'waiting', perm)])).map(x => x.body), ['Permission needed: rm -rf build'])
})

test('a shared-workspace permission after a passing frame still alerts, with its command', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', null)])).length, 1)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ kind: 'unknown', id: 'u1', text: 'Running…', options: [] }))])).length, 0)
  const shared = ask({ kind: 'unknown', id: 'u2', title: 'Bash command', text: 'rm -rf build', options: [] })
  assert.deepEqual(diffAlerts(seen, snap([withAsk('1', 'waiting', shared)])).map(x => x.body), ['Needs input: Bash command: rm -rf build'])
})

test('a permission downgraded to unknown (a shared workspace, a misnamed wait) keeps its command in the alert', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const shared = ask({ kind: 'unknown', id: 'u', title: 'Bash command', text: 'rm -rf build', options: [] })
  const [a] = diffAlerts(seen, snap([withAsk('1', 'waiting', shared)]))
  assert.equal(a.body, 'Needs input: Bash command: rm -rf build')
})

test('a shared-workspace flap (question, its downgrade, the question again) alerts for the question once', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'busy', null)]))
  const q = ask({ id: 'Q', text: 'Red or blue?' })
  // the downgrade keeps the words and drops the options and the kind, under a new id
  const u = { ...q, kind: 'unknown', id: 'U', options: [] }
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', q)])).length, 1)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', u)])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', q)])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', u)])).length, 0)
  // a different question in the same spell still alerts
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ id: 'Q2', text: 'Left or right?' }))])).length, 1)
})

test('the same flap from the first prompt the tab saw raises nothing either', () => {
  const seen = newSeen()
  const q = ask({ id: 'Q', text: 'Red or blue?' })
  diffAlerts(seen, snap([withAsk('1', 'waiting', q)]))
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', { ...q, kind: 'unknown', id: 'U', options: [] })])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', q)])).length, 0)
})

test('a session already waiting when the tab opens raises nothing when its screen is read; a later new prompt in that spell does', () => {
  const seen = newSeen()
  diffAlerts(seen, snap([withAsk('1', 'waiting', null)]))
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', null)])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ id: 'A', text: 'first' }))])).length, 0)
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', ask({ id: 'B', text: 'second' }))])).length, 1)
  // a passing frame read first does not make the prompt already on screen at load alert
  const seen2 = newSeen()
  diffAlerts(seen2, snap([withAsk('1', 'waiting', ask({ kind: 'unknown', id: 'u', text: 'Running…', options: [] }))]))
  assert.equal(diffAlerts(seen2, snap([withAsk('1', 'waiting', ask({ id: 'A', text: 'first' }))])).length, 0)
  assert.equal(diffAlerts(seen2, snap([withAsk('1', 'waiting', ask({ id: 'B', text: 'second' }))])).length, 1)
  // and a new spell after it alerts as usual
  diffAlerts(seen, snap([withAsk('1', 'idle', null)]))
  assert.equal(diffAlerts(seen, snap([withAsk('1', 'waiting', null)])).length, 1)
})

test('an unknown ask with no text still gives a body that says something', () => {
  const [a] = diffAlerts((() => { const s = newSeen(); diffAlerts(s, snap([withAsk('1', 'busy', null)])); return s })(), snap([withAsk('1', 'waiting', ask({ kind: 'unknown', id: 'u', text: '', options: [] }))]))
  assert.equal(a.body, 'Needs input')
})

// ---- My PRs: a row that needs me ----

const prDraft = (over: Partial<MyPrDraft> = {}): MyPrDraft => ({ sha: 'abc123', text: 'draft', state: 'saved', error: null, at: 0, tries: 1, ...over })
const prRow = (over: Partial<MyPrRow> = {}): MyPrRow => ({ repo: 'org/repo', number: 7, title: 'Fix the thing', status: 'ready', slackDraft: prDraft(), ...over }) as MyPrRow
const beforePoll = () => ({ at: 0, sessions: [], runs: [], myPrs: { at: 0, me: null, error: null, loaded: false, draftMaxTries: 3, rows: [] } }) as unknown as Snapshot
const withPrs = (...rows: MyPrRow[]) => ({ at: 0, sessions: [], runs: [], myPrs: { at: 0, loaded: true, draftMaxTries: 3, rows } }) as unknown as Snapshot

test('diffAlerts raises one alert when a row becomes ready and none on the next snapshot', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs(prRow({ status: 'waiting', slackDraft: null })))
  const a = diffAlerts(seen, withPrs(prRow()))
  assert.deepEqual(a, [{ tag: 'myprs:org/repo#7@abc123:saved', title: 'Ready to post', body: 'Fix the thing', href: '#/myprs' }])
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 0)
})

test('a new sha raises a new alert', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 1)
  // a later round pushes a new head, goes back to waiting and is ready again with a draft for the new one
  diffAlerts(seen, withPrs(prRow({ status: 'waiting', slackDraft: null })))
  const a = diffAlerts(seen, withPrs(prRow({ slackDraft: prDraft({ sha: 'def456' }) })))
  assert.deepEqual(a.map(x => x.tag), ['myprs:org/repo#7@def456:saved'])
  // the first head going ready again (a force-pushed back head) is not raised twice
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 0)
})

test('a draft-failed row also alerts with the failure in the body', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  const failed = prRow({ status: 'draft-failed', slackDraft: prDraft({ state: 'failed', error: 'slack\u202e signed out', tries: 3 }) })
  const a = diffAlerts(seen, withPrs(failed))
  assert.equal(a.length, 1)
  assert.equal(a[0].title, 'Draft needs you')
  assert.equal(a[0].tag, 'myprs:org/repo#7@abc123:failed')
  assert.equal(a[0].href, '#/myprs')
  assert.ok(a[0].body.startsWith('Fix the thing'))
  assert.ok(a[0].body.includes('signed out'))
  // control and bidi characters are made visible, as for every other alert
  assert.ok(!a[0].body.includes('\u202e'))
  assert.equal(diffAlerts(seen, withPrs(failed)).length, 0)
  // the draft saved after all, for the same head, is a new thing to tell
  assert.deepEqual(diffAlerts(seen, withPrs(prRow())).map(x => x.title), ['Ready to post'])
})

test('a plain ready row with no draft yet does not alert', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs(prRow({ status: 'running', slackDraft: null })))
  assert.equal(diffAlerts(seen, withPrs(prRow({ status: 'ready', slackDraft: null }))).length, 0)
  assert.equal(diffAlerts(seen, withPrs(prRow({ status: 'ready', slackDraft: undefined }))).length, 0)
  // and the saved draft arriving then does
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 1)
})

test('rows already ready when the tab opened do not alert, even when the first snapshot has no My PRs yet', () => {
  const seen = newSeen()
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 0)
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 0)
  const late = newSeen()
  diffAlerts(late, snap([]))
  // the first My PRs view is the one that seeds
  assert.equal(diffAlerts(late, withPrs(prRow())).length, 0)
  assert.equal(diffAlerts(late, withPrs(prRow(), prRow({ number: 8 }))).length, 1)
})

test('rows already ready are not alerted when the view before the first poll came first', () => {
  const seen = newSeen()
  // the server sends a view with no rows and nothing loaded until its first poll has succeeded: it must not seed
  assert.equal(diffAlerts(seen, beforePoll()).length, 0)
  assert.equal(diffAlerts(seen, withPrs(prRow())).length, 0)
  assert.equal(diffAlerts(seen, withPrs(prRow(), prRow({ number: 8 }))).length, 1)
  // a person with no PRs at all seeds on the loaded empty view
  const none = newSeen()
  diffAlerts(none, beforePoll())
  diffAlerts(none, withPrs())
  assert.equal(diffAlerts(none, withPrs(prRow())).length, 1)
})

test('a failed draft keeps its error when the PR title is long', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  const [a] = diffAlerts(seen, withPrs(prRow({ title: 'x'.repeat(150), status: 'draft-failed', slackDraft: prDraft({ state: 'failed', error: 'slack signed out', tries: 3 }) })))
  assert.ok(a.body.includes('slack signed out'))
  assert.ok(a.body.length <= 125)
})

const failedRow = (tries: number, error: string | null = 'slack signed out') => prRow({ status: 'draft-failed', slackDraft: prDraft({ state: 'failed', error, tries }) })

test('a failed draft that is still being retried does not alert, and the last try failing alerts once', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  assert.equal(diffAlerts(seen, withPrs(failedRow(1))).length, 0)
  assert.equal(diffAlerts(seen, withPrs(failedRow(2))).length, 0)
  const a = diffAlerts(seen, withPrs(failedRow(3)))
  assert.deepEqual(a.map(x => x.title), ['Draft needs you'])
  assert.equal(diffAlerts(seen, withPrs(failedRow(3))).length, 0)
})

test('a draft that fails, is retried and saves raises Ready to post once, and no failure alert', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  assert.equal(diffAlerts(seen, withPrs(failedRow(1))).length, 0)
  const a = diffAlerts(seen, withPrs(prRow({ slackDraft: prDraft({ tries: 2 }) })))
  assert.deepEqual(a.map(x => x.title), ['Ready to post'])
  assert.equal(diffAlerts(seen, withPrs(prRow({ slackDraft: prDraft({ tries: 2 }) }))).length, 0)
})

test('a failed draft at the cap that was there when the tab opened is seeded, not alerted, and a saved one beside it too', () => {
  const seen = newSeen()
  assert.equal(diffAlerts(seen, withPrs(failedRow(3), prRow({ number: 8 }))).length, 0)
  assert.equal(diffAlerts(seen, withPrs(failedRow(3), prRow({ number: 8 }))).length, 0)
  // a failed draft still retrying at load is not seeded: when its last try fails it is news
  const seen2 = newSeen()
  diffAlerts(seen2, withPrs(failedRow(1)))
  assert.deepEqual(diffAlerts(seen2, withPrs(failedRow(3))).map(x => x.title), ['Draft needs you'])
})

test('a draft already saved when the tab opened is seeded whatever the row says then, so the row turning ready later does not alert', () => {
  // the tab opens while a new round runs on the head (or in the idle wait after it ends); the round pushes nothing and the row is ready again
  for (const status of ['running', 'idle', 'waiting', 'findings', 'no-clone'] as const) {
    const seen = newSeen()
    assert.equal(diffAlerts(seen, withPrs(prRow({ status }))).length, 0, status)
    assert.equal(diffAlerts(seen, withPrs(prRow())).length, 0, status)
    // a new head is still news
    assert.equal(diffAlerts(seen, withPrs(prRow({ slackDraft: prDraft({ sha: 'def456' }) }))).length, 1, status)
  }
  // a failed draft is not seeded unless it already needs me: its tag is the same for every try, so its last try failing is news
  const seen = newSeen()
  diffAlerts(seen, withPrs(prRow({ status: 'running', slackDraft: prDraft({ state: 'failed', error: 'x', tries: 1 }) })))
  assert.deepEqual(diffAlerts(seen, withPrs(failedRow(3))).map(x => x.title), ['Draft needs you'])
})

test('a draft that failed for good is seeded at load whatever the row says, so a no-push round handing it back to draft-failed does not alert', () => {
  // the tab opens while a Re-Review runs: the failed draft is at the cap and can never fail again, so it is not news when the row returns
  const seen = newSeen()
  assert.equal(diffAlerts(seen, withPrs(prRow({ status: 'running', slackDraft: prDraft({ state: 'failed', error: 'x', tries: 3 }) }))).length, 0)
  assert.equal(diffAlerts(seen, withPrs(failedRow(3))).length, 0)
  // the same tab open all along does not alert either, and a new head still does
  assert.deepEqual(diffAlerts(seen, withPrs(prRow({ status: 'draft-failed', slackDraft: prDraft({ state: 'failed', error: 'x', tries: 3, sha: 'def456' }) }))).map(x => x.title), ['Draft needs you'])
})

test('an empty error or title never leaves a bare colon in the body', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  const [a] = diffAlerts(seen, withPrs(failedRow(3, '')))
  assert.equal(a.body, 'Fix the thing: the draft failed')
  const seen2 = newSeen()
  diffAlerts(seen2, withPrs())
  const [b] = diffAlerts(seen2, withPrs(prRow({ title: '', status: 'draft-failed', slackDraft: prDraft({ state: 'failed', error: 'slack signed out', tries: 3 }) })))
  assert.equal(b.body, 'slack signed out')
})

test('a title with control characters is one clean line in the alert', () => {
  const seen = newSeen()
  diffAlerts(seen, withPrs())
  const [a] = diffAlerts(seen, withPrs(prRow({ title: 'a\nb\u202ec' })))
  assert.ok(!/[\n\u202e]/.test(a.body))
})
