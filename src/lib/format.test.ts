import assert from 'node:assert/strict'
import { test } from 'node:test'
import { agentDot, agentWaits, LINGER_MS, STALLED_IN_CALL_MAX_MS, STALLED_MAX_MS, splitSwitcher, switcherOrder, visibleAgents, whenLabel } from './format.ts'
import type { Agent } from './types.ts'

const NOW = Date.UTC(2026, 9, 29, 12, 0, 0)
const WEEK = 7 * 86_400_000

const agent = (over: Partial<Agent>): Agent =>
  ({ id: 'a', type: 'Explore', depth: 1, status: 'done', startedAt: NOW - 60_000, lastAt: NOW - 1000, endedAt: NOW - 1000, ...over }) as Agent

test('visibleAgents with archived shows all 12 agents of a session that ended three weeks ago', () => {
  const ended = NOW - 3 * WEEK
  const agents = Array.from({ length: 12 }, (_, i) =>
    agent({ id: `a${i}`, startedAt: ended - 600_000 + i * 1000, lastAt: ended - 1000, endedAt: ended - 1000, status: i === 3 ? 'stalled' : 'done' }),
  )
  assert.equal(visibleAgents(agents, NOW, true).length, 12)
  // The same agents seen live, three weeks on, have all long gone from the tree
  assert.equal(visibleAgents(agents, NOW).length, 0)
  assert.equal(visibleAgents(agents, NOW, false).length, 0)
})

test('visibleAgents with archived still keeps nested agents off the tree', () => {
  const agents = [agent({ id: 'top' }), agent({ id: 'nested', depth: 2, parentId: 'top' })]
  assert.deepEqual(visibleAgents(agents, NOW + 3 * WEEK, true).map(a => a.id), ['top'])
})

test('visibleAgents live: a finished agent lingers 20 s, a running one stays, a stalled one leaves after 10 min', () => {
  const agents = [
    agent({ id: 'fresh', endedAt: NOW - 10_000 }),
    agent({ id: 'gone', endedAt: NOW - 25_000 }),
    agent({ id: 'edge', endedAt: NOW - LINGER_MS }),
    agent({ id: 'run', status: 'running', endedAt: null, lastAt: NOW - 3 * WEEK }),
    agent({ id: 'slow', status: 'stalled', endedAt: null, lastAt: NOW - 60_000 }),
    agent({ id: 'dead', status: 'stalled', endedAt: null, lastAt: NOW - STALLED_MAX_MS }),
  ]
  assert.deepEqual(visibleAgents(agents, NOW).map(a => a.id).sort(), ['fresh', 'run', 'slow'])
  assert.deepEqual(visibleAgents(agents, NOW, false).map(a => a.id).sort(), ['fresh', 'run', 'slow'])
})

test('visibleAgents live: a stalled agent inside a tool call stays on the tree up to STALLED_IN_CALL_MAX_MS, as long as it holds its session busy', () => {
  const agents = [
    agent({ id: 'cmd', status: 'stalled', endedAt: null, inCall: true, lastAt: NOW - 15 * 60_000 }),
    agent({ id: 'killed', status: 'stalled', endedAt: null, inCall: true, lastAt: NOW - STALLED_IN_CALL_MAX_MS }),
    agent({ id: 'quiet', status: 'stalled', endedAt: null, inCall: false, lastAt: NOW - 15 * 60_000 }),
  ]
  assert.deepEqual(visibleAgents(agents, NOW).map(a => a.id), ['cmd'])
})

test('visibleAgents sorts done agents last, newest first within each', () => {
  const agents = [
    agent({ id: 'd-old', startedAt: NOW - 50_000, endedAt: NOW - 5000 }),
    agent({ id: 'r', status: 'running', startedAt: NOW - 40_000, endedAt: null }),
    agent({ id: 'd-new', startedAt: NOW - 20_000, endedAt: NOW - 5000 }),
  ]
  assert.deepEqual(visibleAgents(agents, NOW).map(a => a.id), ['r', 'd-new', 'd-old'])
  assert.deepEqual(visibleAgents(agents, NOW, true).map(a => a.id), ['r', 'd-new', 'd-old'])
})

test('visibleAgents puts agents waiting on the person first and always draws them, however long they have been silent', () => {
  const agents = [
    agent({ id: 'r1', status: 'running', startedAt: NOW - 10_000, endedAt: null }),
    agent({ id: 'r2', status: 'running', startedAt: NOW - 20_000, endedAt: null }),
    agent({ id: 'r3', status: 'running', startedAt: NOW - 30_000, endedAt: null }),
    agent({ id: 'r4', status: 'running', startedAt: NOW - 40_000, endedAt: null }),
    // a teammate stalled on a permission prompt for 11 minutes
    agent({ id: 'mate', type: 'teammate', status: 'stalled', startedAt: NOW - 3_600_000, lastAt: NOW - 11 * 60_000, endedAt: null }),
    // one whose turn is over long ago, waiting in its own session
    agent({ id: 'mate2', type: 'teammate', status: 'done', startedAt: NOW - 7_200_000, endedAt: NOW - 3_000_000 }),
  ]
  assert.deepEqual(visibleAgents(agents, NOW).map(a => a.id), ['r1', 'r2', 'r3', 'r4'])
  const shown = visibleAgents(agents, NOW, false, new Set(['mate', 'mate2']))
  assert.deepEqual(shown.map(a => a.id), ['mate', 'mate2', 'r1', 'r2', 'r3', 'r4'])
  // the Overview draws the first three as cards: both waiting agents get one
  assert.deepEqual(shown.slice(0, 3).map(a => a.id), ['mate', 'mate2', 'r1'])
  // a nested agent stays off the tree even when listed as waiting
  assert.deepEqual(visibleAgents([agent({ id: 'n', depth: 2, status: 'running', endedAt: null })], NOW, false, new Set(['n'])).length, 0)
})

