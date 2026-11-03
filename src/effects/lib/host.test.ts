import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createEffectsHost } from './host.ts'
import type { EffectEnv, EffectLayer, EffectOptions, Mount, RegisteredEffect } from './types.ts'

// A root that only counts children; an effect adds one on mount and takes it away on its stop
type Root = { children: string[]; removed: number; created: number; replaceChildren(): void; remove(): void; lastElementChild: { remove(): void } | null }
const root = (): Root => {
  const r: Root = {
    children: [], removed: 0, created: 0, replaceChildren: () => (r.children.length = 0), remove: () => void r.removed++,
    get lastElementChild() {
      return r.children.length ? { remove: () => void r.children.pop() } : null
    },
  }
  return r
}

const behind = (mount: Mount): RegisteredEffect => ({ mount, layer: 'behind' })
const above = (mount: Mount): RegisteredEffect => ({ mount, layer: 'above' })
const behindAll = (ms: Record<string, Mount>): Record<string, RegisteredEffect> => Object.fromEntries(Object.entries(ms).map(([k, m]) => [k, behind(m)]))

const env = (over: Partial<EffectEnv> = {}): EffectEnv => ({
  reduced: false, hidden: () => false, raf: () => 0, caf: () => {}, now: () => 0, ...over,
})

type Call = { name: string; options: EffectOptions; tokens: Readonly<Record<string, string>>; env: EffectEnv; before: number }
function rig(e: EffectEnv = env()) {
  const r = root()
  const calls: Call[] = []
  const stops: string[] = []
  const effect = (name: string): Mount => (rt, options, tokens, en) => {
    calls.push({ name, options, tokens, env: en, before: (rt as unknown as Root).children.length })
    ;(rt as unknown as Root).children.push(name)
    return () => {
      stops.push(name)
      const i = (rt as unknown as Root).children.indexOf(name)
      if (i >= 0) (rt as unknown as Root).children.splice(i, 1)
    }
  }
  const registry = { rain: behind(effect('rain')), scanlines: behind(effect('scanlines')) }
  let envNow = e
  const host = createEffectsHost({
    registry,
    layer: () => {
      r.created++
      return r as unknown as HTMLElement
    },
    env: () => envNow,
  })
  return { r, calls, stops, host, setEnv: (x: EffectEnv) => void (envNow = x) }
}
const tokens = { bg: '#000', ac: '#0f0' }

test('apply mounts the named effects in order and skips an unknown name', () => {
  const t = rig()
  t.host.apply([{ name: 'scanlines' }, { name: 'nope' }, { name: 'rain', options: { speed: 2 } }], tokens, true)
  assert.deepEqual(t.calls.map((c) => c.name), ['scanlines', 'rain'])
  assert.deepEqual(t.r.children, ['scanlines', 'rain'])
  assert.deepEqual(t.calls[1].options, { speed: 2 })
  assert.deepEqual(t.calls[1].tokens, tokens)
})

test('a name off the registry object (constructor, __proto__, toString) is unknown, not a mount', () => {
  const t = rig()
  t.host.apply([{ name: 'constructor' }, { name: '__proto__' }, { name: 'toString' }, { name: 'hasOwnProperty' }], tokens, true)
  assert.equal(t.calls.length, 0)
  assert.equal(t.r.created, 0)
})

test('apply again unmounts the first set before the new mount', () => {
  const t = rig()
  t.host.apply([{ name: 'rain' }, { name: 'scanlines' }], tokens, true)
  t.host.apply([{ name: 'rain', options: { n: 1 } }], tokens, true)
  assert.deepEqual(t.stops, ['scanlines', 'rain'])
  assert.equal(t.calls[2].before, 0)
  assert.deepEqual(t.r.children, ['rain'])
})

test('the same effects and tokens again are not remounted, a changed token or option is', () => {
  const t = rig()
  t.host.apply([{ name: 'rain', options: { n: 1 } }], { ...tokens }, true)
  t.host.apply([{ name: 'rain', options: { n: 1 } }], { ...tokens }, true)
  assert.equal(t.calls.length, 1)
  assert.deepEqual(t.stops, [])
  t.host.apply([{ name: 'rain', options: { n: 1 } }], { ...tokens, ac: '#f00' }, true)
  assert.equal(t.calls.length, 2)
  t.host.apply([{ name: 'rain', options: { n: 2 } }], { ...tokens, ac: '#f00' }, true)
  assert.equal(t.calls.length, 3)
  assert.deepEqual(t.stops, ['rain', 'rain'])
  assert.deepEqual(t.r.children, ['rain'])
})

