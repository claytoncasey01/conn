import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseEntryAction } from './entryAction.ts'

test('a plain success is ok', () => {
  assert.deepEqual(parseEntryAction(200, { ok: true }), { ok: true })
})

test('a dry run carries the command it would have run', () => {
  assert.deepEqual(parseEntryAction(200, { ok: true, dry: true, command: 'claude --resume x' }), { ok: true, dry: true, command: 'claude --resume x' })
})

test('HTTP 200 with ok:false is a failure, with the error and the command to paste', () => {
  assert.deepEqual(parseEntryAction(200, { ok: false, error: 'no cmux', manualCommand: "cd '/a' && claude --resume x" }), {
    ok: false,
    error: 'no cmux',
    manualCommand: "cd '/a' && claude --resume x",
  })
})

test('ok:false without an error still says something', () => {
  const r = parseEntryAction(200, { ok: false })
  assert.equal(r.ok, false)
  assert.ok(r.error)
})

test('an error status shows the server message, else the status', () => {
  assert.deepEqual(parseEntryAction(409, { error: 'the transcript is gone' }), { ok: false, error: 'the transcript is gone' })
  assert.deepEqual(parseEntryAction(500, {}), { ok: false, error: 'HTTP 500' })
  assert.deepEqual(parseEntryAction(403, null), { ok: false, error: 'HTTP 403' })
})

test('a summary reply says whether it was taken', () => {
  assert.deepEqual(parseEntryAction(200, { state: 'queued', accepted: true }), { ok: true, state: 'queued', accepted: true })
  assert.deepEqual(parseEntryAction(200, { state: 'running', accepted: false }), { ok: true, state: 'running', accepted: false })
})

test('fields of the wrong type are ignored, so the page can render them as plain text without checking', () => {
  const r = parseEntryAction(200, { ok: false, error: { x: 1 }, manualCommand: 5, state: 'bogus', accepted: 'yes' })
  assert.equal(r.ok, false)
  assert.equal(typeof r.error, 'string')
  assert.equal(r.manualCommand, undefined)
  assert.equal(r.state, undefined)
  assert.equal(r.accepted, undefined)
})
