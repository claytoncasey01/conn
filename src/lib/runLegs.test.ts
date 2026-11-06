import assert from 'node:assert/strict'
import { test } from 'node:test'
import legsAnswers from '../../testdata/answers/openwork/legs.json'
import {
  canOpenAll,
  legAfterMerge,
  legCanClean,
  legChecksFailing,
  legHasSessionSlot,
  legKeptForOthers,
  legNeedsPr,
  legPrWarning,
  legResumable,
  legSessionLive,
  legsWithCommits,
  legsToOpen,
  legsWithoutPr,
  legWorker,
  prOpenable,
  prProgress,
  runMerged,
  runPrStatus,
  workSummary,
} from './runLegs.ts'
import type { PrStatus, Run, RunLayout, RunRepo, RunState } from './types.ts'

const status = (state: PrStatus['state'], checks: PrStatus['checks']['state'] = 'passing', review = ''): PrStatus => ({
  state,
  checks: { state: checks, total: 2, pending: checks === 'pending' ? 1 : 0, failed: checks === 'failing' ? ['lint'] : [] },
  review,
  mergeable: '',
  at: 1,
})
const pr = (number: number, s: PrStatus | null = null) => ({ url: `https://github.com/acme/x/pull/${number}`, number, ...(s ? { status: s } : {}) })

const leg = (repo: string, o: Partial<RunRepo> = {}): RunRepo => ({
  repo,
  base: 'main',
  prBase: 'main',
  branch: 'abc-1-export',
  worktree: `/w/${repo}`,
  session: null,
  claudeSessionId: null,
  manualCommand: null,
  ahead: 0,
  dirty: 0,
  pr: null,
  cleaned: null,
  ...o,
})

const runOf = (state: RunState, repos: RunRepo[], layout: RunLayout = 'shared'): Run => ({
  key: 'ABC-1',
  title: 't',
  model: 'haiku',
  state,
  stage: 'build',
  startedAt: 0,
  updatedAt: 0,
  jiraStatus: 'In Progress',
  agents: [],
  answered: [],
  questions: [],
  log: [],
  error: null,
  repos,
  layout,
})

test('legsWithCommits are the legs with commits ahead of their base, in order', () => {
  const rows: { name: string; repos: RunRepo[]; want: string[] }[] = [
    { name: 'none', repos: [leg('a'), leg('b')], want: [] },
    { name: 'one of two', repos: [leg('a'), leg('b', { ahead: 2 })], want: ['b'] },
    { name: 'both', repos: [leg('a', { ahead: 1 }), leg('b', { ahead: 2 })], want: ['a', 'b'] },
  ]
  for (const row of rows) assert.deepEqual(legsWithCommits(runOf('building', row.repos)).map(l => l.repo), row.want, row.name)
})

test('legsWithoutPr are the legs that have no PR yet, in order', () => {
  const rows: { name: string; repos: RunRepo[]; want: string[] }[] = [
    { name: 'none opened', repos: [leg('a'), leg('b')], want: ['a', 'b'] },
    { name: 'one opened', repos: [leg('a', { pr: pr(1) }), leg('b')], want: ['b'] },
    { name: 'all opened', repos: [leg('a', { pr: pr(1) }), leg('b', { pr: pr(2) })], want: [] },
  ]
  for (const row of rows) assert.deepEqual(legsWithoutPr(runOf('pr_open', row.repos)).map(l => l.repo), row.want, row.name)
})

test('legNeedsPr: the run is building, or its PR is open while a leg has none, and the leg has commits and no PR', () => {
  const rows: { name: string; state: RunState; repos: RunRepo[]; want: boolean }[] = [
    { name: 'building, commits', state: 'building', repos: [leg('a', { ahead: 1 }), leg('b')], want: true },
    { name: 'building, no commits', state: 'building', repos: [leg('a'), leg('b', { ahead: 1 })], want: false },
    { name: 'researching', state: 'researching', repos: [leg('a', { ahead: 1 }), leg('b')], want: false },
    { name: 'pr open, this leg has none', state: 'pr_open', repos: [leg('a', { ahead: 1 }), leg('b', { pr: pr(2) })], want: true },
    { name: 'pr open, this leg has one', state: 'pr_open', repos: [leg('a', { ahead: 1, pr: pr(1) }), leg('b')], want: false },
    { name: 'failed', state: 'failed', repos: [leg('a', { ahead: 1 }), leg('b')], want: false },
  ]
  for (const row of rows) {
    const run = runOf(row.state, row.repos)
    assert.equal(legNeedsPr(run, run.repos[0]), row.want, row.name)
  }
})

