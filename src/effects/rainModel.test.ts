import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createColumns, GLYPHS, stepColumns } from './rainModel.ts'

// mulberry32: the same sequence every run
function seeded(seed: number) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

test('GLYPHS is exactly half-width katakana U+FF66..U+FF9D then the digits', () => {
  assert.equal(GLYPHS.length, 56 + 10)
  assert.equal(GLYPHS[0], 'ｦ')
  assert.equal(GLYPHS[55], 'ﾝ')
  assert.equal(GLYPHS.slice(56).join(''), '0123456789')
})

test('createColumns makes about density * width / 14 columns, none at density 0', () => {
  for (const [w, d] of [[1400, 0.6], [1000, 1], [333, 0.25]] as const) {
    assert.ok(Math.abs(createColumns(w, 800, d, seeded(1)).length - (d * w) / 14) <= 1)
  }
  assert.equal(createColumns(1400, 800, 0, seeded(1)).length, 0)
})

test('far columns are slower than near ones, and every glyph is a known one', () => {
  const cols = createColumns(2000, 800, 1, seeded(2))
  const far = cols.filter((c) => c.depth === 0)
  const near = cols.filter((c) => c.depth === 1)
  assert.ok(far.length > 0 && near.length > 0)
  assert.ok(Math.max(...far.map((c) => c.speed)) < Math.min(...near.map((c) => c.speed)))
  for (const c of cols) {
    assert.ok(c.x >= 0 && c.x <= 2000 && c.y >= 0 && c.y <= 800)
    for (const g of c.glyphs) assert.ok(GLYPHS.includes(g))
  }
})

test('stepColumns moves y by speed * dt and wraps a column past the bottom above the top with fresh glyphs', () => {
  const rng = seeded(3)
  const [c] = createColumns(14, 600, 1, rng)
  c.y = 100
  const before = c.glyphs
  stepColumns([c], 500, 600, 2, rng)
  assert.equal(c.y, 100 + (c.speed * 2 * 500) / 1000)
  assert.equal(c.glyphs, before)
  c.y = 599
  stepColumns([c], 1000, 600, 1, rng)
  assert.ok(c.y <= 0 && c.y >= -300)
  assert.notEqual(c.glyphs, before)
  assert.notDeepEqual(c.glyphs, before)
  for (const g of c.glyphs) assert.ok(GLYPHS.includes(g))
})
