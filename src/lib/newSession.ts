// New session on the Sessions page: GET /api/sessions/new lists the repositories a plain Claude session may start in (the
// allowlisted ones with a clone, as the server resolves them) and where sessions open; POST starts one. The page sends a
// repository name and an optional name, never a folder, a command or a flag: the server builds the session itself.
import { api } from './api.ts'

export const NEW_SESSION_PATH = '/api/sessions/new'

/** What the form offers: the repositories, in allowlist order, where sessions open, and whether this run is dry */
export interface NewSessionChoices {
  repos: string[]
  where: string
  dry: boolean
}

/** How a start went. `id` is the host's id of a session started inside Conn (its row's id); `manualCommand` is the line to
 * paste when sessions open as a command or a start did not happen; `command` is what a dry run would have run. */
export interface NewSessionResult {
  ok: boolean
  id?: string
  dry?: boolean
  command?: string
  error?: string
  manualCommand?: string
}

const str = (v: unknown) => (typeof v === 'string' && v ? v : undefined)
const record = (body: unknown) => (body && typeof body === 'object' ? body : {}) as Record<string, unknown>

/** The choices a GET answered, or the error to show. `status` is the HTTP status, `body` the parsed JSON or null. */
export function parseChoices(status: number, body: unknown): NewSessionChoices | { error: string } {
  const j = record(body)
  if (status < 200 || status >= 300) return { error: str(j.error) ?? `HTTP ${status}` }
  const repos = Array.isArray(j.repos) ? j.repos.filter((r): r is string => typeof r === 'string') : []
  return { repos, where: str(j.where) ?? '', dry: j.dry === true }
}

/** A start's answer read into one shape. A refusal is an HTTP error with `error`; a start that did not happen, or that
 * only shows the command, is HTTP 200 {ok:false, error, manualCommand}. Never throws. */
export function parseStarted(status: number, body: unknown): NewSessionResult {
  const j = record(body)
  if (status < 200 || status >= 300) return { ok: false, error: str(j.error) ?? `HTTP ${status}` }
  if (j.ok !== true) {
    const manualCommand = str(j.manualCommand)
    return { ok: false, error: str(j.error) ?? 'the session did not start', ...(manualCommand && { manualCommand }) }
  }
  const out: NewSessionResult = { ok: true }
  const id = str(j.id)
  if (id) out.id = id
  if (j.dry === true) {
    out.dry = true
    const command = str(j.command)
    if (command) out.command = command
  }
  return out
}

/** The body a start sends: the repository, and the name only when one was typed. */
export function startBody(repo: string, name: string): { repo: string; name?: string } {
  const trimmed = name.trim()
  return trimmed ? { repo, name: trimmed } : { repo }
}

/** What the empty Sessions page suggests: New session when sessions open inside Conn (there is no terminal to type claude
 * into), else claude in a terminal. Until the server has said where sessions open, the terminal wording stays. */
export const emptyHint = (where: string | null): 'new-session' | 'terminal' => (where === 'inside' ? 'new-session' : 'terminal')

/** The choices now, or the error to show. */
export async function fetchChoices(): Promise<NewSessionChoices | { error: string }> {
  try {
    const r = await api(NEW_SESSION_PATH)
    return parseChoices(r.status, await r.json().catch(() => null))
  } catch (e) {
    return { error: e instanceof Error ? e.message : 'request failed' }
  }
}

/** Start a session in repo, named name when it is not blank. */
export async function startSession(repo: string, name: string): Promise<NewSessionResult> {
  try {
    const r = await api(NEW_SESSION_PATH, {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify(startBody(repo, name)),
    })
    return parseStarted(r.status, await r.json().catch(() => null))
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'request failed' }
  }
}
