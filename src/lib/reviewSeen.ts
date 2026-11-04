import type { ReviewRow } from './types.ts'

/** PR key (`owner/repo#n`) -> what the row looked like when it was last on screen, e.g. "new:" or "rereview:github". */
export type Seen = Record<string, string>

/** A PR waits on me when it is new or asked again: the same rows the tab badge counts. */
export const waits = (r: ReviewRow): boolean => r.state === 'new' || r.state === 'rereview'

/** What makes a waiting row look different to me: its state and how the author asked again. */
export const fingerprint = (r: ReviewRow): string => `${r.state}:${r.reAsked ?? ''}`

/** The waiting rows I have not seen in their current form. */
export const unseenWaiting = (rows: readonly ReviewRow[], seen: Seen): ReviewRow[] => rows.filter((r) => waits(r) && seen[r.key] !== fingerprint(r))

/** `seen` after rows were on screen: each waiting row among them is recorded as seen. The same object when nothing changed. */
export function markSeen(seen: Seen, shown: readonly ReviewRow[]): Seen {
  let next: Seen | null = null
  for (const r of shown) {
    if (!waits(r) || seen[r.key] === fingerprint(r)) continue
    next ??= { ...seen }
    next[r.key] = fingerprint(r)
  }
  return next ?? seen
}

/**
 * `seen` without the PRs that no longer wait on me (reviewed, merged, gone). Dropping them is what lets the same PR raise the badge again
 * the next time it is asked for another review. The same object when nothing was dropped.
 */
export function pruneSeen(seen: Seen, rows: readonly ReviewRow[]): Seen {
  const waiting = new Set(rows.filter(waits).map((r) => r.key))
  const keep = Object.keys(seen).filter((n) => waiting.has(n))
  return keep.length === Object.keys(seen).length ? seen : Object.fromEntries(keep.map((n) => [n, seen[n]]))
}

/** What was stored: an object of strings, anything else is nothing seen. */
export function parseSeen(raw: string | null): Seen {
  try {
    const v = raw === null ? null : JSON.parse(raw)
    if (typeof v !== 'object' || v === null || Array.isArray(v)) return {}
    return Object.fromEntries(Object.entries(v).filter((e): e is [string, string] => typeof e[1] === 'string'))
  } catch {
    return {}
  }
}
