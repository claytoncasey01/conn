import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import * as flicker from './flicker.ts'
import * as roll from './roll.ts'
import * as scanlines from './scanlines.ts'
import * as vignette from './vignette.ts'
import { buildRegistry } from './lib/build.ts'
import type { EffectEnv, EffectOptions, Mount } from './lib/types.ts'

type FakeEl = { style: { cssText: string; opacity: string }; attrs: Record<string, string>; parent: FakeRoot | null; setAttribute(k: string, v: string): void; remove(): void }
type FakeRoot = { children: FakeEl[]; appendChild(el: FakeEl): FakeEl }
const g = globalThis as unknown as Record<string, unknown>
const saved = { had: Object.hasOwn(g, 'document'), value: g.document }
const stops: (() => void)[] = []
afterEach(() => {
  // a test that threw must not leave an effect held for the next one
  for (const s of stops.splice(0)) s()
  if (saved.had) g.document = saved.value
  else delete g.document
})

const tokens = { bg: '#010a04', ac: '#00ff41', tx: '#a8ffb8', bright: '#e6ffe9' }
const MODULES: [string, { name: string; layer: string; mount: Mount }][] = [
  ['scanlines', scanlines],
  ['vignette', vignette],
  ['flicker', flicker],
  ['roll', roll],
]

function rig(opts: { reduced?: boolean; hidden?: boolean } = {}) {
  const root: FakeRoot = {
    children: [],
    appendChild(el) {
      el.parent = root
      root.children.push(el)
      return el
    },
  }
  let docListeners = 0
  g.document = {
    createElement(tag: string) {
      assert.equal(tag, 'div')
      const el: FakeEl = {
        style: { cssText: '', opacity: '' },
        attrs: {},
        parent: null,
        setAttribute: (k, v) => void (el.attrs[k] = v),
        remove() {
          if (el.parent) el.parent.children.splice(el.parent.children.indexOf(el), 1)
          el.parent = null
        },
      }
      return el
    },
    addEventListener: () => void docListeners++,
    removeEventListener: () => void docListeners--,
  }
  let clock = 0
  const pending = new Map<number, (t: number) => void>()
  let nextId = 1
  const stats = { raf: 0, caf: 0 }
  const env: EffectEnv = {
    reduced: !!opts.reduced,
    hidden: () => !!opts.hidden,
    raf: (cb) => {
      stats.raf++
      pending.set(nextId, cb)
      return nextId++
    },
    caf: (id) => {
      stats.caf++
      pending.delete(id)
    },
    now: () => clock,
  }
  return {
    root: root as unknown as HTMLElement,
    rawRoot: root,
    env,
    stats,
    pending: () => pending.size,
    // the queued frame callbacks, held by a caller that outlives a stop
    queued: () => [...pending.values()],
    docListeners: () => docListeners,
    // advance the clock and run the queued frame, if any
    frame(ms: number) {
      clock += ms
      const run = [...pending.entries()]
      pending.clear()
      for (const [, cb] of run) cb(clock)
    },
  }
}

// a mount whose stop the afterEach also calls, so a failing assertion cannot leave the effect claimed
function mountOf(m: Mount, r: ReturnType<typeof rig>, options: EffectOptions = {}) {
  const stop = m(r.root, options, tokens, r.env)
  stops.push(stop)
  return stop
}

