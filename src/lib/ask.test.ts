import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ARM_MS, waitingAgents, CHANGED, STUCK_MS, UNSURE, answerBody, armedId, askFromJson, askOwner, begin, isLocked, isStuck, linesBelow, mustScrollFirst, newPanel, onResult, panelView, parseAnswer, parseOk, pendingBody, plainText, pressable, readGate, shownAsk, statusLine, sync, viaName, waitedFor } from './ask.ts'
import type { Agent, Ask, SessionDetail } from './types.ts'

const agent = (over: Partial<Agent>): Agent => ({ id: 'a1', type: 'Explore', description: '', status: 'running', ...over }) as Agent
const sess = (over: Partial<SessionDetail>): SessionDetail =>
  ({ id: 'lead-1', sessionId: 'lead-uuid', status: 'idle', canReply: true, ask: null, agents: [], updatedAt: 1000, waitingFor: null, ...over }) as SessionDetail
const ask = (over: Partial<Ask> = {}): Ask => ({ kind: 'question', id: 'h1', title: 't', text: 'Which?', options: [{ key: '1', label: 'A', role: 'pick' }], since: 5000, ...over })
const PERM: Ask = ask({
  kind: 'permission',
  options: [
    { key: '1', label: 'Yes', role: 'allow' },
    { key: '4', label: 'No', role: 'deny' },
  ],
})

test('the lead of a session is answered in the session itself', () => {
  const s = sess({ status: 'waiting' })
  assert.deepEqual(askOwner(s, 'lead', [s]), { session: s, viaLead: false })
  const idle = sess({})
  assert.deepEqual(askOwner(idle, 'lead', [idle]), { session: idle, viaLead: false })
})

const mateOf = (id: string, uuid: string, leadId = 'lead-1') => sess({ id, sessionId: uuid, team: { name: 't', leadSessionId: 'x', leadId } })

test('a teammate is answered in the live session whose uuid is the agent id', () => {
  const mate = mateOf('4242', 'uuid-of-mate')
  const lead = sess({ agents: [agent({ id: 'uuid-of-mate', type: 'teammate', description: 'dev' })] })
  assert.deepEqual(askOwner(lead, 'uuid-of-mate', [lead, mate]), { session: mate, viaLead: false })
})

test('with two live teammates, each agent maps to its own session', () => {
  const m1 = mateOf('1', 'u1')
  const m2 = mateOf('2', 'u2')
  const lead = sess({ agents: [agent({ id: 'u1', type: 'teammate' }), agent({ id: 'u2', type: 'teammate' })] })
  assert.equal(askOwner(lead, 'u1', [lead, m2, m1])?.session, m1)
  assert.equal(askOwner(lead, 'u2', [lead, m2, m1])?.session, m2)
})

test('a teammate agent with no live session of its own has no owner, nor does one whose session names another lead', () => {
  const lead = sess({ status: 'waiting', agents: [agent({ id: 'u1', type: 'teammate' })] })
  assert.equal(askOwner(lead, 'u1', [lead]), null)
  assert.equal(askOwner(lead, 'u1', [lead, mateOf('1', 'u2')]), null)
  assert.equal(askOwner(lead, 'u1', [lead, mateOf('1', 'u1', 'someone-else')]), null)
})

test('a subagent has the lead as its owner, only while the lead is waiting', () => {
  const waiting = sess({ status: 'waiting', agents: [agent({ id: 'sub-1' })] })
  assert.deepEqual(askOwner(waiting, 'sub-1', [waiting]), { session: waiting, viaLead: true, via: 'lead' })
  const busy = sess({ status: 'busy', agents: [agent({ id: 'sub-1' })] })
  assert.equal(askOwner(busy, 'sub-1', [busy]), null)
})

test('a running or stalled subagent is waiting through the lead (a blocked one turns stalled); a finished one is not', () => {
  for (const status of ['running', 'stalled'] as const) {
    const s = sess({ status: 'waiting', agents: [agent({ id: 'sub-1', status })] })
    assert.deepEqual(askOwner(s, 'sub-1', [s]), { session: s, viaLead: true, via: 'lead' })
  }
  const done = sess({ status: 'waiting', agents: [agent({ id: 'sub-1', status: 'done' })] })
  assert.equal(askOwner(done, 'sub-1', [done]), null)
})