test('canOpenAll: more than one leg needs a PR', () => {
  const rows: { name: string; state: RunState; repos: RunRepo[]; want: boolean }[] = [
    { name: 'one leg with commits', state: 'building', repos: [leg('a', { ahead: 1 })], want: false },
    { name: 'two legs, one with commits', state: 'building', repos: [leg('a', { ahead: 1 }), leg('b')], want: false },
    { name: 'two legs with commits', state: 'building', repos: [leg('a', { ahead: 1 }), leg('b', { ahead: 3 })], want: true },
    { name: 'two with commits, one opened', state: 'pr_open', repos: [leg('a', { ahead: 1, pr: pr(1) }), leg('b', { ahead: 3 })], want: false },
    { name: 'three, two still to open', state: 'pr_open', repos: [leg('a', { ahead: 1, pr: pr(1) }), leg('b', { ahead: 3 }), leg('c', { ahead: 1 })], want: true },
    { name: 'not building', state: 'asking', repos: [leg('a', { ahead: 1 }), leg('b', { ahead: 3 })], want: false },
  ]
  for (const row of rows) assert.equal(canOpenAll(runOf(row.state, row.repos)), row.want, row.name)
})

test('legsToOpen are the legs OPEN ALL PRS opens: commits and no PR, while PRs may open', () => {
  const run = runOf('pr_open', [leg('a', { ahead: 1, pr: pr(1) }), leg('b', { ahead: 3 }), leg('c'), leg('d', { ahead: 1 })])
  assert.deepEqual(legsToOpen(run).map(l => l.repo), ['b', 'd'])
  assert.deepEqual(legsToOpen(runOf('cancelled', run.repos)), [])
})

test('runMerged: every PR merged or closed, at least one merged, a leg with no PR not counted', () => {
  const rows: { name: string; state: RunState; repos: RunRepo[]; want: boolean }[] = [
    { name: 'one merged', state: 'pr_open', repos: [leg('a', { pr: pr(1, status('merged')) })], want: true },
    { name: 'merged and closed', state: 'pr_open', repos: [leg('a', { pr: pr(1, status('merged')) }), leg('b', { pr: pr(2, status('closed')) })], want: true },
    { name: 'merged and a leg with no PR', state: 'pr_open', repos: [leg('a', { pr: pr(1, status('merged')) }), leg('b', { ahead: 2 })], want: true },
    { name: 'merged and open', state: 'pr_open', repos: [leg('a', { pr: pr(1, status('merged')) }), leg('b', { pr: pr(2, status('open')) })], want: false },
    { name: 'merged and not yet polled', state: 'pr_open', repos: [leg('a', { pr: pr(1, status('merged')) }), leg('b', { pr: pr(2) })], want: false },
    { name: 'only closed', state: 'pr_open', repos: [leg('a', { pr: pr(1, status('closed')) })], want: false },
    { name: 'no PR at all', state: 'pr_open', repos: [leg('a')], want: false },
    { name: 'not pr open', state: 'building', repos: [leg('a', { pr: pr(1, status('merged')) })], want: false },
  ]
  for (const row of rows) assert.equal(runMerged(runOf(row.state, row.repos)), row.want, row.name)
})

test('legChecksFailing: the PR is open, polled and red', () => {
  const rows: { name: string; state: RunState; l: RunRepo; want: boolean }[] = [
    { name: 'red', state: 'pr_open', l: leg('a', { pr: pr(1, status('open', 'failing')) }), want: true },
    { name: 'green', state: 'pr_open', l: leg('a', { pr: pr(1, status('open', 'passing')) }), want: false },
    { name: 'not polled', state: 'pr_open', l: leg('a', { pr: pr(1) }), want: false },
    { name: 'no PR', state: 'pr_open', l: leg('a'), want: false },
    { name: 'merged while red', state: 'pr_open', l: leg('a', { pr: pr(1, status('merged', 'failing')) }), want: false },
    { name: 'run not pr open', state: 'building', l: leg('a', { pr: pr(1, status('open', 'failing')) }), want: false },
  ]
  for (const row of rows) {
    const run = runOf(row.state, [row.l, leg('b')])
    assert.equal(legChecksFailing(run, run.repos[0]), row.want, row.name)
  }
})

