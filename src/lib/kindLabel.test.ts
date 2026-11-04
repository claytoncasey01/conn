import assert from 'node:assert/strict'
import { test } from 'node:test'
import { kindLabel } from './kindLabel.ts'

test('each session kind has its label', () => {
  assert.equal(kindLabel('build'), 'Build')
  assert.equal(kindLabel('adhoc'), 'Ad hoc')
  assert.equal(kindLabel('review'), 'Review')
  assert.equal(kindLabel('selfreview'), 'Self-review')
})

test('a kind this build does not know shows as itself', () => {
  assert.equal(kindLabel('deploy'), 'deploy')
  assert.equal(kindLabel(''), '')
})
