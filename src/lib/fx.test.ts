import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { bootIn, bootOut, glowIn, glowOut, NAV_QUIET_MS, noteNavigation, rowIn, rowOut } from './fx.ts'

const el = null as unknown as Element
type T = { duration: number; css?: (t: number) => string }
const all = () => [bootIn(el), bootOut(el), rowIn(el), rowOut(el), glowIn(el), glowOut(el)] as T[]
// The theme's motion token reaches fx.ts as <html data-motion>; a fake document stands in for the browser's.
const motion = (m: string | undefined) => {
  ;(globalThis as { document?: unknown }).document = { documentElement: { dataset: m === undefined ? {} : { motion: m } } }
}
afterEach(() => void delete (globalThis as { document?: unknown }).document)
const settled = () => noteNavigation(Date.now() - NAV_QUIET_MS - 50)

test('right after a navigation no transition plays, the way out included, in either motion', () => {
  for (const m of ['power', 'calm']) {
    motion(m)
    noteNavigation(Date.now())
    assert.deepEqual(all().map(t => t.duration), [0, 0, 0, 0, 0, 0], m)
  }
})

test('power: a transition plays again once the quiet time after a navigation is over, at today\'s lengths', () => {
  motion('power')
  settled()
  assert.deepEqual(all().map(t => t.duration), [650, 480, 380, 260, 600, 420])
})

test('the quiet time is short enough for a live change soon after to animate', () => {
  assert.ok(NAV_QUIET_MS <= 1000)
})

test('power: the boot, row and glow flashes take their color from the accent token and their halo from the glow strength', () => {
  motion('power')
  settled()
  for (const t of [bootIn(el), rowIn(el), glowIn(el)] as Required<T>[]) {
    const css = t.css(0.5)
    assert.doesNotMatch(css, /#[0-9a-f]{3,8}\b|\brgba?\(/i)
    assert.match(css, /drop-shadow\(0 0 [\d.]+px color-mix\(in oklch, var\(--ac\) calc\((?:[\d.]+ \* 100%|[\d.]+%) \* var\(--glow\)\), transparent\)\)/)
  }
})

test('power: the boot keeps its line, flash and flicker, and its corners follow the theme radius', () => {
  motion('power')
  settled()
  const t = bootIn(el) as Required<T>
  assert.match(t.css(0.2), /clip-path: inset\([\d.]+% [\d.]+% [\d.]+% [\d.]+% round calc\(var\(--radius\) \* 1\.5\)\)/)
  assert.match(t.css(0.2), /brightness\(/)
  const opacities = Array.from({ length: 40 }, (_, i) => 0.4 + i * 0.01).map(x => Number(/opacity: ([\d.]+)/.exec(t.css(x))![1]))
  assert.ok(opacities.some(o => o < 1) && opacities.some(o => o === 1), 'the scan flicker dips the opacity')
})

test('calm: every arrival and departure is a plain fade, no line, flash, flicker, glow or movement', () => {
  motion('calm')
  settled()
  const ts = all()
  assert.ok(ts.every(t => t.duration > 0 && t.duration <= 300), ts.map(t => t.duration).join(','))
  for (const t of ts as Required<T>[]) {
    for (const x of [0, 0.25, 0.5, 0.75, 1]) {
      const css = t.css(x)
      assert.equal(css.trim(), `opacity: ${x};`, `${x}: ${css}`)
    }
  }
})

test('calm is the reading when the attribute is missing or unknown: less motion is the safe side', () => {
  for (const m of [undefined, 'loud']) {
    motion(m)
    settled()
    assert.equal((bootIn(el) as Required<T>).css(0.5).trim(), 'opacity: 0.5;', String(m))
  }
  delete (globalThis as { document?: unknown }).document
  settled()
  assert.equal((rowIn(el) as Required<T>).css(0.5).trim(), 'opacity: 0.5;', 'no document')
})
