// Shared filter selection rules: chips in a group combine as OR, groups combine as AND.
// "ALL" is the absence of a selection. Pure and dependency-free so it runs under bun test.

export interface Chip<T> { id: string; label: string; test: (item: T) => boolean }
export interface Group<T> { id: string; label: string; chips: Chip<T>[]; exclusive?: boolean }
export type Selection = Record<string, string[]> // group id -> selected chip ids

export const emptySelection = (): Selection => ({})

export const isActive = (sel: Selection): boolean => Object.values(sel).some((ids) => ids.length > 0)

export const toggle = (sel: Selection, group: { id: string; exclusive?: boolean }, chipId: string): Selection => {
  const current = sel[group.id] ?? []
  let next: string[]
  if (current.includes(chipId)) next = current.filter((id) => id !== chipId)
  else next = group.exclusive ? [chipId] : [...current, chipId]
  const out: Selection = { ...sel }
  if (next.length) out[group.id] = next
  else delete out[group.id]
  return out
}

// A group with no selection is not applied. Selected ids that match no chip do not pass.
const passes = <T>(item: T, group: Group<T>, sel: Selection): boolean => {
  const ids = sel[group.id]
  if (!ids || ids.length === 0) return true
  return group.chips.some((chip) => ids.includes(chip.id) && chip.test(item))
}

export const matches = <T>(item: T, groups: Group<T>[], sel: Selection): boolean => groups.every((g) => passes(item, g, sel))

// How many items the chip would show if clicked: its own group is ignored, every other group applies.
export const chipCount = <T>(items: T[], groups: Group<T>[], sel: Selection, groupId: string, chipId: string): number => {
  const chip = groups.find((g) => g.id === groupId)?.chips.find((c) => c.id === chipId)
  if (!chip) return 0
  const others = groups.filter((g) => g.id !== groupId)
  return items.filter((item) => chip.test(item) && matches(item, others, sel)).length
}

export interface BarGroup {
  id: string
  label: string
  exclusive?: boolean
  /** A long group (projects) shows this many chips until expanded; a selected chip always shows */
  cap?: number
  chips: { id: string; label: string; count?: number }[]
}

/** The chips of a group the bar draws now, and how many a SHOW ALL would add. */
export const visibleChips = (g: BarGroup, selected: string[], expanded: boolean): { chips: BarGroup['chips']; hidden: number } => {
  if (!g.cap || expanded || g.chips.length <= g.cap) return { chips: g.chips, hidden: 0 }
  const chips = g.chips.filter((c, i) => i < g.cap! || selected.includes(c.id))
  return { chips, hidden: g.chips.length - chips.length }
}

// What the bar draws: every chip, including those that would show nothing, with its count.
export const barGroups = <T>(items: T[], groups: Group<T>[], sel: Selection): BarGroup[] =>
  groups.map((g) => ({
    id: g.id,
    label: g.label,
    exclusive: g.exclusive,
    chips: g.chips.map((c) => ({ id: c.id, label: c.label, count: chipCount(items, groups, sel, g.id, c.id) })),
  }))

// ---- The repo group, the same on every page that filters by repo ----

/** A repo chip: its id is the lowercase `owner/repo` (what a saved selection and the History query carry), its label the spelling first seen. */
export interface RepoChoice { id: string; label: string }

/**
 * The repos of a page's data as chips: each distinct repo (a name differing only in case is one), sorted by id, and any selected id the
 * data no longer has, so it can be unselected. Empty, which hides the group, when there is one repo or none and nothing is selected: a
 * filter of one choice filters nothing. A selection keeps the group on screen even then, or it could filter invisibly with no chip to clear it.
 */
export const repoChoices = (repos: Iterable<string | null | undefined>, selected: readonly string[] = []): RepoChoice[] => {
  const out = new Map<string, string>()
  for (const r of repos) if (typeof r === 'string' && r !== '' && !out.has(r.toLowerCase())) out.set(r.toLowerCase(), r)
  for (const id of selected) if (!out.has(id)) out.set(id, id)
  if (out.size <= 1 && selected.length === 0) return []
  return [...out].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([id, label]) => ({ id, label }))
}

/**
 * The repo group for a page that holds its whole list on the client (Sessions, My PRs): zero groups or one, ready to spread into the
 * page's groups. An item in no repo (null) matches no chip, so choosing a repo leaves it out.
 */
export const repoGroups = <T extends { repo?: string | null }>(items: readonly T[], sel: Selection): Group<T>[] => {
  const chips = repoChoices(items.map((i) => i.repo), Object.hasOwn(sel, 'repo') ? sel.repo : [])
  if (chips.length === 0) return []
  return [{ id: 'repo', label: 'Repo', chips: chips.map((c) => ({ id: c.id, label: c.label, test: (i: T) => (i.repo ?? '').toLowerCase() === c.id })) }]
}

/**
 * The repo group for a page that filters on the server (History): the facet's repos with the server's counts, which already apply every filter
 * but this group's own. Zero groups or one, like repoGroups.
 */
export const repoBarGroups = (repos: readonly string[], counts: Record<string, number>, selected: readonly string[], cap?: number): BarGroup[] => {
  const chips = repoChoices(repos, selected)
  if (chips.length === 0) return []
  return [{ id: 'repo', label: 'Repo', cap, chips: chips.map((c) => ({ id: c.id, label: c.label, count: Object.hasOwn(counts, c.id) ? counts[c.id] : 0 })) }]
}