test('a change of reduced motion remounts, so the effect hears it', () => {
  const t = rig()
  t.host.apply([{ name: 'rain' }], tokens, true)
  t.setEnv(env({ reduced: true }))
  t.host.apply([{ name: 'rain' }], tokens, true)
  assert.equal(t.calls.length, 2)
  assert.equal(t.calls[1].env.reduced, true)
})

test('enabled false mounts nothing and unmounts what was there, taking the layer away', () => {
  const t = rig()
  t.host.apply([{ name: 'rain' }], tokens, false)
  assert.equal(t.calls.length, 0)
  assert.equal(t.r.created, 0)
  t.host.apply([{ name: 'rain' }], tokens, true)
  t.host.apply([{ name: 'rain' }], tokens, false)
  assert.deepEqual(t.stops, ['rain'])
  assert.deepEqual(t.r.children, [])
  assert.equal(t.r.removed, 1)
  t.host.apply([{ name: 'rain' }], tokens, true)
  assert.deepEqual(t.r.children, ['rain'])
})

test('an empty effect list leaves no layer', () => {
  const t = rig()
  t.host.apply([{ name: 'rain' }], tokens, true)
  t.host.apply([], tokens, true)
  assert.equal(t.r.removed, 1)
  assert.deepEqual(t.r.children, [])
})

test('dispose leaves nothing: effects stopped, layer removed, and apply after it works again', () => {
  const t = rig()
  t.host.apply([{ name: 'rain' }, { name: 'scanlines' }], tokens, true)
  t.host.dispose()
  assert.deepEqual(t.stops.sort(), ['rain', 'scanlines'])
  assert.deepEqual(t.r.children, [])
  assert.equal(t.r.removed, 1)
  t.host.dispose()
  assert.equal(t.r.removed, 1)
  t.host.apply([{ name: 'rain' }], tokens, true)
  assert.deepEqual(t.r.children, ['rain'])
})

test('env().reduced is passed through to mount', () => {
  const t = rig(env({ reduced: true }))
  t.host.apply([{ name: 'rain' }], tokens, true)
  assert.equal(t.calls[0].env.reduced, true)
})

test('options are data: a non-primitive value is dropped before mount, and the effect cannot change the theme object', () => {
  const t = rig()
  const options = { n: 1, s: 'x', b: true, bad: { a: 1 }, f: () => 1, nan: Number.NaN, inf: Infinity, nul: null, u: undefined } as unknown as EffectOptions
  t.host.apply([{ name: 'rain', options }], tokens, true)
  assert.deepEqual(t.calls[0].options, { n: 1, s: 'x', b: true })
  assert.notEqual(t.calls[0].options, options)
  assert.equal(Object.isFrozen(t.calls[0].tokens), true)
  assert.throws(() => {
    'use strict'
    ;(t.calls[0].tokens as Record<string, string>).bg = 'red'
  })
  assert.equal(tokens.bg, '#000')
})

test('an effect that throws after adding to the layer leaves nothing behind, and the rest still mount, on every identical apply', () => {
  const r = root()
  const mounted: string[] = []
  const host = createEffectsHost({
    registry: behindAll({
      rain: (rt) => {
        ;(rt as unknown as Root).children.push('canvas')
        throw new Error('bad option')
      },
      scanlines: (rt) => {
        ;(rt as unknown as Root).children.push('scanlines')
        mounted.push('scanlines')
        return () => {}
      },
    }),
    layer: () => r as unknown as HTMLElement,
    env: () => env(),
  })
  host.apply([{ name: 'rain' }, { name: 'scanlines' }], tokens, true)
  assert.deepEqual(mounted, ['scanlines'])
  assert.deepEqual(r.children, ['scanlines'])
  // a focus refresh: the same apply again is skipped and must not have an orphan to keep
  host.apply([{ name: 'rain' }, { name: 'scanlines' }], tokens, true)
  assert.deepEqual(mounted, ['scanlines'])
  assert.deepEqual(r.children, ['scanlines'])
})

