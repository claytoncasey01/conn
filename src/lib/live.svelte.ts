import { api } from './api.ts'
import { startStream } from './stream.ts'
import { onSnapshot } from './notify.svelte.ts'
import { answerBody, parseAnswer, parseOk, type AnswerResult } from './ask.ts'
import { parseEntryAction, type EntryActionResult } from './entryAction.ts'
import { fetchEntry as readEntry } from './entry.ts'
import { repoBody } from './runLegs.ts'
import type { AgentDetail, HistoryEntry, HistoryList, ResearchModel, RunLayout, SessionDetail, Snapshot, WorkList } from './types.ts'

/**
 * The whole picture, pushed by the server over its event stream (stream.ts). `now` ticks each second for runtimes. `locked`:
 * the server refused the stream for want of this launch's secret (the page was opened without one, or the server restarted).
 */
export const live = $state({ snap: null as Snapshot | null, connected: false, locked: false, now: Date.now() })

let started = false
export function startLive() {
  if (started) return
  started = true
  startStream('/api/events', {
    onState: connected => {
      live.connected = connected
      if (connected) live.locked = false
    },
    onRefused: () => (live.locked = true),
    onData: data => {
      live.snap = JSON.parse(data)
      onSnapshot(live.snap!)
      live.connected = true
    },
  })
  setInterval(() => (live.now = Date.now()), 1000)
}

export const sessionById = (id: string): SessionDetail | undefined => live.snap?.sessions.find(s => s.id === id)

/** One agent's tool calls: fetched on demand, refetched whenever the snapshot moves. */
export async function fetchAgent(sessionId: string, agentId: string): Promise<AgentDetail | null> {
  const r = await api(`/api/sessions/${sessionId}/agents/${agentId}`)
  return r.ok ? r.json() : null
}

/** SIGTERM one session. `busy` in the reply means it was working and needs `force`. */
export async function stopSession(sessionId: string, force: boolean): Promise<{ ok: boolean; busy?: boolean; error?: string }> {
  const r = await api(`/api/sessions/${sessionId}/stop`, {
    method: 'POST',
    headers: { 'x-conn': '1', 'content-type': 'application/json' },
    body: JSON.stringify({ force }),
  })
  const j = await r.json().catch(() => ({}))
  return r.ok ? { ok: true } : { ok: false, busy: !!j.busy, error: j.error ?? `HTTP ${r.status}` }
}