// The server puts a teammate's own subagents in the lead's roster with `parentId` naming the teammate
const teamOf = (leadStatus: string, mateStatus: string | null) => {
  const lead = sess({
    status: leadStatus as SessionDetail['status'],
    agents: [
      agent({ id: 'mate-uuid', type: 'teammate', description: 'dev', parentId: null }),
      agent({ id: 'mate-sub', parentId: 'mate-uuid', depth: 2 }),
      agent({ id: 'mate-sub-sub', parentId: 'mate-sub', depth: 3 }),
      agent({ id: 'lead-sub', parentId: null }),
    ],
  })
  const mate = mateStatus ? { ...mateOf('4242', 'mate-uuid'), status: mateStatus as SessionDetail['status'] } : null
  return { lead, mate, sessions: mate ? [lead, mate] : [lead] }
}

test('a teammate\'s own subagent is answered through the teammate\'s session, never through a lead waiting on its own prompt', () => {
  // the lead waits on its own prompt, the teammate is busy: the teammate's subagent is not waiting on anything
  let t = teamOf('waiting', 'busy')
  assert.equal(askOwner(t.lead, 'mate-sub', t.sessions), null)
  assert.equal(askOwner(t.lead, 'mate-sub-sub', t.sessions), null)
  assert.deepEqual(askOwner(t.lead, 'lead-sub', t.sessions), { session: t.lead, viaLead: true, via: 'lead' })
  // the teammate waits (its subagent asked), the lead is busy: the subagent points to the teammate, at any depth
  t = teamOf('busy', 'waiting')
  assert.deepEqual(askOwner(t.lead, 'mate-sub', t.sessions), { session: t.mate, viaLead: true, via: 'mate-uuid' })
  assert.deepEqual(askOwner(t.lead, 'mate-sub-sub', t.sessions), { session: t.mate, viaLead: true, via: 'mate-uuid' })
  assert.equal(askOwner(t.lead, 'lead-sub', t.sessions), null)
  assert.equal(viaName(t.lead, 'mate-uuid'), 'teammate dev')
  assert.equal(viaName(t.lead, 'lead'), 'the lead')
})

test('a teammate\'s subagent whose teammate has no live session falls back to the lead', () => {
  const t = teamOf('waiting', null)
  assert.deepEqual(askOwner(t.lead, 'mate-sub', t.sessions), { session: t.lead, viaLead: true, via: 'lead' })
})

test('a spawn line that loops or breaks is walked safely', () => {
  const loop = sess({ status: 'waiting', agents: [agent({ id: 'x', parentId: 'y' }), agent({ id: 'y', parentId: 'x' })] })
  assert.deepEqual(askOwner(loop, 'x', [loop]), { session: loop, viaLead: true, via: 'lead' })
  const broken = sess({ status: 'waiting', agents: [agent({ id: 'x', parentId: 'gone' })] })
  assert.deepEqual(askOwner(broken, 'x', [broken]), { session: broken, viaLead: true, via: 'lead' })
})

test('an agent the session does not have has no owner', () => {
  const s = sess({ status: 'waiting', agents: [agent({ id: 'sub-1' })] })
  assert.equal(askOwner(s, 'nobody', [s]), null)
})

test('waitedFor is the time since the ask appeared', () => {
  assert.equal(waitedFor(ask({ since: 5000 }), 5000 + 75_000), '1m 15s')
  assert.equal(waitedFor(ask({ since: 5000 }), 3000), '0s')
})

test('the panel is empty for a session that is not waiting and has no ask, even when ask is missing from an old row', () => {
  assert.equal(panelView(sess({}), false).view, 'none')
  assert.equal(panelView({ ...sess({}), ask: undefined } as unknown as SessionDetail, false).view, 'none')
})

test('waiting with no ask yet shows the state and no buttons', () => {
  assert.equal(panelView(sess({ status: 'waiting' }), false).view, 'pending')
  assert.equal(panelView({ ...sess({ status: 'waiting' }), ask: undefined } as unknown as SessionDetail, false).view, 'pending')
})

