import { emptySelection, type Selection } from './filters.ts'

/** Module-level so the filters survive opening an entry and coming back to the list, as sessionFilter does. */
export const historyFilter = $state({
  selection: emptySelection() as Selection,
  text: '',
  ref: '',
  branch: '',
  minCost: '',
  from: '',
  to: '',
})

export const resetHistoryFilter = () => {
  historyFilter.selection = emptySelection()
  historyFilter.text = ''
  historyFilter.ref = ''
  historyFilter.branch = ''
  historyFilter.minCost = ''
  historyFilter.from = ''
  historyFilter.to = ''
}
