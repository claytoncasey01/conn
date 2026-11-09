// New session's markup, held as text like the other source tests: where the control sits, how it reaches the person
// (44px targets, a labelled form, the error in an alert) and the Sessions page's empty state by where sessions open.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const form = readFileSync(new URL('./NewSession.svelte', import.meta.url), 'utf8')
const page = readFileSync(new URL('../pages/Sessions.svelte', import.meta.url), 'utf8')

test('the Sessions page puts New session beside its filter bar and reads where sessions open from it', () => {
  assert.match(page, /<FilterBar[\s\S]*?\/>\s*<NewSession bind:where \/>/)
})

test('the empty page points at New session while sessions open inside Conn, and at claude in a terminal otherwise', () => {
  assert.match(page, /\{#if emptyHint\(where\) === 'new-session'\}[\s\S]*?New session<\/span> above[\s\S]*?\{:else\}[\s\S]*?claude<\/span> in a terminal/)
})

test('the form is labelled, its fields are labelled, and its error is an alert', () => {
  assert.match(form, /<form\s+aria-label="New session"/)
  assert.match(form, /<label[^>]*>\s*<span[^>]*>Repository<\/span>\s*<select/)
  assert.match(form, /<label[^>]*>\s*<span[^>]*>Name \(optional\)<\/span>\s*<input/)
  assert.match(form, /<p role="alert"[^>]*>\{error\}<\/p>/)
})

test('every control is at least 44px tall', () => {
  assert.match(form, /const btn =\s*'[^']*min-height: 44px/)
  assert.match(form, /const field =\s*'[^']*min-height: 44px/)
  for (const control of form.match(/<(button|select|input)\b[^>]*>/g) ?? []) assert.match(control, /style=(\{(plain|field)\}|"\{btn\})/, control)
})

test('focus goes to the repository on open, to Cancel when there is none, and back to New session on close', () => {
  assert.match(form, /if \(choices\?\.repos\.length\) picker\?\.focus\(\)\s*else cancel\?\.focus\(\)/)
  assert.match(form, /async function close\(\)[\s\S]*?opener\?\.focus\(\)/)
})

test('the repositories come from the server, never typed, and none says to set a clone in Settings', () => {
  assert.match(form, /\{#each choices\.repos as choice \(choice\)\}/)
  assert.match(form, /No allowed repository has a local clone yet: set a clone folder in <a href=\{href\.settings\(\)\}/)
})

test('a session started inside Conn opens its terminal', () => {
  assert.match(form, /if \(result\.id\) showTerminal\(result\.id\)/)
})
