import assert from 'node:assert/strict'
import { test } from 'node:test'
import { accentTokens, DEFAULT_ACCENT, nudged, SWATCHES } from './accent.ts'
import { contrast, parseColor } from './color.ts'
import { slotProblem } from './validate.ts'

const rgba = (v: string) => {
  const c = parseColor(v)
  assert.ok(c, `${v} parses`)
  return c
}

test('the six swatches are the spec pairs and Teal is the default', () => {
  assert.deepEqual(
    SWATCHES.map((s) => [s.name, s.hue, s.chroma]),
    [['Teal', 195, 0.12], ['Cobalt', 255, 0.15], ['Violet', 295, 0.15], ['Rose', 355, 0.16], ['Amber', 70, 0.14], ['Graphite', 260, 0.02]],
  )
  assert.equal(DEFAULT_ACCENT, SWATCHES[0])
})

test('ac is oklch at .5 in light and .78 in dark', () => {
  assert.equal(accentTokens('dark', 195, 0.12).ac, 'oklch(0.78 0.12 195)')
  assert.equal(accentTokens('light', 195, 0.12).ac, 'oklch(0.5 0.12 195)')
})

test('onac keeps 4.5:1 on ac for every swatch in both modes', () => {
  for (const s of SWATCHES)
    for (const mode of ['light', 'dark'] as const) {
      const t = accentTokens(mode, s.hue, s.chroma)
      const ratio = contrast(rgba(t.onac), rgba(t.ac))
      assert.ok(ratio >= 4.5, `${s.name} ${mode} is ${ratio.toFixed(2)}`)
    }
})

test('the nudge walks L darker in light and lighter in dark until a text color reaches 4.5', () => {
  // L .54 at hue 195 is the dead zone: white is 4.47 and black 4.29 there, so neither reads
  const at = (L: number) => rgba(`oklch(${L} 0.12 195)`)
  const best = (L: number) => Math.max(contrast(rgba('oklch(0.99 0 0)'), at(L)), contrast(rgba('oklch(0.15 0 0)'), at(L)))
  assert.ok(best(0.54) < 4.5)
  for (const mode of ['light', 'dark'] as const) {
    const t = nudged(mode, 0.54, 195, 0.12)
    assert.equal(t.ac, mode === 'light' ? 'oklch(0.52 0.12 195)' : 'oklch(0.56 0.12 195)')
    assert.ok(contrast(rgba(t.onac), rgba(t.ac)) >= 4.5)
  }
})

test('ac-soft and ac-line are ac at 14% and 40% over transparent', () => {
  for (const mode of ['light', 'dark'] as const) {
    const t = accentTokens(mode, 255, 0.15)
    assert.equal(t['ac-soft'], `color-mix(in oklch, ${t.ac} 14%, transparent)`)
    assert.equal(t['ac-line'], `color-mix(in oklch, ${t.ac} 40%, transparent)`)
  }
})

test('every derived token passes the slot grammar', () => {
  for (const s of SWATCHES)
    for (const mode of ['light', 'dark'] as const) {
      const t = accentTokens(mode, s.hue, s.chroma)
      assert.deepEqual(Object.keys(t).sort(), ['ac', 'ac-line', 'ac-soft', 'focus', 'meter-lit', 'onac'])
      for (const [slot, v] of Object.entries(t)) assert.equal(slotProblem(slot, v), null, `${slot} ${v}`)
    }
})
