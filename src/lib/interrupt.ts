// Interrupt: sends Ctrl-C (one ETX byte) to a hosted program through POST /api/term/<id>/interrupt, which types it through
// the host's one writer. It needs no live terminal, so it reaches a program that floods output faster than the page draws
// it, whose terminal panel has closed. Never throws.
import { api } from './api.ts'
import { SESSION_ID, type Phase } from './terminal.ts'

/** What the button says it does. */
export const INTERRUPT_HELP = 'Sends Ctrl-C to the program'

/** A closed panel offers it: its keys have no way in. A live one has its own Ctrl-C, and an ended program has nothing to stop. */
export const interruptOffered = (view: { phase: Phase }): boolean => view.phase === 'closed'

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)

/** POST the interrupt for id, an id the host issued, with no body. */
export async function interruptSession(id: string): Promise<{ ok: true } | { ok: false; error: string }> {
  if (!SESSION_ID.test(id)) return { ok: false, error: 'not a hosted session' }
  try {
    const r = await api(`/api/term/${id}/interrupt`, { method: 'POST', headers: { 'x-conn': '1' } })
    if (r.ok) return { ok: true }
    const body: unknown = await r.json().catch(() => null)
    return { ok: false, error: isObj(body) && typeof body.error === 'string' && body.error ? body.error : `HTTP ${r.status}` }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}
