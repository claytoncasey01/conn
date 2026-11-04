import assert from 'node:assert/strict'
import { test } from 'node:test'
import { barGroups, emptySelection, matches } from './filters.ts'
import { emptyState, sessionGroups } from './sessionGroups.ts'
import type { SessionSummary } from './types.ts'

const session = (over: Partial<SessionSummary>): SessionSummary =>
  ({ id: 'a', status: 'idle', kind: 'adhoc', model: 'claude-opus-4-7', pr: null, ...over }) as SessionSummary

const group = (sessions: SessionSummary[], sel = emptySelection(), id = 'status') => sessionGroups(sessions, sel).find((g) => g.id === id)!
const chip = (sessions: SessionSummary[], groupId: string, chipId: string, sel = emptySelection()) => group(sessions, sel, groupId).chips.find((c) => c.id === chipId)!

test('status chips map needs/live/idle to waiting/busy/idle', () => {
  const g = group([])
  assert.deepEqual(g.chips.map((c) => c.id), ['needs', 'live', 'idle'])
  assert.deepEqual(g.chips.map((c) => c.label), ['Needs input', 'Live', 'Idle'])
  const [w, b, i] = [session({ status: 'waiting' }), session({ status: 'busy' }), session({ status: 'idle' })]
  assert.deepEqual([w, b, i].map((s) => g.chips.map((c) => c.test(s))), [
    [true, false, false],
    [false, true, false],
    [false, false, true],
  ])
})

test('kind chips are build, adhoc, review and selfreview; pr chips are has and none', () => {
  const build = session({ kind: 'build' })
  const adhoc = session({ kind: 'adhoc' })
  assert.deepEqual(group([], emptySelection(), 'kind').chips.map((c) => c.id), ['build', 'adhoc', 'review', 'selfreview'])
  assert.equal(chip([], 'kind', 'selfreview').test(session({ kind: 'selfreview' })), true)
  assert.equal(chip([], 'kind', 'review').test(session({ kind: 'selfreview' })), false)
  assert.equal(group([], emptySelection(), 'kind').chips.find((c) => c.id === 'review')!.label, 'Review')
  assert.equal(chip([], 'kind', 'review').test(session({ kind: 'review' })), true)
  assert.equal(chip([], 'kind', 'review').test(adhoc), false)
  assert.equal(chip([], 'kind', 'build').test(session({ kind: 'review' })), false)
  assert.equal(chip([], 'kind', 'build').test(build), true)
  assert.equal(chip([], 'kind', 'build').test(adhoc), false)
  assert.equal(chip([], 'kind', 'adhoc').test(adhoc), true)
  const withPr = session({ pr: { number: 7 } as SessionSummary['pr'] })
  const noPr = session({ pr: null })
  assert.deepEqual(group([], emptySelection(), 'pr').chips.map((c) => c.id), ['has', 'none'])
  assert.deepEqual(group([], emptySelection(), 'pr').chips.map((c) => c.label), ['Has PR', 'No PR'])
  assert.equal(chip([], 'pr', 'has').test(withPr), true)
  assert.equal(chip([], 'pr', 'has').test(noPr), false)
  assert.equal(chip([], 'pr', 'none').test(noPr), true)
  assert.equal(chip([], 'pr', 'none').test(withPr), false)
})

test('model chips come from the sessions present', () => {
  const sessions = [session({ model: 'claude-opus-4-7' }), session({ model: 'claude-sonnet-5-5' }), session({ model: 'claude-opus-4-7' })]
  const g = group(sessions, emptySelection(), 'model')
  assert.deepEqual(g.chips.map((c) => c.label).sort(), ['Opus 4.7', 'Sonnet 5.5'])
  assert.deepEqual(g.chips.map((c) => c.id).sort(), ['OPUS 4.7', 'SONNET 5.5'], 'the ids are the names the cards print, so a saved selection holds')
  const bar = barGroups(sessions, sessionGroups(sessions, emptySelection()), emptySelection()).find((b) => b.id === 'model')!
  assert.deepEqual(bar.chips.map((c) => [c.label, c.count]).sort(), [['Opus 4.7', 2], ['Sonnet 5.5', 1]])
})

test('a selected model that is gone stays as a chip', () => {
  const sessions = [session({ model: 'claude-opus-4-7' })]
  const sel = { model: ['SONNET 5.5'] }
  const bar = barGroups(sessions, sessionGroups(sessions, sel), sel).find((b) => b.id === 'model')!
  const gone = bar.chips.find((c) => c.id === 'SONNET 5.5')!
  assert.equal(gone.label, 'Sonnet 5.5')
  assert.equal(gone.count, 0)
  assert.equal(bar.chips.length, 2)
  assert.equal(matches(sessions[0], sessionGroups(sessions, sel), sel), false)
})

test('an empty model string becomes UNKNOWN (labelled Unknown)', () => {
  const sessions = [session({ model: '' }), session({ model: 'claude-opus-4-7' })]
  const g = group(sessions, emptySelection(), 'model')
  const unknown = g.chips.find((c) => c.id === 'UNKNOWN')!
  assert.equal(unknown.label, 'Unknown')
  assert.equal(unknown.test(sessions[0]), true)
  assert.equal(unknown.test(sessions[1]), false)
})

test('emptyState: no sessions at all, filtered out, and shown', () => {
  assert.equal(emptyState(0, 0, emptySelection()), 'no-sessions')
  assert.equal(emptyState(0, 0, { status: ['live'] }), 'no-sessions')
  assert.equal(emptyState(3, 0, { status: ['live'] }), 'filtered-out')
  assert.equal(emptyState(3, 2, { status: ['live'] }), null)
  assert.equal(emptyState(3, 3, emptySelection()), null)
})

test('a repo group appears only when the sessions are in more than one repo; its chips filter by repo and leave out a session in none', () => {
  const sessions = [session({ id: 'a', repo: 'acme/widgets' }), session({ id: 'b', repo: 'acme/gadgets' }), session({ id: 'c', repo: null })]
  assert.equal(sessionGroups([sessions[0], sessions[2]], emptySelection()).some((g) => g.id === 'repo'), false)
  assert.equal(sessionGroups([], emptySelection()).some((g) => g.id === 'repo'), false)
  const sel = { repo: ['acme/gadgets'] }
  const groups = sessionGroups(sessions, sel)
  assert.deepEqual(groups.map((g) => g.id), ['status', 'kind', 'pr', 'model', 'repo'])
  assert.deepEqual(sessions.filter((s) => matches(s, groups, sel)).map((s) => s.id), ['b'])
  assert.deepEqual(barGroups(sessions, groups, emptySelection()).find((g) => g.id === 'repo')!.chips.map((c) => [c.id, c.count]), [['acme/gadgets', 1], ['acme/widgets', 1]])
})
