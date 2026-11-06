// The agents and skills conn provides: typed wrappers over /api/provided. Not post() in live.svelte.ts: the page needs the server's
// own words when an install is refused (a file of the user's is in the way), so every wrapper answers a result and never throws.
import { api, type ApiInit } from './api.ts'

/** What is at the place a link goes: nothing, a link of conn's, or anything else (never replaced). */
export type ProvidedState = 'notInstalled' | 'installed' | 'conflict'
export interface ProvidedRow {
  name: string
  state: ProvidedState
  /** What is in the way, when the state is a conflict */
  note?: string
}
export interface ProvidedView {
  agents: ProvidedRow[]
  skills: ProvidedRow[]
  /** Claude Code's folder the links go in, and conn's folder the copies are in */
  linkDir: string
  copyDir: string
  /** False in a dry run: INSTALL and REMOVE do nothing */
  writable: boolean
}
export type ProvidedResult = { ok: true; view: ProvidedView } | { ok: false; error: string }

const STATES: readonly ProvidedState[] = ['notInstalled', 'installed', 'conflict']

function parseRows(value: unknown): ProvidedRow[] | null {
  if (!Array.isArray(value)) return null
  const rows: ProvidedRow[] = []
  for (const raw of value) {
    const r = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
    if (typeof r.name !== 'string' || !r.name) return null
    if (!STATES.includes(r.state as ProvidedState)) return null
    rows.push({ name: r.name, state: r.state as ProvidedState, ...(typeof r.note === 'string' && r.note && { note: r.note }) })
  }
  return rows
}

/** The server's answer read into a view, or null for a body that is not one. Never throws. */
export function parseProvided(body: unknown): ProvidedView | null {
  const j = (body && typeof body === 'object' ? body : {}) as Record<string, unknown>
  const agents = parseRows(j.agents)
  const skills = parseRows(j.skills)
  if (!agents || !skills) return null
  if (typeof j.linkDir !== 'string' || typeof j.copyDir !== 'string' || typeof j.writable !== 'boolean') return null
  return { agents, skills, linkDir: j.linkDir, copyDir: j.copyDir, writable: j.writable }
}

/** `status` is the HTTP status, `body` the parsed JSON or null. A refusal carries the server's sentence. Never throws. */
export function parseProvidedResult(status: number, body: unknown): ProvidedResult {
  if (status >= 200 && status < 300) {
    const view = parseProvided(body)
    return view ? { ok: true, view } : { ok: false, error: 'the server answered something this page does not understand' }
  }
  const error = (body as { error?: unknown } | null)?.error
  return { ok: false, error: typeof error === 'string' && error ? error : `HTTP ${status}` }
}

async function call(path: string, init: ApiInit): Promise<ProvidedResult> {
  try {
    const r = await api(path, init)
    return parseProvidedResult(r.status, await r.json().catch(() => null))
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Read the state from the disk. */
export const loadProvided = (): Promise<ProvidedResult> => call('/api/provided', { method: 'GET' })

/** INSTALL or REMOVE one skill; the answer is the new state of everything. */
export const changeSkill = (action: 'install' | 'remove', name: string): Promise<ProvidedResult> =>
  call(`/api/provided/${action}`, { method: 'POST', headers: { 'x-conn': '1', 'content-type': 'application/json' }, body: JSON.stringify({ name }) })
