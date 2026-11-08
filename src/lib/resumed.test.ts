// The banner a launch shows after it resumed hosted sessions, read from testdata/answers/sessions/resumed.json, which the Go
// tests hold the server's snapshot to.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import answer from '../../testdata/answers/sessions/resumed.json'
import { bannerShown, resumedLine, resumedOf, stopAll } from './resumed.ts'
import type { Snapshot } from './types.ts'

const snap = answer as unknown as Snapshot
const sessions = resumedOf(snap)

test('the banner lists the resumed sessions the server still runs, in order', () => {
  assert.deepEqual(sessions.map(s => s.name), ['PROJ-1', 'fix the widget'])
})

test('no snapshot, or one with nothing resumed, lists none', () => {
  assert.deepEqual(resumedOf(null), [])
  assert.deepEqual(resumedOf({ ...snap, resumed: undefined }), [])
})

test('the banner says how many sessions it resumed and which', () => {
  assert.equal(resumedLine(sessions), 'Resumed 2 sessions that ran when Conn quit: PROJ-1, fix the widget.')
  assert.equal(resumedLine(sessions.slice(0, 1)), 'Resumed 1 session that ran when Conn quit: PROJ-1.')
})

test('Stop all stops each session whatever it is doing, and says each one that would not stop', async () => {
  const asked: [string, boolean][] = []
  const failed = await stopAll(sessions, async (id, force) => {
    asked.push([id, force])
    return id === sessions[1].id ? { ok: false, error: 'could not stop it' } : { ok: true }
  })
  assert.deepEqual(asked, sessions.map(s => [s.id, true]))
  assert.deepEqual(failed, ['fix the widget: could not stop it'])
})

test('a dismissed banner stays down until a session it did not list is resumed', () => {
  assert.equal(bannerShown(sessions, new Set()), true)
  assert.equal(bannerShown(sessions, new Set(sessions.map(s => s.id))), false)
  assert.equal(bannerShown([], new Set()), false)
})
