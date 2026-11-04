// The Settings page's "old review records" block: the records from before Reviews read several repos that conn could not place in a
// repo. Pure, so it runs under bun test.
import { covered, repoName } from './settingsModel.ts'

/** The spec's wording: "N old review records conn could not place". */
export const legacyCountText = (n: number): string => `${n} old review ${n === 1 ? 'record' : 'records'} conn could not place`

/** The repos a record can be placed under: the exact patterns of the list and the repos conn has seen that the list covers, each once, sorted. */
export function legacyChoices(allow: readonly string[] | null, seen: readonly string[]): string[] {
  const out = new Set<string>()
  for (const p of allow ?? []) if (!p.endsWith('/*') && repoName(p)) out.add(p.toLowerCase())
  for (const r of seen) if (repoName(r) && covered(r, allow)) out.add(r.toLowerCase())
  return [...out].sort()
}

/** Why a typed repo cannot take a record, or null when it can (the server checks again). */
export function legacyProblem(raw: string, allow: readonly string[] | null): string | null {
  const repo = raw.trim()
  if (!repo) return 'Write the repository as owner/repo'
  if (!repoName(repo)) return 'Write the repository as owner/repo'
  if (!covered(repo, allow)) return `${repo} is not an allowed repository`
  return null
}
