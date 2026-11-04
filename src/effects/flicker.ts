// CRT flicker: a dark veil over the page whose opacity wobbles between 0 and `amount`, a few steps a second.
// Photosensitivity: both options are capped (a step is never more than 6% and never more than 8 a second, well under
// the general flash threshold), it draws nothing and asks for no frame under reduced motion, and unmount stops the loop.
// Options: amount (0 to .06, .03), hz (steps a second, 1 to 8, 4).
import { createLoop } from './lib/frame.ts'
import { createOverlay, createSlot, darkOf, numberOption } from './lib/overlay.ts'
import type { Mount } from './lib/types.ts'

export const name = 'flicker'
export const layer = 'above'

export const AMOUNT_MAX = 0.06
const HZ_MAX = 8
const slot = createSlot()

export const mount: Mount = (root, options, tokens, env) => {
  const amount = numberOption(options, 'amount', 0.03, 0, AMOUNT_MAX, 'flicker')
  const hz = numberOption(options, 'hz', 4, 1, HZ_MAX, 'flicker')
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
    const veil = (el = createOverlay(root, `opacity:0;background:${darkOf(tokens)}`))
    loop = createLoop(env, () => void (veil.style.opacity = String(Math.random() * amount)), hz)
    loop.start()
  } catch (e) {
    // a failed start must not leave the veil up or the effect claimed (it would stay dead until reload)
    teardown()
    throw e
  }
  return teardown
}
