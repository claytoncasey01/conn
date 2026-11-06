// The terminal's colours, derived from the theme's tokens, so every theme dresses its terminal with no change to the theme
// files. Pure: tokens in, an xterm.js theme out. Every colour is resolved here to an opaque #rrggbb, because xterm.js
// parses other forms (oklch, a translucent colour) through a canvas, and because what is measured here must be what is drawn.
//
// The rules, each checked for every built-in look (terminalTheme.test.ts):
// - The text (tx) is at least TEXT_CONTRAST_MIN on the background (bg); failing that it is moved toward black or white.
// - The 16 colours: red, green, yellow, blue, magenta and cyan are the theme's bad, ok, warn, info, role-advisor and
//   k-subtask; their bright forms are each mixed toward the theme's bright. The four neutrals follow the background's side:
//   on a dark background black is a paper colour just off the background, bright black is mu, white is the text and bright
//   white is bright; on a light one black is the text, bright black mu, and white and bright white are paper colours.
//   Every colour but the paper ones is "ink" and is moved toward the text until it is at least TEXT_CONTRAST_MIN on the
//   background (the text itself passes, so the walk ends).
// - The cursor is the theme's focus (its keyboard focus and text cursor colour), at least CURSOR_CONTRAST_MIN on the
//   background, and the character under it is drawn in the background colour.
// - A selection is the accent washed into the background, thinned until the text on it is at least TEXT_CONTRAST_MIN.
import type { ITheme } from '@xterm/xterm'
import defaultTheme from '../../themes/default/theme.json'
import { composite, contrast, parseColor, type Rgba } from './theme/color.ts'
import type { TokenSet } from './theme/types.ts'

export type XtermTheme = ITheme

/** WCAG's ratio for body text. */
export const TEXT_CONTRAST_MIN = 4.5
/** WCAG's ratio for a part of the interface that is not text: the cursor. */
export const CURSOR_CONTRAST_MIN = 3

type Ansi = 'black' | 'red' | 'green' | 'yellow' | 'blue' | 'magenta' | 'cyan' | 'white' | 'brightBlack' | 'brightRed' | 'brightGreen' | 'brightYellow' | 'brightBlue' | 'brightMagenta' | 'brightCyan' | 'brightWhite'

const CHROMATIC = ['red', 'green', 'yellow', 'blue', 'magenta', 'cyan'] as const
type Chromatic = (typeof CHROMATIC)[number]
const BRIGHT_OF: Record<Chromatic, Ansi> = { red: 'brightRed', green: 'brightGreen', yellow: 'brightYellow', blue: 'brightBlue', magenta: 'brightMagenta', cyan: 'brightCyan' }
/** The token each of the six hues is drawn from. */
const HUE_TOKEN: Record<Chromatic, string> = { red: 'bad', green: 'ok', yellow: 'warn', blue: 'info', magenta: 'role-advisor', cyan: 'k-subtask' }
const CHROMATIC_INK: readonly Ansi[] = [...CHROMATIC, ...CHROMATIC.map(c => BRIGHT_OF[c])]

/** The colours a program writes text in on a dark background: all but black, which is a paper colour there. */
export const ANSI_INK_DARK: readonly Ansi[] = [...CHROMATIC_INK, 'brightBlack', 'white', 'brightWhite']
/** The colours a program writes text in on a light background: all but white and bright white. */
export const ANSI_INK_LIGHT: readonly Ansi[] = [...CHROMATIC_INK, 'black', 'brightBlack']

/** How far a bright colour is mixed toward the theme's bright. */
const BRIGHT_MIX = 0.35
/** How far a paper colour is mixed from the background toward the text. */
const PAPER_MIX = 0.12
/** How much accent a selection starts with, and how much an unfocused one has (of mu). */
const SELECTION_MIX = 0.35
const SELECTION_INACTIVE_MIX = 0.22
/** One step of a walk toward a colour; MIX_STEPS_MAX of them reach it. */
const MIX_STEP = 0.05
const MIX_STEPS_MAX = 20

const BLACK: Rgba = { r: 0, g: 0, b: 0, a: 1 }
const WHITE: Rgba = { r: 255, g: 255, b: 255, a: 1 }
const FALLBACK = (defaultTheme as { tokens: { dark: Record<string, string> } }).tokens.dark