for (const [label, m] of MODULES) {
  test(`${label}: named, in the above layer, and the registry sees it that way`, () => {
    assert.equal(m.name, label)
    assert.equal(m.layer, 'above')
    assert.equal(buildRegistry({ m })[label].layer, 'above')
  })

  test(`${label}: one hidden pointer-proof element that fills the root, and unmount removes it`, () => {
    const r = rig()
    const stop = mountOf(m.mount, r)
    assert.equal(r.rawRoot.children.length, 1)
    const el = r.rawRoot.children[0]
    assert.match(el.style.cssText, /pointer-events:none/)
    assert.match(el.style.cssText, /position:absolute/)
    assert.match(el.style.cssText, /inset:0/)
    assert.equal(el.attrs['aria-hidden'], 'true')
    stop()
    assert.equal(r.rawRoot.children.length, 0)
  })

  test(`${label}: a second mount does not stack, and unmounting it leaves the first in place`, () => {
    const r = rig()
    const first = mountOf(m.mount, r)
    const second = mountOf(m.mount, r)
    assert.equal(r.rawRoot.children.length, 1)
    second()
    assert.equal(r.rawRoot.children.length, 1)
    first()
    assert.equal(r.rawRoot.children.length, 0)
    // released: a new mount draws again
    mountOf(m.mount, r)
    assert.equal(r.rawRoot.children.length, 1)
  })

  test(`${label}: a stop called twice does not release a later mount's hold`, () => {
    const r = rig()
    const first = mountOf(m.mount, r)
    first()
    mountOf(m.mount, r)
    first()
    assert.equal(r.rawRoot.children.length, 1)
    // still held: a third mount does not stack on the second
    mountOf(m.mount, r)
    assert.equal(r.rawRoot.children.length, 1)
  })

  test(`${label}: a wrong option type throws before anything is added, and the effect is not left claimed`, () => {
    const r = rig()
    const bad: EffectOptions = label === 'scanlines' ? { opacity: 'x' } : label === 'vignette' || label === 'roll' ? { strength: true } : { amount: 'x' }
    assert.throws(() => m.mount(r.root, bad, tokens, r.env), /must be a number/)
    assert.equal(r.rawRoot.children.length, 0)
    assert.equal(r.stats.raf, 0)
    mountOf(m.mount, r)
    assert.equal(r.rawRoot.children.length, 1)
  })

  test(`${label}: a throw while building the element does not leave the effect claimed`, () => {
    const r = rig()
    const doc = g.document as { createElement(t: string): unknown }
    const real = doc.createElement
    doc.createElement = () => {
      throw new Error('no dom')
    }
    assert.throws(() => m.mount(r.root, {}, tokens, r.env), /no dom/)
    doc.createElement = real
    mountOf(m.mount, r)
    assert.equal(r.rawRoot.children.length, 1)
  })

  test(`${label}: an unknown option is ignored`, () => {
    const r = rig()
    mountOf(m.mount, r, { nope: 1, other: 'x' })
    assert.equal(r.rawRoot.children.length, 1)
  })
}

test('scanlines: a repeating gradient on the pitch option, dark from the bg token, opacity capped at .2', () => {
  const r = rig()
  mountOf(scanlines.mount, r, { pitch: 4 })
  const el = r.rawRoot.children[0]
  assert.match(el.style.cssText, /repeating-linear-gradient\(to bottom,transparent 0,transparent 3px,/)
  assert.match(el.style.cssText, /4px\)/)
  assert.match(el.style.cssText, /color-mix\(in srgb, #010a04 25%, black\)/)
  assert.match(el.style.cssText, /opacity:0\.14;/)
})

test('scanlines: opacity and pitch are clamped, and a pitch is a whole number of pixels', () => {
  const opacity = (o: EffectOptions) => {
    const r = rig()
    const stop = scanlines.mount(r.root, o, tokens, r.env)
    const css = r.rawRoot.children[0].style.cssText
    stop()
    return { opacity: Number(/opacity:([\d.]+);/.exec(css)![1]), css }
  }
  assert.equal(opacity({ opacity: 5 }).opacity, 0.2)
  assert.equal(opacity({ opacity: -1 }).opacity, 0)
  assert.match(opacity({ pitch: 99 }).css, /12px\)/)
  assert.match(opacity({ pitch: 0 }).css, /transparent 1px,.* 2px\)/)
  assert.match(opacity({ pitch: 2.6 }).css, /transparent 2px,.* 3px\)/)
})

