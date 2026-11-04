import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import * as rain from './rain.ts'
import { buildRegistry } from './lib/build.ts'
import * as rainModel from './rainModel.ts'
import type { EffectEnv, EffectOptions } from './lib/types.ts'

type Listener = () => void
type FakeCanvas = { width: number; height: number; style: { cssText: string }; parent: FakeRoot | null; setAttribute(): void; getContext(kind: string): unknown; remove(): void }
type FakeRoot = { clientWidth: number; clientHeight: number; children: FakeCanvas[]; appendChild(el: FakeCanvas): FakeCanvas }
type Text = { text: string; x: number; y: number; fillStyle: string; font: string; alpha: number }
const g = globalThis as unknown as Record<string, unknown>
const KEYS = ['document', 'window', 'FontFace', 'setTimeout', 'clearTimeout', 'devicePixelRatio']
const saved = Object.fromEntries(KEYS.map((k) => [k, { had: Object.hasOwn(g, k), value: g[k] }]))
afterEach(() => {
  for (const k of KEYS) {
    if (saved[k].had) g[k] = saved[k].value
    else delete g[k]
  }
})

const tokens = { bg: '#010a04', ac: '#00ff41', hi: '#ffaa00' }
const HEAD = 'rgb(220 255 230)'

// Everything outside rain.ts is a fake: the document, the window, the timer, the canvas and its recording 2D context
function rig(opts: { reduced?: boolean; hidden?: boolean; width?: number; height?: number; fonts?: boolean } = {}) {
  const alphas: number[] = []
  const fades: { alpha: number; fillStyle: string }[] = []
  const texts: Text[] = []
  const scales: unknown[][] = []
  const canvases: unknown[] = []
  let alpha = 1
  const ctx = {
    fillStyle: '',
    font: '',
    textBaseline: '',
    get globalAlpha() {
      return alpha
    },
    set globalAlpha(v: number) {
      alpha = v
      alphas.push(v)
    },
    fillRect(_x: number, _y: number, w: number, h: number) {
      // the fade is the rect that covers the whole canvas
      if (w >= canvas.width / dpr && h >= canvas.height / dpr) fades.push({ alpha, fillStyle: ctx.fillStyle })
    },
    fillText(text: string, x: number, y: number) {
      texts.push({ text, x, y, fillStyle: ctx.fillStyle, font: ctx.font, alpha })
    },
    clearRect() {},
    setTransform() {},
    scale(...a: unknown[]) {
      scales.push(a)
    },
  }
  const dpr = 1
  const canvas: FakeCanvas = {
    width: 0,
    height: 0,
    style: { cssText: '' },
    parent: null,
    setAttribute() {},
    getContext: (kind: string) => (kind === '2d' ? ctx : null),
    remove() {
      if (canvas.parent) canvas.parent.children.splice(canvas.parent.children.indexOf(canvas), 1)
      canvas.parent = null
    },
  }
  const root: FakeRoot = {
    clientWidth: opts.width ?? 1400,
    clientHeight: opts.height ?? 800,
    children: [],
    appendChild(el) {
      el.parent = root
      root.children.push(el)
      return el
    },
  }
  const docListeners = new Map<string, Set<Listener>>()
  const winListeners = new Map<string, Set<Listener>>()
  const add = (m: Map<string, Set<Listener>>) => (t: string, f: Listener) => void (m.get(t) ?? m.set(t, new Set()).get(t)!).add(f)
  const del = (m: Map<string, Set<Listener>>) => (t: string, f: Listener) => void m.get(t)?.delete(f)
  const fontLog = { added: [] as unknown[], deleted: [] as unknown[] }
  g.document = {
    createElement: (tag: string) => {
      assert.equal(tag, 'canvas')
      canvases.push(canvas)
      return canvas
    },
    addEventListener: add(docListeners),
    removeEventListener: del(docListeners),
    ...(opts.fonts === false ? {} : { fonts: { add: (f: unknown) => void fontLog.added.push(f), delete: (f: unknown) => void fontLog.deleted.push(f) } }),
  }
  g.window = { addEventListener: add(winListeners), removeEventListener: del(winListeners) }
  g.devicePixelRatio = dpr
  // a timer that only runs when the test says so
  const timers = new Map<number, () => void>()
  let nextTimer = 1
  g.setTimeout = (f: () => void) => {
    timers.set(nextTimer, f)
    return nextTimer++
  }
  g.clearTimeout = (id: number) => void timers.delete(id)
  let clock = 0
  const pending = new Map<number, (t: number) => void>()
  let nextId = 1
  const stats = { raf: 0 }
  const env: EffectEnv = {
    reduced: !!opts.reduced,
    hidden: () => !!opts.hidden,
    raf: (cb) => {
      stats.raf++
      pending.set(nextId, cb)
      return nextId++
    },
    caf: (id) => void pending.delete(id),
    now: () => clock,
  }
  return {
    root: root as unknown as HTMLElement,
    rawRoot: root,
    env,
    stats,
    texts,
    fades,
    alphas,
    scales,
    canvases,
    fontLog,
    ctx,
    winCount: () => [...winListeners.values()].reduce((n, s) => n + s.size, 0),
    docCount: () => [...docListeners.values()].reduce((n, s) => n + s.size, 0),
    fireResize: () => winListeners.get('resize')?.forEach((f) => f()),
    timers: () => timers.size,
    runTimers() {
      const run = [...timers.values()]
      timers.clear()
      run.forEach((f) => f())
    },
    frame(ms = 100) {
      clock += ms
      const run = [...pending.values()]
      pending.clear()
      run.forEach((cb) => cb(clock))
    },
    mount(options: EffectOptions = {}, t: Record<string, string> = tokens) {
      return rain.mount(root as unknown as HTMLElement, options, t, env)
    },
  }
}

