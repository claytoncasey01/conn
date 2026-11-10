// Open work's promises that no render test checks, pinned as text like the other page source tests: no company or workflow name in the
// pages, nothing from Jira rendered as HTML, the nav item and the pages asking nothing while Atlassian is off, and the repository picker.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8')
const work = read('../pages/OpenWork.svelte')
const run = read('../pages/TicketRun.svelte')
const nav = read('./Nav.svelte')
const history = read('../pages/HistoryPage.svelte')
const routes = read('./routes.ts')

test('no page spells a Jira key or compares a status with a name', () => {
  for (const [name, text] of [['OpenWork', work], ['TicketRun', run]] as const) {
    // a key literal (two or more capitals, a dash, digits) would be a project the code knows
    assert.doesNotMatch(text, /['"`>(\s][A-Z][A-Z0-9]{1,9}-\d+\b/, name)
    // a status is compared by its category, never by a name
    assert.doesNotMatch(text, /\b(?:t|sel|ticket)\.status\s*[!=]==?\s*['"`]/, name)
    assert.doesNotMatch(text, /jiraStatus\s*[!=]==?\s*['"`]/, name)
  }
  // the routes know the shape of a key, never one
  assert.doesNotMatch(routes, /['"`][A-Z][A-Z0-9]{1,9}-\d+['"`]/)
})

test('Jira text (titles, descriptions, statuses) is rendered as text, never as HTML', () => {
  for (const [name, text] of [['OpenWork', work], ['TicketRun', run]] as const) assert.doesNotMatch(text, /@html/, name)
})

test('the nav item is shown only while the server says Open work is on', () => {
  assert.match(nav, /\{#if live\.snap\?\.openWork\}<a class="tabc cap" href=\{href\.work\(\)\}/)
})

test('a direct route says Atlassian is off, with a link to Settings, and asks the server for nothing', () => {
  for (const [name, text] of [['OpenWork', work], ['TicketRun', run]] as const) {
    assert.match(text, /Atlassian is off\. Enable it in Settings\./, name)
    assert.match(text, /href=\{href\.settings\(\)\}/, name)
  }
  // the list is fetched only while it is on (an effect that returns first)
  assert.match(work, /\$effect\(\(\) => \{\s*if \(!enabled\) return\s*fetchWork\(\)/)
})

test('each repository is a checkbox with its base, disabled and explained when a run cannot start there', () => {
  assert.match(work, /\{#each build\.repos as r \(r\.repo\.toLowerCase\(\)\)\}/)
  assert.match(work, /<input type="checkbox" checked=\{on\} disabled=\{!r\.ready\} onchange=\{\(\) => pickRepo\(r\.repo\)\}/)
  assert.match(work, /from origin\/\{r\.base \?\? 'the default branch'\}/)
  assert.match(work, /\{r\.reason \?\? 'There is no clone to build in\.'\}/)
  // with no repository at all, or one that is not ready, the panel links to Settings
  assert.match(work, /\{#if build\.repos\.length === 0 \|\| build\.repos\.some\(r => !r\.ready\)\}\s*<a href=\{href\.settings\(\)\}/)
})

test('with one repository the footer says what it always said', () => {
  assert.match(work, /\{:else\}Moves \{sel\.key\} to an In Progress status, creates the branch and opens a session named \{sel\.key\}\.\{\/if\}/)
})

test('START is off and says "Choose a repository" while none that is ready is chosen', () => {
  assert.match(work, /disabled=\{step === 'working' \|\| chosenReady\.length === 0\}/)
  assert.match(work, /\{:else if chosenReady\.length === 0\}<span>Choose a repository<\/span>/)
  // a remembered repository that stopped being ready is not sent
  assert.match(work, /const chosenReady = \$derived\(chosen\.filter\(r => build\.repos\.some\(b => b\.repo === r && b\.ready\)\)\)/)
})

test('the layout choice is shown only with two or more repositories chosen', () => {
  assert.match(work, /\{#if chosenReady\.length > 1\}\s*<div[^>]*>\s*<div[^>]*>Sessions<\/div>/)
  assert.match(work, /aria-label="Sessions across repositories"/)
})

test('the picker starts on the remembered choice, else the default, and remembers each change', () => {
  assert.match(work, /const remembered = loadChoice\(\)\s*const initial = preselect\(build, remembered\.repos\)\s*layout = remembered\.layout \?\? build\.layout/)
  // nothing preselected leaves the picker open to the next list; a preselection or a pick settles it
  assert.match(work, /if \(initial\.length === 0\) return\s*picked = true\s*chosen = initial/)
  assert.match(work, /const pickRepo = \(repo: string\) => \{\s*picked = true/)
  assert.match(work, /const pickLayout = \(next: RunLayout\) => \{\s*picked = true/)
  assert.equal(work.match(/saveChoice\(\{ repos: chosen, layout \}\)/g)?.length, 2)
  // the browser's storage is touched only in repoChoice.ts, inside try/catch
  assert.doesNotMatch(work, /localStorage/)
})

test('the History field takes a ticket key only while Open work is on', () => {
  assert.match(history, /const tickets = \$derived\(!!live\.snap\?\.openWork\)/)
  assert.match(history, /\{tickets \? 'PR OR TICKET' : 'PR'\}/)
  assert.match(history, /ref = \$derived\(tickets \|\| \/\^#\?\\d\+\$\/\.test\(typed\.ref\.trim\(\)\) \? typed\.ref : ''\)/)
})

test('START sends the model, the repositories, the layout and the advisor picked, and nothing about a base branch: Settings and the clone decide it', () => {
  assert.match(work, /startRun\(t\.key, startBody\(model, force, chosenReady, layout, advisor\)\)/)
  assert.doesNotMatch(work, /BASES|aria-label="Base branch"/)
})
