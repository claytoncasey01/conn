import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import * as grain from './grain.ts'
import type { EffectEnv, EffectOptions } from './lib/types.ts'

type El = { tag: string; style: Record<string, string> & { cssText: string }; attrs: Record<string, string>; parent: Root | null; setAttribute(k: string, v: string): void; remove(): void; width?: number; height?: number }
type Root = { children: El[]; appendChild(el: El): El }
const g = globalThis as unknown as Record<string, unknown>
const saved = { had: Object.hasOwn(g, 'document'), value: g.document }
const stops: (() => void)[] = []
afterEach(() => {
  for (const s of stops.splice(0)) s()
  if (saved.had) g.document = saved.value
  else delete g.document
})

const tokens = { bg: '#010a04', tx: '#a8ffb8' }

function rig(opts: { reduced?: boolean; hidden?: boolean } = {}) {
  const root: Root = { children: [], appendChild(el) { el.parent = root; root.children.push(el); return el } }
  const fills: { style: string; alpha: number }[] = []
  let listeners = 0
  g.document = {
    createElement(tag: string) {
      const el: El = {
        tag,
        style: { cssText: '' } as El['style'],
        attrs: {},
        parent: null,
        setAttribute: (k, v) => void (el.attrs[k] = v),
        remove() {
          if (el.parent) el.parent.children.splice(el.parent.children.indexOf(el), 1)
          el.parent = null
        },
      }
      if (tag === 'canvas') {
        const ctx = { fillStyle: '', globalAlpha: 1, fillRect: () => void fills.push({ style: ctx.fillStyle, alpha: ctx.globalAlpha }) }
        Object.assign(el, { getContext: () => ctx, toDataURL: () => 'data:image/png;base64,AAAA' })
      }
      return el
    },
    addEventListener: () => void listeners++,
    removeEventListener: () => void listeners--,
  }
  let clock = 0
  const pending = new Map<number, (t: number) => void>()
  let id = 1
  const stats = { raf: 0 }
  const env: EffectEnv = {
    reduced: !!opts.reduced,
    hidden: () => !!opts.hidden,
    raf: (cb) => (stats.raf++, pending.set(id, cb), id++),
    caf: (i) => void pending.delete(i),
    now: () => clock,
  }
  return {
    root: root as unknown as HTMLElement, raw: root, env, stats, fills, listeners: () => listeners, pending: () => pending.size,
    frame(ms: number) {
      clock += ms
      const run = [...pending.values()]
      pending.clear()
      for (const cb of run) cb(clock)
    },
  }
}
const mountOf = (r: ReturnType<typeof rig>, o: EffectOptions = {}) => {
  const stop = grain.mount(r.root, o, tokens, r.env)
  stops.push(stop)
  return stop
}

test('grain: named, above the page, one hidden pointer-proof element tiled with a noise tile made in code from the theme', () => {
  assert.equal(grain.name, 'grain')
  assert.equal(grain.layer, 'above')
  const r = rig()
  mountOf(r)
  assert.equal(r.raw.children.length, 1, 'the tile canvas is never added to the page')
  const el = r.raw.children[0]
  assert.equal(el.attrs['aria-hidden'], 'true')
  assert.match(el.style.cssText, /pointer-events:none/)
  assert.match(el.style.cssText, /background-image:url\(data:image\/png;base64,AAAA\)/)
  assert.ok(r.fills.length > 1000, 'a tile of specks')
  assert.ok(r.fills.every((f) => f.style === tokens.tx || f.style === tokens.bg), 'specks in the theme\'s text and bg only')
  assert.ok(r.fills.every((f) => f.alpha >= 0 && f.alpha <= 1))
})

test('grain: strength is capped at .12 and its steps at 8 a second, so the noise shimmers but never flashes', () => {
  const at = (o: EffectOptions) => {
    const r = rig()
    const stop = grain.mount(r.root, o, tokens, r.env)
    const el = r.raw.children[0]
    const opacity = Number(/opacity:([\d.]+)/.exec(el.style.cssText)![1])
    // count the steps in two seconds at a fine frame rate
    const seen = new Set<string>()
    for (let i = 0; i < 120; i++) {
      r.frame(1000 / 60)
      seen.add(el.style.backgroundPosition ?? '')
    }
    stop()
    return { opacity, steps: seen.size }
  }
  assert.equal(at({ strength: 9 }).opacity, 0.12)
  assert.equal(at({}).opacity, 0.06)
  assert.ok(at({ hz: 60 }).steps <= 17, 'at most 8 steps a second')
  assert.throws(() => grain.mount(rig().root, { strength: 'x' }, tokens, rig().env), /strength/)
})

test('grain: under reduced motion it is one still tile and asks for no frame; a hidden tab asks for none; unmount leaves nothing', () => {
  // one mount at a time (the effect does not stack), so each is stopped before the next draws
  const still = rig({ reduced: true })
  const stopStill = mountOf(still)
  assert.equal(still.raw.children.length, 1)
  assert.equal(still.stats.raf, 0)
  stopStill()
  const hidden = rig({ hidden: true })
  const stopHidden = mountOf(hidden)
  assert.equal(hidden.raw.children.length, 1)
  assert.equal(hidden.stats.raf, 0)
  stopHidden()
  const r = rig()
  const stop = mountOf(r)
  r.frame(200)
  stop()
  assert.equal(r.raw.children.length, 0)
  assert.equal(r.pending(), 0)
  assert.equal(r.listeners(), 0)
})