test('a subagent page and a session without cmux get their own views, ahead of the ask', () => {
  assert.equal(panelView(sess({ status: 'waiting', ask: PERM }), true).view, 'lead')
  assert.equal(panelView(sess({ status: 'waiting', canReply: false, ask: PERM }), false).view, 'nocmux')
  assert.equal(panelView(sess({ status: 'waiting', canReply: false }), false).view, 'nocmux')
})

test('a permission with exactly one allow and one deny has the two buttons', () => {
  const v = panelView(sess({ status: 'waiting', ask: PERM }), false)
  assert.equal(v.view, 'permission')
  assert.equal(v.allow?.key, '1')
  assert.equal(v.deny?.key, '4')
})

test('a permission that is not exactly allow and deny falls back to unknown', () => {
  const o = (key: string, role: 'pick' | 'allow' | 'deny') => ({ key, label: key, role })
  for (const options of [
    [o('1', 'allow')],
    [o('4', 'deny')],
    [o('1', 'allow'), o('2', 'allow')],
    [o('1', 'allow'), o('2', 'pick'), o('4', 'deny')],
    [o('1', 'allow'), o('4', 'pick')],
    [],
  ]) {
    assert.equal(panelView(sess({ status: 'waiting', ask: ask({ kind: 'permission', options }) }), false).view, 'unknown')
  }
})

test('a question has one button per option; none, or a non-pick option, falls back to unknown', () => {
  const two = ask({ options: [{ key: '1', label: 'A', role: 'pick' }, { key: '2', label: 'B', role: 'pick' }] })
  assert.equal(panelView(sess({ status: 'waiting', ask: two }), false).view, 'question')
  assert.equal(panelView(sess({ status: 'waiting', ask: ask({ options: [] }) }), false).view, 'unknown')
  assert.equal(panelView(sess({ status: 'waiting', ask: ask({ options: [{ key: '1', label: 'A', role: 'allow' }] }) }), false).view, 'unknown')
  const dup = ask({ options: [{ key: '1', label: 'A', role: 'pick' }, { key: '1', label: 'B', role: 'pick' }] })
  assert.equal(panelView(sess({ status: 'waiting', ask: dup }), false).view, 'unknown')
})

test('text and unknown asks follow their kind, also after a deny leaves an empty box', () => {
  assert.equal(panelView(sess({ status: 'waiting', ask: ask({ kind: 'text', options: [] }) }), false).view, 'text')
  assert.equal(panelView(sess({ status: 'waiting', ask: ask({ kind: 'unknown', options: [] }) }), false).view, 'unknown')
})

test('the answer body carries the id of the prompt shown and the choice', () => {
  assert.deepEqual(answerBody('h1', '4'), { askId: 'h1', choice: '4' })
})

test('a 200 saying ok is sent; a 409 carries the ask the server saw, never an error to resend', () => {
  assert.deepEqual(parseAnswer(200, { ok: true }), { ok: true })
  assert.deepEqual(parseAnswer(409, { error: 'changed', ask: PERM }), { ok: false, conflict: true, ask: PERM, error: 'changed' })
  assert.deepEqual(parseAnswer(409, { error: 'changed', ask: null }), { ok: false, conflict: true, ask: null, error: 'changed' })
  assert.deepEqual(parseAnswer(409, null), { ok: false, conflict: true, ask: null, error: 'HTTP 409' })
})

test('a 409 ask that is not shaped like an ask is dropped', () => {
  assert.equal(parseAnswer(409, { ask: 'x' }).ok, false)
  assert.deepEqual(parseAnswer(409, { ask: 'x' }), { ok: false, conflict: true, ask: null, error: 'HTTP 409' })
  assert.equal((parseAnswer(409, { ask: { kind: 'bogus', id: 'x' } }) as { ask: Ask | null }).ask, null)
  assert.equal(askFromJson({ kind: 'question', id: 'x', title: 't', text: 'q', options: [{ key: 1 }], since: 1 }), null)
  assert.deepEqual(askFromJson({ kind: 'text', id: 'x', title: 't', text: '', options: [], since: 1 }), { kind: 'text', id: 'x', title: 't', text: '', options: [], since: 1 })
})