test('legCanClean: the PR merged or closed and the worktree is still there; the shared lead waits for the other PRs', () => {
  const merged = pr(1, status('merged'))
  const rows: { name: string; layout: RunLayout; repos: RunRepo[]; at: number; want: boolean; kept: boolean }[] = [
    { name: 'merged', layout: 'shared', repos: [leg('a', { pr: merged })], at: 0, want: true, kept: false },
    { name: 'closed', layout: 'shared', repos: [leg('a', { pr: pr(1, status('closed')) })], at: 0, want: true, kept: false },
    { name: 'open', layout: 'shared', repos: [leg('a', { pr: pr(1, status('open')) })], at: 0, want: false, kept: false },
    { name: 'no PR', layout: 'shared', repos: [leg('a')], at: 0, want: false, kept: false },
    { name: 'cleaned', layout: 'shared', repos: [leg('a', { pr: merged, cleaned: true })], at: 0, want: false, kept: false },
    { name: 'shared lead, other open', layout: 'shared', repos: [leg('a', { pr: merged }), leg('b', { pr: pr(2, status('open')) })], at: 0, want: false, kept: true },
    { name: 'shared lead, other not polled', layout: 'shared', repos: [leg('a', { pr: merged }), leg('b', { pr: pr(2) })], at: 0, want: false, kept: true },
    { name: 'shared lead, other merged', layout: 'shared', repos: [leg('a', { pr: merged }), leg('b', { pr: pr(2, status('merged')) })], at: 0, want: true, kept: false },
    { name: 'shared lead, other has no PR', layout: 'shared', repos: [leg('a', { pr: merged }), leg('b', { ahead: 2 })], at: 0, want: true, kept: false },
    { name: 'shared lead, other open but cleaned', layout: 'shared', repos: [leg('a', { pr: merged }), leg('b', { pr: pr(2, status('open')), cleaned: true })], at: 0, want: true, kept: false },
    { name: 'shared other leg, lead open', layout: 'shared', repos: [leg('a', { pr: pr(1, status('open')) }), leg('b', { pr: merged })], at: 1, want: true, kept: false },
    { name: 'split lead, other open', layout: 'split', repos: [leg('a', { pr: merged }), leg('b', { pr: pr(2, status('open')) })], at: 0, want: true, kept: false },
  ]
  for (const row of rows) {
    const run = runOf('pr_open', row.repos, row.layout)
    assert.equal(legCanClean(run, run.repos[row.at]), row.want, row.name)
    assert.equal(legKeptForOthers(run, run.repos[row.at]), row.kept, `${row.name}: kept`)
  }
  assert.equal(legCanClean(runOf('building', [leg('a', { pr: merged })]), leg('a', { pr: merged })), false, 'not pr open')
})

test('legHasSessionSlot and legWorker: the lead alone in the shared layout, every leg in the split one', () => {
  const rows: { name: string; layout: RunLayout; at: number; slot: boolean; worker: string }[] = [
    { name: 'shared lead', layout: 'shared', at: 0, slot: true, worker: 'a' },
    { name: 'shared other', layout: 'shared', at: 1, slot: false, worker: 'a' },
    { name: 'split lead', layout: 'split', at: 0, slot: true, worker: 'a' },
    { name: 'split other', layout: 'split', at: 1, slot: true, worker: 'b' },
  ]
  for (const row of rows) {
    const run = runOf('building', [leg('a'), leg('b')], row.layout)
    assert.equal(legHasSessionSlot(run, run.repos[row.at]), row.slot, row.name)
    assert.equal(legWorker(run, run.repos[row.at]).repo, row.worker, `${row.name}: worker`)
  }
})

