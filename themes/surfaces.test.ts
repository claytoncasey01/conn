// What a popover or a backing may rest on: an opaque base, in every look.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseColor } from '../src/lib/theme/color.ts'
import { looks, type Look } from './looks.ts'

// the looks whose bg lets anything through
const translucentBg = (all: Look[]) => all.filter((l) => (parseColor(l.tokens.bg ?? '')?.a ?? 0) !== 1).map((l) => l.name)

test('bg is opaque in every look: a panel layered over it (the Theme menu) can never show the page through', () => {
  const all = looks()
  // the four built-ins, with Default's two modes and CRT's three variants
  assert.deepEqual([...new Set(all.map((l) => l.id))], ['crt', 'default', 'matrix', 'neon'])
  assert.deepEqual(translucentBg(all), [])
})

test('the check is not vacuous: a translucent bg, or one that is not a color, is named', () => {
  const [one] = looks()
  assert.deepEqual(translucentBg([{ ...one, tokens: { ...one.tokens, bg: 'rgba(1,2,3,.9)' } }]), [one.name])
  assert.deepEqual(translucentBg([{ ...one, tokens: { ...one.tokens, bg: 'nope' } }]), [one.name])
})
