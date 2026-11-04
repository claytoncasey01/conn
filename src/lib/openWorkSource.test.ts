// Open work's promises that no render test checks, pinned as text like the other page source tests: no company or workflow name in the
// pages, nothing from Jira rendered as HTML, the nav item and the pages asking nothing while Atlassian is off, and START off without a clone.
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

test('START is disabled, and says why, while there is no clone of the build repository', () => {
  assert.match(work, /disabled=\{step === 'working' \|\| !build\.ready\}/)
  assert.match(work, /\{#if !build\.ready\}/)
  assert.match(work, /build\.reason \?\? /)
})

test('the History field takes a ticket key only while Open work is on', () => {
  assert.match(history, /const tickets = \$derived\(!!live\.snap\?\.openWork\)/)
  assert.match(history, /\{tickets \? 'PR OR TICKET' : 'PR'\}/)
  assert.match(history, /ref = \$derived\(tickets \|\| \/\^#\?\\d\+\$\/\.test\(typed\.ref\.trim\(\)\) \? typed\.ref : ''\)/)
})

test('START sends the model and nothing about a base branch: Settings and the clone decide it', () => {
  assert.match(work, /startRun\(t\.key, \{ model, force: force \|\| undefined \}\)/)
  assert.doesNotMatch(work, /BASES|aria-label="Base branch"/)
  assert.match(work, /origin\/\{build\.base \?\? 'the default branch'\}/)
})
