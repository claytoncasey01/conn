// When a My PRs row's "Started" note is still news.
import type { MyPrStatus } from './types.ts'

/** What a row looked like when its press was made, and (`after`) what it said once the press's own round was on it */
export interface StartedAt {
  status: MyPrStatus
  round: number
  after?: MyPrStatus
}

/**
 * The press's own round is on the row (its round count went up by one): the status the row shows then is the one the note waits on
 * from there. It is taken once. A new round can change the status by itself (a RE-REVIEW from `findings` starts again from `idle`), so
 * the status at the press is not the one to wait on after it.
 */
export const settleStarted = (at: StartedAt, row: { status: MyPrStatus; round: number }): StartedAt =>
  at.after === undefined && row.round === at.round + 1 ? { ...at, after: row.status } : at

/**
 * Whether the "Started" note still belongs on the row. It goes once the session shows up (`running`), once a round after the press's
 * own is recorded, and once the status moves on: from the one at the press while the press's round is not on the row yet (its record
 * can arrive before or after the answer), from the one taken by `settleStarted` after.
 */
export function startedShows(at: StartedAt | null, row: { status: MyPrStatus; round: number }): boolean {
  if (at === null || row.status === 'running') return false
  if (row.round <= at.round) return row.status === at.status
  return row.round === at.round + 1 && (at.after === undefined || row.status === at.after)
}
