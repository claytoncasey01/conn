// The My PRs list's order, and the viewer's remembered choice of it.
import type { MyPrRow } from './types.ts'

export type MyPrsSort = 'opened' | 'comment'

/** Newest first by the chosen date. Array.sort is stable, so equal dates (a PR with no comment is 0) keep the order the server sent. Returns a copy. */
export function sortRows(rows: MyPrRow[], by: MyPrsSort): MyPrRow[] {
  const key = (r: MyPrRow) => (by === 'comment' ? r.lastCommentAt : r.createdAt)
  return [...rows].sort((a, b) => key(b) - key(a))
}

type Store = Pick<Storage, 'getItem' | 'setItem'>
const SORT_KEY = 'conn.myprs.sort'

/** localStorage, or null where it is missing or the browser refuses it (a private window). */
const browserStore = (): Store | null => {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

/** The last choice; `opened` when there is none, it is not one of the two, or storage cannot be read. */
export function loadSort(storage: Store | null = browserStore()): MyPrsSort {
  try {
    return storage?.getItem(SORT_KEY) === 'comment' ? 'comment' : 'opened'
  } catch {
    return 'opened'
  }
}

export function saveSort(by: MyPrsSort, storage: Store | null = browserStore()): void {
  try {
    storage?.setItem(SORT_KEY, by)
  } catch {
    // not remembered: the choice still applies to this visit
  }
}
