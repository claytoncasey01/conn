// What a My PRs row says in words: its status tag and its Slack draft line. Pure, so the row's choices are tested without a browser.
import type { MyPrRow, MyPrStatus } from './types.ts'

const LABEL: Record<MyPrStatus, string> = {
  'no-clone': 'No local clone',
  fork: 'Fork PR',
  'draft-pr': 'Draft PR',
  running: 'Session running',
  waiting: 'Waiting for Greptile',
  findings: 'Findings to fix',
  ready: 'Clean, draft ready',
  'draft-failed': 'Draft failed',
  idle: 'Idle',
}

/** The status tag, in sentence case (the row cases it with the theme). A clean PR whose draft is not saved yet says so: the draft is still being made (or retried). */
export function statusText(row: MyPrRow): string {
  const open = row.openBot + row.openHuman
  if (row.status === 'findings') return `${open} ${open === 1 ? 'finding' : 'findings'} to fix`
  if (row.status === 'no-clone') return `No local clone of ${row.repo}`
  // No draft is made (Slack off, or no channel for it): the PR is clean, and nothing is being drafted
  if (row.status === 'ready' && row.slackDraft?.state !== 'saved') return row.draftOff ? 'Clean' : 'Clean, drafting…'
  // A status this build does not know (a newer server) must not take the whole list down
  return LABEL[row.status] ?? LABEL.idle
}

export type DraftView =
  | { kind: 'none' }
  /** `stale`: a later review found something on the draft's head (the row shows findings), so the text ("no open bot findings") no longer holds */
  | { kind: 'saved'; stale: boolean }
  /** `retryAt`: when conn tries it again; `byHand`: no try will save it, so its text is the one to post */
  | { kind: 'failed'; retryAt: number | null; byHand: boolean; stale: boolean }

/**
 * The Slack draft line of a row, from the draft kept for its head (the server keeps none for an earlier head) and the server's retry
 * time. It is out of date only when the row shows findings: a new round at work or about to start, the wait for an ask, a missing
 * clone or a draft PR say nothing against it.
 */
export function draftView(row: MyPrRow): DraftView {
  const d = row.slackDraft
  if (!d) return { kind: 'none' }
  const stale = row.status === 'findings'
  if (d.state === 'saved') return { kind: 'saved', stale }
  const live = row.status === 'draft-failed'
  const retryAt = live ? (row.draftRetryAt ?? null) : null
  return { kind: 'failed', retryAt, byHand: live && retryAt === null, stale }
}

/** A time of day, HH:MM, in this browser's zone */
export const clock = (at: number): string => {
  const d = new Date(at)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
