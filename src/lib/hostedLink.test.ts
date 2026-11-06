// A hosted row with no Claude session behind it says so.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { NOT_LINKED, notLinked } from './hostedLink.ts'

test('only a running hosted row with no Claude session is not linked', () => {
  assert.equal(notLinked({ hosted: true, sessionId: '' }), true)
  assert.equal(notLinked({ hosted: true, sessionId: 'd1b6bcb2-1689-4642-a6c7-a800ee0b664a' }), false)
  assert.equal(notLinked({ hosted: true, sessionId: '', exitCode: 143 }), false)
  assert.equal(notLinked({ sessionId: '' }), false)
})

test('the Sessions card says it', () => {
  const page = readFileSync(new URL('../pages/Sessions.svelte', import.meta.url), 'utf8')
  assert.match(page, /\{#if notLinked\(s\)\}[\s\S]*?\{NOT_LINKED\}/)
  assert.ok(NOT_LINKED.length > 0)
})
