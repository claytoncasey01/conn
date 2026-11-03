import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { createLoop } from './frame.ts'
import type { EffectEnv } from './types.ts'

type Listener = () => void
const g = globalThis as unknown as Record<string, unknown>
const hadDocument = Object.hasOwn(g, 'document')
const savedDocument = g.document
afterEach(() => {
  if (hadDocument) g.document = savedDocument
  else delete g.document
})

// A clock and a raf queue that only move when the test moves them; document is a bare event target
function rig() {
  let clock = 0
  let hidden = false
  let nextId = 1
  const pending = new Map<number, (t: number) => void>()
  const listeners = new Map<string, Set<Listener>>()
  const stats = { raf: 0, caf: 0 }
  g.document = {
    addEventListener: (t: string, f: Listener) => void (listeners.get(t) ?? listeners.set(t, new Set()).get(t)!).add(f),
    removeEventListener: (t: string, f: Listener) => void listeners.get(t)?.delete(f),
  }
  const env: EffectEnv = {
    reduced: false,
    hidden: () => hidden,
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
    env,
    stats,
    pending: () => pending.size,
    listenerCount: () => [...listeners.values()].reduce((n, s) => n + s.size, 0),
    setHidden(h: boolean) {
      hidden = h
      listeners.get('visibilitychange')?.forEach((f) => f())
    },
    // one display refresh: time moves, and whatever was queued runs
    tick(ms = 1000 / 60) {
      clock += ms
      const run = [...pending.entries()]
      pending.clear()
      for (const [, cb] of run) cb(clock)
    },
  }
}

test('draws at most fps times a second on a 60 Hz display, and does not starve under the cap', () => {
  const r = rig()
  let draws = 0
  createLoop(r.env, () => draws++, 30).start()
  for (let i = 0; i < 60; i++) r.tick()
  assert.ok(draws <= 30, `drew ${draws}`)
  assert.ok(draws >= 28, `drew ${draws}`)
})

test('a cap at the display rate draws every frame', () => {
  const r = rig()
  let draws = 0
  createLoop(r.env, () => draws++, 60).start()
  for (let i = 0; i < 60; i++) r.tick()
  assert.ok(draws >= 58, `drew ${draws}`)
  assert.ok(draws <= 60, `drew ${draws}`)
})

test('draw is told the time since the last draw, and a long gap is clamped', () => {
  const r = rig()
  const dts: number[] = []
  createLoop(r.env, (dt) => dts.push(dt), 10).start()
  r.tick(100)
  r.tick(100)
  r.tick(5000)
  assert.deepEqual(dts, [0, 100, 250])
})

test('while hidden no frame is queued, and one is queued again when the tab shows', () => {
  const r = rig()
  let draws = 0
  createLoop(r.env, () => draws++, 60).start()
  r.tick()
  r.setHidden(true)
  r.tick() // the frame already queued fires: it draws nothing and queues nothing
  const raf = r.stats.raf
  const drawn = draws
  for (let i = 0; i < 10; i++) r.tick()
  assert.equal(r.stats.raf, raf)
  assert.equal(r.pending(), 0)
  assert.equal(draws, drawn)
  r.setHidden(false)
  assert.equal(r.stats.raf, raf + 1)
  r.tick()
  r.tick()
  assert.ok(draws > drawn)
})

test('starting while hidden queues nothing until the tab shows', () => {
  const r = rig()
  r.setHidden(true)
  const loop = createLoop(r.env, () => {}, 30)
  loop.start()
  assert.equal(r.stats.raf, 0)
  r.setHidden(false)
  assert.equal(r.stats.raf, 1)
})

test('a tab that shows and hides again before the next frame does not queue two frames', () => {
  const r = rig()
  createLoop(r.env, () => {}, 30).start()
  r.setHidden(true)
  r.setHidden(false)
  r.setHidden(false)
  assert.equal(r.pending(), 1)
})

test('stop cancels the pending frame, draws nothing after and removes its listener', () => {
  const r = rig()
  let draws = 0
  const loop = createLoop(r.env, () => draws++, 60)
  loop.start()
  assert.equal(r.listenerCount(), 1)
  loop.stop()
  assert.equal(r.stats.caf, 1)
  assert.equal(r.pending(), 0)
  assert.equal(r.listenerCount(), 0)
  r.tick()
  r.setHidden(false)
  assert.equal(draws, 0)
  assert.equal(r.pending(), 0)
})

test('start twice is one loop, and a stopped loop starts again', () => {
  const r = rig()
  let draws = 0
  const loop = createLoop(r.env, () => draws++, 60)
  loop.start()
  loop.start()
  assert.equal(r.pending(), 1)
  assert.equal(r.listenerCount(), 1)
  loop.stop()
  loop.stop()
  loop.start()
  r.tick()
  assert.equal(draws, 1)
})

test('a draw that throws stops the loop from running away, and stop still cleans up', () => {
  const r = rig()
  const loop = createLoop(r.env, () => {
    throw new Error('boom')
  }, 60)
  loop.start()
  assert.throws(() => r.tick(), /boom/)
  loop.stop()
  assert.equal(r.pending(), 0)
  assert.equal(r.listenerCount(), 0)
})

test('a bad fps is a sane cap, never a division by zero or an unbounded loop', () => {
  const r = rig()
  let draws = 0
  createLoop(r.env, () => draws++, 0).start()
  for (let i = 0; i < 120; i++) r.tick()
  assert.ok(draws > 0 && draws <= 62, `drew ${draws}`)
})
