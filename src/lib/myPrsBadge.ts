import type { MyPrRow, MyPrsView } from './types.ts'

/**
 * A row that waits on me: the Slack draft is saved and only needs posting, or the draft failed for good (its retries are used up,
 * `draftMaxTries` being the server's cap) and its text must be copied by hand. A `ready` row with no draft yet is not one: the draft is
 * still being made. Nor is a failed draft that is still being retried: the row shows it, but the next try may save it.
 */
export const needsMe = (r: MyPrRow, draftMaxTries: number): boolean => {
  const d = r.slackDraft
  if (!d) return false
  return d.state === 'failed' ? r.status === 'draft-failed' && d.tries >= draftMaxTries : r.status === 'ready' && d.state === 'saved'
}

/** The tab badge: PRs that need me, 0 until the first My PRs view arrives. */
export const readyCount = (view: MyPrsView | undefined): number => view?.rows.filter(r => needsMe(r, view.draftMaxTries)).length ?? 0
