// The run page's promises about its legs, pinned as text like the other page source tests: a run of one leg posts what it
// always posted, and with several each row's buttons name their repository while OPEN ALL PRS names none.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { repoBody } from './runLegs.ts'

const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8')
const run = read('../pages/TicketRun.svelte')
const live = read('./live.svelte.ts')

test('a leg action sends its repository, and no repository means every leg', () => {
  assert.deepEqual(repoBody(undefined), {})
  assert.deepEqual(repoBody('acme/widgets'), { repo: 'acme/widgets' })
  for (const name of ['openPr', 'fixChecks', 'cleanupRun', 'resumeRun']) {
    assert.match(live, new RegExp(`export const ${name} = \\(key: string, repo\\?: string\\) => post\\(\`/api/runs/\\$\\{key\\}/[a-z-]+\`, repoBody\\(repo\\)\\)`), name)
  }
})

test('with one leg the buttons post what they always posted', () => {
  assert.match(run, /confirmLabel="OPEN PR" tone="green" run=\{\(\) => openPr\(key\)\} \/>/)
  assert.match(run, /confirmLabel="SEND" tone="cyan" run=\{\(\) => fixChecks\(key\)\} \/>/)
  assert.match(run, /confirmLabel="REMOVE" tone="red" run=\{\(\) => cleanupRun\(key\)\} \/>/)
  assert.match(run, /confirmLabel="RESUME" tone="cyan" run=\{\(\) => resumeRun\(key\)\} \/>/)
  assert.match(run, /Pushes \{lead\.branch\} and opens a PR into \{lead\.prBase\}\. Jira moves to its review status, if it has one\.\{prWarning\}/)
})

test('with several legs each row is one leg, and its buttons name its repository', () => {
  assert.match(run, /\{#if single\}/)
  assert.match(run, /<section aria-label="Repositories"/)
  assert.match(run, /\{#each run\.repos as leg, i \(leg\.repo\.toLowerCase\(\)\)\}/)
  assert.match(run, /run=\{\(\) => openPr\(key, leg\.repo\)\}/)
  assert.match(run, /run=\{\(\) => fixChecks\(key, leg\.repo\)\}/)
  assert.match(run, /run=\{\(\) => cleanupRun\(key, leg\.repo\)\}/)
  assert.match(run, /run=\{\(\) => resumeRun\(key, leg\.repo\)\}/)
})

test('each row\'s buttons appear only where the server would take them', () => {
  assert.match(run, /\{#if legNeedsPr\(run, leg\)\}\s*<ConfirmAction label="OPEN PR"/)
  assert.match(run, /\{#if legChecksFailing\(run, leg\)\}/)
  assert.match(run, /\{#if legResumable\(run, leg, legSessionLive\(run, leg, sessions\)\)\}/)
  assert.match(run, /\{#if legCanClean\(run, leg\)\}\s*<ConfirmAction label="REMOVE WORKTREE"/)
  assert.match(run, /\{#if legAfterMerge\(run, leg\)\}/)
})

test('OPEN ALL PRS appears when more than one leg needs a PR, and names no repository', () => {
  assert.match(run, /\{#if canOpenAll\(run\)\}\s*(?:<[^>]*>\s*)*<ConfirmAction label="OPEN ALL PRS"[^/]*run=\{\(\) => openPr\(key\)\} \/>/)
})

test('the ticket stays at the top: CANCEL RUN and RETRY for every run, MOVE JIRA TO there once several legs merged', () => {
  assert.match(run, /<ConfirmAction label="CANCEL RUN"/)
  assert.equal(run.match(/id="jira-target"/g)?.length, 1, 'one Jira field, in whichever branch shows')
  assert.match(run, /\{#if !single && mergedAll\}\s*<div[^>]*>\s*\{@render moveTicket\(run\)\}/)
})

test('Open work reads every leg: the list\'s PR state, the first PR linked and how many there are', () => {
  const work = read('../pages/OpenWork.svelte')
  assert.match(work, /const p = runPrStatus\(r\)/)
  assert.match(work, /\{#if selPr\}<a href=\{selPr\.url\}/)
  assert.match(work, /\{#if selPrs > 1\}<span>\{selPrs\} PRs<\/span>\{\/if\}/)
  assert.doesNotMatch(work, /repos\[0\]/)
})