test('other statuses are errors with the server\'s message, or the status; a 5xx may have pressed the key', () => {
  assert.deepEqual(parseAnswer(400, { error: 'bad choice' }), { ok: false, error: 'bad choice' })
  assert.deepEqual(parseAnswer(404, { error: 42 }), { ok: false, error: 'HTTP 404' })
  assert.deepEqual(parseAnswer(502, { error: 'cmux said no' }), { ok: false, unsure: true, error: 'cmux said no' })
  assert.deepEqual(parseAnswer(500, null), { ok: false, unsure: true, error: 'HTTP 500' })
})

test('a 2xx counts as sent only when its body is {ok: true}: anything else (the SPA shell, an old server) is not sure', () => {
  for (const body of [null, '<!doctype html>', {}, { ok: 'yes' }, { ok: 1 }, []]) {
    const r = parseAnswer(200, body)
    assert.equal(r.ok, false)
    assert.equal((r as { unsure?: boolean }).unsure, true)
  }
})

test('OPEN TERMINAL is ok only on a 2xx with {ok: true}, and its error is always a string', () => {
  assert.deepEqual(parseOk(200, { ok: true }), { ok: true })
  assert.deepEqual(parseOk(200, '<html>'), { ok: false, error: 'HTTP 200' })
  assert.deepEqual(parseOk(502, { error: 'cmux would not open it' }), { ok: false, error: 'cmux would not open it' })
  assert.deepEqual(parseOk(409, { error: { nested: true } }), { ok: false, error: 'HTTP 409' })
  assert.deepEqual(parseOk(500, null), { ok: false, error: 'HTTP 500' })
})

test('a key that is not one digit 1-9, or a question of more than 7 options, is the unknown layout', () => {
  const pick = (key: string) => ({ key, label: key, role: 'pick' as const })
  const w = (a: Ask) => panelView(sess({ status: 'waiting', ask: a }), false).view
  assert.equal(w(ask({ options: [pick('1'), pick('10')] })), 'unknown')
  assert.equal(w(ask({ options: [pick('0')] })), 'unknown')
  assert.equal(w(ask({ options: [pick('a')] })), 'unknown')
  assert.equal(w(ask({ kind: 'permission', options: [{ key: '1', label: 'Yes', role: 'allow' }, { key: 'n', label: 'No', role: 'deny' }] })), 'unknown')
  const keys = (n: number) => Array.from({ length: n }, (_, i) => pick(String(i + 1)))
  assert.equal(w(ask({ options: keys(7) })), 'question')
  assert.equal(w(ask({ options: keys(8) })), 'unknown')
})

test('plainText shows bidi controls as visible escapes and leaves the rest alone', () => {
  assert.equal(plainText('rm \u202Efdp.txt'), 'rm <U+202E>fdp.txt')
  assert.equal(plainText('a\u2066b\u2069c\u061Cd\u200Ee\u200Ff\u202Ag\u202D'), 'a<U+2066>b<U+2069>c<U+061C>d<U+200E>e<U+200F>f<U+202A>g<U+202D>')
  assert.equal(plainText('plain <b>text</b>\n  ünï\tx 😀'), 'plain <b>text</b>\n  ünï\tx 😀')
})

test('plainText also shows zero-width, tag, line-separator and control characters, keeping newline and tab', () => {
  assert.equal(plainText('rm\u200B -rf\u2060 /\u00AD\uFEFF\u200C\u200D'), 'rm<U+200B> -rf<U+2060> /<U+00AD><U+FEFF><U+200C><U+200D>')
  // tag characters are astral: one escape each, never half a surrogate pair
  assert.equal(plainText('ok\u{E0041}\u{E007F}'), 'ok<U+E0041><U+E007F>')
  assert.equal(plainText('a\u2028b\u2029c'), 'a<U+2028>b<U+2029>c')
  assert.equal(plainText('\u0000\u0007\u001B[31m\r\u007F\u0085\u009B'), '<U+0000><U+0007><U+001B>[31m<U+000D><U+007F><U+0085><U+009B>')
  assert.equal(plainText('a\nb\tc'), 'a\nb\tc')
})

// ---- the panel's state ----