/** Type a message into the session's own terminal (cmux). The server refuses anything it cannot send safely. */
export async function replySession(sessionId: string, text: string): Promise<{ ok: boolean; error?: string }> {
  try {
    const r = await api(`/api/sessions/${sessionId}/reply`, {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify({ text }),
    })
    const j = await r.json().catch(() => ({}))
    return r.ok ? { ok: true } : { ok: false, error: j.error ?? `HTTP ${r.status}` }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Answer the prompt on a session's screen with an option key. `askId` names the prompt that was shown; the server refuses (409) when the screen has moved on. Never retried here. */
export async function answerSession(sessionId: string, askId: string, choice: string): Promise<AnswerResult> {
  try {
    const r = await api(`/api/sessions/${sessionId}/answer`, {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify(answerBody(askId, choice)),
    })
    return parseAnswer(r.status, await r.json().catch(() => null))
  } catch (e) {
    // The request may have reached the server and the key been pressed before the connection went
    return { ok: false, unsure: true, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Make the session's cmux workspace the active one. */
export async function openTerminal(sessionId: string): Promise<{ ok: true } | { ok: false; error: string }> {
  try {
    const r = await api(`/api/sessions/${sessionId}/terminal`, { method: 'POST', headers: { 'x-conn': '1' } })
    return parseOk(r.status, await r.json().catch(() => null))
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

// ---- Open work ----

/** The sprint list. `loading` covers the request in flight; the server's own Jira fetch is `list.fetching`. */
export const work = $state({ list: null as WorkList | null, loading: false })

export interface ActionResult {
  ok: boolean
  error?: string
  /** The server wants a second press with `force`: the ticket is someone else's or not in a To Do status */
  needsConfirm?: 'assignee' | 'status'
}

export async function fetchWork(): Promise<void> {
  if (work.loading) return
  work.loading = true
  try {
    const r = await api('/api/work')
    if (r.ok) work.list = await r.json()
    else {
      const j = await r.json().catch(() => ({}))
      failWork(j.error ?? `HTTP ${r.status}`)
    }
  } catch (e) {
    failWork(e instanceof Error ? e.message : 'request failed')
  } finally {
    work.loading = false
  }
}

/** A failed request keeps the tickets already on screen and shows the reason above them. */
function failWork(error: string) {
  work.list = work.list
    ? { ...work.list, error }
    : { tickets: [], fetchedAt: null, fetching: false, fetchStartedAt: null, step: null, needsAuth: [], error, truncated: false, projects: [], build: { repos: [], default: null, layout: 'shared' } }
}

async function post(path: string, body: object): Promise<ActionResult> {
  try {
    const r = await api(path, {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    const j = await r.json().catch(() => ({}))
    return r.ok ? { ok: true } : { ok: false, error: j.error ?? `HTTP ${r.status}`, needsConfirm: j.needsConfirm }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Ask the server to re-read Jira, then take the new list. */
export async function refreshWork(): Promise<ActionResult> {
  const r = await post('/api/work/refresh', {})
  await fetchWork()
  return r
}

export const startRun = (key: string, o: { model: ResearchModel; force?: boolean; repos: string[]; layout?: RunLayout }) => post(`/api/work/${key}/start`, o)
export const answerRun = (key: string, answers: Record<string, string>) => post(`/api/runs/${key}/answers`, { answers })
export const openPr = (key: string, repo?: string) => post(`/api/runs/${key}/pr`, repoBody(repo))
export const cancelRun = (key: string) => post(`/api/runs/${key}/cancel`, {})
export const retryRun = (key: string) => post(`/api/runs/${key}/retry`, {})
export const fixChecks = (key: string, repo?: string) => post(`/api/runs/${key}/fix-checks`, repoBody(repo))
export const moveJira = (key: string, status: string) => post(`/api/runs/${key}/jira`, { status })
export const cleanupRun = (key: string, repo?: string) => post(`/api/runs/${key}/cleanup`, repoBody(repo))
export const resumeRun = (key: string, repo?: string) => post(`/api/runs/${key}/resume`, repoBody(repo))

// ---- History ----

/** One page of the archive for a query string from historyQueryString; null when the request fails. */
export async function fetchHistory(qs: string): Promise<HistoryList | null> {
  try {
    const r = await api(`/api/history${qs ? `?${qs}` : ''}`)
    return r.ok ? ((await r.json()) as HistoryList) : null
  } catch {
    return null
  }
}

/** One archived session with its summary, or null. Kept for the interface the History brief lists (nothing in the app calls it: archivedSource uses entry.ts's tri-state fetchEntry, which tells missing from error). */
export async function fetchEntry(uuid: string): Promise<HistoryEntry | null> {
  const r = await readEntry(uuid)
  return r.kind === 'ready' ? r.entry : null
}

// Not post(): the server answers a resume it could not open with HTTP 200 {ok:false, error, manualCommand}, which post()
// would take for success. These read the body.
async function postEntry(path: string, body: object): Promise<EntryActionResult> {
  try {
    const r = await api(path, {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    return parseEntryAction(r.status, await r.json().catch(() => null))
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Queue a summary of an archived session. No model uses the default; the reply's `accepted` is false when one is already generating. */
export const summarizeEntry = (uuid: string, model?: 'haiku' | 'sonnet') =>
  postEntry(`/api/history/${encodeURIComponent(uuid)}/summary`, model ? { model } : {})

/** Open the session again where Settings say sessions open. A start that did not happen (or sessions set to open as a command) comes back as ok:false with `manualCommand` to paste. */
export const resumeEntry = (uuid: string) => postEntry(`/api/history/${encodeURIComponent(uuid)}/resume`, {})
