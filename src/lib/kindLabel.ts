// The label a session kind carries wherever one is shown (History rows, the entry header, the filter chips).

const LABELS: Record<string, string> = { build: 'Build', adhoc: 'Ad hoc', review: 'Review', selfreview: 'Self-review' }

/** 'build' -> Build, 'adhoc' -> Ad hoc, 'review' -> Review, 'selfreview' -> Self-review, in sentence case: where it is shown, the cap class
 * gives it the theme's case. A kind this build does not know shows as itself. */
export const kindLabel = (kind: string): string => LABELS[kind] ?? kind