const A = ask({ id: 'A' })
const B = ask({ id: 'B' })
const T0 = 1_000_000
/** Snapshot `at`s: one per push from the server */
const S1 = 501
const S2 = 502
const shown = (st: ReturnType<typeof newPanel>, live: Ask | null, now: number, waiting = true, at = S1) => sync(st, live, waiting, now, at)
const answered = (st: ReturnType<typeof newPanel>, clicked: string, onShow: Ask | null, r: Parameters<typeof onResult>[3], now: number, live: Ask | null = onShow, at = S1) =>
  sync(onResult(begin(st), clicked, onShow, r, now, at), live, true, now, at)

test('the answer buttons arm 700 ms after the shown prompt appears, and an id change disarms them again', () => {
  let st = shown(newPanel(T0), A, T0)
  assert.equal(ARM_MS, 700)
  assert.equal(armedId(st, T0 + 699), '')
  assert.equal(armedId(st, T0 + 700), 'A')
  st = shown(st, A, T0 + 5000) // the same prompt again: still armed, no new wait
  assert.equal(armedId(st, T0 + 5000), 'A')
  st = shown(st, B, T0 + 6000)
  assert.equal(armedId(st, T0 + 6000), '')
  assert.equal(armedId(st, T0 + 6700), 'B')
  st = shown(st, A, T0 + 6800) // back to the first one: waits again
  assert.equal(armedId(st, T0 + 6800), '')
})

test('nothing is armed without a prompt', () => {
  const st = shown(newPanel(T0), null, T0)
  assert.equal(armedId(st, T0 + 10_000), '')
})

test('a click may answer only the prompt on show, once armed, with no request out and no answer locked on it', () => {
  const st = shown(newPanel(T0), A, T0)
  assert.equal(pressable(st, A, false, T0 + 699), '') // not armed yet
  assert.equal(pressable(st, A, false, T0 + 700), 'A')
  assert.equal(pressable(st, A, true, T0 + 700), '') // a request is out
  assert.equal(pressable(st, B, false, T0 + 700), '') // armed for A, but B is on show: the click-time check
  assert.equal(pressable(st, null, false, T0 + 700), '')
  const sent = answered(st, 'A', A, { ok: true }, T0 + 1000)
  assert.equal(pressable(sent, A, false, T0 + 60_000), '') // locked on the answered prompt
})

test('a 200 locks the answered prompt until the live prompt moves on or goes', () => {
  let st = shown(newPanel(T0), A, T0)
  st = answered(st, 'A', A, { ok: true }, T0 + 1000)
  assert.equal(isLocked(st, 'A'), true)
  assert.equal(isStuck(st, T0 + 1000 + STUCK_MS - 1), false)
  assert.equal(isStuck(st, T0 + 1000 + STUCK_MS), true)
  st = shown(st, A, T0 + 20_000) // a long time with the same prompt: still locked
  assert.equal(isLocked(st, 'A'), true)
  assert.equal(shown(st, B, T0 + 21_000).sent, null)
  assert.equal(shown(st, null, T0 + 21_000, false).sent, null) // nothing to show any more
  assert.equal(isLocked(shown(st, null, T0 + 21_000, false), 'A'), false)
})

test('after a 200 the ask drops while the session still waits: the panel stays SENT and offers the terminal after 8 s, never "not read yet"', () => {
  let st = shown(newPanel(T0), A, T0)
  st = answered(st, 'A', A, { ok: true }, T0 + 1000)
  st = shown(st, null, T0 + 1500) // the server dropped the answered ask; the session still waits
  assert.equal(st.sent?.id, 'A')
  assert.equal(isLocked(st, ''), true)
  assert.equal(pendingBody(st, isLocked(st, '')), 'none')
  assert.equal(statusLine(st, false, true), 'SENT ✓ · waiting for the screen to move on')
  assert.equal(isStuck(st, T0 + 1000 + STUCK_MS), true)
  st = shown(st, A, T0 + 4000) // the terminal did not move
  assert.equal(isLocked(st, 'A'), true)
  assert.equal(shown(st, B, T0 + 5000).sent, null) // a different prompt ends it
  assert.equal(shown(st, null, T0 + 5000, false).sent, null) // and so does the wait ending
})

