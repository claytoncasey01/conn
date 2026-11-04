// The History page's filter state turned into a request, and the server's counts turned into the filter bar.
// Pure and dependency-free so it runs under bun test.
import { repoBarGroups, type BarGroup, type Selection } from './filters.ts'
import { modelName } from './format.ts'
import { kindLabel } from './kindLabel.ts'

export interface HistoryInputs {
  text: string
  ref: string
  /** Part of a branch name */
  branch?: string
  minCost: string
  /** YYYY-MM-DD from a date input; counts only with the custom range */
  from?: string
  to?: string
}

const enc = encodeURIComponent

/** A real calendar day as a date input writes it. The server reads it as a day on this machine's clock. */
const day = (s: string | undefined): string | null => {
  const t = s?.trim()
  const m = t && /^(\d{4})-(\d{2})-(\d{2})$/.exec(t)
  if (!m) return null
  const [y, mo, d] = [Number(m[1]), Number(m[2]) - 1, Number(m[3])]
  const at = new Date(y, mo, d)
  return at.getFullYear() === y && at.getMonth() === mo && at.getDate() === d ? t : null
}

const RANGES = ['today', '7d', '30d', 'custom']
const LISTS = ['kind', 'outcome', 'model', 'project', 'repo'] as const
/** The query parameter of a group: its id, except that the server's name for the repo list is `repos` */
const PARAM: Record<(typeof LISTS)[number], string> = { kind: 'kind', outcome: 'outcome', model: 'model', project: 'project', repo: 'repos' }

/** The query string (no leading ?) for GET /api/history. Empty when nothing is selected or typed. */
export const historyQueryString = (sel: Selection, o: HistoryInputs): string => {
  const parts: string[] = []
  const range = (Object.hasOwn(sel, 'range') ? sel.range : []).find(id => RANGES.includes(id))
  if (range) {
    parts.push(`range=${range}`)
    if (range === 'custom') {
      const from = day(o.from), to = day(o.to)
      if (from) parts.push(`from=${from}`)
      if (to) parts.push(`to=${to}`)
    }
  }
  for (const g of LISTS) {
    const ids = (Object.hasOwn(sel, g) ? sel[g] : []).filter(id => id !== '')
    if (ids.length) parts.push(`${PARAM[g]}=${ids.map(enc).join(',')}`)
  }
  const text = o.text.trim()
  if (text) parts.push(`q=${enc(text)}`)
  const ref = o.ref.trim()
  if (ref) parts.push(`ref=${enc(ref)}`)
  const branch = o.branch?.trim()
  if (branch) parts.push(`branch=${enc(branch)}`)
  const cost = o.minCost.trim()
  // Number('') is 0 and Number('1e999') is Infinity: only a plain non-negative decimal is a minimum cost
  if (/^(\d+\.?\d*|\.\d+)$/.test(cost) && Number.isFinite(Number(cost))) parts.push(`minCost=${cost}`)
  return parts.join('&')
}

const KINDS = ['build', 'adhoc', 'review', 'selfreview'] as const
// What the archive writes today (archive.ts outcomeOf). Always offered, so a filter that hides one does not make its chip vanish.
const OUTCOMES = ['merged', 'PR opened', 'ended']

/** The bar's range group: no counts, a range does not partition the rows the way a kind does. */
const rangeGroup: BarGroup = {
  id: 'range',
  label: 'When',
  exclusive: true,
  chips: [
    { id: 'today', label: 'Today' },
    { id: '7d', label: '7 days' },
    { id: '30d', label: '30 days' },
    { id: 'custom', label: 'Custom' },
  ],
}

/** Projects shown before SHOW ALL; a selected one always shows. */
export const PROJECT_CAP = 8

/**
 * The chips of the five groups, and a sixth for repos that is there only when more than one repo is in the archive. Counts are the server's (HistoryList.counts), which already apply every filter but the
 * group's own. A selected outcome, model or project stays so it can be unselected; a blank model or project is not a chip.
 */
export const historyBar = (counts: Record<string, Record<string, number>>, models: string[], sel: Selection, projects: string[] = [], repos: string[] = []): BarGroup[] => {
  const n = (g: string, id: string) => (Object.hasOwn(counts, g) && Object.hasOwn(counts[g], id) ? counts[g][id] : 0)
  const picked = (g: string) => (Object.hasOwn(sel, g) ? sel[g] : [])
  const values = (g: string, base: string[]) => [...new Set([...base, ...Object.keys(counts[g] ?? {}), ...picked(g)])].filter(id => id !== '')
  return [
    rangeGroup,
    { id: 'kind', label: 'Kind', chips: KINDS.map((id) => ({ id, label: kindLabel(id), count: n('kind', id) })) },
    { id: 'outcome', label: 'Outcome', chips: values('outcome', OUTCOMES).map(id => ({ id, label: id, count: n('outcome', id) })) },
    { id: 'model', label: 'Model', chips: values('model', models).sort().map(id => ({ id, label: modelName(id), count: n('model', id) })) },
    { id: 'project', label: 'Project', cap: PROJECT_CAP, chips: values('project', projects).sort().map(id => ({ id, label: id, count: n('project', id) })) },
    ...repoBarGroups(repos, Object.hasOwn(counts, 'repo') ? counts.repo : {}, picked('repo'), PROJECT_CAP),
  ]
}

/** The server's largest page (historyApi MAX_LIMIT) */
export const MAX_PAGE = 500

/** More rows after the ones shown, first occurrence of an id kept: a session archived between two pages shifts the offsets and can repeat a row. */
export const appendRows = <T extends { id: string }>(shown: T[], more: T[]): T[] => {
  const seen = new Set(shown.map(r => r.id))
  const out = [...shown]
  for (const r of more) if (!seen.has(r.id)) (seen.add(r.id), out.push(r))
  return out
}

/** How many rows a refetch asks for so what SHOW MORE loaded stays: the larger of the loaded count and one page. Past the server's cap it is the cap, so rows beyond 500 fall back to the first 500. */
export const refreshLimit = (loaded: number, page: number): number => Math.min(Math.max(loaded, page), MAX_PAGE)
