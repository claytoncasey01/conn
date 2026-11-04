import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseProvided, parseProvidedResult } from './providedApi.ts'

const good = {
  agents: [{ name: 'search', state: 'installed' }],
  skills: [
    { name: 'deep-code-review', state: 'conflict', note: 'a file or folder that is not Conn’s' },
    { name: 'other', state: 'notInstalled' },
  ],
  linkDir: '/home/jane/.claude',
  copyDir: '/home/jane/.config/conn',
  writable: true,
}

test('parseProvided: the server answer is read as it is', () => {
  const view = parseProvided(good)
  assert.deepEqual(view?.agents, [{ name: 'search', state: 'installed' }])
  assert.equal(view?.skills[0]?.note, 'a file or folder that is not Conn’s')
  assert.equal(view?.skills[1]?.note, undefined)
  assert.equal(view?.linkDir, '/home/jane/.claude')
  assert.equal(view?.writable, true)
})

test('parseProvided: a body that is not an answer is null', () => {
  const bad: unknown[] = [
    null,
    'text',
    {},
    { ...good, agents: 'none' },
    { ...good, skills: [{ name: 'x', state: 'maybe' }] },
    { ...good, skills: [{ name: '', state: 'installed' }] },
    { ...good, writable: 'yes' },
    { ...good, linkDir: 1 },
  ]
  for (const body of bad) assert.equal(parseProvided(body), null, JSON.stringify(body))
})

test('parseProvidedResult: a 2xx answer is a view, a refusal carries the server words', () => {
  assert.equal(parseProvidedResult(200, good).ok, true)
  assert.deepEqual(parseProvidedResult(409, { error: '/c/skills/x is a file' }), { ok: false, error: '/c/skills/x is a file' })
  assert.deepEqual(parseProvidedResult(500, null), { ok: false, error: 'HTTP 500' })
  assert.equal(parseProvidedResult(200, { nope: 1 }).ok, false)
})
