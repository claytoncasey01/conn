// The app's Dock icon for a theme: the Conn mark drawn the way the theme's `glyph-brand` draws it in the header, on a
// tile of the theme's dark ground, as a 1024 SVG the page turns into the PNG it sends the app (appIcon.svelte.ts). Pure.
// Each drawing keeps to one effect, so it still reads at the 32 to 128 pixels the Dock shows it at: the tube glows, the
// phosphor blooms under coarse scanlines, the pixels stay crisp, and the solid mark is flat like the bundle's icon.
import { BRAND_MARK_SHAPES, BRAND_PIXEL_SHAPES, BRAND_TUBE_SHAPES } from './brandMark.ts'
import { composite, parseColor, type Rgba } from './color.ts'
import { pick, resolveTheme } from './resolve.ts'
import { FALLBACK_ID, ID, type Choice } from './storage.ts'
import type { ThemeEntry, ThemeManifest, TokenSet } from './types.ts'

/** The PNG the app takes (desk.Icon): this many pixels square, at most this many bytes. testdata/app-icon.json holds both sides to them. */
export const ICON_LIMITS = { pixels: 1024, maxBytes: 4 << 20 } as const

export type Drawing = 'solid' | 'tube' | 'pixel' | 'phosphor'

/** What an icon is drawn from: the drawing, the mark's color and the tile's, each `#rrggbb` */
export type IconSpec = { drawing: Drawing; mark: string; ground: string }

/** The icon in the app's bundle (scripts/make-app-icon.swift): Default's Teal on black. An icon equal to it is sent as "classic". */
export const CLASSIC: IconSpec = { drawing: 'solid', mark: '#3bcfcf', ground: '#000000' }

const DRAWINGS: Record<string, Drawing> = { conn: 'solid', 'conn-tube': 'tube', 'conn-pixel': 'pixel', 'conn-phosphor': 'phosphor' }

const BLACK: Rgba = { r: 0, g: 0, b: 0, a: 1 }
const hex = (c: Rgba) => `#${[c.r, c.g, c.b].map((n) => Math.round(n).toString(16).padStart(2, '0')).join('')}`

/** A token's color made opaque over black, as `#rrggbb` */
const opaque = (c: Rgba): string => hex(composite(c, BLACK))

/** `a` with `t` of `b` mixed in, both `#rrggbb` */
function mix(a: string, b: string, t: number): string {
  const [x, y] = [parseColor(a) ?? BLACK, parseColor(b) ?? BLACK]
  return hex({ r: x.r + (y.r - x.r) * t, g: x.g + (y.g - x.g) * t, b: x.b + (y.b - x.b) * t, a: 1 })
}

/**
 * The icon for a theme's resolved tokens. The text glyphs (blocks, prompt, diamond, none) have no drawing of the mark, so
 * they get the solid one. The solid mark sits on black, as the bundle's icon does, so Default at its own Teal is the classic.
 * A theme may give its accent or background as a color-mix() this cannot read: it gets the classic, never a guess.
 */
export function iconSpecOf(tokens: Readonly<TokenSet>): IconSpec {
  const drawing = DRAWINGS[tokens['glyph-brand'] ?? ''] ?? 'solid'
  const [ac, bg] = [parseColor(tokens.ac ?? ''), parseColor(tokens.bg ?? '')]
  if (!ac || !bg) return CLASSIC
  return { drawing, mark: opaque(ac), ground: drawing === 'solid' ? CLASSIC.ground : opaque(bg) }
}

export const isClassic = (spec: IconSpec): boolean =>
  spec.drawing === CLASSIC.drawing && spec.mark === CLASSIC.mark && spec.ground === CLASSIC.ground

/** One string per distinct icon, so the page sends an icon only when it changed */
export const iconKey = (spec: IconSpec): string => (isClassic(spec) ? 'classic' : `${spec.drawing} ${spec.mark} ${spec.ground}`)

/** What Settings > App icon holds: follow the theme, the bundle's icon, or one theme's icon whatever the theme is */
export type IconChoice = 'match' | 'classic' | { theme: string }

/** A stored choice read back: anything else is `match`, the setting's default */
export function iconChoiceOf(v: unknown): IconChoice {
  if (v === 'classic') return 'classic'
  if (typeof v === 'string' && v.startsWith('theme:') && ID.test(v.slice('theme:'.length))) return { theme: v.slice('theme:'.length) }
  return 'match'
}

/** A choice as it is stored, and as the settings' radio values are */
export const iconChoiceText = (c: IconChoice): string => (typeof c === 'string' ? c : `theme:${c.theme}`)

/**
 * The icon a choice means. `match` follows the person's theme, variant and accent; a pinned theme is that theme's own look
 * (its first variant, its own accent). Both are read in dark mode, which every bundled theme has, so a light-mode page
 * does not turn the Dock icon pale. A pinned theme that is no longer listed gives the classic, as Default does.
 */
