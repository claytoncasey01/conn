// Where a detail page gets its session from: the live snapshot, or a frozen History entry. The Overview, Agent and
// Advisor pages read only this, so the same pages serve both.
import { fetchEntry, fetchEntryAgent, type EntryResult } from './entry.ts'
import { fetchAgent, live, sessionById } from './live.svelte.ts'
import { href } from './routes.ts'
import type { AgentDetail, HistoryEntry, SessionDetail } from './types.ts'

/** loading: nothing to show yet (live: no snapshot so far); missing: no such session; error: the archive could not be read */
export type SourceState = 'loading' | 'ready' | 'missing' | 'error'

export interface SessionSource {
  readonly archived: boolean
  /** The session id (live) or History entry id (archived), as given in the route */
  readonly id: string
  session(): SessionDetail | undefined // reactive
  /** live.now, or the entry's endedAt so every "ago" and runtime stays as it was when the session ended */
  now(): number
  /** archived: always true */
  connected(): boolean
  state(): SourceState
  /** Why state() is 'error'; empty otherwise */
  error(): string
  agent(aid: string): Promise<AgentDetail | null>
  /** The loaded History entry (row, summary); null for a live session and until an archived one has loaded */
  entry(): HistoryEntry | null
  /** Read the entry again (its summary moves while it is generated). A failed read keeps the entry already shown. Live: nothing to do. */
  refresh(): Promise<void>
  href: { overview(): string; agent(aid?: string | null): string; advisor(): string; back(): string }
}

// One source per id is kept, so the pages (and App's route-to-source mapping) see the same object while moving
// between one session's Overview, Agent and Advisor pages.
let lastLive: SessionSource | null = null

export function liveSource(sid: string): SessionSource {
  if (lastLive?.id === sid) return lastLive
  lastLive = {
    archived: false,
    id: sid,
    session: () => sessionById(sid),
    now: () => live.now,
    connected: () => live.connected,
    state: () => (!live.snap ? 'loading' : sessionById(sid) ? 'ready' : 'missing'),
    error: () => '',
    agent: aid => fetchAgent(sid, aid),
    entry: () => null,
    refresh: async () => {},
    href: {
      overview: () => href.overview(sid),
      agent: aid => href.agent(sid, aid),
      advisor: () => href.advisor(sid),
      back: () => href.sessions(),
    },
  }
  return lastLive
}

let lastArchived: { src: SessionSource; failed: () => boolean } | null = null

/** Fetches /api/history/:uuid once and each agent's tool calls the first time it is asked for. */
export function archivedSource(uuid: string): SessionSource {
  // A failed read is retried by coming back to the entry; a good one is kept.
  if (lastArchived?.src.id === uuid && !lastArchived.failed()) return lastArchived.src

  // Created in its final initial form and only written after the request settles: App builds the source inside a
  // $derived, where a synchronous write to new state is refused.
  // Raw: the entry is replaced whole, never edited, so it needs no deep proxy
  let result = $state.raw<EntryResult | null>(null)
  // Kept outside $state on purpose: App's $derived calls this function, and must not re-run (and refetch) when it flips.
  let failed = false
  const entry = (): HistoryEntry | null => (result?.kind === 'ready' ? result.entry : null)
  // Only the newest read may write: a slow earlier one must not put an older summary back
  let reads = 0
  async function refresh() {
    const mine = ++reads
    const r = await fetchEntry(uuid)
    if (mine !== reads) return
    // A failed refresh must not replace an entry that is on screen with an error page
    if (r.kind === 'ready' || !entry()) {
      failed = r.kind === 'error'
      result = r
    }
  }
  void refresh()
  const agents = new Map<string, Promise<AgentDetail | null>>()

  const src: SessionSource = {
    archived: true,
    id: uuid,
    session: () => entry()?.detail,
    now: () => {
      const e = entry()
      return e ? (e.detail.archived?.endedAt ?? e.row.endedAt) : 0
    },
    connected: () => true,
    state: () => result?.kind ?? 'loading',
    error: () => (result?.kind === 'error' ? result.error : ''),
    entry,
    refresh,
    agent: aid => {
      const hit = agents.get(aid)
      if (hit) return hit
      const p = fetchEntryAgent(uuid, aid)
      agents.set(aid, p)
      // A failed request is not remembered, so the next selection tries again
      p.catch(() => agents.delete(aid))
      return p
    },
    href: {
      overview: () => href.entry(uuid),
      agent: aid => href.entryAgent(uuid, aid),
      advisor: () => href.entryAdvisor(uuid),
      back: () => href.history(),
    },
  }
  lastArchived = { src, failed: () => failed }
  return src
}
