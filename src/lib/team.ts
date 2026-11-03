// How a teammate is shown. Pure, so it runs under bun test.
import type { Agent, SessionSummary, SessionTeam } from './types.ts'

export const TEAMMATE_LABEL = 'TEAMMATE'

/** The tag's colours, distinct from the lead (orange), agents (blue), advisor (violet) and the status colours. */
export const TEAMMATE_COLORS = { fg: 'var(--teammate)', border: 'color-mix(in oklch, var(--teammate) 50%, transparent)', bg: 'color-mix(in oklch, var(--teammate) 12%, transparent)' }

export const isTeammateAgent = (a: Pick<Agent, 'type'>): boolean => a.type === 'teammate'

/**
 * The rows to add up (cost, advisor calls): a teammate with a live lead is already in the lead's totals, so its own row is
 * left out. One whose lead is not live (it ended) is the only row that counts it.
 */
export const ownRows = <T extends { team?: SessionTeam | null }>(rows: T[]): T[] => rows.filter(r => !r.team?.leadId)

/**
 * The Sessions page's fleet numbers. Every row counts as a session and its PR is a PR, but running agents, advisor calls and
 * cost come from `ownRows`: a teammate with a live lead is in the lead's row, its own subagents included.
 */
export function fleetTotals(sessions: (Pick<SessionSummary, 'agentsRunning' | 'advisorCalls' | 'costUsd' | 'pr'> & { team?: SessionTeam | null })[]) {
  const own = ownRows(sessions)
  return {
    sessions: sessions.length,
    agents: own.reduce((n, s) => n + s.agentsRunning, 0),
    advisor: own.reduce((n, s) => n + s.advisorCalls, 0),
    prs: new Set(sessions.flatMap(s => (s.pr ? [s.pr.number] : []))).size,
    cost: own.reduce((n, s) => n + s.costUsd, 0),
  }
}
