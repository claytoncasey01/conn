// Which hosted session is a run's own, for its terminal panel on the run page.
import type { SessionSummary } from './types.ts'

/** key is null for a slot of a run of several repositories: the key names every slot's session, so only the folder finds one. */
type Run = { key: string | null; worktree: string; sessionId: string | null }

/**
 * The run's hosted session: the one with the run's session id. Only when the run has no session id, or no listed session
 * has it, is it found by the run's name or folder, and then only when exactly one hosted session has it. A run whose own
 * session is listed but not hosted has no terminal here: another session in its worktree is never shown as its own,
 * since the terminal shown is the one typed into.
 */
export function runTerminalSession(sessions: readonly SessionSummary[], run: Run): SessionSummary | null {
  if (run.sessionId !== null) {
    const own = sessions.find(each => each.id === run.sessionId)
    if (own) return own.hosted ? own : null
  }
  const matching = sessions.filter(each => each.hosted).filter(each => (run.key !== null && each.name === run.key) || each.cwd === run.worktree)
  return matching.length === 1 ? matching[0] : null
}
