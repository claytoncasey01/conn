// A run's legs (one per repository) as the run page reads them: which may open a PR, fix its checks, resume its session or
// remove its worktree. Each rule follows the server's (internal/openwork), named in its comment, so the page offers a button
// only where the server would take it. RESUME is the one narrower than the server: while the run builds, a slot with no captured
// id is offered nothing, since the page cannot see whether its session is still opening.
import { runTerminalSession } from './runTerminal.ts'
import type { PrStatus, Run, RunRepo, SessionSummary } from './types.ts'

/** The legs with commits ahead of their base, in order. */
export const legsWithCommits = (run: Run): RunRepo[] => run.repos.filter(leg => leg.ahead > 0)

/** The legs with no PR yet, in order. */
export const legsWithoutPr = (run: Run): RunRepo[] => run.repos.filter(leg => leg.pr === null)

/** Whether OPEN PR may run: while the run builds, and while its PR is open as long as a leg has none (openable, pr_open.go). */
export const prOpenable = (run: Run): boolean => {
  if (run.state === 'building') return true
  if (run.state === 'pr_open') return legsWithoutPr(run).length > 0
  return false
}

/** Whether a leg's OPEN PR applies: the run may open PRs, and the leg has commits and no PR (a leg with none is skipped). */
export const legNeedsPr = (run: Run, leg: RunRepo): boolean => {
  if (!prOpenable(run)) return false
  if (leg.pr !== null) return false
  return leg.ahead > 0
}

/** The legs whose OPEN PR applies, in order: what OPEN ALL PRS opens. */
export const legsToOpen = (run: Run): RunRepo[] => run.repos.filter(leg => legNeedsPr(run, leg))

/** Whether OPEN ALL PRS applies: more than one leg needs its PR. */
export const canOpenAll = (run: Run): boolean => legsToOpen(run).length > 1

const settled = (s: PrStatus | undefined): boolean => s?.state === 'merged' || s?.state === 'closed'

/** Whether the run counts as merged: every PR settled (merged or closed) and at least one merged; a leg with no PR does not hold it back (merged, pr_cleanup.go). */
export const runMerged = (run: Run): boolean => {
  if (run.state !== 'pr_open') return false
  const prs = run.repos.flatMap(leg => (leg.pr ? [leg.pr] : []))
  if (!prs.every(pr => settled(pr.status))) return false
  return prs.some(pr => pr.status?.state === 'merged')
}

/** Whether FIX FAILING CHECKS applies to a leg: its PR is open, polled and red (fixQuiet, pr_fix.go). */
export const legChecksFailing = (run: Run, leg: RunRepo): boolean => {
  if (run.state !== 'pr_open') return false
  const s = leg.pr?.status
  if (!s) return false
  if (settled(s)) return false
  return s.checks.state === 'failing'
}

/** Whether a leg is the shared layout's lead while another leg's PR is open: the run's session works there for it, so its worktree stays (openElsewhere, pr_cleanup.go). */
export const legKeptForOthers = (run: Run, leg: RunRepo): boolean => {
  if (run.layout === 'split') return false
  if (leg !== run.repos[0]) return false
  return run.repos.slice(1).some(other => !other.cleaned && other.pr !== null && !settled(other.pr.status))
}

/** Whether REMOVE WORKTREE applies to a leg: its PR merged or closed and its worktree is still there (cleanupQuiet, pr_cleanup.go). */
export const legCanClean = (run: Run, leg: RunRepo): boolean => {
  if (run.state !== 'pr_open') return false
  if (!settled(leg.pr?.status)) return false
  if (leg.cleaned) return false
  return !legKeptForOthers(run, leg)
}

/** Whether a leg has a build session of its own: the lead in the shared layout, every leg in the split one (sessionSlots, slots.go). */
export const legHasSessionSlot = (run: Run, leg: RunRepo): boolean => run.layout === 'split' || leg === run.repos[0]

/** The leg whose session works in a leg's folder: itself in the split layout, the lead in the shared one (workerOf, pr.go). */
export const legWorker = (run: Run, leg: RunRepo): RunRepo => (run.layout === 'split' ? leg : run.repos[0])

