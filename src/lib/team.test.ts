import assert from 'node:assert/strict'
import { test } from 'node:test'
import { agentName } from './format.ts'
import { TEAMMATE_COLORS, TEAMMATE_LABEL, fleetTotals, isTeammateAgent, ownRows } from './team.ts'
import type { Agent, SessionTeam } from './types.ts'

const agent = (over: Partial<Agent>): Agent => ({ id: 'a', type: 'Explore', description: '', ...over }) as Agent
const team = (leadId: string | null): SessionTeam => ({ name: 'session-abcd1234', leadSessionId: 'lead-uuid', leadId })

test('a teammate agent is recognised by its type, and is named by its description', () => {
  const mate = agent({ type: 'teammate', description: 'dev' })
  assert.equal(isTeammateAgent(mate), true)
  assert.equal(isTeammateAgent(agent({ type: 'Explore' })), false)
  assert.equal(isTeammateAgent(agent({ type: 'general-purpose' })), false)
  assert.equal(TEAMMATE_LABEL, 'TEAMMATE')
  assert.equal(agentName(mate), 'dev')
  assert.equal(agentName(agent({ type: 'teammate' })), 'teammate') // no name: the type
  assert.equal(agentName(agent({ type: 'Explore', description: 'look around' })), 'Explore')
})

test('ownRows leaves a teammate out only while its lead has a row of its own, since the lead\'s totals hold it', () => {
  const rows = [
    { id: '1', team: null },
    { id: '2' }, // an entry from before teammates existed has no team field
    { id: '3', team: team('1') },
    { id: '4', team: team(null) }, // its lead is not live: nothing else counts it
  ]
  assert.deepEqual(ownRows(rows).map(r => r.id), ['1', '2', '4'])
  assert.deepEqual(ownRows([]), [])
})

test('fleetTotals: a live teammate\'s running subagents, cost and advisor calls are in its lead\'s row, so its own row adds none of them', () => {
  const row = (over: object) => ({ agentsRunning: 0, advisorCalls: 0, costUsd: 0, pr: null, ...over })
  const rows = [
    row({ agentsRunning: 2, advisorCalls: 3, costUsd: 0.04, pr: { number: 7 }, team: null }), // the lead: a teammate and its one subagent
    row({ agentsRunning: 1, advisorCalls: 1, costUsd: 0.02, pr: { number: 7 }, team: team('1') }), // the teammate's own row
    row({ agentsRunning: 1, advisorCalls: 0, costUsd: 0.01, team: team(null) }), // its lead is not live: this row is the only one
  ]
  assert.deepEqual(fleetTotals(rows as any), { sessions: 3, agents: 3, advisor: 3, prs: 1, cost: 0.05 })
  assert.deepEqual(fleetTotals([]), { sessions: 0, agents: 0, advisor: 0, prs: 0, cost: 0 })
})

test('the teammate tag is drawn from the teammate token: the text itself, the border at half, the fill at 12%', () => {
  assert.deepEqual(TEAMMATE_COLORS, {
    fg: 'var(--teammate)',
    border: 'color-mix(in oklch, var(--teammate) 50%, transparent)',
    bg: 'color-mix(in oklch, var(--teammate) 12%, transparent)',
  })
})
