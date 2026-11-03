import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { errorText, listState } from './myPrsList.ts'
import type { MyPrRow } from './types.ts'

const row = { repo: 'acme/widgets', number: 1 } as MyPrRow

test('before the first poll has answered the list is loading, not empty', () => {
  assert.equal(listState({ loaded: false, error: null, rows: [] }), 'loading')
})

test('a first poll that failed shows the error, an answered empty list is empty, and rows are rows', () => {
  assert.equal(listState({ loaded: false, error: 'gh missing', rows: [] }), 'error')
  assert.equal(listState({ loaded: true, error: null, rows: [] }), 'empty')
  assert.equal(listState({ loaded: true, error: null, rows: [row] }), 'rows')
  // a later failure keeps the last rows
  assert.equal(listState({ loaded: true, error: 'boom', rows: [row] }), 'rows')
})

test('a list that is off says why before anything else: no allowlist, or GitHub off', () => {
  assert.equal(listState({ loaded: false, error: null, rows: [], off: 'setup' }), 'setup')
  assert.equal(listState({ loaded: false, error: null, rows: [], off: 'github' }), 'github-off')
  assert.equal(listState({ loaded: true, error: 'x', rows: [row], off: 'github' }), 'github-off')
  assert.equal(listState({ loaded: false, error: null, rows: [], off: null }), 'loading')
})

test('the error banner says the rows are the last good list whenever the server sent rows, whatever a filter shows', () => {
  assert.equal(errorText({ error: null, rows: [row] }), null)
  assert.equal(errorText({ error: 'gh missing', rows: [] }), 'gh missing')
  assert.equal(errorText({ error: 'boom', rows: [row] }), 'boom The list below is from the last time it could be read.')
})

test('the page takes the banner from the whole view, not from the rows its filter left', () => {
  const src = readFileSync(new URL('../pages/MyPrsPage.svelte', import.meta.url), 'utf8')
  assert.match(src, /errorText\(view\)/)
  assert.doesNotMatch(src, /rows\.length \? ' The list below/)
})
