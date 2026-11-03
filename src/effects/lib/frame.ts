// The frame loop every animated effect shares: capped, paused on a hidden tab, and gone on stop.
import type { EffectEnv } from './types.ts'

// A tab that was away (or a stalled frame) must not make an effect jump the whole gap at once
const MAX_DT_MS = 250

export function createLoop(env: EffectEnv, draw: (dtMs: number) => void, fps: number): { start(): void; stop(): void } {
  const interval = 1000 / (Number.isFinite(fps) && fps > 0 ? Math.min(fps, 60) : 30)
  let running = false
  let id: number | null = null
  // the grid draws are aligned to, and when the last one really happened (what draw is told)
  let grid: number | null = null
  let drew = 0

  const queue = () => {
    if (!running || id !== null || env.hidden()) return
    id = env.raf(tick)
  }
  function tick() {
    id = null
    // a frame already queued when the tab hid lands here: draw nothing, queue nothing, wait for visibilitychange
    if (!running || env.hidden()) return
    const now = env.now()
    const since = grid === null ? Infinity : now - grid
    if (since >= interval - 1e-6) {
      // carry the remainder so the average holds at fps without ever drawing more often than it
      const dt = grid === null ? 0 : Math.min(now - drew, MAX_DT_MS)
      grid = grid === null || since >= interval * 2 ? now : grid + interval
      drew = now
      draw(dt)
    }
    queue()
  }
  const onVisible = () => {
    if (!running || env.hidden()) return
    // the gap while hidden is not an animation step
    grid = null
    queue()
  }

  return {
    start() {
      if (running) return
      running = true
      grid = null
      document.addEventListener('visibilitychange', onVisible)
      queue()
    },
    stop() {
      if (!running) return
      running = false
      document.removeEventListener('visibilitychange', onVisible)
      if (id !== null) env.caf(id)
      id = null
    },
  }
}