test('a mount that throws after several nodes loses all of them, and a healthy effect before it keeps its own', () => {
  const r = root()
  const host = createEffectsHost({
    registry: behindAll({
      scanlines: (rt) => {
        ;(rt as unknown as Root).children.push('scanlines')
        return () => {}
      },
      rain: (rt) => {
        ;(rt as unknown as Root).children.push('canvas', 'overlay')
        throw new Error('late')
      },
    }),
    layer: () => r as unknown as HTMLElement,
    env: () => env(),
  })
  host.apply([{ name: 'scanlines' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(r.children, ['scanlines'])
})

test('a mount that returns no stop cannot be stopped later, so what it added is removed now', () => {
  const r = root()
  const host = createEffectsHost({
    registry: behindAll({
      rain: (rt) => {
        ;(rt as unknown as Root).children.push('canvas')
        return undefined as unknown as () => void
      },
      scanlines: (rt) => {
        ;(rt as unknown as Root).children.push('scanlines')
        return () => {}
      },
    }),
    layer: () => r as unknown as HTMLElement,
    env: () => env(),
  })
  host.apply([{ name: 'rain' }, { name: 'scanlines' }], tokens, true)
  assert.deepEqual(r.children, ['scanlines'])
})

test('a stop that throws does not keep the others from stopping, and the layer is swept', () => {
  const r = root()
  const stopped: string[] = []
  const host = createEffectsHost({
    registry: behindAll({
      rain: (rt) => {
        ;(rt as unknown as Root).children.push('rain')
        return () => {
          throw new Error('stuck')
        }
      },
      scanlines: (rt) => {
        ;(rt as unknown as Root).children.push('scanlines')
        return () => void stopped.push('scanlines')
      },
    }),
    layer: () => r as unknown as HTMLElement,
    env: () => env(),
  })
  host.apply([{ name: 'rain' }, { name: 'scanlines' }], tokens, true)
  host.apply([], tokens, true)
  assert.deepEqual(stopped, ['scanlines'])
  assert.deepEqual(r.children, [])
})

test('a layer that cannot be made leaves no effect, no throw, and the next apply tries again', () => {
  const r = root()
  let fail = true
  const mounted: string[] = []
  const host = createEffectsHost({
    registry: behindAll({ rain: () => (mounted.push('rain'), () => {}) }),
    layer: () => {
      if (fail) throw new Error('no body yet')
      return r as unknown as HTMLElement
    },
    env: () => env(),
  })
  host.apply([{ name: 'rain' }], tokens, true)
  assert.deepEqual(mounted, [])
  fail = false
  host.apply([{ name: 'rain' }], tokens, true)
  assert.deepEqual(mounted, ['rain'])
})

// Two layers: each a root of its own, an effect mounted into the one it names
type Two = { behind: Root; above: Root; asked: EffectLayer[]; mounts: string[]; stops: string[]; host: ReturnType<typeof createEffectsHost>; failLayer: Set<EffectLayer> }
function two(): Two {
  const t = { behind: root(), above: root(), asked: [] as EffectLayer[], mounts: [] as string[], stops: [] as string[], failLayer: new Set<EffectLayer>() } as Two
  const add = (name: string, throws = false): Mount => (rt) => {
    ;(rt as unknown as Root).children.push(name)
    if (throws) throw new Error('bad')
    t.mounts.push(name)
    return () => {
      t.stops.push(name)
      const c = (rt as unknown as Root).children
      c.splice(c.indexOf(name), 1)
    }
  }
  t.host = createEffectsHost({
    registry: { rain: behind(add('rain')), tube: above(add('tube')), broken: above(add('broken', true)), brokenRain: behind(add('brokenRain', true)) },
    layer: (which) => {
      t.asked.push(which)
      if (t.failLayer.has(which)) throw new Error(`no ${which}`)
      t[which].created++
      return t[which] as unknown as HTMLElement
    },
    env: () => env(),
  })
  return t
}

test('an effect is mounted into the layer it names: one of each go to their own layer', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.behind.children, ['rain'])
  assert.deepEqual(t.above.children, ['tube'])
  assert.deepEqual(t.asked.sort(), ['above', 'behind'])
})

test('a list with only behind effects never asks for the above layer', () => {
  const t = two()
  t.host.apply([{ name: 'rain' }], tokens, true)
  assert.deepEqual(t.asked, ['behind'])
  assert.equal(t.above.created, 0)
})

test('a list with only above effects never asks for the behind layer', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }], tokens, true)
  assert.deepEqual(t.asked, ['above'])
  assert.equal(t.behind.created, 0)
})