/** A token as an opaque colour over `under`; Default's dark value when the theme's does not parse. */
function tokenColor(tokens: Readonly<TokenSet>, name: string, under: Rgba): Rgba {
  const own = tokens[name] === undefined ? null : parseColor(tokens[name])
  const parsed = own ?? parseColor(FALLBACK[name] ?? '') ?? under
  return composite(parsed, under)
}

/** `from` moved `amount` (0 to 1) of the way to `to`, in sRGB. */
function mix(from: Rgba, to: Rgba, amount: number): Rgba {
  const channel = (a: number, b: number) => Math.round(a + (b - a) * amount)
  return { r: channel(from.r, to.r), g: channel(from.g, to.g), b: channel(from.b, to.b), a: 1 }
}

/** `ink` moved toward `toward` in MIX_STEP steps until it is at least `min` on `paper`: `toward` itself must pass. */
function readable(ink: Rgba, paper: Rgba, toward: Rgba, min: number): Rgba {
  for (let step = 0; step <= MIX_STEPS_MAX; step++) {
    const tried = mix(ink, toward, step * MIX_STEP)
    if (contrast(tried, paper) >= min) return tried
  }
  return toward
}

/** Black or white, whichever is further from `paper`: one of them is always more than 4.5:1. */
const extreme = (paper: Rgba): Rgba => (contrast(BLACK, paper) >= contrast(WHITE, paper) ? BLACK : WHITE)

/** The most of `tint` washed into `paper`, from `start` down, that keeps `text` at least TEXT_CONTRAST_MIN on it. */
function wash(paper: Rgba, tint: Rgba, text: Rgba, start: number): Rgba {
  for (let step = 0; step <= MIX_STEPS_MAX; step++) {
    const tried = mix(paper, tint, Math.max(0, start - step * MIX_STEP))
    if (contrast(text, tried) >= TEXT_CONTRAST_MIN) return tried
  }
  return paper
}

const hex = (c: Rgba): string => '#' + [c.r, c.g, c.b].map(n => n.toString(16).padStart(2, '0')).join('')

/** The theme the page's terminal wears, from the theme's resolved tokens. */
export function terminalTheme(tokens: Readonly<TokenSet>): XtermTheme {
  // A translucent background is laid over black: the page behind a terminal is not known here.
  const background = tokenColor(tokens, 'bg', BLACK)
  const foreground = readable(tokenColor(tokens, 'tx', background), background, extreme(background), TEXT_CONTRAST_MIN)
  const bright = tokenColor(tokens, 'bright', background)
  const dark = contrast(background, BLACK) < contrast(foreground, BLACK)
  const ink = (c: Rgba) => readable(c, background, foreground, TEXT_CONTRAST_MIN)
  const paper = mix(background, foreground, PAPER_MIX)
  const muted = ink(tokenColor(tokens, 'mu', background))

  const ansi = {} as Record<Ansi, Rgba>
  for (const hue of CHROMATIC) {
    const base = tokenColor(tokens, HUE_TOKEN[hue], background)
    ansi[hue] = ink(base)
    ansi[BRIGHT_OF[hue]] = ink(mix(base, bright, BRIGHT_MIX))
  }
  ansi.brightBlack = muted
  if (dark) {
    ansi.black = paper
    ansi.white = foreground
    ansi.brightWhite = ink(bright)
  } else {
    ansi.black = foreground
    ansi.white = paper
    ansi.brightWhite = background
  }

  const cursor = readable(tokenColor(tokens, 'focus', background), background, foreground, CURSOR_CONTRAST_MIN)
  const accent = tokenColor(tokens, 'ac', background)
  const theme: XtermTheme = {
    background: hex(background),
    foreground: hex(foreground),
    cursor: hex(cursor),
    cursorAccent: hex(background),
    selectionBackground: hex(wash(background, accent, foreground, SELECTION_MIX)),
    selectionForeground: hex(foreground),
    selectionInactiveBackground: hex(wash(background, muted, foreground, SELECTION_INACTIVE_MIX)),
  }
  for (const name of Object.keys(ansi) as Ansi[]) theme[name] = hex(ansi[name])
  return theme
}

/**
 * The terminal's font: the theme's monospace family, ending in the generic `monospace`. A theme's list ends in
 * `ui-monospace`, which a canvas does not know, and xterm.js measures its cells on a canvas: without a family the canvas
 * knows, a cell is measured in the canvas's serif default and every character is drawn spaced apart.
 */
export function terminalFont(tokens: Readonly<TokenSet>): string {
  const family = tokens['font-mono']
  return family === undefined ? 'monospace' : `${family}, monospace`
}
