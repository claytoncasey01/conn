import assert from 'node:assert/strict'
import { test } from 'node:test'
import { HEAD_SHAPES, headsOf } from './treeHeads.ts'

// the arrows the tree drew before heads became a token, coordinate for coordinate and in the same order, so Neon is unchanged
test('an arrow head is the triangle the tree always drew, for each direction', () => {
  assert.equal(headsOf([[437, 172, 'r'], [201, 172, 'l']]).arrow, 'M427 166 L437 172 L427 178 Z M211 166 L201 172 L211 178 Z')
  assert.equal(headsOf([[300, 297, 'd']]).arrow, 'M294 287 L306 287 L300 297 Z')
  assert.equal(headsOf([[555, 581, 'd']]).arrow, 'M549 571 L561 571 L555 581 Z')
  assert.equal(headsOf([[675, 172, 'l']]).arrow, 'M685 166 L675 172 L685 178 Z')
})

const box = (d: string) => {
  const n = [...d.matchAll(/-?\d+(?:\.\d+)?/g)].map(Number)
  // path numbers come in x y pairs except the arcs; plug and bar are polygons, so every pair is a point
  const xs = n.filter((_, i) => i % 2 === 0), ys = n.filter((_, i) => i % 2 === 1)
  return { x0: Math.min(...xs), x1: Math.max(...xs), y0: Math.min(...ys), y1: Math.max(...ys) }
}

test('every shape ends at the tip, on the wire\'s side of it, for every direction', () => {
  assert.deepEqual([...HEAD_SHAPES], ['arrow', 'plug', 'dot', 'bar'])
  for (const [x, y, d] of [[437, 172, 'r'], [201, 172, 'l'], [300, 297, 'd']] as const) {
    const h = headsOf([[x, y, d]])
    for (const k of ['plug', 'bar'] as const) {
      const b = box(h[k])
      if (d === 'r') assert.ok(b.x1 === x && b.x0 < x, `${k} ${d}`)
      if (d === 'l') assert.ok(b.x0 === x && b.x1 > x, `${k} ${d}`)
      if (d === 'd') assert.ok(b.y1 === y && b.y0 < y, `${k} ${d}`)
    }
    // a dot sits just inside the tip: its circle's leading edge is the tip
    assert.match(h.dot, /^M-?\d+(\.\d+)? -?\d+(\.\d+)? a3\.5 3\.5 0 1 0 7 0 a3\.5 3\.5 0 1 0 -7 0$/)
  }
  assert.deepEqual(headsOf([]), { arrow: '', plug: '', dot: '', bar: '' })
})
