// CRT vignette: the page fades toward the theme's bg at the corners, as a CSS radial gradient. With glass, the tube's face is
// drawn too: a rounded edge whose corners go dark and a soft highlight on the glass at the top left, in the theme's bright.
// Options: strength (0 to .6, .55), how far the corners go toward bg; glass (true or false, false).
// Static: it draws the same under reduced motion and asks for no frame. The one overlay opacity caps the shading and the
// highlight together.
import { booleanOption, createOverlay, createSlot, numberOption } from './lib/overlay.ts'
import type { Mount } from './lib/types.ts'

export const name = 'vignette'
export const layer = 'above'

export const STRENGTH_MAX = 0.6
const slot = createSlot()

export const mount: Mount = (root, options, tokens) => {
  const strength = numberOption(options, 'strength', 0.55, 0, STRENGTH_MAX, 'vignette')
  const glass = booleanOption(options, 'glass', false, 'vignette')
  const release = slot.claim()
  if (!release) return () => {}
  let el: HTMLElement
  try {
    const bg = tokens.bg || 'black'
    // farthest-corner is the 100% stop, so only the very corner reaches the full strength
    const fade = `radial-gradient(ellipse at center,transparent 55%,${bg} 100%)`
    // the glass: its rounded corners filled from outside (the spread), the edge shaded from inside, and a highlight up and left
    const face = `border-radius:22px;box-shadow:0 0 0 48px ${bg}, inset 0 0 120px 24px ${bg};background:radial-gradient(ellipse 55% 40% at 28% 16%,color-mix(in srgb, ${tokens.bright || 'white'} 10%, transparent),transparent 70%),${fade}`
    el = createOverlay(root, `opacity:${strength};${glass ? face : `background:${fade}`}`)
  } catch (e) {
    release()
    throw e
  }
  return () => {
    el.remove()
    release()
  }
}
