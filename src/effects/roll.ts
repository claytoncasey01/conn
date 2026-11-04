// CRT roll: the tube's refresh band, a soft lighter stripe a third of the screen tall, drifting slowly down the screen and
// round again, in the theme's text and bright colors.
// Options: strength (0 to .06, .04), how light the band's brightest line is; period (seconds for one pass, 3 to 30, 8).
// Flash safety: the band is at most 6% lighter at its brightest and takes at least three seconds to cross the screen, so
// nothing flashes. It draws nothing under reduced motion (a band standing still would read as a stain), a hidden tab pauses
// it, and unmount stops the loop.
import { createLoop } from './lib/frame.ts'
import { createOverlay, createSlot, numberOption } from './lib/overlay.ts'
import type { Mount } from './lib/types.ts'

export const name = 'roll'
export const layer = 'above'

export const STRENGTH_MAX = 0.06
const slot = createSlot()
// the band is 30% of the screen; its top runs from just above the screen (-30%) to just below it (100%). With a background of
// 30% height, position P puts its top at P x 70% of the screen, so P runs from -3/7 to 10/7
const FROM = -42.86
const SPAN = 185.71

export const mount: Mount = (root, options, tokens, env) => {
  const strength = numberOption(options, 'strength', 0.04, 0, STRENGTH_MAX, 'roll')
  const period = numberOption(options, 'period', 8, 3, 30, 'roll')
  if (env.reduced) return () => {}
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
    const pct = (k: number) => +(strength * 100 * k).toFixed(2)
    const band = `linear-gradient(to bottom,transparent 0%,color-mix(in srgb, ${tokens.tx || 'white'} ${pct(0.6)}%, transparent) 55%,color-mix(in srgb, ${tokens.bright || 'white'} ${pct(1)}%, transparent) 70%,transparent 100%)`
    const strip = (el = createOverlay(root, `background:${band};background-repeat:no-repeat;background-size:100% 30%;background-position:0 ${FROM}%`))
    let t = 0
    loop = createLoop(env, (dt) => {
      t = (t + dt / 1000) % period
      strip.style.backgroundPosition = `0 ${(FROM + (t / period) * SPAN).toFixed(2)}%`
    }, 30)
    loop.start()
  } catch (e) {
    teardown()
    throw e
  }
  return teardown
}
