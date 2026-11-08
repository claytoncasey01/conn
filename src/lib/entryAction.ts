// What the History POST routes (resume, summary) answer, read into one shape. The existing post() in live.svelte.ts
// counts any 2xx as success and drops the body, but a resume whose session did not start (or opens as a command) is HTTP 200 {ok:false, error, manualCommand}.
import type { ActionResult } from './live.svelte.ts'
import type { SummaryState } from './types.ts'

export interface EntryActionResult extends ActionResult {
  /** resume under CONN_WORK_DRY: nothing was launched; `command` is what would have run */
  dry?: boolean
  command?: string
  /** resume that could not open a workspace: a command to paste into a terminal */
  manualCommand?: string
  /** summary: the state after the request, and whether it was taken (false: one is already generating) */
  state?: SummaryState
  accepted?: boolean
}

const STATES: SummaryState[] = ['none', 'queued', 'running', 'ready', 'failed']
const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)

/** `status` is the HTTP status, `body` the parsed JSON or null. Never throws. */
export function parseEntryAction(status: number, body: unknown): EntryActionResult {
  const j = (body && typeof body === 'object' ? body : {}) as Record<string, unknown>
  if (status < 200 || status >= 300) return { ok: false, error: str(j.error) ?? `HTTP ${status}` }
  if (j.ok === false) return { ok: false, error: str(j.error) ?? 'the request did not go through', ...(str(j.manualCommand) && { manualCommand: str(j.manualCommand) }) }
  const out: EntryActionResult = { ok: true }
  if (j.dry === true) {
    out.dry = true
    const c = str(j.command)
    if (c) out.command = c
  }
  if (STATES.includes(j.state as SummaryState)) out.state = j.state as SummaryState
  if (typeof j.accepted === 'boolean') out.accepted = j.accepted
  return out
}
