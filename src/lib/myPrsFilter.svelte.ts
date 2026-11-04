import { emptySelection, type Selection } from './filters.ts'

/** Module-level so the selection survives opening a PR and coming back to the list, as sessionFilter does. */
export const myPrsFilter = $state({ selection: emptySelection() as Selection })