export function iconSpecFor(choice: IconChoice, args: { list: ThemeEntry[]; theme: Choice; fallback: ThemeManifest }): IconSpec {
  if (choice === 'classic') return CLASSIC
  const base: Choice = typeof choice === 'string' ? args.theme : { id: choice.theme, mode: 'dark', accent: null, variant: null, effects: 'auto' }
  const resolved = resolveTheme({ list: args.list, choice: { ...base, mode: 'dark' }, systemDark: true, accepted: {}, fallback: args.fallback })
  return iconSpecOf(resolved.tokens)
}

/** The themes Settings offers to pin, by name: each listed theme once, but Default, whose own icon is the classic */
export function pinnable(list: ThemeEntry[]): { id: string; name: string }[] {
  const ids = [...new Set(list.map((e) => e.id))].filter((id) => id !== FALLBACK_ID)
  return ids.flatMap((id) => {
    const e = pick(list, id)
    return e ? [{ id, name: e.manifest.name }] : []
  })
}

// The tile is 824 of the 1024 grid with the rest left clear, the size macOS gives its own icons, and the mark is 58% of
// its width, centred on the box its strokes cover (x 5.7 to 28.9, y 5.7 to 26.3 of the 32 grid): make-app-icon.swift's.
const TILE = 'x="100" y="100" width="824" height="824" rx="185"'
const PLACE = 'transform="translate(512 512) scale(20.6) translate(-17.3 -16)"'
// The pixel mark covers x 6 to 30, y 6 to 26.
const PLACE_PIXEL = 'transform="translate(512 512) scale(21) translate(-18 -16)"'

/** A glow: the drawing blurred by `spread` (in units of the 32 grid) at `strength`, under the drawing itself */
const glow = (spread: number, strength: number) =>
  '<filter id="glow" x="-50%" y="-50%" width="200%" height="200%">' +
  `<feGaussianBlur in="SourceGraphic" stdDeviation="${spread}" result="blur"/>` +
  `<feComponentTransfer in="blur" result="soft"><feFuncA type="linear" slope="${strength}"/></feComponentTransfer>` +
  '<feMerge><feMergeNode in="soft"/><feMergeNode in="SourceGraphic"/></feMerge></filter>'

/** The tile lit from the top: `ground` with a little of `mark` in it at the top edge */
const litTile = (spec: IconSpec, share: number) =>
  '<linearGradient id="tile" x1="0" y1="0" x2="0" y2="1">' +
  `<stop offset="0" stop-color="${mix(spec.ground, spec.mark, share)}"/><stop offset="1" stop-color="${spec.ground}"/></linearGradient>`

const svg = (defs: string, body: string) =>
  `<svg xmlns="http://www.w3.org/2000/svg" width="${ICON_LIMITS.pixels}" height="${ICON_LIMITS.pixels}" viewBox="0 0 1024 1024">` +
  `<defs>${defs}</defs>${body}</svg>`

/** The icon as an SVG document of ICON_LIMITS.pixels square. Its colors come from iconSpecOf, so they are always `#rrggbb`. */
export function iconSvg(spec: IconSpec): string {
  const { mark, ground } = spec
  switch (spec.drawing) {
    case 'tube':
      // The tube's hot core is lighter than the glass (lookCss does the same with .bm-core)
      return svg(
        litTile(spec, 0.1) + glow(0.9, 0.9) + `<style>.bm-core{stroke:${mix(mark, '#ffffff', 0.72)}}</style>`,
        `<rect ${TILE} fill="url(#tile)"/>` +
          `<rect x="104" y="104" width="816" height="816" rx="181" fill="none" stroke="${mark}" stroke-opacity=".22" stroke-width="8"/>` +
          `<g ${PLACE} color="${mark}" filter="url(#glow)">${BRAND_TUBE_SHAPES}</g>`,
      )
    case 'pixel':
      return svg(
        litTile(spec, 0.07) + glow(0.45, 0.55),
        `<rect ${TILE} fill="url(#tile)"/><g ${PLACE_PIXEL} color="${mark}" filter="url(#glow)" shape-rendering="crispEdges">${BRAND_PIXEL_SHAPES}</g>`,
      )
    case 'phosphor':
      // A screen lit in its middle, and scanlines 32 of the 1024 grid apart: coarse enough to show at 128 pixels
      return svg(
        `<radialGradient id="screen" cx=".5" cy=".45" r=".7"><stop offset="0" stop-color="${mark}" stop-opacity=".13"/>` +
          `<stop offset="1" stop-color="${mark}" stop-opacity="0"/></radialGradient>` +
          glow(0.7, 0.7) +
          '<pattern id="scan" width="32" height="32" patternUnits="userSpaceOnUse"><rect y="20" width="32" height="12" fill="#000" fill-opacity=".38"/></pattern>' +
          `<clipPath id="clip"><rect ${TILE}/></clipPath>`,
        `<rect ${TILE} fill="${ground}"/><rect ${TILE} fill="url(#screen)"/>` +
          `<g ${PLACE} color="${mark}" filter="url(#glow)">${BRAND_MARK_SHAPES}</g><rect ${TILE} fill="url(#scan)" clip-path="url(#clip)"/>`,
      )
    case 'solid':
      return svg('', `<rect ${TILE} fill="${ground}"/><g ${PLACE} color="${mark}">${BRAND_MARK_SHAPES}</g>`)
  }
}