test('"not read yet" is only for a wait that never had an ask; a gap after one is a re-read, and after a text prompt the reply box stays', () => {
  let st = shown(newPanel(T0), null, T0)
  assert.equal(pendingBody(st, false), 'unread')
  st = shown(st, A, T0 + 100)
  st = shown(st, null, T0 + 200) // one failed read
  assert.equal(pendingBody(st, false), 'reread')
  const T = ask({ id: 'T', kind: 'text', options: [] })
  st = shown(shown(st, T, T0 + 300), null, T0 + 400)
  assert.equal(pendingBody(st, false), 'reply')
  assert.equal(st.last?.id, 'T')
  st = shown(st, null, T0 + 500, false) // the wait is over: a later wait starts unread
  assert.equal(st.last, null)
  assert.equal(pendingBody(st, false), 'unread')
})

test('a 409 about a different prompt says it changed and shows the ask the server saw', () => {
  const st0 = shown(newPanel(T0), A, T0)
  const r = { ok: false as const, conflict: true as const, ask: B, error: 'not the one shown' }
  const st = answered(st0, 'A', A, r, T0 + 1000)
  assert.equal(st.notice, CHANGED)
  assert.equal(st.error, '')
  assert.equal(shownAsk(st, A, true)?.id, 'B')
  assert.equal(armedId(st, T0 + 1000), '') // a new prompt arms after the delay
  assert.equal(armedId(st, T0 + 1700), 'B')
})

test('a 409\'s prompt lasts only until the next snapshot: with the live prompt unchanged, it is shown again and re-armed after the delay', () => {
  const st0 = shown(newPanel(T0), A, T0)
  let st = answered(st0, 'A', A, { ok: false, conflict: true, ask: B, error: 'x' }, T0 + 1000)
  assert.equal(shownAsk(st, A, true)?.id, 'B')
  st = shown(st, A, T0 + 1100) // the same snapshot again (an unrelated re-render): still B
  assert.equal(shownAsk(st, A, true)?.id, 'B')
  st = shown(st, A, T0 + 5000, true, S2) // a newer snapshot still has A: the screen is on A
  assert.equal(st.override, null)
  assert.equal(shownAsk(st, A, true)?.id, 'A')
  assert.equal(st.notice, CHANGED) // what is on show did change
  assert.equal(armedId(st, T0 + 5000), '')
  assert.equal(armedId(st, T0 + 5700), 'A')
  // a newer snapshot that moved to the 409's prompt keeps showing it, armed since it first showed
  let st2 = answered(st0, 'A', A, { ok: false, conflict: true, ask: B, error: 'x' }, T0 + 1000)
  st2 = shown(st2, B, T0 + 1300, true, S2)
  assert.equal(shownAsk(st2, B, true)?.id, 'B')
  assert.equal(armedId(st2, T0 + 1700), 'B')
})

test('a 409 with no ask (no cmux, a send in flight, not in the foreground) shows the server\'s error and follows the live snapshot', () => {
  const st0 = shown(newPanel(T0), A, T0)
  const st = answered(st0, 'A', A, { ok: false, conflict: true, ask: null, error: 'another message is still being sent' }, T0 + 1000)
  assert.equal(st.error, 'another message is still being sent')
  assert.equal(st.notice, '')
  assert.equal(shownAsk(st, A, true)?.id, 'A')
})

test('a 409 about the same prompt shows the server\'s own error, not "changed"', () => {
  const st0 = shown(newPanel(T0), A, T0)
  const r = { ok: false as const, conflict: true as const, ask: A, error: 'this prompt was just answered' }
  const st = answered(st0, 'A', A, r, T0 + 1000)
  assert.equal(st.error, 'this prompt was just answered')
  assert.equal(st.notice, '')
  assert.equal(shownAsk(st, A, true)?.id, 'A')
})

test('a late 409 is ignored when the prompt shown is no longer the one clicked', () => {
  let st = shown(newPanel(T0), A, T0)
  st = begin(st)
  st = shown(st, B, T0 + 500) // the live snapshot moved on while the request was out
  const r = { ok: false as const, conflict: true as const, ask: ask({ id: 'C' }), error: 'x' }
  st = answered(st, 'A', B, r, T0 + 900)
  assert.equal(shownAsk(st, B, true)?.id, 'B')
  assert.equal(st.notice, CHANGED)
})