test('scanlines: static, so it draws under reduced motion and never asks for a frame', () => {
  const r = rig({ reduced: true })
  mountOf(scanlines.mount, r)
  assert.equal(r.rawRoot.children.length, 1)
  assert.equal(r.stats.raf, 0)
})

test('vignette: a radial gradient out to the bg token at the strength, capped at .6', () => {
  const css = (o: EffectOptions) => {
    const r = rig()
    const stop = vignette.mount(r.root, o, tokens, r.env)
    const out = r.rawRoot.children[0].style.cssText
    stop()
    return out
  }
  assert.match(css({}), /radial-gradient\(ellipse at center,transparent 55%,#010a04 100%\)/)
  assert.match(css({}), /opacity:0\.55;/)
  assert.match(css({ strength: 3 }), /opacity:0\.6;/)
  assert.match(css({ strength: 0.3 }), /opacity:0\.3;/)
})

test('vignette: static, so it draws under reduced motion and never asks for a frame', () => {
  const r = rig({ reduced: true })
  mountOf(vignette.mount, r)
  assert.equal(r.rawRoot.children.length, 1)
  assert.equal(r.stats.raf, 0)
})

test('flicker: under reduced motion it appends nothing and requests no frame, and does not claim the effect', () => {
  const r = rig({ reduced: true })
  mountOf(flicker.mount, r)
  assert.equal(r.rawRoot.children.length, 0)
  assert.equal(r.stats.raf, 0)
  const r2 = rig()
  mountOf(flicker.mount, r2)
  assert.equal(r2.rawRoot.children.length, 1)
})

test('flicker: the veil starts clear and the opacity stays within 0 and amount', () => {
  const r = rig()
  mountOf(flicker.mount, r, { amount: 0.05, hz: 8 })
  const el = r.rawRoot.children[0]
  assert.match(el.style.cssText, /opacity:0;/)
  const seen: number[] = []
  for (let i = 0; i < 200; i++) {
    r.frame(125)
    seen.push(Number(el.style.opacity))
  }
  assert.ok(seen.every((v) => v >= 0 && v <= 0.05), 'within amount')
  assert.ok(new Set(seen).size > 1, 'it does wobble')
})

test('flicker: amount is capped at .06 and hz at 8, even when the options ask for more', () => {
  const r = rig()
  mountOf(flicker.mount, r, { amount: 1, hz: 60 })
  const el = r.rawRoot.children[0]
  let draws = 0
  let max = 0
  let last = el.style.opacity
  // one second of 60 Hz frames
  for (let i = 0; i < 60; i++) {
    r.frame(1000 / 60)
    if (el.style.opacity !== last) draws++
    last = el.style.opacity
    max = Math.max(max, Number(el.style.opacity))
  }
  assert.ok(max <= 0.06, `max ${max}`)
  assert.ok(draws <= 8, `${draws} steps in a second`)
  assert.ok(draws >= 4, 'but it does step')
})

test('flicker: the default is about 4 steps a second', () => {
  const r = rig()
  mountOf(flicker.mount, r)
  const el = r.rawRoot.children[0]
  let draws = 0
  let last = el.style.opacity
  for (let i = 0; i < 60; i++) {
    r.frame(1000 / 60)
    if (el.style.opacity !== last) draws++
    last = el.style.opacity
  }
  assert.ok(draws >= 3 && draws <= 5, `${draws}`)
})

test('flicker: a negative amount is lifted to 0', () => {
  const r = rig()
  mountOf(flicker.mount, r, { amount: -1 })
  const el = r.rawRoot.children[0]
  for (let i = 0; i < 10; i++) r.frame(1000)
  assert.equal(Number(el.style.opacity), 0)
})

// Without the clamp a non-positive hz reaches createLoop, where fps <= 0 falls back to 30 fps: the clamp is what holds it near 1 a second
for (const hz of [-5, 0]) {
  test(`flicker: hz ${hz} is lifted to 1, so it steps about once a second`, () => {
    const r = rig()
    mountOf(flicker.mount, r, { amount: 0.05, hz })
    const el = r.rawRoot.children[0]
    let draws = 0
    let last = el.style.opacity
    // one second of 60 Hz frames
    for (let i = 0; i < 60; i++) {
      r.frame(1000 / 60)
      if (el.style.opacity !== last) draws++
      last = el.style.opacity
    }
    assert.ok(draws >= 1 && draws <= 2, `${draws} steps in a second`)
  })
}

test('flicker: a start() that throws leaves no veil behind and the effect free, and its listener is gone', () => {
  const r = rig()
  const real = r.env.raf
  r.env.raf = () => {
    throw new Error('no raf')
  }
  assert.throws(() => flicker.mount(r.root, {}, tokens, r.env), /no raf/)
  assert.equal(r.rawRoot.children.length, 0)
  assert.equal(r.docListeners(), 0)
  r.env.raf = real
  mountOf(flicker.mount, r)
  assert.equal(r.rawRoot.children.length, 1)
})

test('flicker: no frame is requested while the tab is hidden', () => {
  const r = rig({ hidden: true })
  mountOf(flicker.mount, r)
  assert.equal(r.rawRoot.children.length, 1)
  assert.equal(r.stats.raf, 0)
})

test('flicker: unmount stops the loop at once: the queued frame is cancelled, no later frame is asked for, the veil is gone', () => {
  const r = rig()
  const stop = mountOf(flicker.mount, r)
  r.frame(500)
  assert.equal(r.pending(), 1)
  assert.equal(r.docListeners(), 1)
  const asked = r.stats.raf
  stop()
  assert.equal(r.pending(), 0)
  assert.equal(r.docListeners(), 0)
  assert.equal(r.rawRoot.children.length, 0)
  r.frame(500)
  assert.equal(r.stats.raf, asked)
})

test('flicker: a frame callback captured before the stop draws nothing and asks for nothing when it lands after', () => {
  const r = rig()
  const stop = mountOf(flicker.mount, r, { amount: 0.05 })
  r.frame(500)
  const [late] = r.queued()
  assert.ok(late, 'a frame was queued')
  const el = r.rawRoot.children[0]
  const opacity = el.style.opacity
  stop()
  const asked = r.stats.raf
  late(10_000)
  assert.equal(el.style.opacity, opacity)
  assert.equal(r.stats.raf, asked)
  assert.equal(r.pending(), 0)
})

test('the three together: each one is capped on its own, whatever the options ask for', () => {
  const r = rig()
  mountOf(scanlines.mount, r, { opacity: 9 })
  mountOf(vignette.mount, r, { strength: 9 })
  mountOf(flicker.mount, r, { amount: 9 })
  const opacities = r.rawRoot.children.map((c) => Number(/opacity:([\d.]+)/.exec(c.style.cssText)![1]))
  assert.deepEqual(opacities, [0.2, 0.6, 0])
  for (let i = 0; i < 20; i++) r.frame(1000)
  assert.ok(Number(r.rawRoot.children[2].style.opacity) <= 0.06)
})

// ---- the tube (round 3): options on the existing effects, and roll. Flash safety: nothing here brightens or darkens the page by
// more than a few percent, and nothing that moves steps faster than a slow drift; the caps hold whatever a theme asks.
const pos = (el: { style: unknown }) => (el.style as Record<string, string>).backgroundPosition ?? ''

test('scanlines: crawl drifts the lines down on a frame loop, and is still by default, under reduced motion and on a hidden tab', () => {
  const r = rig()
  const stop = mountOf(scanlines.mount, r, { crawl: 2.4 })
  const el = r.rawRoot.children[0]
  assert.equal(r.stats.raf, 1, 'a crawl asks for frames')
  r.frame(100)
  const a = pos(el)
  r.frame(600)
  assert.notEqual(pos(el), a, 'the lines moved')
  assert.match(pos(el), /^0 \d+(\.\d+)?px/)
  stop()
  // each mount below must really draw: the effect is one at a time, so the one before is stopped first
  const still = rig({ reduced: true })
  const stopStill = mountOf(scanlines.mount, still, { crawl: 2.4 })
  assert.equal(still.rawRoot.children.length, 1, 'reduced motion still draws the lines')
  assert.equal(still.stats.raf, 0, 'reduced motion: still lines')
  stopStill()
  const hidden = rig({ hidden: true })
  mountOf(scanlines.mount, hidden, { crawl: 2.4 })
  assert.equal(hidden.rawRoot.children.length, 1, 'a hidden tab still draws the lines')
  assert.equal(hidden.stats.raf, 0, 'a hidden tab asks for nothing')
  assert.throws(() => scanlines.mount(rig().root, { crawl: 'fast' }, tokens, rig().env), /crawl/)
})

test('scanlines: the grille adds a fine vertical aperture from the theme\'s own colors, inside the same capped overlay', () => {
  const css = (o: EffectOptions) => {
    const r = rig()
    const stop = scanlines.mount(r.root, o, tokens, r.env)
    const out = r.rawRoot.children[0].style.cssText
    stop()
    return out
  }
  assert.doesNotMatch(css({}), /to right/, 'no grille by default')
  const g = css({ grille: true, opacity: 9 })
  assert.match(g, /repeating-linear-gradient\(to right,color-mix\(in srgb, #00ff41 /)
  assert.match(g, /opacity:0\.2;/, 'the one overlay opacity caps the lines and the grille together')
  assert.throws(() => css({ grille: 'yes' }), /grille/)
})

test('vignette: glass rounds the tube\'s edge, darkens its corners and lights one corner, all under the capped strength', () => {
  const css = (o: EffectOptions) => {
    const r = rig()
    const stop = vignette.mount(r.root, o, tokens, r.env)
    const out = r.rawRoot.children[0].style.cssText
    stop()
    return out
  }
  assert.doesNotMatch(css({}), /border-radius/, 'flat by default')
  const g = css({ glass: true, strength: 9 })
  assert.match(g, /border-radius:\d+px/)
  assert.match(g, /box-shadow:0 0 0 \d+px #010a04, inset 0 0 \d+px \d+px #010a04/)
  assert.match(g, /color-mix\(in srgb, #e6ffe9 \d+%, transparent\)/, 'the highlight is the theme\'s bright')
  assert.match(g, /opacity:0\.6;/)
  assert.throws(() => css({ glass: 1 }), /glass/)
})

test('roll: a soft band of the theme\'s light drifts down the screen, capped at .06 and slowed to at least 3 s a pass', () => {
  const r = rig()
  mountOf(roll.mount, r, { strength: 9, period: 0.1 })
  const el = r.rawRoot.children[0]
  assert.match(el.style.cssText, /linear-gradient\(to bottom,transparent 0%,color-mix\(in srgb, #a8ffb8 [\d.]+%, transparent\) 55%,color-mix\(in srgb, #e6ffe9 6%, transparent\) 70%,transparent 100%\)/)
  assert.match(el.style.cssText, /background-size:100% 30%/)
  r.frame(100)
  const a = pos(el)
  r.frame(1000)
  const b = pos(el)
  assert.notEqual(a, b)
  // a pass takes at least 3 s whatever the theme asks: in one second the band moves at most a third of its way
  const y = (p: string) => Number(/^0 (-?[\d.]+)%$/.exec(p)![1])
  assert.ok(Math.abs(y(b) - y(a)) <= (185.7 / 3) + 0.01, `${a} -> ${b}`)
})

test('roll: under reduced motion it draws nothing, asks for no frame and claims nothing; unmount stops its loop', () => {
  const r = rig({ reduced: true })
  mountOf(roll.mount, r)
  assert.equal(r.rawRoot.children.length, 0)
  assert.equal(r.stats.raf, 0)
  const live = rig()
  const stop = mountOf(roll.mount, live)
  stop()
  assert.equal(live.pending(), 0)
  assert.equal(live.docListeners(), 0)
})
