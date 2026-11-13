import assert from 'node:assert/strict'
import { test } from 'node:test'
import { placeOn, type SheenPage, type SheenPointer, type SheenSurface, trackSheen } from './sheen.ts'

function surface(left: number, top: number) {
  const props = new Map<string, string>()
  const s: SheenSurface & { props: Map<string, string> } = {
    props,
    getBoundingClientRect: () => ({ left, top }),
    style: { setProperty: (n, v) => void props.set(n, v), removeProperty: (n) => void props.delete(n) },
  }
  return s
}

function page(lit = true) {
  let asked = 0
  let restyle = () => {}
  const listeners = new Map<string, (e: SheenPointer) => void>()
  const frames: (() => void)[] = []
  const p: SheenPage & { fire(type: string, e: Partial<SheenPointer>): void; flush(): void; frames: (() => void)[]; asked(): number; restyle(): void } = {
    frames,
    addEventListener: (type, listener) => void listeners.set(type, listener),
    requestAnimationFrame: (callback) => void frames.push(callback),
    lit: () => (asked++, lit),
    asked: () => asked,
    onRestyle: (callback) => void (restyle = callback),
    restyle: () => restyle(),
    fire: (type, e) => listeners.get(type)?.({ pointerType: 'mouse', clientX: 0, clientY: 0, surface: null, leftPage: false, ...e }),
    flush: () => frames.splice(0).forEach((f) => f()),
  }
  return p
}

test('placeOn gives the pointer place inside the surface in whole px', () => {
  assert.deepEqual(placeOn({ left: 100, top: 40 }, 150.4, 60.6), { mx: '50px', my: '21px' })
})

test('the surface under the mouse gets the place, once a frame however many moves came in', () => {
  const p = page()
  const card = surface(10, 20)
  trackSheen(p)
  p.fire('pointermove', { surface: card, clientX: 30, clientY: 30 })
  p.fire('pointermove', { surface: card, clientX: 40, clientY: 50 })
  assert.equal(p.frames.length, 1)
  p.flush()
  assert.deepEqual([...card.props], [['--mx', '30px'], ['--my', '30px']])
})

test('a surface the pointer leaves goes back to its rest', () => {
  const p = page()
  const a = surface(0, 0)
  const b = surface(0, 0)
  trackSheen(p)
  p.fire('pointermove', { surface: a, clientX: 5, clientY: 5 })
  p.flush()
  p.fire('pointermove', { surface: b, clientX: 5, clientY: 5 })
  p.flush()
  assert.equal(a.props.size, 0)
  assert.equal(b.props.size, 2)
  p.fire('pointerout', { leftPage: true })
  p.flush()
  assert.equal(b.props.size, 0)
})

test('a touch or pen moves nothing, and a theme without the highlight gets no writes', () => {
  const touched = page()
  const card = surface(0, 0)
  trackSheen(touched)
  touched.fire('pointermove', { surface: card, pointerType: 'touch' })
  assert.equal(touched.frames.length, 0)
  const unlit = page(false)
  trackSheen(unlit)
  unlit.fire('pointermove', { surface: card })
  unlit.flush()
  assert.equal(card.props.size, 0)
})

test('the theme is asked once a surface, not once a frame, since asking reads the computed style', () => {
  const p = page()
  const card = surface(0, 0)
  trackSheen(p)
  for (const x of [1, 2, 3]) {
    p.fire('pointermove', { surface: card, clientX: x })
    p.flush()
  }
  assert.equal(p.asked(), 1)
  assert.equal(card.props.get('--mx'), '3px')
  p.fire('pointermove', { surface: null })
  p.flush()
  p.fire('pointermove', { surface: card, clientX: 4 })
  p.flush()
  assert.equal(p.asked(), 2)
})

test('a theme or mode change is asked about on the next move, even over the same surface, and rests the old place', () => {
  let shines = false
  const p = page()
  const card = surface(0, 0)
  p.lit = () => shines
  trackSheen(p)
  p.fire('pointermove', { surface: card, clientX: 5 })
  p.flush()
  assert.equal(card.props.size, 0, 'a theme without the highlight')
  shines = true
  p.restyle()
  p.fire('pointermove', { surface: card, clientX: 6 })
  p.flush()
  assert.equal(card.props.get('--mx'), '6px', 'the new theme draws it')
  shines = false
  p.restyle()
  assert.equal(card.props.size, 0, 'and a change away rests it at once')
})
