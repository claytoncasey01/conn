import { markSeen, parseSeen, pruneSeen, type Seen } from './reviewSeen.ts'
import type { ReviewRow } from './types.ts'

// Per viewer: which waiting PRs this browser has already shown me. A convenience only, so it lives in localStorage and works without it.
const KEY = 'conn.reviews.seen'
const stored = (): Seen => {
  try {
    return parseSeen(localStorage.getItem(KEY))
  } catch {
    return {}
  }
}

export const seen = $state({ map: stored() })

function set(next: Seen) {
  if (next === seen.map) return
  seen.map = next
  try {
    localStorage.setItem(KEY, JSON.stringify(next))
  } catch {}
}

/** The rows that were on the Reviews page: the waiting ones are seen now. */
export const markShown = (shown: readonly ReviewRow[]) => set(markSeen(seen.map, shown))

/** Forget PRs that stopped waiting, whichever page is open. */
export const pruneShown = (rows: readonly ReviewRow[]) => set(pruneSeen(seen.map, rows))