test('a late 200 does not lock a prompt that was not the one answered', () => {
  let st = shown(newPanel(T0), A, T0)
  st = shown(st, B, T0 + 500)
  st = answered(st, 'A', B, { ok: true }, T0 + 900)
  assert.equal(isLocked(st, 'B'), false)
})

test('a refusal (4xx) is shown as text and leaves the prompt answerable', () => {
  const st = answered(shown(newPanel(T0), A, T0), 'A', A, { ok: false, error: 'that is not one of the choices' }, T0 + 1000)
  assert.equal(st.error, 'that is not one of the choices')
  assert.equal(st.notice, '')
  assert.equal(st.sent, null)
  assert.equal(pressable(st, A, false, T0 + 1000), 'A')
})

test('after a 502 or a lost request the prompt is locked, says the answer may have arrived, and offers the terminal at once', () => {
  let st = shown(newPanel(T0), A, T0)
  st = answered(st, 'A', A, { ok: false, unsure: true, error: 'cmux would not send it: timed out' }, T0 + 1000)
  assert.equal(st.error, 'cmux would not send it: timed out')
  assert.equal(isLocked(st, 'A'), true)
  assert.equal(pressable(st, A, false, T0 + 60_000), '') // never offered again
  assert.equal(isStuck(st, T0 + 1000), true) // OPEN TERMINAL right away
  assert.equal(statusLine(st, false, true), UNSURE)
  st = shown(st, null, T0 + 1500) // the read dropped while waiting: still locked
  assert.equal(isLocked(st, ''), true)
  st = shown(st, A, T0 + 2000)
  assert.equal(pressable(st, A, false, T0 + 60_000), '')
  st = shown(st, B, T0 + 3000) // a different prompt unlocks as usual
  assert.equal(st.sent, null)
  assert.equal(pressable(st, B, false, T0 + 3700), 'B')
})

test('the status line: sending, sent, not sure, and the workspace opened', () => {
  const st = shown(newPanel(T0), A, T0)
  assert.equal(statusLine(st, false, false), '')
  assert.equal(statusLine(st, true, false), 'SENDING…')
  assert.equal(statusLine({ ...st, opened: true }, false, false), 'cmux workspace selected')
  const sent = answered(st, 'A', A, { ok: true }, T0 + 1000)
  assert.equal(statusLine({ ...sent, opened: true }, false, true), 'SENT ✓ · waiting for the screen to move on · cmux workspace selected')
})

test('a new prompt clears the old messages, but the live prompt catching up with an override keeps them', () => {
  const st0 = shown(newPanel(T0), A, T0)
  const conflict = { ok: false as const, conflict: true as const, ask: B, error: 'x' }
  let st = answered(st0, 'A', A, conflict, T0 + 1000)
  assert.equal(st.notice, CHANGED)
  st = shown(st, B, T0 + 2000) // live caught up with what the server showed
  assert.equal(st.notice, CHANGED)
  assert.equal(shownAsk(st, B, true)?.id, 'B')
  st = shown(st, ask({ id: 'C' }), T0 + 3000)
  assert.equal(st.notice, '')
  st = { ...st, error: 'e', opened: true }
  assert.equal(shown(st, ask({ id: 'D' }), T0 + 4000).error, '')
  assert.equal(shown(st, ask({ id: 'D' }), T0 + 4000).opened, false)
})

test('the messages go when the panel has nothing to show', () => {
  let st = { ...shown(newPanel(T0), A, T0), notice: CHANGED, error: 'e', opened: true }
  st = shown(st, null, T0 + 1000, false)
  assert.deepEqual([st.notice, st.error, st.opened, st.override], ['', '', false, null])
})

test('sync gives back the same object when nothing changed', () => {
  const st = shown(newPanel(T0), A, T0)
  assert.equal(shown(st, A, T0 + 10), st)
  assert.equal(shown(st, A, T0 + 10, true, S2), st) // a new snapshot with no override changes nothing
})

