// CRT scanlines: thin dark lines over the page on a fixed pitch, as a CSS gradient. They can crawl slowly down the screen and lie
// over a fine aperture grille in the theme's own phosphor.
// Options: opacity (0 to .2, .14), pitch (px between lines, 2 to 12, 3), crawl (seconds for the lines to drift eight pitches,
// 0 for still lines (the default), else 1 to 20), grille (true or false, false).
// Flash safety: the one overlay opacity caps the lines and the grille together, and a crawl is a slow drift with no change in
// brightness. Reduced motion and a hidden tab hold the lines still; unmount stops the loop.
import { createLoop } from './lib/frame.ts'
import { booleanOption, createOverlay, createSlot, darkOf, numberOption } from './lib/overlay.ts'
import type { Mount } from './lib/types.ts'

export const name = 'scanlines'
export const layer = 'above'

// It draws over content, so the strongest it may ever be is capped here whatever a theme asks for
export const OPACITY_MAX = 0.2
const slot = createSlot()

export const mount: Mount = (root, options, tokens, env) => {
  const opacity = numberOption(options, 'opacity', 0.14, 0, OPACITY_MAX, 'scanlines')
  const pitch = Math.round(numberOption(options, 'pitch', 3, 2, 12, 'scanlines'))
  const crawlAsked = numberOption(options, 'crawl', 0, 0, 20, 'scanlines')
  const crawl = crawlAsked === 0 ? 0 : Math.max(1, crawlAsked)
  const grille = booleanOption(options, 'grille', false, 'scanlines')
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
    const dark = darkOf(tokens)
    const lines = `repeating-linear-gradient(to bottom,transparent 0,transparent ${pitch - 1}px,${dark} ${pitch - 1}px,${dark} ${pitch}px)`
    // the aperture: a lit stripe, a dark one and a gap, a pixel each, in the theme's accent
    const aperture = `repeating-linear-gradient(to right,color-mix(in srgb, ${tokens.ac || 'white'} 22%, transparent) 0 1px,color-mix(in srgb, ${dark} 30%, transparent) 1px 2px,transparent 2px 3px)`
    const lines_ = (el = createOverlay(root, `opacity:${opacity};background:${grille ? `${lines},${aperture}` : lines}`))
    if (crawl > 0 && !env.reduced) {
      const span = pitch * 8
      let t = 0
      loop = createLoop(env, (dt) => {
        t = (t + dt / 1000) % crawl
        lines_.style.backgroundPosition = `0 ${((t / crawl) * span).toFixed(2)}px${grille ? ', 0 0' : ''}`
      }, 30)
      loop.start()
    }
  } catch (e) {
    teardown()
    throw e
  }
  return teardown
}