/** Whether a slot's session is running: with one leg by the ticket key or a worktree, as always; with several by its folder alone, since the key names every leg's session (findSession, slots.go). */
export const legSessionLive = (run: Run, leg: RunRepo, sessions: readonly { name: string; cwd: string }[]): boolean => {
  if (run.repos.length > 1) return sessions.some(x => x.cwd === leg.worktree)
  return sessions.some(x => x.name === run.key || run.repos.some(l => x.cwd === l.worktree))
}

/** A slot's session when Conn hosts it, found as runTerminalSession finds a run's (with several legs by the slot's folder alone): it has a terminal panel on the run page. */
export const legHosted = (run: Run, leg: RunRepo, sessions: readonly SessionSummary[]): SessionSummary | null =>
  runTerminalSession(sessions, { key: run.repos.length > 1 ? null : run.key, worktree: leg.worktree, sessionId: leg.session?.sessionId ?? null })

/** Whether RESUME SESSION applies to a leg: it has a session slot whose session is gone, while its PR is open or the run builds with a captured id. While the first session is still opening it is not in the registry yet, and offering RESUME then would start a second one. */
export const legResumable = (run: Run, leg: RunRepo, live: boolean): boolean => {
  if (!legHasSessionSlot(run, leg)) return false
  if (leg.cleaned) return false
  if (live) return false
  if (run.state === 'pr_open') return true
  return run.state === 'building' && !!leg.claudeSessionId
}

/** Whether a leg has commits and no PR while the run counts as merged without it. */
export const legAfterMerge = (run: Run, leg: RunRepo): boolean => {
  if (leg.pr !== null) return false
  if (leg.ahead === 0) return false
  return runMerged(run)
}

/** What a leg's PR would miss, said before it opens: the session working there is not done, or files are not committed. */
export const legPrWarning = (run: Run, leg: RunRepo): string => {
  const s = legWorker(run, leg).session
  if (!s) return ''
  const w: string[] = []
  if (s.status !== 'idle') w.push(s.status === 'waiting' ? 'The session is waiting on a prompt.' : 'The session is still working.')
  if (leg.dirty > 0) w.push(`${leg.dirty} changed file${leg.dirty === 1 ? ' is' : 's are'} not committed and will not be in the PR.`)
  return w.length ? ` ${w.join(' ')}` : ''
}

/** The PRs opened, of the legs that have or need one (a PR or commits), and how many merged. */
export const prProgress = (run: Run): { opened: number; wanted: number; merged: number } => ({
  opened: run.repos.filter(leg => leg.pr !== null).length,
  wanted: run.repos.filter(leg => leg.pr !== null || leg.ahead > 0).length,
  merged: run.repos.filter(leg => leg.pr?.status?.state === 'merged').length,
})

/** How much an open PR needs the person, most first: red, running, no checks, green, approved. */
const need = (s: PrStatus): number => {
  if (s.checks.state === 'failing') return 0
  if (s.checks.state === 'pending') return 1
  if (s.checks.state === 'none') return 2
  return s.review === 'APPROVED' ? 4 : 3
}

/** The one PR status a list shows for a run: one leg's own; with several, a merged one once the run counts as merged, else the open one that most needs the person; null while one is not polled yet. */
export const runPrStatus = (run: Run): PrStatus | null => {
  if (run.repos.length === 1) return run.repos[0].pr?.status ?? null
  const prs = run.repos.flatMap(leg => (leg.pr ? [leg.pr] : []))
  if (runMerged(run)) return prs.find(pr => pr.status?.state === 'merged')?.status ?? null
  const open = prs.flatMap(pr => (pr.status && !settled(pr.status) ? [pr.status] : []))
  if (open.length === 0) return null
  return open.reduce((worst, s) => (need(s) < need(worst) ? s : worst))
}

/** The commits and changed files of the run: one leg's, as always, or every leg's summed with how many there are. */
export const workSummary = (run: Run): string => {
  const ahead = run.repos.reduce((n, leg) => n + leg.ahead, 0)
  const dirty = run.repos.reduce((n, leg) => n + leg.dirty, 0)
  const text = `${ahead} commits · ${dirty} changed files`
  return run.repos.length > 1 ? `${text} in ${run.repos.length} repositories` : text
}

/** The body of a leg action: the leg's repository, or nothing for every leg it applies to (bodyRepo, slots.go). */
export const repoBody = (repo?: string): { repo?: string } => (repo ? { repo } : {})