test('legSessionLive: one leg by name or folder as always, several by the folder alone', () => {
  const rows: { name: string; repos: RunRepo[]; sessions: { name: string; cwd: string }[]; want: boolean }[] = [
    { name: 'one leg, by name', repos: [leg('a')], sessions: [{ name: 'ABC-1', cwd: '/elsewhere' }], want: true },
    { name: 'one leg, by folder', repos: [leg('a')], sessions: [{ name: 'x', cwd: '/w/a' }], want: true },
    { name: 'one leg, none', repos: [leg('a')], sessions: [{ name: 'x', cwd: '/elsewhere' }], want: false },
    { name: 'two legs, name is not enough', repos: [leg('a'), leg('b')], sessions: [{ name: 'ABC-1', cwd: '/w/b' }], want: false },
    { name: 'two legs, its folder', repos: [leg('a'), leg('b')], sessions: [{ name: 'ABC-1', cwd: '/w/a' }], want: true },
  ]
  for (const row of rows) {
    const run = runOf('building', row.repos, 'split')
    assert.equal(legSessionLive(run, run.repos[0], row.sessions), row.want, row.name)
  }
})

test('legResumable: a slot whose session is gone, while the run builds with a captured id or its PR is open', () => {
  const rows: { name: string; state: RunState; layout: RunLayout; repos: RunRepo[]; at: number; live: boolean; want: boolean }[] = [
    { name: 'one leg, pr open', state: 'pr_open', layout: 'shared', repos: [leg('a')], at: 0, live: false, want: true },
    { name: 'one leg, live', state: 'pr_open', layout: 'shared', repos: [leg('a')], at: 0, live: true, want: false },
    { name: 'one leg, building with id', state: 'building', layout: 'shared', repos: [leg('a', { claudeSessionId: 'u' })], at: 0, live: false, want: true },
    { name: 'one leg, building no id', state: 'building', layout: 'shared', repos: [leg('a')], at: 0, live: false, want: false },
    { name: 'one leg, cleaned', state: 'pr_open', layout: 'shared', repos: [leg('a', { cleaned: true })], at: 0, live: false, want: false },
    { name: 'one leg, asking', state: 'asking', layout: 'shared', repos: [leg('a', { claudeSessionId: 'u' })], at: 0, live: false, want: false },
    { name: 'shared other leg', state: 'pr_open', layout: 'shared', repos: [leg('a'), leg('b')], at: 1, live: false, want: false },
    { name: 'split other leg', state: 'pr_open', layout: 'split', repos: [leg('a'), leg('b')], at: 1, live: false, want: true },
  ]
  for (const row of rows) {
    const run = runOf(row.state, row.repos, row.layout)
    assert.equal(legResumable(run, run.repos[row.at], row.live), row.want, row.name)
  }
})

test('legAfterMerge: a leg with commits and no PR once the run counts as merged', () => {
  const merged = pr(1, status('merged'))
  const rows: { name: string; repos: RunRepo[]; want: boolean }[] = [
    { name: 'others merged', repos: [leg('a', { pr: merged }), leg('b', { ahead: 2 })], want: true },
    { name: 'others open', repos: [leg('a', { pr: pr(1, status('open')) }), leg('b', { ahead: 2 })], want: false },
    { name: 'no commits', repos: [leg('a', { pr: merged }), leg('b')], want: false },
  ]
  for (const row of rows) {
    const run = runOf('pr_open', row.repos)
    assert.equal(legAfterMerge(run, run.repos[1]), row.want, row.name)
  }
})

test('legPrWarning says what the PR will miss: the working session and the leg\'s uncommitted files', () => {
  const ses = (s: 'idle' | 'busy' | 'waiting') => ({ name: 'ABC-1', sessionId: null, pid: null, status: s })
  const rows: { name: string; layout: RunLayout; repos: RunRepo[]; at: number; want: string }[] = [
    { name: 'no session', layout: 'shared', repos: [leg('a', { dirty: 3 })], at: 0, want: '' },
    { name: 'idle, clean', layout: 'shared', repos: [leg('a', { session: ses('idle') })], at: 0, want: '' },
    { name: 'busy, one file', layout: 'shared', repos: [leg('a', { session: ses('busy'), dirty: 1 })], at: 0, want: ' The session is still working. 1 changed file is not committed and will not be in the PR.' },
    { name: 'waiting, two files', layout: 'shared', repos: [leg('a', { session: ses('waiting'), dirty: 2 })], at: 0, want: ' The session is waiting on a prompt. 2 changed files are not committed and will not be in the PR.' },
    { name: 'shared other leg reads the lead session', layout: 'shared', repos: [leg('a', { session: ses('busy') }), leg('b', { dirty: 2 })], at: 1, want: ' The session is still working. 2 changed files are not committed and will not be in the PR.' },
    { name: 'split other leg reads its own', layout: 'split', repos: [leg('a', { session: ses('busy') }), leg('b', { session: ses('idle'), dirty: 0 })], at: 1, want: '' },
  ]
  for (const row of rows) {
    const run = runOf('building', row.repos, row.layout)
    assert.equal(legPrWarning(run, run.repos[row.at]), row.want, row.name)
  }
})

