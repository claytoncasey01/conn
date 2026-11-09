// New session's markup, held as text like the other source tests: where the control sits, how it reaches the person
// (44px targets, a labelled form, the error in an alert) and the Sessions page's empty state by where sessions open.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const form = readFileSync(new URL('./NewSession.svelte', import.meta.url), 'utf8')
const page = readFileSync(new URL('../pages/Sessions.svelte', import.meta.url), 'utf8')

test('the Sessions page puts New session beside its filter bar, and takes where sessions open from the snapshot', () => {
  assert.match(page, /<FilterBar[\s\S]*?\/>\s*<NewSession \/>/)
  assert.match(page, /const where = \$derived\(live\.snap\?\.where \?\? null\)/)
})

test('the repositories are read only when the form opens, never on the page\'s mount', () => {
  assert.doesNotMatch(form, /onMount/)
  assert.match(form, /async function open\(\)[\s\S]*?await load\(\)/)
})

test('the empty page points at New session while sessions open inside Conn, and at claude in a terminal otherwise', () => {
  assert.match(page, /\{#if emptyHint\(where\) === 'new-session'\}[\s\S]*?New session<\/span> above[\s\S]*?\{:else\}[\s\S]*?claude<\/span> in a terminal/)
})

test('the form is labelled, its fields are labelled, and its error is an alert', () => {
  assert.match(form, /<form\s[^>]*aria-label="New session"/)
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
  assert.match(form, /No allowed repository has a local clone Conn can use yet: set a clone folder in <a href=\{href\.settings\(\)\}[^<]*<\/a>\. A clone must be a real folder of yours, not a link\./)
})

test('a session started inside Conn opens its terminal', () => {
  assert.match(form, /if \(result\.id\) showTerminal\(result\.id\)/)
})

test('Escape closes the form from anywhere in it, Start and Cancel too, and nowhere else on the page', () => {
  assert.match(form, /<svelte:window onkeydown=\{escape\} \/>/)
  assert.match(form, /function escape\(event: KeyboardEvent\): void \{\s*if \(event\.key !== 'Escape'\) return\s*if \(!\(event\.target instanceof Node\) \|\| !form\?\.contains\(event\.target\)\) return\s*void close\(\)/)
  assert.match(form, /<form\s+bind:this=\{form\}/)
})

test('a start that did not open gives the focus back to what was pressed, or to Start', () => {
  assert.match(form, /const returnTo = document\.activeElement instanceof HTMLElement \? document\.activeElement : null\s*step = 'starting'/)
  assert.match(form, /step = 'form'[\s\S]*?await tick\(\)\s*;\(returnTo && form\?\.contains\(returnTo\) \? returnTo : starter\)\?\.focus\(\)/)
  assert.match(form, /<button bind:this=\{starter\} type="submit"/)
})
