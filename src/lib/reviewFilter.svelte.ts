import { emptySelection, type Selection } from './filters.ts'

/** Module-level so the selection survives opening a session and coming back to the list, as sessionFilter does. */
export const reviewFilter = $state({ selection: emptySelection() as Selection })
