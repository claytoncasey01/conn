import type { Agent } from './types.ts'

/** 3725000 -> "1h 02m", 125000 -> "2m 05s", 9000 -> "9s" */
export function dur(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const r = s % 60
  if (h) return `${h}h ${String(m).padStart(2, '0')}m`
  if (m) return `${m}m ${String(r).padStart(2, '0')}s`
  return `${r}s`
}

export const ago = (at: number, now: number) => (at ? `${dur(now - at)} ago` : '—')

/** claude-opus-5-5 -> "OPUS 5.5", claude-haiku-5-5 -> "HAIKU 5.5" */
export function modelName(id: string): string {
  const m = id.match(/claude-([a-z]+)-(\d+)(?:-(\d+))?/)
  if (!m) return id ? id.toUpperCase() : '—'
  return `${m[1].toUpperCase()} ${m[2]}${m[3] ? '.' + m[3] : ''}`
}

/** An estimated dollar figure: "<$0.01", "$1.23", "$142". */
export function usd(n: number): string {
  if (n < 0.005) return '<$0.01'
  return n < 100 ? `$${n.toFixed(2)}` : `$${Math.round(n)}`
}

export const COST_NOTE = 'Estimated from the token counts in the transcripts at list prices, advisor calls included. Plans with included usage, discounts and batch pricing are not modelled.'

export function tokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1000) return `${Math.round(n / 1000)}k`
  return String(n)
}

/** The CLI's own wording for a permission mode, as the status line shows it. */
export function modeLabel(mode: string): string {
  switch (mode) {
    case 'auto': return 'AUTO MODE'
    case 'acceptEdits': return 'ACCEPT EDITS'
    case 'plan': return 'PLAN MODE'
    case 'bypassPermissions': return 'BYPASS PERMISSIONS'
    default: return 'DEFAULT'
  }
}

/** Stable short label for an agent by spawn order within its session: N-01, N-02 … */
export const agentLabels = (agents: Agent[]): Map<string, string> =>
  new Map(agents.map((a, i) => [a.id, `N-${String(i + 1).padStart(2, '0')}`]))

/** How long a finished agent stays on the tree, so its return is seen before it goes. */
/** A stalled agent may be on a long build; it leaves the tree only after this much silence. The server holds a quiet
 *  session busy for a stalled agent as long (the server's IDLE_QUIET_MS). */
export const STALLED_MAX_MS = 10 * 60_000
/** A stalled agent inside a tool call holds its session busy this long (the server's OPEN_CALL_MAX_MS); the tree and
 *  the switcher keep it in view as long. */
export const STALLED_IN_CALL_MAX_MS = 30 * 60_000
export const LINGER_MS = 20_000
/** How long the return path animates after an agent actually came back. */
export const RETURN_MS = 5_000

/** What the tree draws: agents waiting on the person (`waiting`, their ids) first and however long they have been silent,
 *  then agents running now, or back within the last few seconds. Nothing else lingers.
 *  An archived session has no "now": every agent the lead spawned is drawn, however long ago it finished. */
export function visibleAgents(agents: Agent[], now: number, archived = false, waiting?: ReadonlySet<string>): Agent[] {
  const w = (a: Agent) => (waiting?.has(a.id) ? 0 : 1)
  const top = agents.filter(a => a.depth === 1 && (archived || !w(a) || a.status === 'running' || (a.status === 'stalled' ? now - a.lastAt < (a.inCall ? STALLED_IN_CALL_MAX_MS : STALLED_MAX_MS) : now - (a.endedAt ?? a.lastAt) < LINGER_MS)))
  return [...top].sort((a, b) => w(a) - w(b) || (a.status === 'done' ? 1 : 0) - (b.status === 'done' ? 1 : 0) || b.startedAt - a.startedAt)
}

/** The agent switcher's order: the lead first, then agents waiting on the person (`waiting`, their ids), then agents still running, then stalled ones, then finished ones.
 *  Within a group they keep spawn order, so N-01 … stay where a reader expects them and a finished agent drops to the end. */
