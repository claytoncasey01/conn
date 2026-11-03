// CRT grain: the phosphor's fine noise over the page. A small tile of specks in the theme's text and bg colors is drawn once in
// code (no image file), tiled over the page, and shifted to a new place a few times a second so it shimmers.
// Options: strength (0 to .12, .06), how visible the grain is; hz (steps a second, 1 to 8, 6).
// Flash safety: every step shows the same tile, so the page's brightness does not change from one step to the next, and the
// steps are capped at 8 a second. Under reduced motion it is one still tile; a hidden tab pauses it; unmount stops the loop.
import { createLoop } from './lib/frame.ts'
import { createOverlay, createSlot, numberOption } from './lib/overlay.ts'
import type { Mount } from './lib/types.ts'

export const name = 'grain'
export const layer = 'above'

export const STRENGTH_MAX = 0.12
const HZ_MAX = 8
const TILE = 128
const slot = createSlot()

// the tile: every pixel a speck of text or bg at a random strength
function tile(tokens: Readonly<Record<string, string>>): string {
  const canvas = document.createElement('canvas') as HTMLCanvasElement
  canvas.width = TILE
  canvas.height = TILE
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('grain: no 2d canvas')
  const light = tokens.tx || 'white'
  const dark = tokens.bg || 'black'
  for (let y = 0; y < TILE; y++) {
    for (let x = 0; x < TILE; x++) {
      const v = Math.random()
      ctx.fillStyle = v < 0.5 ? dark : light
      ctx.globalAlpha = Math.abs(v - 0.5) * 2
      ctx.fillRect(x, y, 1, 1)
    }
  }
  return canvas.toDataURL()
}

export const mount: Mount = (root, options, tokens, env) => {
  const strength = numberOption(options, 'strength', 0.06, 0, STRENGTH_MAX, 'grain')
  const hz = numberOption(options, 'hz', 6, 1, HZ_MAX, 'grain')
  const release = slot.claim()
  if (!release) return () => {}
  let el: HTMLElement | undefined
  let loop: ReturnType<typeof createLoop> | undefined
  const teardown = () => {
    loop?.stop()
    el?.remove()
    release()
  }
  try {
    const url = tile(tokens)
    const layer = (el = createOverlay(root, `opacity:${strength};background-image:url(${url});background-size:${TILE}px ${TILE}px`))
    if (!env.reduced) {
      loop = createLoop(env, () => void (layer.style.backgroundPosition = `${Math.floor(Math.random() * TILE)}px ${Math.floor(Math.random() * TILE)}px`), hz)
      loop.start()
    }
  } catch (e) {
    teardown()
    throw e
  }
  return teardown
}