const heads = (texts: Text[], height: number) => texts.filter((t) => t.fillStyle === HEAD && t.y >= 0 && t.y <= height)

test('rain is an effect in the behind layer, and rainModel is not one (the registry skips it)', () => {
  assert.equal(rain.name, 'rain')
  assert.equal(rain.layer, 'behind')
  assert.equal(typeof rain.mount, 'function')
  assert.deepEqual(Object.keys(buildRegistry({ a: rain, b: rainModel })), ['rain'])
})

test('mount appends exactly one pointer-events:none canvas and unmount removes it and the resize listener', () => {
  const r = rig()
  const stop = r.mount()
  assert.equal(r.rawRoot.children.length, 1)
  assert.ok(r.rawRoot.children[0].style.cssText.includes('pointer-events:none'))
  assert.equal(r.winCount(), 1)
  stop()
  assert.equal(r.rawRoot.children.length, 0)
  assert.equal(r.winCount(), 0)
  assert.equal(r.docCount(), 0)
})

test('unmount cancels the frame, the pending resize timer, and draws nothing more', () => {
  const r = rig()
  const stop = r.mount()
  r.fireResize()
  assert.equal(r.timers(), 1)
  stop()
  assert.equal(r.timers(), 0)
  const drawn = r.texts.length
  r.frame()
  assert.equal(r.texts.length, drawn)
})

test('reduced motion requests no frame, pre-fills with constant .09 fades and never writes alpha 1', () => {
  const r = rig({ reduced: true })
  r.mount()
  assert.equal(r.stats.raf, 0)
  assert.ok(r.fades.length >= 60, `${r.fades.length} pre-fill steps`)
  assert.ok(r.fades.every((f) => f.alpha === 0.09 && f.fillStyle === tokens.bg))
  assert.ok(r.alphas.length > 0 && r.alphas.every((a) => a !== 1))
})

test('the reduced-motion still frame has glyphs in many columns, inside the canvas (the pre-fill draws and wraps, it does not only advance y)', () => {
  const r = rig({ reduced: true, width: 1400, height: 800 })
  r.mount({ density: 0.6 })
  const columns = (1400 * 0.6) / 14
  const x = new Set(heads(r.texts, 800).map((t) => t.x))
  assert.ok(x.size >= columns * 0.6, `${x.size} of ${columns} columns have a head glyph on the canvas`)
  // and the whole run stays on the canvas: nothing is drawn at a row off the bottom
  assert.ok(r.texts.filter((t) => t.y >= 0).every((t) => t.y <= 800 + 20))
})

test('a hidden tab requests no frame', () => {
  const r = rig({ hidden: true })
  const stop = r.mount()
  assert.equal(r.stats.raf, 0)
  stop()
})

