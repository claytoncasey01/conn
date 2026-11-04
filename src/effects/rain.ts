// Falling glyph columns on one canvas behind the page: far columns are smaller, dimmer and slower, near ones larger
// and brighter. The canvas is never cleared, only faded at a constant alpha, which is what leaves the trails.
// Options: density (0 to 1, .6), speed (a multiplier, 1), color (the name of the token the trails use, `ac`).
import { createLoop } from './lib/frame.ts'
import type { Mount } from './lib/types.ts'
import { createColumns, stepColumns } from './rainModel.ts'
import type { Column } from './rainModel.ts'

export const name = 'rain'
export const layer = 'behind'

const FAMILY = 'Conn Matrix Code'
// Vite bundles a file named this way and gives back its hashed URL; Bun resolves it as a path
const FONT_URL = new URL('./fonts/matrix-code/ConnMatrixCode.woff2', import.meta.url).href

// The one color that is not a token: the bright head of a column, nearly white with a green cast
const HEAD = 'rgb(220 255 230)'
const FPS = 30
const FADE = 0.09
const SIZE = { 0: 12, 1: 16 } as const
const ALPHA = { 0: 0.5, 1: 0.9 } as const
const DPR_MAX = 2
const RESIZE_MS = 150
// The still frame for reduced motion is this many steps of the real animation, drawn and wrapped like any other
const PREFILL_STEPS = 120
const PREFILL_DT_MS = 33
// A fast column (speed 5, or a long frame) moves several rows in one step; every row it passed is drawn, up to this many
const MAX_ROWS_PER_STEP = 24

function number(o: unknown, fallback: number, lo: number, hi: number, what: string): number {
  if (o === undefined) return fallback
  if (typeof o !== 'number' || !Number.isFinite(o)) throw new Error(`rain: ${what} must be a number`)
  return Math.min(hi, Math.max(lo, o))
}

// Only when both the API and a document exist (the unit tests have neither); a failed load keeps the fallback font.
// `loaded` runs once the face is ready: a canvas only draws a face that is loaded, so a frame painted before that is in the fallback
function addFont(loaded: () => void): () => void {
  const FontFace = (globalThis as { FontFace?: typeof globalThis.FontFace }).FontFace
  const fonts = (globalThis as { document?: Document }).document?.fonts
  if (typeof FontFace !== 'function' || !fonts) return () => {}
  let face: FontFace | undefined
  const remove = () => {
    try {
      if (face) fonts.delete(face)
    } catch {
      // the font going away is not worth a failed unmount
    }
  }
  try {
    face = new FontFace(FAMILY, `url(${FONT_URL})`)
    fonts.add(face)
    face.load().then(loaded).catch(() => {})
    return remove
  } catch {
    // a throw after the add must not leave the face in the document
    remove()
    return () => {}
  }
}

export const mount: Mount = (root, options, tokens, env) => {
  const density = number(options.density, 0.6, 0, 1, 'density')
  const speed = number(options.speed, 1, 0.1, 5, 'speed')
  const colorName = options.color ?? 'ac'
  const trail = typeof colorName === 'string' ? tokens[colorName] : undefined
  if (typeof trail !== 'string' || !trail) throw new Error('rain: color must name a token')
  const bg = tokens.bg || 'black'

  const canvas = document.createElement('canvas')
  const ctx = canvas.getContext('2d')
  if (!ctx) return () => {}
  canvas.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;pointer-events:none'
  canvas.setAttribute('aria-hidden', 'true')
  root.appendChild(canvas)

  let w = 0
  let h = 0
  let cols: Column[] = []
  // the row each column's head was last drawn at, so a step that moved several rows leaves no gap in the trail
  let lastRow = new WeakMap<Column, number>()
  let mounted = true
  const rng = Math.random

  function draw(dtMs: number) {
    stepColumns(cols, dtMs, h, speed, rng)
    ctx!.globalAlpha = FADE
    ctx!.fillStyle = bg
    ctx!.fillRect(0, 0, w, h)
    ctx!.textBaseline = 'top'
    ctx!.textAlign = 'center'
    let sized = -1
    for (const c of cols) {
      // still above the top
      if (c.y < 0) {
        lastRow.delete(c)
        continue
      }
      if (c.depth !== sized) {
        sized = c.depth
        ctx!.font = `${SIZE[c.depth]}px '${FAMILY}', monospace`
      }
      const size = SIZE[c.depth]
      const row = Math.floor(c.y / size)
      const glyph = (r: number) => c.glyphs[((r % c.glyphs.length) + c.glyphs.length) % c.glyphs.length]
      ctx!.globalAlpha = ALPHA[c.depth]
      // the cells the head just left turn to the trail color; the fade does the rest
      const last = lastRow.get(c)
      const from = Math.max(0, row - MAX_ROWS_PER_STEP, Math.min(last === undefined || last > row ? row - 1 : last, row - 1))
      ctx!.fillStyle = trail as string
      for (let r = from; r < row; r++) ctx!.fillText(glyph(r), c.x, r * size)
      lastRow.set(c, row)
      ctx!.fillStyle = HEAD
      ctx!.fillText(glyph(row), c.x, row * size)
    }
  }

  // sizes the canvas (which also clears it), makes new columns, and for reduced motion paints the one still frame
  function build() {
    w = root.clientWidth || globalThis.innerWidth || 1280
    h = root.clientHeight || globalThis.innerHeight || 720
    const dpr = Math.min(Number(globalThis.devicePixelRatio) || 1, DPR_MAX)
    canvas.width = Math.floor(w * dpr)
    canvas.height = Math.floor(h * dpr)
    ctx!.setTransform(dpr, 0, 0, dpr, 0, 0)
    cols = createColumns(w, h, density, rng)
    lastRow = new WeakMap()
    if (env.reduced) for (let i = 0; i < PREFILL_STEPS; i++) draw(PREFILL_DT_MS)
  }

  // the font goes in before the first frame; reduced motion paints one still frame only, so it is painted again when the font is in
  const removeFont = addFont(() => {
    if (mounted && env.reduced) build()
  })
  let loop: ReturnType<typeof createLoop> | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  const onResize = () => {
    clearTimeout(timer)
    timer = setTimeout(() => {
      timer = undefined
      build()
    }, RESIZE_MS)
  }
  const teardown = () => {
    mounted = false
    loop?.stop()
    clearTimeout(timer)
    window.removeEventListener('resize', onResize)
    canvas.remove()
    removeFont()
  }
  try {
    build()
    loop = env.reduced ? null : createLoop(env, draw, FPS)
    loop?.start()
    window.addEventListener('resize', onResize)
  } catch (e) {
    // a failed build must not leave the font in the document or the canvas on the page
    teardown()
    throw e
  }
  return teardown
}
