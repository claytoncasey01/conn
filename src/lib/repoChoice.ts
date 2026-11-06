// The repositories a START builds in and the layout of their sessions: what the picker starts on, and the viewer's remembered
// choice of it. Remembering is a convenience only, so it lives in localStorage and the page works without it.
import type { ResearchModel, RunLayout, WorkBuild } from './types.ts'

type Store = Pick<Storage, 'getItem' | 'setItem'>
const REPOS_KEY = 'conn.work.repos'
const LAYOUT_KEY = 'conn.work.layout'

/** What the viewer chose last: the repositories in order, and the layout (null when none was remembered). */
export interface RepoChoice {
  repos: string[]
  layout: RunLayout | null
}

/** localStorage, or null where it is missing or the browser refuses it (a private window). */
const browserStore = (): Store | null => {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

const parseRepos = (text: string | null): string[] => {
  if (text === null) return []
  try {
    const value: unknown = JSON.parse(text)
    return Array.isArray(value) ? value.filter((r): r is string => typeof r === 'string') : []
  } catch {
    return []
  }
}

/** The last choice; none when there is none, it cannot be read, or storage is refused. */
export function loadChoice(storage: Store | null = browserStore()): RepoChoice {
  try {
    const layout = storage?.getItem(LAYOUT_KEY) ?? null
    return { repos: parseRepos(storage?.getItem(REPOS_KEY) ?? null), layout: layout === 'shared' || layout === 'split' ? layout : null }
  } catch {
    return { repos: [], layout: null }
  }
}

export function saveChoice(choice: RepoChoice, storage: Store | null = browserStore()): void {
  try {
    storage?.setItem(REPOS_KEY, JSON.stringify(choice.repos))
    if (choice.layout) storage?.setItem(LAYOUT_KEY, choice.layout)
  } catch {
    // not remembered: the choice still applies to this visit
  }
}

/** What the picker starts on: the remembered repositories that are still ready, in their order and spelled as the list spells them; else the default when it is ready; else nothing. */
export function preselect(build: WorkBuild, remembered: readonly string[]): string[] {
  const ready = new Map(build.repos.filter((r) => r.ready).map((r) => [r.repo.toLowerCase(), r.repo]))
  const kept: string[] = []
  for (const name of remembered) {
    const listed = ready.get(name.toLowerCase())
    if (listed && !kept.includes(listed)) kept.push(listed)
  }
  if (kept.length > 0) return kept
  const fallback = build.default === null ? undefined : ready.get(build.default.toLowerCase())
  return fallback ? [fallback] : []
}

/** The choice with repo ticked (after the ones chosen, so the first ticked stays the lead) or unticked. */
export const toggleRepo = (chosen: readonly string[], repo: string): string[] => (chosen.includes(repo) ? chosen.filter((r) => r !== repo) : [...chosen, repo])

/** START's body: the repositories in order, and the layout only when there are several (one repository has one session). */
export function startBody(model: ResearchModel, force: boolean, repos: readonly string[], layout: RunLayout) {
  const body = { model, force: force || undefined, repos: [...repos] }
  return repos.length > 1 ? { ...body, layout } : body
}
