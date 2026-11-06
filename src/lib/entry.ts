// Reading one History entry and its agents from the server. Pure apart from the fetch it is given, so it can be tested.
import { api } from './api.ts'
import { isAgentId, isHistoryId } from './routes.ts'
import type { AgentDetail, HistoryEntry } from './types.ts'

export type EntryResult =
  | { kind: 'ready'; entry: HistoryEntry }
  /** No such entry, or the id is not a session id at all */
  | { kind: 'missing' }
  | { kind: 'error'; error: string }

type Fetch = (url: string) => Promise<Response>

/** GET /api/history/:uuid. An id of the wrong shape never reaches the request. */
export async function fetchEntry(uuid: string, f: Fetch = api): Promise<EntryResult> {
  if (!isHistoryId(uuid)) return { kind: 'missing' }
  try {
    const r = await f(`/api/history/${encodeURIComponent(uuid)}`)
    if (r.status === 404) return { kind: 'missing' }
    if (!r.ok) {
      const j = await r.json().catch(() => ({}))
      return { kind: 'error', error: typeof j?.error === 'string' ? j.error : `HTTP ${r.status}` }
    }
    const entry = (await r.json()) as HistoryEntry
    if (!entry || typeof entry !== 'object' || !entry.detail || !entry.row) return { kind: 'error', error: 'the server sent an entry without its detail' }
    return { kind: 'ready', entry }
  } catch (e) {
    return { kind: 'error', error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** GET /api/history/:uuid/agents/:aid. null when there is no such agent; throws when the request itself fails. */
export async function fetchEntryAgent(uuid: string, aid: string, f: Fetch = api): Promise<AgentDetail | null> {
  if (!isHistoryId(uuid) || !isAgentId(aid)) return null
  const r = await f(`/api/history/${encodeURIComponent(uuid)}/agents/${encodeURIComponent(aid)}`)
  if (r.status === 404 || r.status === 400) return null
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  return r.json()
}