test('switching to a list with no above effect stops it and removes the above layer, keeping the behind one', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  t.host.apply([{ name: 'rain', options: { n: 1 } }], tokens, true)
  assert.equal(t.above.removed, 1)
  assert.deepEqual(t.above.children, [])
  assert.equal(t.behind.removed, 0)
  assert.deepEqual(t.behind.children, ['rain'])
  assert.deepEqual(t.stops.sort(), ['rain', 'tube'])
  // the behind layer node is reused, not remade
  assert.equal(t.behind.created, 1)
})

test('switching to a list with no behind effect removes the behind layer', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  t.host.apply([{ name: 'tube' }], tokens, true)
  assert.equal(t.behind.removed, 1)
  assert.equal(t.above.removed, 0)
  assert.deepEqual(t.above.children, ['tube'])
})

test('dispose stops every effect and removes both layers, and a second dispose is a no-op', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  t.host.dispose()
  assert.deepEqual(t.stops.sort(), ['rain', 'tube'])
  assert.deepEqual([t.behind.removed, t.above.removed], [1, 1])
  assert.deepEqual([t.behind.children, t.above.children], [[], []])
  t.host.dispose()
  assert.deepEqual([t.behind.removed, t.above.removed], [1, 1])
  t.host.apply([{ name: 'tube' }], tokens, true)
  assert.deepEqual(t.above.children, ['tube'])
})

test('enabled false takes both layers away', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, false)
  assert.deepEqual([t.behind.removed, t.above.removed], [1, 1])
})

test('applying the same two-layer list twice is a no-op: no remount, no new layer', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], { ...tokens }, true)
  assert.deepEqual(t.mounts, ['tube', 'rain'])
  assert.deepEqual(t.stops, [])
  assert.deepEqual([t.behind.created, t.above.created], [1, 1])
})

test('a changed option remounts both layers', () => {
  const t = two()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  t.host.apply([{ name: 'tube', options: { n: 1 } }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.stops.sort(), ['rain', 'tube'])
  assert.deepEqual(t.behind.children, ['rain'])
  assert.deepEqual(t.above.children, ['tube'])
})

test('a failed mount in the above layer leaves its nodes removed and the behind effect running, on every identical apply', () => {
  const t = two()
  t.host.apply([{ name: 'broken' }, { name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.above.children, ['tube'])
  assert.deepEqual(t.behind.children, ['rain'])
  t.host.apply([{ name: 'broken' }, { name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.above.children, ['tube'])
  assert.deepEqual(t.behind.children, ['rain'])
})

test('a failed mount in the behind layer leaves the above effect running and no orphan node', () => {
  const t = two()
  t.host.apply([{ name: 'brokenRain' }, { name: 'tube' }], tokens, true)
  assert.deepEqual(t.behind.children, [])
  assert.deepEqual(t.above.children, ['tube'])
})

test('an above layer that cannot be made leaves no above effect, the behind one still runs, and the next apply tries again', () => {
  const t = two()
  t.failLayer.add('above')
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.behind.children, ['rain'])
  assert.deepEqual(t.above.children, [])
  t.failLayer.clear()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.above.children, ['tube'])
  assert.deepEqual(t.behind.children, ['rain'])
})

test('a behind layer that cannot be made leaves no behind effect, the above one still runs, and the next apply tries again', () => {
  const t = two()
  t.failLayer.add('behind')
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.above.children, ['tube'])
  t.failLayer.clear()
  t.host.apply([{ name: 'tube' }, { name: 'rain' }], tokens, true)
  assert.deepEqual(t.behind.children, ['rain'])
  assert.deepEqual(t.above.children, ['tube'])
})

test('a stop that throws in one layer does not keep the other layer from stopping', () => {
  const a = root()
  const b = root()
  const host = createEffectsHost({
    registry: {
      rain: behind((rt) => {
        ;(rt as unknown as Root).children.push('rain')
        return () => {
          throw new Error('stuck')
        }
      }),
      tube: above((rt) => {
        ;(rt as unknown as Root).children.push('tube')
        return () => {}
      }),
    },
    layer: (which) => (which === 'above' ? a : b) as unknown as HTMLElement,
    env: () => env(),
  })
  host.apply([{ name: 'rain' }, { name: 'tube' }], tokens, true)
  host.dispose()
  assert.deepEqual([a.children, b.children], [[], []])
  assert.deepEqual([a.removed, b.removed], [1, 1])
})