test('drawing starts at once: a frame is requested on mount and it fades at .09, heads near-white, trails in the color token', () => {
  const r = rig()
  r.mount()
  assert.equal(r.stats.raf, 1)
  r.frame()
  r.frame()
  assert.ok(r.fades.length >= 1 && r.fades.every((f) => f.alpha === 0.09))
  assert.ok(r.texts.some((t) => t.fillStyle === HEAD))
  assert.ok(r.texts.some((t) => t.fillStyle === tokens.ac))
  assert.ok(r.texts.every((t) => t.fillStyle === HEAD || t.fillStyle === tokens.ac))
})

test('the color option names the token the trail is drawn in', () => {
  const r = rig()
  r.mount({ color: 'hi' })
  r.frame()
  r.frame()
  assert.ok(r.texts.some((t) => t.fillStyle === tokens.hi))
  assert.ok(!r.texts.some((t) => t.fillStyle === tokens.ac))
})

test('the font is Conn Matrix Code then monospace, and the canvas is never mirrored', () => {
  const r = rig()
  r.mount()
  r.frame()
  r.frame()
  assert.ok(r.texts.length > 0)
  for (const t of r.texts) assert.match(t.font, /'Conn Matrix Code', monospace$/)
  // far glyphs are smaller than near ones
  const sizes = new Set(r.texts.map((t) => parseInt(t.font, 10)))
  assert.ok(sizes.size === 2, `font sizes ${[...sizes]}`)
  assert.equal(r.scales.length, 0)
})

test('a resize is debounced, then rebuilds the columns and leaves one canvas and one listener', () => {
  const r = rig({ reduced: true, width: 1400 })
  r.mount({ density: 1 })
  const before = new Set(heads(r.texts, 800).map((t) => t.x)).size
  r.texts.length = 0
  r.rawRoot.clientWidth = 700
  r.fireResize()
  r.fireResize()
  r.fireResize()
  assert.equal(r.timers(), 1)
  assert.equal(r.texts.length, 0)
  r.runTimers()
  const after = new Set(heads(r.texts, 800).map((t) => t.x)).size
  assert.ok(after > 0 && after < before * 0.7, `${before} columns then ${after}`)
  assert.equal(r.rawRoot.children.length, 1)
  assert.equal(r.winCount(), 1)
  assert.equal(r.canvases.length, 1)
})

test('with FontFace and document.fonts it adds the face for the bundled woff2 and unmount removes it', () => {
  const r = rig()
  const made: { family: string; source: string }[] = []
  g.FontFace = class {
    constructor(public family: string, public source: string) {
      made.push({ family, source })
    }
    load() {
      // never settles: drawing must not wait for it
      return new Promise<void>(() => {})
    }
  }
  const stop = r.mount()
  assert.equal(r.stats.raf, 1)
  assert.equal(made.length, 1)
  assert.equal(made[0].family, 'Conn Matrix Code')
  assert.match(made[0].source, /ConnMatrixCode.*\.woff2/)
  assert.equal(r.fontLog.added.length, 1)
  stop()
  assert.deepEqual(r.fontLog.deleted, r.fontLog.added)
})

test('a failed font load keeps the fallback and throws nothing', async () => {
  const r = rig()
  g.FontFace = class {
    load() {
      return Promise.reject(new Error('404'))
    }
  }
  const stop = r.mount()
  // the rejection has been handled by the time the microtasks drain (setTimeout is a fake here)
  for (let i = 0; i < 3; i++) await Promise.resolve()
  r.frame()
  assert.ok(r.texts.length > 0)
  stop()
  // a FontFace that throws on construction, and a document with no fonts, are fine too
  g.FontFace = class {
    constructor() {
      throw new Error('bad source')
    }
  }
  r.mount()()
  const bare = rig({ fonts: false })
  g.FontFace = class {
    load() {
      return Promise.resolve()
    }
  }
  bare.mount()()
  assert.equal(bare.rawRoot.children.length, 0)
})

