// The Reviews page's filter groups and the tab badge. Pure, so it runs under bun test.
import { repoGroups, type Group, type Selection } from './filters.ts'
import { unseenWaiting, type Seen } from './reviewSeen.ts'
import type { ReviewRow, ReviewState, ReviewsView } from './types.ts'

const STATUS: [ReviewState, string][] = [
  ['new', 'New'],
  ['reviewing', 'Reviewing'],
  ['waiting', 'Waiting'],
  ['rereview', 'Re-review'],
  ['done', 'Done'],
]

/** Author chips are the authors present, plus any selected one whose PRs are gone, so it can be unselected. */
export const reviewGroups = (rows: ReviewRow[], sel: Selection): Group<ReviewRow>[] => {
  const authors = new Set(rows.map((r) => r.author))
  for (const id of sel.author ?? []) authors.add(id)
  return [
    {
      id: 'status',
      label: 'Status',
      chips: STATUS.map(([id, label]) => ({ id, label, test: (r: ReviewRow) => r.state === id })),
    },
    {
      id: 'author',
      label: 'Author',
      chips: [...authors].sort((a, b) => a.localeCompare(b)).map((a) => ({ id: a, label: a, test: (r: ReviewRow) => r.author === a })),
    },
    // the repos of the rows on the page; absent with one repo or none, as on Sessions and My PRs
    ...repoGroups(rows, sel),
  ]
}

/** The tab badge: PRs that wait on Casey (new, or asked again) and that he has not yet seen on the Reviews page. */
export const badgeCount = (view: ReviewsView | undefined, seen: Seen = {}): number => unseenWaiting(view?.rows ?? [], seen).length

/** The link as an href only when it is https; GitHub and Slack text is untrusted, so anything else (javascript:, data:) is no link. */
export const safeUrl = (u: string | null): string | null => (u !== null && u.startsWith('https://') ? u : null)
