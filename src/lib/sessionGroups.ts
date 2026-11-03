// The Sessions page's filter groups. Pure, so it runs under bun test.
import { isActive, repoGroups, type Group, type Selection } from './filters.ts'
import { modelName } from './format.ts'
import { kindLabel } from './kindLabel.ts'
import type { SessionSummary } from './types.ts'

const modelLabel = (model: string): string => (model ? modelName(model) : 'UNKNOWN')

/** Model chips are the models the sessions use, plus any selected one whose sessions are gone, so it can be unselected. */
export const sessionGroups = (sessions: SessionSummary[], sel: Selection): Group<SessionSummary>[] => {
  const labels = new Set(sessions.map((s) => modelLabel(s.model)))
  for (const id of sel.model ?? []) labels.add(id)
  return [
    {
      id: 'status',
      label: 'Status',
      chips: [
        { id: 'needs', label: 'Needs input', test: (s) => s.status === 'waiting' },
        { id: 'live', label: 'Live', test: (s) => s.status === 'busy' },
        { id: 'idle', label: 'Idle', test: (s) => s.status === 'idle' },
      ],
    },
    {
      id: 'kind',
      label: 'Kind',
      chips: [
        { id: 'build', label: kindLabel('build'), test: (s) => s.kind === 'build' },
        { id: 'adhoc', label: kindLabel('adhoc'), test: (s) => s.kind === 'adhoc' },
        { id: 'review', label: kindLabel('review'), test: (s) => s.kind === 'review' },
        { id: 'selfreview', label: kindLabel('selfreview'), test: (s) => s.kind === 'selfreview' },
      ],
    },
    {
      id: 'pr',
      label: 'Has PR',
      chips: [
        { id: 'has', label: 'Has PR', test: (s) => !!s.pr },
        { id: 'none', label: 'No PR', test: (s) => !s.pr },
      ],
    },
    {
      id: 'model',
      label: 'Model',
      // the id stays the model's name as the cards print it (a saved selection keeps working); the label is sentence case, cased by the theme
      chips: [...labels].sort().map((id) => ({ id, label: id.charAt(0) + id.slice(1).toLowerCase(), test: (s: SessionSummary) => modelLabel(s.model) === id })),
    },
    // the repos of the sessions on the page; absent with one repo or none
    ...repoGroups(sessions, sel),
  ]
}

/** 'no-sessions': nothing is running. 'filtered-out': sessions exist but the filters hide them all. */
export const emptyState = (total: number, shown: number, sel: Selection): 'no-sessions' | 'filtered-out' | null => {
  if (shown > 0) return null
  if (total === 0) return 'no-sessions'
  return isActive(sel) ? 'filtered-out' : null
}
