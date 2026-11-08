// The My PRs REFRESH request. Not post() in live.svelte.ts: the answer carries the press number the page waits on (as reviewsApi.ts does).
import { api } from './api.ts'
import { parseEntryAction } from './entryAction.ts'
import type { ReviewStartResult } from './reviewsApi.ts'

/** SELF-REVIEW or RE-REVIEW: start a round for one PR. A launch whose session did not start (or opens as a command) is HTTP 200 {ok:false, error, manualCommand}, so the body is read. Never throws. */
export async function startSelf(repo: string, number: number, model: 'sonnet' | 'opus'): Promise<ReviewStartResult> {
  try {
    const r = await api('/api/myprs/start', {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify({ repo, number, model }),
    })
    const { ok, error, manualCommand } = parseEntryAction(r.status, await r.json().catch(() => null))
    return { ok, ...(error && { error }), ...(manualCommand && { manualCommand }) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** REFRESH: poll GitHub now. `seq` is the press number the snapshot's `refresh.done` reaches when the poll has finished. Never throws. */
export async function refreshMyPrs(): Promise<{ ok: boolean; error?: string; seq?: number }> {
  try {
    const r = await api('/api/myprs/refresh', { method: 'POST', headers: { 'x-conn': '1' } })
    const body = await r.json().catch(() => null)
    const { ok, error } = parseEntryAction(r.status, body)
    const seq = (body as { seq?: unknown } | null)?.seq
    return { ok, ...(error && { error }), ...(ok && typeof seq === 'number' && Number.isFinite(seq) && { seq }) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** POST AND RESOLVE or RESOLVE: reply with `text` (nothing when it is empty) and resolve one thread. A write that GitHub refused is HTTP 200 {ok:false, error}, so the body is read. Never throws. */
export async function postThread(repo: string, number: number, threadId: string, text: string): Promise<{ ok: boolean; error?: string }> {
  try {
    const r = await api('/api/myprs/thread', {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify({ repo, number, threadId, text }),
    })
    const { ok, error } = parseEntryAction(r.status, await r.json().catch(() => null))
    return { ok, ...(error && { error }) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}