export function switcherOrder(roster: Agent[], waiting?: ReadonlySet<string>): Agent[] {
  const rank = (a: Agent) => (a.id === 'lead' ? 0 : waiting?.has(a.id) ? 1 : a.status === 'running' ? 2 : a.status === 'done' ? 4 : 3)
  return [...roster].sort((a, b) => rank(a) - rank(b))
}

/**
 * An agent that is not running but is waiting for its next message rather than finished: the lead (when its session is
 * idle it waits for the next prompt), and, while the session is live, a teammate whose turn is over (it takes the
 * lead's next message).
 */
export const agentWaits = (a: Agent, archived = false): boolean =>
  a.status !== 'running' && (a.id === 'lead' || (!archived && a.status === 'done' && a.type === 'teammate'))

/** The colour of an agent's status dot: running, waiting for a message, done, or stalled. */
export const agentDot = (a: Agent, archived = false): string =>
  a.status === 'running' ? 'var(--info)' : agentWaits(a, archived) ? 'var(--idle)' : a.status === 'done' ? 'var(--ok)' : 'var(--stall)'

/**
 * The switcher for a session with many agents: what is still going (and the lead) stays in view, in switcherOrder; the
 * done ones fold into a list, the latest finish first, and so do stalled ones once they have been silent STALLED_MAX_MS,
 * or STALLED_IN_CALL_MAX_MS inside a tool call (the list marks them): the quiet time at which the server stops holding
 * the session busy for them, so a live session held busy by an agent shows that agent. A teammate of a live session never folds: between messages it waits, it is not done. The
 * agent being looked at stays in view, and in the list too when it is one of the folded ones, so picking from the list
 * leaves the list as it was.
 */
export function splitSwitcher(roster: Agent[], selectedId: string | null, now: number, archived = false, waiting?: ReadonlySet<string>): { shown: Agent[]; finished: Agent[] } {
  const folded = (a: Agent) =>
    a.id !== 'lead' &&
    !waiting?.has(a.id) &&
    !(a.type === 'teammate' && !archived) &&
    (a.status === 'done' || (a.status === 'stalled' && now - a.lastAt >= (a.inCall ? STALLED_IN_CALL_MAX_MS : STALLED_MAX_MS)))
  const finishedAt = (a: Agent) => a.endedAt ?? a.lastAt
  return {
    shown: switcherOrder(roster, waiting).filter(a => !folded(a) || a.id === selectedId),
    finished: roster.filter(folded).sort((a, b) => finishedAt(b) - finishedAt(a)),
  }
}

/** Generic agent types say nothing; their description is the better name (a teammate's is its own name). The lead is just "LEAD". */
export const agentName = (a: Agent): string =>
  a.id === 'lead' ? 'LEAD' : (a.type === 'general-purpose' || a.type === 'agent' || a.type === 'teammate') && a.description ? a.description : a.type

/** The research model as the design writes it: haiku -> "HAIKU", sonnet -> "SONNET 5.5", a full id through modelName. */
export function researchModel(m: string): string {
  if (m.startsWith('claude-')) return modelName(m)
  return m === 'sonnet' ? 'SONNET 5.5' : m.toUpperCase()
}

/** proj-1-export-saves-an-empty-file-when */
export const branchName = (key: string, title: string): string =>
  key.toLowerCase() + '-' + title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').split('-').slice(0, 6).join('-')

const MONTHS = ['JAN', 'FEB', 'MAR', 'APR', 'MAY', 'JUN', 'JUL', 'AUG', 'SEP', 'OCT', 'NOV', 'DEC']

/** A moment on this machine's clock: "29 OCT 14:05", with the year when it is not this year's. 0 is "—". */
export function whenLabel(at: number, now: number): string {
  if (!at) return '—'
  const d = new Date(at)
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  return `${d.getDate()} ${MONTHS[d.getMonth()]}${d.getFullYear() === new Date(now).getFullYear() ? '' : ' ' + d.getFullYear()} ${hm}`
}