test('prProgress counts the PRs opened of the legs that have or need one, and those merged', () => {
  const rows: { name: string; repos: RunRepo[]; want: { opened: number; wanted: number; merged: number } }[] = [
    { name: 'none yet', repos: [leg('a', { ahead: 1 }), leg('b', { ahead: 1 })], want: { opened: 0, wanted: 2, merged: 0 } },
    { name: 'one of two', repos: [leg('a', { pr: pr(1, status('open')) }), leg('b', { ahead: 1 })], want: { opened: 1, wanted: 2, merged: 0 } },
    { name: 'a leg with nothing', repos: [leg('a', { pr: pr(1, status('merged')) }), leg('b')], want: { opened: 1, wanted: 1, merged: 1 } },
  ]
  for (const row of rows) assert.deepEqual(prProgress(runOf('pr_open', row.repos)), row.want, row.name)
})

test('runPrStatus: one leg\'s own status, else the merged one, else the one most in need of the person', () => {
  const red = status('open', 'failing')
  const running = status('open', 'pending')
  const green = status('open', 'passing')
  const approved = status('open', 'passing', 'APPROVED')
  const merged = status('merged')
  const rows: { name: string; repos: RunRepo[]; want: PrStatus | null }[] = [
    { name: 'one leg', repos: [leg('a', { pr: pr(1, approved) })], want: approved },
    { name: 'one leg, not polled', repos: [leg('a', { pr: pr(1) })], want: null },
    { name: 'one leg, no PR', repos: [leg('a')], want: null },
    { name: 'red beats green', repos: [leg('a', { pr: pr(1, green) }), leg('b', { pr: pr(2, red) })], want: red },
    { name: 'running beats approved', repos: [leg('a', { pr: pr(1, approved) }), leg('b', { pr: pr(2, running) })], want: running },
    { name: 'green beats approved', repos: [leg('a', { pr: pr(1, approved) }), leg('b', { pr: pr(2, green) })], want: green },
    { name: 'open beats merged', repos: [leg('a', { pr: pr(1, merged) }), leg('b', { pr: pr(2, green) })], want: green },
    { name: 'merged with a leg with no PR', repos: [leg('a', { pr: pr(1, merged) }), leg('b', { ahead: 1 })], want: merged },
    { name: 'merged, other not polled', repos: [leg('a', { pr: pr(1, merged) }), leg('b', { pr: pr(2) })], want: null },
    { name: 'no PRs', repos: [leg('a'), leg('b')], want: null },
  ]
  for (const row of rows) assert.deepEqual(runPrStatus(runOf('pr_open', row.repos)), row.want, row.name)
})

test('workSummary: one leg as always, several summed with their count', () => {
  assert.equal(workSummary(runOf('building', [leg('a', { ahead: 3, dirty: 2 })])), '3 commits · 2 changed files')
  assert.equal(workSummary(runOf('building', [leg('a', { ahead: 3, dirty: 2 }), leg('b', { ahead: 1 })])), '4 commits · 2 changed files in 2 repositories')
})

test('the per-leg rules match the server, case by case (testdata/answers/openwork/legs.json)', () => {
  for (const c of legsAnswers.legs) {
    const given = c.args[0] as unknown as Pick<Run, 'key' | 'state' | 'layout' | 'repos'>
    const run = { ...runOf(given.state, given.repos, given.layout), key: given.key }
    const got = {
      openable: prOpenable(run),
      merged: runMerged(run),
      legs: run.repos.map(l => ({
        checksFailing: legChecksFailing(run, l),
        canClean: legCanClean(run, l),
        keptForOthers: legKeptForOthers(run, l),
      })),
    }
    assert.deepEqual(got, c.out, JSON.stringify(c.args[0]))
  }
})