test('arming does not depend on the clock only moving forward', () => {
  let st = shown(newPanel(T0 + 1000), A, T0 + 1000)
  assert.equal(armedId(st, T0 + 500), '') // a clock stepped back is not armed, and does not throw
  assert.equal(shown(st, A, T0 + 500), st) // the same prompt keeps its time
  assert.equal(armedId(st, T0 + 1700), 'A')
  st = shown(st, B, T0 + 400) // a new prompt on a clock that went back is timed from there
  assert.equal(armedId(st, T0 + 1000), '')
  assert.equal(armedId(st, T0 + 1100), 'B')
})

// ---- a long permission command ----

const box = (scrollTop: number, scrollHeight = 1000, clientHeight = 400) => ({ scrollTop, scrollHeight, clientHeight, lineHeight: 18 })

test('linesBelow counts the lines under the view, 0 at the end or when it all fits (a pixel of rounding is no line)', () => {
  assert.equal(linesBelow(box(0, 300, 300)), 0)
  assert.equal(linesBelow(box(0)), 34) // 600 px under the view
  assert.equal(linesBelow(box(598)), 0)
  assert.equal(linesBelow(box(590)), 1)
})

test('ALLOW ONCE waits until a command that overflows has been scrolled to its end; one that fits, or none, has nothing to read', () => {
  const P = { id: 'P', text: 'echo one\necho two' }
  // fits: read at once
  assert.equal(mustScrollFirst(readGate('', 'P', box(0, 300, 300)).readTo, P), false)
  // overflows: blocked until the end is seen, then it stays read when scrolled back up
  let g = readGate('', 'P', box(0))
  assert.deepEqual(g, { more: 34, readTo: '' })
  assert.equal(mustScrollFirst(g.readTo, P), true)
  g = readGate(g.readTo, 'P', box(300))
  assert.equal(mustScrollFirst(g.readTo, P), true)
  g = readGate(g.readTo, 'P', box(600))
  assert.deepEqual(g, { more: 0, readTo: 'P' })
  g = readGate(g.readTo, 'P', box(0))
  assert.deepEqual(g, { more: 34, readTo: 'P' })
  assert.equal(mustScrollFirst(g.readTo, P), false)
  // a new prompt must be read again, and one not measured yet is blocked
  assert.equal(mustScrollFirst(g.readTo, { id: 'Q', text: 'x' }), true)
  assert.equal(mustScrollFirst('', { id: 'Q', text: 'x' }), true)
  assert.equal(mustScrollFirst('', { id: 'Q', text: '' }), false)
})

test('waitingAgents: the lead while its session waits and a teammate while its own session waits; never a subagent', () => {
  const sub = (id: string, status: 'running' | 'stalled' | 'done' = 'running') => agent({ id, status })
  // lead waiting: only the lead; its subagents keep their own state (each page still says it may wait through the lead)
  const lead = sess({ status: 'waiting', agents: [sub('s1'), sub('s2', 'stalled'), sub('s3', 'done')] })
  assert.deepEqual([...waitingAgents(lead, [lead])], ['lead'])
  assert.equal(askOwner(lead, 's1', [lead])?.viaLead, true)
  // nothing waiting: empty
  const idle = sess({ status: 'busy', agents: [sub('s1')] })
  assert.deepEqual([...waitingAgents(idle, [idle])], [])
  // a teammate waiting in its own session: that teammate's agent id, not the lead's
  const mate = sess({ id: '4242', sessionId: 'u1', status: 'waiting', team: { name: 't', leadSessionId: 'x', leadId: 'lead-1' } })
  const l2 = sess({ status: 'busy', agents: [agent({ id: 'u1', type: 'teammate' }), sub('s1'), agent({ id: 'u1-sub', parentId: 'u1' })] })
  assert.deepEqual([...waitingAgents(l2, [l2, mate])], ['u1'])
  // a teammate that is not waiting is not in the set
  const calm = { ...mate, status: 'busy' } as SessionDetail
  assert.deepEqual([...waitingAgents(l2, [l2, calm])], [])
  // the lead and a teammate both waiting
  const l3 = { ...l2, status: 'waiting' } as SessionDetail
  assert.deepEqual([...waitingAgents(l3, [l3, mate])].sort(), ['lead', 'u1'])
})