test('whenLabel writes the local day and time, with the year only when it is not this year', () => {
  const at = new Date(2026, 9, 3, 7, 5).getTime()
  assert.equal(whenLabel(at, new Date(2026, 10, 1).getTime()), '3 OCT 07:05')
  assert.equal(whenLabel(at, new Date(2027, 0, 2).getTime()), '3 OCT 2026 07:05')
  assert.equal(whenLabel(0, NOW), '—')
})

test('switcherOrder puts the lead first, running agents next, stalled then done last, each group in spawn order', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    agent({ id: 'a1', status: 'done' }),
    agent({ id: 'a2', status: 'running' }),
    agent({ id: 'a3', status: 'stalled' }),
    agent({ id: 'a4', status: 'done' }),
    agent({ id: 'a5', status: 'running', depth: 2 }),
  ]
  assert.deepEqual(switcherOrder(roster).map(a => a.id), ['lead', 'a2', 'a5', 'a3', 'a1', 'a4'])
  // a copy: the roster the labels were counted from is left alone
  assert.deepEqual(roster.map(a => a.id), ['lead', 'a1', 'a2', 'a3', 'a4', 'a5'])
})

test('switcherOrder with a waiting set puts those agents right after the lead, before running ones, spawn order kept in each group', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'running' }),
    agent({ id: 'a1', status: 'running' }),
    agent({ id: 'a2', status: 'done' }),
    agent({ id: 'a3', status: 'stalled' }),
    agent({ id: 'a4', status: 'running' }),
    agent({ id: 'a5', status: 'stalled' }),
  ]
  assert.deepEqual(switcherOrder(roster, new Set(['a4', 'a2', 'a5'])).map(a => a.id), ['lead', 'a2', 'a4', 'a5', 'a1', 'a3'])
  assert.deepEqual(switcherOrder(roster, new Set(['lead'])).map(a => a.id), ['lead', 'a1', 'a4', 'a3', 'a5', 'a2'])
  assert.deepEqual(switcherOrder(roster, new Set()).map(a => a.id), switcherOrder(roster).map(a => a.id))
})

test('splitSwitcher never folds a waiting agent, even one whose status says done', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'running' }),
    agent({ id: 'a1', status: 'done', endedAt: 5 }),
    agent({ id: 'a2', status: 'done', endedAt: 9 }),
  ]
  const { shown, finished } = splitSwitcher(roster, null, NOW, false, new Set(['a1']))
  assert.deepEqual(shown.map(a => a.id), ['lead', 'a1'])
  assert.deepEqual(finished.map(a => a.id), ['a2'])
})

test('splitSwitcher keeps the lead, running and recently stalled agents in view and folds the done ones, latest finish first', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    agent({ id: 'a1', status: 'done', endedAt: NOW - 50_000, lastAt: NOW - 50_000 }),
    agent({ id: 'a2', status: 'running', endedAt: null }),
    agent({ id: 'a3', status: 'stalled', endedAt: null }),
    agent({ id: 'a4', status: 'done', endedAt: NOW - 10_000, lastAt: NOW - 10_000 }),
    agent({ id: 'a5', status: 'done', endedAt: NOW - 30_000, lastAt: NOW - 30_000 }),
  ]
  const { shown, finished } = splitSwitcher(roster, null, NOW)
  assert.deepEqual(shown.map(a => a.id), ['lead', 'a2', 'a3'])
  assert.deepEqual(finished.map(a => a.id), ['a4', 'a5', 'a1'])
})

test('splitSwitcher folds a stalled agent once it has been silent STALLED_MAX_MS, in finish order with the done ones', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    agent({ id: 'old', status: 'stalled', endedAt: null, lastAt: NOW - STALLED_MAX_MS }),
    agent({ id: 'recent', status: 'stalled', endedAt: null, lastAt: NOW - STALLED_MAX_MS + 1000 }),
    agent({ id: 'd1', status: 'done', endedAt: NOW - STALLED_MAX_MS - 5000, lastAt: NOW - STALLED_MAX_MS - 5000 }),
  ]
  const { shown, finished } = splitSwitcher(roster, null, NOW)
  assert.deepEqual(shown.map(a => a.id), ['lead', 'recent'])
  assert.deepEqual(finished.map(a => [a.id, a.status]), [['old', 'stalled'], ['d1', 'done']])
})

