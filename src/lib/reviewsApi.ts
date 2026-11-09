// The REVIEW button's request, and the remembered model choice. Not post() in live.svelte.ts: a review whose session did not start (or opens as a command) is HTTP 200 {ok:false, error, manualCommand},
// which post() would take for success, so the body is read here (the same parse as History's RESUME).
import { withAdvisor, type AdvisorPick } from './advisor.ts'
import { api } from './api.ts'
import { parseEntryAction } from './entryAction.ts'

export interface ReviewStartResult {
  ok: boolean
  error?: string
  /** The session did not start, or sessions open as a command: a command to paste into a terminal */
  manualCommand?: string
}

/** REVIEW on PR `n` of `repo` (`owner/repo`; the server checks both), on the advisor picked ('' for Settings'). Never throws: a network failure is `{ ok: false, error }`. */
export async function startReview(repo: string, n: number, model: 'sonnet' | 'opus', advisor: AdvisorPick = ''): Promise<ReviewStartResult> {
  try {
    const r = await api('/api/reviews/start', {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify(withAdvisor({ repo, number: Math.trunc(n), model }, advisor)),
    })
    const { ok, error, manualCommand } = parseEntryAction(r.status, await r.json().catch(() => null))
    return { ok, ...(error && { error }), ...(manualCommand && { manualCommand }) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Puts an old review record conn could not place under `repo` (the Settings page), or every one (`'all'`). Never throws. */
export async function placeLegacyReview(n: number | 'all', repo: string): Promise<{ ok: boolean; error?: string }> {
  try {
    const r = await api('/api/reviews/legacy', {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify({ number: n === 'all' ? 'all' : Math.trunc(n), repo }),
    })
    const { ok, error } = parseEntryAction(r.status, await r.json().catch(() => null))
    return { ok, ...(error && { error }) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** REFRESH: poll GitHub now, and ask Slack again at once if it was signed out. Never throws. */
export async function refreshReviews(): Promise<{ ok: boolean; error?: string; seq?: number }> {
  try {
    const r = await api('/api/reviews/refresh', { method: 'POST', headers: { 'x-conn': '1' } })
    const body = await r.json().catch(() => null)
    const { ok, error } = parseEntryAction(r.status, body)
    const seq = (body as { seq?: unknown } | null)?.seq
    return { ok, ...(error && { error }), ...(ok && typeof seq === 'number' && Number.isFinite(seq) && { seq }) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

// ---- the SONNET | OPUS choice, remembered between visits ----

type Store = Pick<Storage, 'getItem' | 'setItem'>
const MODEL_KEY = 'conn.reviews.model'

/** localStorage, or null where it is missing or the browser refuses it (a private window). */
const browserStore = (): Store | null => {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

/** The last choice; Sonnet when there is none, it is not one of the two, or storage cannot be read. */
export function loadModel(storage: Store | null = browserStore()): 'sonnet' | 'opus' {
  try {
    return storage?.getItem(MODEL_KEY) === 'opus' ? 'opus' : 'sonnet'
  } catch {
    return 'sonnet'
  }
}

export function saveModel(model: 'sonnet' | 'opus', storage: Store | null = browserStore()): void {
  try {
    storage?.setItem(MODEL_KEY, model)
  } catch {
    // not remembered: the choice still applies to this visit
  }
}
