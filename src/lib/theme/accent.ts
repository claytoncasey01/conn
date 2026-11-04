// The free accent: ac, onac, ac-soft and ac-line from a picked hue and chroma. Pure.
import { contrast, parseColor } from './color.ts'
import type { Mode, TokenSet } from './types.ts'

export const SWATCHES: { name: string; hue: number; chroma: number }[] = [
  { name: 'Teal', hue: 195, chroma: 0.12 },
  { name: 'Cobalt', hue: 255, chroma: 0.15 },
  { name: 'Violet', hue: 295, chroma: 0.15 },
  { name: 'Rose', hue: 355, chroma: 0.16 },
  { name: 'Amber', hue: 70, chroma: 0.14 },
  { name: 'Graphite', hue: 260, chroma: 0.02 },
]
export const DEFAULT_ACCENT = SWATCHES[0]

const WHITE = 'oklch(0.99 0 0)'
const BLACK = 'oklch(0.15 0 0)'
const MIN = 4.5
const STEP = 0.02

// Rounded so repeated .02 steps never print as 0.7800000000000001.
const fix = (n: number) => String(Number(n.toFixed(2)))

export const accentTokens = (mode: Mode, hue: number, chroma: number): TokenSet => nudged(mode, mode === 'light' ? 0.5 : 0.78, hue, chroma)

// Exported for the test: the six swatches read at their start L, so only a start L chosen
// in the dead zone (mid lightness, where neither text color reaches 4.5) walks the loop.
export function nudged(mode: Mode, start: number, hue: number, chroma: number): TokenSet {
  let L = start
  // The nudge is bounded by the L range, and at either end one of the two reads at 21:1 or so.
  for (;;) {
    const ac = `oklch(${fix(L)} ${chroma} ${hue})`
    const rgb = parseColor(ac)
    // an unparseable pick (NaN) has nothing to measure: no tokens, so no onac is left pairing with another ac
    if (!rgb) return {}
    const white = contrast(parseColor(WHITE)!, rgb)
    const black = contrast(parseColor(BLACK)!, rgb)
    if (Math.max(white, black) >= MIN || L <= 0 || L >= 1) return derived(ac, white >= black ? WHITE : BLACK)
    L = Math.min(1, Math.max(0, L + (mode === 'light' ? -STEP : STEP)))
  }
}

const derived = (ac: string, onac: string): TokenSet => ({
  ac,
  onac,
  'ac-soft': `color-mix(in oklch, ${ac} 14%, transparent)`,
  'ac-line': `color-mix(in oklch, ${ac} 40%, transparent)`,
  focus: ac,
  'meter-lit': ac,
})
