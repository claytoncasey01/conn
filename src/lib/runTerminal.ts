// Which hosted session is a run's own, for its terminal panel on the run page.
import type { SessionSummary } from './types.ts'

/** key is null for a slot of a run of several repositories: the key names every slot's session, so only the folder finds one. */
type Run = { key: string | null; worktree: string; sessionId: string | null }

/**
 * The run's hosted session: the one with the run's session id; else, by the run's name or folder, only when exactly one
 * hosted session has it. Two in one worktree are not told apart by guessing, since the terminal shown is the one typed into.
 */
export function runTerminalSession(sessions: readonly SessionSummary[], run: Run): SessionSummary | null {
  const hosted = sessions.filter(each => each.hosted)
  if (run.sessionId !== null) {
    const byId = hosted.find(each => each.id === run.sessionId)
    if (byId) return byId
  }
  const matching = hosted.filter(each => (run.key !== null && each.name === run.key) || each.cwd === run.worktree)
  return matching.length === 1 ? matching[0] : null
}
