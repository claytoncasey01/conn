// New session's answers, read from testdata/answers/sessions/new.json, which the Go tests hold the server's answers to, so
// the page and the server agree on every case.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import answers from '../../testdata/answers/sessions/new.json'
import { emptyHint, parseChoices, parseStarted, startBody } from './newSession.ts'

type Answer = { status: number; body: unknown }
const cases = answers as Record<string, Answer>
const started = (name: string) => parseStarted(cases[name].status, cases[name].body)

test('the choices are the repositories the server offers, where sessions open, and whether the run is dry', () => {
  assert.deepEqual(parseChoices(cases.choices.status, cases.choices.body), { repos: ['acme/widgets'], where: 'inside', dry: false })
})

test('choices the server refused are its error', () => {
  assert.deepEqual(parseChoices(503, { error: 'off' }), { error: 'off' })
  assert.deepEqual(parseChoices(500, null), { error: 'HTTP 500' })
})

test('choices keep only text repositories', () => {
  assert.deepEqual(parseChoices(200, { repos: ['acme/widgets', 7, null], where: 'inside' }), { repos: ['acme/widgets'], where: 'inside', dry: false })
})

test('a session started inside Conn names its row', () => {
  assert.deepEqual(started('inside'), { ok: true, id: '0123456789abcdef0123456789abcdef' })
})

test('a session opened in a terminal has no row id to open', () => {
  assert.deepEqual(started('terminal'), { ok: true })
})

test('sessions set to open as a command give the line to paste', () => {
  const result = started('command')
  assert.equal(result.ok, false)
  assert.equal(result.manualCommand, 'cd /work/widgets && claude --session-id 0b6c4f7e-2d1a-4c3b-9e8f-112233445566 --name fix-the-widget')
})

test('a dry run says what it would have run', () => {
  const result = started('dry')
  assert.equal(result.dry, true)
  assert.equal(result.command, 'cd /work/widgets && claude --session-id 0b6c4f7e-2d1a-4c3b-9e8f-112233445566 --name fix-the-widget')
})

test('a refusal is the server error, whatever the status', () => {
  assert.deepEqual(started('noClone'), { ok: false, error: 'No local clone of acme/gadgets: set a folder in Settings.' })
  assert.deepEqual(started('badName'), { ok: false, error: 'cannot start the session: the name must hold a letter or a digit (A-Z, a-z, 0-9)' })
  assert.match(started('full').error ?? '', /already runs 12 sessions/)
})

test('an answer that is not JSON is the status', () => {
  assert.deepEqual(parseStarted(502, null), { ok: false, error: 'HTTP 502' })
})

test('a start sends the name only when one was typed, trimmed', () => {
  assert.deepEqual(startBody('acme/widgets', ''), { repo: 'acme/widgets' })
  assert.deepEqual(startBody('acme/widgets', '   '), { repo: 'acme/widgets' })
  assert.deepEqual(startBody('acme/widgets', ' fix it '), { repo: 'acme/widgets', name: 'fix it' })
})

test('the empty page points at New session only while sessions open inside Conn', () => {
  assert.equal(emptyHint('inside'), 'new-session')
  assert.equal(emptyHint('terminal'), 'terminal')
  assert.equal(emptyHint('command'), 'terminal')
  assert.equal(emptyHint(null), 'terminal')
})
