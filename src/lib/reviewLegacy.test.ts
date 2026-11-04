import assert from 'node:assert/strict'
import { test } from 'node:test'
import { legacyChoices, legacyCountText, legacyProblem } from './reviewLegacy.ts'

test('the count reads as the spec words it', () => {
  assert.equal(legacyCountText(1), '1 old review record conn could not place')
  assert.equal(legacyCountText(12), '12 old review records conn could not place')
})

test('the choices are the exact patterns and the seen repos the list covers, each once, in lowercase', () => {
  assert.deepEqual(legacyChoices(['acme/*', 'jane/tools'], ['Acme/Widgets', 'other/x', 'jane/tools', 'not a repo']), ['acme/widgets', 'jane/tools'])
  assert.deepEqual(legacyChoices(null, ['acme/widgets']), [])
})

test('a typed repo must be owner/repo and allowed', () => {
  assert.equal(legacyProblem('acme/widgets', ['acme/*']), null)
  assert.equal(legacyProblem(' acme/widgets ', ['acme/*']), null)
  assert.match(legacyProblem('', ['acme/*']) ?? '', /owner\/repo/)
  assert.match(legacyProblem('../x', ['acme/*']) ?? '', /owner\/repo/)
  assert.match(legacyProblem('other/x', ['acme/*']) ?? '', /not an allowed repository/)
})
