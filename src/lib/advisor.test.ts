import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { ADVISOR_CHOICES, ADVISOR_PICKS, DEFAULT_ADVISOR, advisorLabel, advisorOf, withAdvisor } from './advisor.ts'

// The server's choices and default (internal/advisor reads the same file), so the page offers only what the server takes.
const shared = JSON.parse(readFileSync(new URL('../../testdata/advisor-choices.json', import.meta.url), 'utf8')) as { default: string; choices: string[] }

test('the choices and the default are the server’s, from testdata/advisor-choices.json', () => {
  assert.deepEqual(
    ADVISOR_CHOICES.map((c) => c.value),
    shared.choices,
  )
  assert.equal(DEFAULT_ADVISOR, shared.default)
})

test('each choice has a label, and none names a model version', () => {
  for (const c of ADVISOR_CHOICES) {
    assert.ok(c.label.length > 0, c.value)
    assert.doesNotMatch(c.label, /\d/, c.value)
  }
})

test('what is not a choice reads as the default', () => {
  for (const v of [undefined, null, '', 'haiku', 'claude-opus-5-5', 7, ['opus']]) assert.equal(advisorOf(v), 'claude-code', String(v))
  assert.equal(advisorOf('fable'), 'fable')
  assert.equal(advisorLabel('opus'), 'Opus')
  assert.equal(advisorLabel('nope'), 'Claude Code’s setting')
})

test('a start sends the advisor it picked, and nothing for Settings’ choice', () => {
  assert.deepEqual(withAdvisor({ repo: 'acme/widgets' }, ''), { repo: 'acme/widgets' })
  assert.deepEqual(withAdvisor({ repo: 'acme/widgets' }, 'fable'), { repo: 'acme/widgets', advisor: 'fable' })
  assert.deepEqual(withAdvisor({ repo: 'acme/widgets' }, 'claude-code'), { repo: 'acme/widgets', advisor: 'claude-code' })
})

test('a start’s picker offers Settings’ choice first, then every choice', () => {
  assert.deepEqual(
    ADVISOR_PICKS.map((p) => p.value),
    ['', ...shared.choices],
  )
})