test('options are validated before any side effect', () => {
  const bad: EffectOptions[] = [{ density: 'lots' }, { speed: 'fast' }, { color: 'nope' }, { color: 3 }, { density: Number.NaN }]
  for (const options of bad) {
    const r = rig()
    g.FontFace = class {
      constructor() {
        throw new Error('no side effects before validation')
      }
    }
    assert.throws(() => r.mount(options), Error, JSON.stringify(options))
    assert.equal(r.canvases.length, 0)
    assert.equal(r.rawRoot.children.length, 0)
    assert.equal(r.winCount(), 0)
    assert.equal(r.docCount(), 0)
    assert.equal(r.stats.raf, 0)
    assert.equal(r.fontLog.added.length, 0)
  }
})

test('density 0 draws a still canvas and does not throw', () => {
  const r = rig({ reduced: true })
  r.mount({ density: 0 })()
  assert.equal(r.texts.length, 0)
})

const settle = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

function deferredFace() {
  let resolve!: () => void
  const loaded = new Promise<void>((done) => (resolve = done))
  g.FontFace = class {
    load() {
      return loaded
    }
  }
  return resolve
}

test('reduced motion repaints the still frame once when the font has loaded', async () => {
  const r = rig({ reduced: true })
  const resolve = deferredFace()
  const stop = r.mount()
  const first = r.fades.length
  assert.ok(first > 0)
  resolve()
  await settle()
  assert.equal(r.fades.length, first * 2)
  assert.equal(r.rawRoot.children.length, 1)
  stop()
})

test('a font that loads after unmount repaints nothing', async () => {
  const r = rig({ reduced: true })
  const resolve = deferredFace()
  r.mount()()
  const first = r.fades.length
  resolve()
  await settle()
  assert.equal(r.fades.length, first)
})

test('animated rain does not repaint when the font loads (every frame sets the font anyway)', async () => {
  const r = rig()
  const resolve = deferredFace()
  const stop = r.mount()
  resolve()
  await settle()
  assert.equal(r.fades.length, 0)
  stop()
})

test('a font load that throws synchronously after the add takes the face back out', () => {
  const r = rig()
  g.FontFace = class {
    load(): Promise<void> {
      throw new Error('boom')
    }
  }
  const stop = r.mount()
  assert.equal(r.fontLog.added.length, 1)
  assert.deepEqual(r.fontLog.deleted, r.fontLog.added)
  assert.equal(r.stats.raf, 1)
  stop()
  assert.equal(r.rawRoot.children.length, 0)
})

test('a build that throws takes the face back out and the canvas off the page, and leaves no listener or frame', () => {
  for (const reduced of [false, true]) {
    const r = rig({ reduced })
    g.FontFace = class {
      load() {
        return new Promise<void>(() => {})
      }
    }
    r.ctx.setTransform = () => {
      throw new Error('no 2d transform')
    }
    assert.throws(() => r.mount(), /no 2d transform/)
    assert.equal(r.fontLog.added.length, 1)
    assert.deepEqual(r.fontLog.deleted, r.fontLog.added)
    assert.equal(r.rawRoot.children.length, 0)
    assert.equal(r.winCount(), 0)
    assert.equal(r.docCount(), 0)
    assert.equal(r.stats.raf, 0)
  }
})

// Every row a column passes is drawn, however far one step moves it: at speed 5 or after a long frame, no dotted trail
for (const reduced of [false, true]) {
  test(`at speed 5 no row between consecutive head positions is skipped (${reduced ? 'reduced pre-fill' : 'animated'})`, () => {
    const random = Math.random
    // every column the same near one, far from the bottom, so nothing wraps and the rows are easy to read
    Math.random = () => 0.9
    try {
      const r = rig({ reduced, height: 100000 })
      r.mount({ speed: 5, density: 0.1 })
      if (!reduced) for (const ms of [34, 34, 250, 34, 34]) r.frame(ms)
      const ys = new Map<number, Set<number>>()
      for (const t of r.texts) (ys.get(t.x) ?? ys.set(t.x, new Set()).get(t.x)!).add(t.y)
      assert.ok(ys.size > 0)
      for (const set of ys.values()) {
        const rows = [...set].sort((a, b) => a - b)
        assert.ok(rows.length > 10)
        for (let i = 1; i < rows.length; i++) assert.equal(rows[i] - rows[i - 1], 16, `rows ${rows[i - 1]} then ${rows[i]}`)
      }
    } finally {
      Math.random = random
    }
  })
}
