import assert from 'node:assert/strict'
import { test } from 'node:test'
import { needsConsent, readAccepted, remember } from './consent.ts'
import { KEYS } from './storage.ts'
import type { ThemeEntry, ThemeSource } from './types.ts'

const fake = (init: Record<string, string> = {}) => {
  const m = new Map(Object.entries(init))
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m }
}
const entry = (source: ThemeSource, hasCss: boolean, sha: string | null = 'aaa'): ThemeEntry => ({
  id: 'mine', source, hasCss, cssSha256: hasCss ? sha : null, invalid: 'x',
})

test('a user theme with CSS needs consent when the stored hash is absent or differs', () => {
  assert.equal(needsConsent(entry('user', true), {}), true)
  assert.equal(needsConsent(entry('user', true), { mine: 'bbb' }), true)
  assert.equal(needsConsent(entry('user', true), { other: 'aaa' }), true)
})

test('it does not when the hash matches', () => {
  assert.equal(needsConsent(entry('user', true), { mine: 'aaa' }), false)
})

test('never for a theme without CSS, and never for a built-in with CSS', () => {
  assert.equal(needsConsent(entry('user', false), {}), false)
  assert.equal(needsConsent(entry('builtin', true), {}), false)
  assert.equal(needsConsent(entry('gallery', false), {}), false)
})

test('a prototype name is not a stored hash', () => {
  const e = { ...entry('user', true), id: 'constructor' }
  assert.equal(needsConsent(e, {}), true)
})

test('readAccepted: missing, throwing, corrupt and wrong-shaped storage give {}; non-string values are dropped', () => {
  assert.deepEqual(readAccepted(fake()), {})
  assert.deepEqual(readAccepted(null), {})
  assert.deepEqual(readAccepted({ getItem: () => { throw new Error('no') } }), {})
  for (const bad of ['{x', 'null', '[]', '7']) assert.deepEqual(readAccepted(fake({ [KEYS.cssOk]: bad })), {}, bad)
  assert.deepEqual(readAccepted(fake({ [KEYS.cssOk]: '{"a":"s1","b":4,"c":null}' })), { a: 's1' })
})

test('remember stores the hash per theme and keeps the others; a throwing store does not throw', () => {
  const s = fake()
  remember(s, { ...entry('user', true, 'h1'), id: 'a' })
  remember(s, { ...entry('user', true, 'h2'), id: 'b' })
  remember(s, { ...entry('user', true, 'h3'), id: 'a' })
  assert.deepEqual(readAccepted(s), { a: 'h3', b: 'h2' })
  remember({ getItem: () => { throw new Error('x') }, setItem: () => { throw new Error('y') } }, entry('user', true))
  remember(s, entry('user', false))
  assert.deepEqual(readAccepted(s), { a: 'h3', b: 'h2' })
})