test('splitSwitcher keeps a stalled agent inside a tool call in view up to STALLED_IN_CALL_MAX_MS, as long as it holds its session busy', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'running' }),
    agent({ id: 'cmd', status: 'stalled', endedAt: null, inCall: true, lastAt: NOW - STALLED_IN_CALL_MAX_MS + 1000 }),
    agent({ id: 'killed', status: 'stalled', endedAt: null, inCall: true, lastAt: NOW - STALLED_IN_CALL_MAX_MS }),
    agent({ id: 'quiet', status: 'stalled', endedAt: null, inCall: false, lastAt: NOW - STALLED_MAX_MS }),
  ]
  const { shown, finished } = splitSwitcher(roster, null, NOW)
  assert.deepEqual(shown.map(a => a.id), ['lead', 'cmd'])
  assert.deepEqual(finished.map(a => a.id), ['quiet', 'killed'])
})

test('splitSwitcher: sixty agents that never reported back and thirty done ones leave only the lead in the row', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    ...Array.from({ length: 60 }, (_, i) => agent({ id: `s${i}`, status: 'stalled', endedAt: null, lastAt: NOW - 3_600_000 + i })),
    ...Array.from({ length: 30 }, (_, i) => agent({ id: `d${i}`, status: 'done', endedAt: NOW - 60_000 + i, lastAt: NOW - 60_000 + i })),
  ]
  const { shown, finished } = splitSwitcher(roster, null, NOW)
  assert.deepEqual(shown.map(a => a.id), ['lead'])
  assert.equal(finished.length, 90)
})

test('splitSwitcher never folds a teammate of a live session; an ended session folds it like any agent', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    agent({ id: 'tm', type: 'teammate', description: 'reviewer', status: 'done', endedAt: NOW - 60_000, lastAt: NOW - 60_000 }),
    agent({ id: 'tm2', type: 'teammate', description: 'tester', status: 'stalled', endedAt: null, lastAt: NOW - 3_600_000 }),
    agent({ id: 'a1', status: 'done', endedAt: NOW - 50_000, lastAt: NOW - 50_000 }),
  ]
  const live = splitSwitcher(roster, null, NOW)
  assert.deepEqual(live.shown.map(a => a.id), ['lead', 'tm2', 'tm'])
  assert.deepEqual(live.finished.map(a => a.id), ['a1'])
  assert.deepEqual(splitSwitcher(roster, null, NOW, true).finished.map(a => a.id), ['a1', 'tm', 'tm2'])
})

test('splitSwitcher keeps the agent being looked at in view, and in the list where it was, so picking from the list moves nothing', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    agent({ id: 'a1', status: 'done', endedAt: NOW - 50_000, lastAt: NOW - 50_000 }),
    agent({ id: 'a2', status: 'done', endedAt: NOW - 10_000, lastAt: NOW - 10_000 }),
  ]
  const before = splitSwitcher(roster, 'a2', NOW)
  const after = splitSwitcher(roster, 'a1', NOW)
  assert.deepEqual(after.shown.map(a => a.id), ['lead', 'a1'])
  assert.deepEqual(after.finished.map(a => a.id), ['a2', 'a1'])
  assert.deepEqual(after.finished.map(a => a.id), before.finished.map(a => a.id))
})

test('splitSwitcher orders by when an agent last acted when it has no end time, and leaves the roster alone', () => {
  const roster = [
    agent({ id: 'lead', type: 'lead', status: 'done' }),
    agent({ id: 'a1', status: 'done', endedAt: null, lastAt: NOW - 5000 }),
    agent({ id: 'a2', status: 'done', endedAt: null, lastAt: NOW - 1000 }),
  ]
  assert.deepEqual(splitSwitcher(roster, null, NOW).finished.map(a => a.id), ['a2', 'a1'])
  assert.deepEqual(roster.map(a => a.id), ['lead', 'a1', 'a2'])
})

test('agentDot and agentWaits: the lead and a live teammate between messages wait; done, stalled and running have their own colour', () => {
  const lead = agent({ id: 'lead', type: 'lead', status: 'done' })
  const tm = agent({ id: 'tm', type: 'teammate', status: 'done' })
  assert.equal(agentDot(agent({ id: 'r', status: 'running' })), 'var(--info)')
  assert.equal(agentDot(agent({ id: 'lead', type: 'lead', status: 'running' })), 'var(--info)')
  assert.equal(agentDot(lead), 'var(--idle)')
  assert.equal(agentDot(tm), 'var(--idle)')
  assert.equal(agentWaits(tm), true)
  // once the session has ended a teammate is done like any agent; the lead still reads as idle
  assert.equal(agentDot(tm, true), 'var(--ok)')
  assert.equal(agentWaits(tm, true), false)
  assert.equal(agentWaits(lead, true), true)
  assert.equal(agentDot(agent({ id: 'd', status: 'done' })), 'var(--ok)')
  assert.equal(agentDot(agent({ id: 's', status: 'stalled' })), 'var(--stall)')
  assert.equal(agentDot(agent({ id: 'tms', type: 'teammate', status: 'stalled' })), 'var(--stall)')
})
