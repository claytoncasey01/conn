// Turns a manifest into stylesheet text. Pure: no DOM, so the server, the loader and `conn theme check` share it.
import { accentTokens, DEFAULT_ACCENT } from './accent.ts'
import { FROM_VAR, GLASS, GLASS_DEFAULT, SLOTS } from './tokens.ts'
import type { Mode, ThemeManifest, TokenSet } from './types.ts'
import { slotProblem } from './validate.ts'

export type Pick = { accent?: { hue: number; chroma: number }; variant?: string }

const ID = /^[a-z0-9][a-z0-9-]{0,39}$/
const MODES: Mode[] = ['light', 'dark']

// The look slots as the stylesheets read them (app.css, the preview's sheet): each keyword picks code-owned values, never CSS
// from the theme. The values sit on the root, so one that needs a card's own data (its --wash, its --halo) is `initial` here
// and the reader falls back to that data. NULL is a shadow that draws nothing, so a list of shadows stays valid.
const NULL = '0 0 transparent'
const corners = (size: string) => ['top left', 'top right', 'bottom left', 'bottom right'].flatMap((at) => [`linear-gradient(currentColor,currentColor) ${at}/${size} 2px no-repeat`, `linear-gradient(currentColor,currentColor) ${at}/2px ${size} no-repeat`]).join(', ')
const BRACKETS = corners('14px')
const TICKS = ['top center/2px 7px', 'bottom center/2px 7px', 'left center/7px 2px', 'right center/7px 2px'].map((at) => `linear-gradient(currentColor,currentColor) ${at} no-repeat`).join(', ')
// a cut corner: the clip, and the rim redrawn along each cut in the card's own color (a clip takes the border with it)
const CUT = 'var(--card-cut)'
const CHAMFER = `polygon(${CUT} 0, calc(100% - ${CUT}) 0, 100% ${CUT}, 100% calc(100% - ${CUT}), calc(100% - ${CUT}) 100%, ${CUT} 100%, 0 calc(100% - ${CUT}), 0 ${CUT})`
const NOTCH = `polygon(0 0, calc(100% - ${CUT}) 0, 100% ${CUT}, 100% 100%, ${CUT} 100%, 0 calc(100% - ${CUT}))`
const diagonal = (dir: string, at: string) => `linear-gradient(${dir}, transparent calc(50% - 1px), currentColor calc(50% - .5px) calc(50% + .5px), transparent calc(50% + 1px)) ${at}/${CUT} ${CUT} no-repeat`
const CHAMFER_RIM = [diagonal('to bottom right', 'top left'), diagonal('to bottom left', 'top right'), diagonal('to bottom left', 'bottom left'), diagonal('to bottom right', 'bottom right')].join(', ')
const NOTCH_RIM = [diagonal('to bottom left', 'top right'), diagonal('to bottom left', 'bottom left')].join(', ')
const plainCorner = (round: string) => ({ '--look-round': round, '--look-clip': 'none', '--look-cut': 'none', '--look-cut-art': 'none', '--look-focus': '3px', '--look-out': '1', '--look-sel': '1px solid color-mix(in oklch, var(--tx) 55%, transparent)' })
// The brand tile: the accent with its ink, or lit, reversed so the mark is the accent glowing on its ink (a glow reads as light only
// on the dark, so lit marks are for dark themes), with a ring so a tile the color of the page keeps its edge. The mark is one of
// brandMark.ts's drawings, each a group a variable shows; a text glyph shows none of them
const ACCENT_TILE = { '--look-brand-tile': 'var(--ac)', '--look-brand-ink': 'var(--onac)', '--look-brand-ring': 'none', '--look-brand-bloom': 'none', '--look-brand-lines': 'none' }
const litTile = (bloom: string, lines: string) => ({
  '--look-brand-tile': 'var(--onac)',
  '--look-brand-ink': 'var(--ac)',
  '--look-brand-ring': '1px solid color-mix(in oklab, var(--ac) 45%, transparent)',
  '--look-brand-bloom': bloom,
  '--look-brand-lines': lines,
})
const PHOSPHOR_BLOOM = 'drop-shadow(0 0 1px currentColor) drop-shadow(0 0 4px color-mix(in oklab, currentColor 70%, transparent))'
const SCANLINES = 'repeating-linear-gradient(to bottom, #000 0 1.5px, rgb(0 0 0 / 45%) 1.5px 2.25px)'
const BRAND_DRAWINGS = ['solid', 'tube', 'pixel'] as const
const drawn = (shown: string) => Object.fromEntries(BRAND_DRAWINGS.map((name) => [`--look-brand-${name}`, name === shown ? 'inline' : 'none']))
const brandMark = (drawing: (typeof BRAND_DRAWINGS)[number], tile: Record<string, string>) => ({
  '--look-brand': '""',
  '--look-brand-size': '1em',
  '--look-brand-mark': 'block',
  ...drawn(drawing),
  ...tile,
})
const brandText = (glyph: string, size: string) => ({ '--look-brand': glyph, '--look-brand-size': size, '--look-brand-mark': 'none', ...drawn(''), ...ACCENT_TILE })
// the edge light: a lit line along the top and left edges, a fainter one along the bottom and right, and a soft glow inside,
// in white at glass-edge and the mode's share of it. SHEEN is the highlight's image, a soft white spot each surface places
const white = (share: string) => `color-mix(in oklch, white calc(var(--glass-edge) * var(--look-edge-k, 1) * ${share}), transparent)`
const EDGE_LIGHT = [
  `inset 1.5px 1.5px 0 -.5px ${white('95%')}`,
  `inset -1px -1px 0 -.5px ${white('55%')}`,
  `inset 0 0 8px ${white('35%')}`,
  `inset 0 0 28px color-mix(in oklch, white calc(var(--glass-edge) * var(--look-edge-glow, ${GLASS.light.edgeGlow}) * 100%), transparent)`,
].join(', ')
const SHEEN = `radial-gradient(closest-side, color-mix(in oklch, white calc(var(--card-sheen) * var(--look-sheen-max, ${GLASS.light.sheen}) * 100%), transparent), transparent 62%)`
export const LOOK: Readonly<Record<string, Readonly<Record<string, Readonly<Record<string, string>>>>>> = {
  'font-body': { ui: { '--look-body': 'var(--font-ui)' }, mono: { '--look-body': 'var(--font-mono), monospace' } },
  // styled chrome takes case and tracking from the tokens; authored chrome keeps what the markup gives it (Neon tracks each
  // element itself). `initial` makes the chrome rule's var() invalid, so the property falls back to inherit
  chrome: {
    styled: { '--look-chrome-case': 'var(--case)', '--look-chrome-tracking': 'var(--tracking)' },
    authored: { '--look-chrome-case': 'initial', '--look-chrome-tracking': 'initial' },
  },
  cursor: {
    bar: { '--look-cursor-w': '7px', '--look-cursor-h': '13px', '--look-cursor-va': '-2px', '--look-cursor-ml': '4px', '--look-cursor-glow': 'none' },
    block: { '--look-cursor-w': '.6em', '--look-cursor-h': '1.15em', '--look-cursor-va': '-.2em', '--look-cursor-ml': '.2em', '--look-cursor-glow': '0 0 8px color-mix(in oklch, var(--ac) 50%, transparent)' },
  },
  'hover-lift': {
    off: { '--look-lift': '0', '--look-go-glow': '0 0 0 3px var(--ac-soft)' },
    on: { '--look-lift': '1', '--look-go-glow': '0 0 38px color-mix(in oklch, var(--ac) 55%, transparent)' },
  },
  // a meter's lit stretch is a run of 5px segments 8px apart, or (smooth) segments as long as their period, which is one bar
  meter: { smooth: { '--look-seg-on': '8px', '--look-meter-r': '2px' }, segmented: { '--look-seg-on': '5px', '--look-meter-r': '0' } },
  backdrop: {
    none: { '--look-grid-h': '0', '--look-grid-v': '0', '--look-dots': '0' },
    grid: { '--look-grid-h': '1', '--look-grid-v': '1', '--look-dots': '0' },
    lines: { '--look-grid-h': '1', '--look-grid-v': '0', '--look-dots': '0' },
    dots: { '--look-grid-h': '0', '--look-grid-v': '0', '--look-dots': '1' },
  },
  orbs: { none: { '--look-orbs': '0' }, soft: { '--look-orbs': '.5' }, bright: { '--look-orbs': '1' } },
  // each page picks its own orb hues from the role colors; a theme may paint all three in its accent or its edge instead
  'orbs-tint': { roles: { '--look-orb-tint': 'initial' }, accent: { '--look-orb-tint': 'var(--ac)' }, edge: { '--look-orb-tint': 'var(--edge)' } },
  header: {
    flat: { '--look-head-blur': 'none', '--look-glint': 'none', '--look-head-rule': '0' },
    frost: { '--look-head-blur': 'blur(14px)', '--look-glint': 'none', '--look-head-rule': '0' },
    rule: { '--look-head-blur': 'none', '--look-glint': 'none', '--look-head-rule': '1' },
    glint: { '--look-head-blur': 'blur(14px)', '--look-glint': 'block', '--look-head-rule': '0' },
  },
  // a float is inset from the window's edges, ringed all round and lifted by the lift token; a bar spans the window
  // (the preview's bar draws its bottom edge in line; a float's ring is one color all round, --look-head-ring, as in the app)
  'header-shape': {
    bar: { '--look-head-float': '0', '--look-head-lift': 'initial', '--look-head-ring': 'initial' },
    float: { '--look-head-float': '1', '--look-head-lift': 'var(--lift)', '--look-head-ring': 'color-mix(in oklch, var(--edge) 16%, transparent)' },
  },
  stage: {
    plain: { '--look-stage-dots': '0', '--look-stage-glow': '0', '--look-hud': 'none' },
    dots: { '--look-stage-dots': '1', '--look-stage-glow': '0', '--look-hud': 'none' },
    hud: { '--look-stage-dots': '1', '--look-stage-glow': '1', '--look-hud': 'block' },
  },
  // glass is panel at glass-tint over a blur of what is behind, boosted by glass-saturate with the tone of its mode (lookCss.ts
  // sets --look-tone per mode from GLASS); each slot left out is GLASS_DEFAULT, the glass of before. Its solid form is what
  // Reduce transparency paints instead
  'card-fill': {
    plain: { '--look-fill': 'var(--panel)', '--look-glass': 'none', '--look-fill-solid': 'initial' },
    wash: { '--look-fill': 'initial', '--look-glass': 'none', '--look-fill-solid': 'initial' },
    glass: {
      '--look-fill': `color-mix(in oklch, var(--panel) calc(var(--glass-tint, ${GLASS_DEFAULT.tint}) * 100%), transparent)`,
      '--look-glass': `blur(var(--glass-blur, ${GLASS_DEFAULT.blur}px)) saturate(var(--glass-saturate, ${GLASS_DEFAULT.saturate})) brightness(calc(1 + (var(--glass-saturate, ${GLASS_DEFAULT.saturate}) - 1) * var(--look-tone, ${GLASS.light.tone})))`,
      '--look-fill-solid': 'var(--panel)',
    },
  },
  // square and the cuts drop the radius; a cut clips, so the focus ring moves inside the card where the clip cannot take it
  'card-corner': {
    round: plainCorner('1'),
    square: plainCorner('0'),
    chamfer: { '--look-round': '0', '--look-clip': CHAMFER, '--look-cut': '""', '--look-cut-art': CHAMFER_RIM, '--look-focus': '-4px', '--look-out': '0', '--look-sel': '2px solid color-mix(in oklch, var(--ac) 70%, transparent)' },
    notch: { '--look-round': '0', '--look-clip': NOTCH, '--look-cut': '""', '--look-cut-art': NOTCH_RIM, '--look-focus': '-4px', '--look-out': '0', '--look-sel': '2px solid color-mix(in oklch, var(--ac) 70%, transparent)' },
  },
  // which sides of the border show (top, the rest), and the inset lines added inside it
  'card-frame': {
    none: { '--look-edge-top': '0', '--look-edge-sides': '0', '--look-frame': NULL },
    hairline: { '--look-edge-top': '1', '--look-edge-sides': '1', '--look-frame': NULL },
    bold: { '--look-edge-top': '1', '--look-edge-sides': '1', '--look-frame': 'inset 0 0 0 1px var(--line2)' },
    double: { '--look-edge-top': '1', '--look-edge-sides': '1', '--look-frame': 'inset 0 0 0 3px var(--bg), inset 0 0 0 4px color-mix(in oklch, var(--line2) 55%, transparent)' },
    rule: { '--look-edge-top': '1', '--look-edge-sides': '0', '--look-frame': 'inset 0 1px 0 var(--line2)' },
  },
  'card-marks': {
    none: { '--look-marks': 'none', '--look-marks-art': 'none', '--look-gutter': '0' },
    brackets: { '--look-marks': '""', '--look-marks-art': BRACKETS, '--look-gutter': '0' },
    ticks: { '--look-marks': '""', '--look-marks-art': TICKS, '--look-gutter': '0' },
    gutter: { '--look-marks': 'none', '--look-marks-art': 'none', '--look-gutter': '1' },
  },
  'card-stripe': { off: { '--look-stripe': 'none' }, on: { '--look-stripe': 'block' } },
  'card-shadow': { none: { '--look-shadow': NULL }, lift: { '--look-shadow': 'var(--lift)' }, halo: { '--look-shadow': 'initial' } },
  // fwd and back sit on buttons and the links within a page; go and go-back on links that lead to another page (Neon draws
  // those as plain arrows, so its triangle keyword keeps them)
  'glyph-arrow': {
    triangle: { '--look-fwd': '"▸"', '--look-back': '"◂"', '--look-go': '"→"', '--look-go-back': '"←"' },
    chevron: { '--look-fwd': '"›"', '--look-back': '"‹"', '--look-go': '"›"', '--look-go-back': '"‹"' },
    arrow: { '--look-fwd': '"→"', '--look-back': '"←"', '--look-go': '"→"', '--look-go-back': '"←"' },
    none: { '--look-fwd': '""', '--look-back': '""', '--look-go': '""', '--look-go-back': '""' },
  },
  // Neon's triangle sits tight against its line, as it always has; the others keep a small gap
  'glyph-bullet': {
    triangle: { '--look-bullet': '"▸"', '--look-bullet-gap': '0' },
    dot: { '--look-bullet': '"•"', '--look-bullet-gap': '.45em' },
    dash: { '--look-bullet': '"–"', '--look-bullet-gap': '.45em' },
    prompt: { '--look-bullet': '"❯"', '--look-bullet-gap': '.45em' },
    none: { '--look-bullet': '""', '--look-bullet-gap': '0' },
  },
  'glyph-sep': { slash: { '--look-sep': '"//"' }, dot: { '--look-sep': '"·"' }, bar: { '--look-sep': '"|"' }, dash: { '--look-sep': '"—"' } },
  // `conn` and its kin are the Conn mark, an inline SVG (brandMark.ts) that `--look-brand-mark` shows in one of its drawings, with no
  // text glyph beside it; a text glyph alone is drawn larger, so it fills the tile as the three blocks do
  'glyph-brand': {
    conn: brandMark('solid', ACCENT_TILE),
    'conn-tube': brandMark('tube', litTile('drop-shadow(0 0 1.5px currentColor) drop-shadow(0 0 5px currentColor)', 'none')),
    'conn-pixel': brandMark('pixel', ACCENT_TILE),
    'conn-phosphor': brandMark('solid', litTile(PHOSPHOR_BLOOM, SCANLINES)),
    blocks: brandText('"▟█▙"', '1em'),
    prompt: brandText('"❯_"', '1.15em'),
    diamond: brandText('"◆"', '1.7em'),
    none: brandText('""', '1em'),
  },
  // every wire of the agent tree is drawn as a base line, an under-sheath, a cutter and a sheen, and every head in each shape:
  // a style shows its parts (a cable is a dim sheath, the line as its core and a sheen; a rail is a sheath cut down its middle)
  'wire-style': {
    line: { '--look-wb': 'inline', '--look-wb-dash': 'none', '--look-wsh': 'none', '--look-wsh-k': '0', '--look-wcut': 'none', '--look-wsn': 'none' },
    cable: { '--look-wb': 'inline', '--look-wb-dash': 'none', '--look-wsh': 'inline', '--look-wsh-k': '3', '--look-wcut': 'none', '--look-wsn': 'inline' },
    rail: { '--look-wb': 'none', '--look-wb-dash': 'none', '--look-wsh': 'inline', '--look-wsh-k': '2.6', '--look-wcut': 'inline', '--look-wsn': 'none' },
    dash: { '--look-wb': 'inline', '--look-wb-dash': 'calc(var(--wire-width, 2px) * 2.5) calc(var(--wire-width, 2px) * 2)', '--look-wsh': 'none', '--look-wsh-k': '0', '--look-wcut': 'none', '--look-wsn': 'none' },
  },
  'wire-head': {
    arrow: { '--look-h-arrow': 'inline', '--look-h-plug': 'none', '--look-h-dot': 'none', '--look-h-bar': 'none' },
    plug: { '--look-h-arrow': 'none', '--look-h-plug': 'inline', '--look-h-dot': 'none', '--look-h-bar': 'none' },
    dot: { '--look-h-arrow': 'none', '--look-h-plug': 'none', '--look-h-dot': 'inline', '--look-h-bar': 'none' },
    bar: { '--look-h-arrow': 'none', '--look-h-plug': 'none', '--look-h-dot': 'none', '--look-h-bar': 'inline' },
    none: { '--look-h-arrow': 'none', '--look-h-plug': 'none', '--look-h-dot': 'none', '--look-h-bar': 'none' },
  },
  // the dashes that crawl along the trunk (and back along the return while an agent comes home)
  'wire-crawl': { off: { '--look-crawl': 'none' }, on: { '--look-crawl': 'inline' } },
  // what travels a wire while an agent works or returns, and between the lead and the advisor right after a consult
  'wire-flow': {
    packets: { '--look-comet': 'inline', '--look-blocks': 'none', '--look-pulse': 'none', '--look-glyphs': 'none' },
    blocks: { '--look-comet': 'none', '--look-blocks': 'inline', '--look-pulse': 'none', '--look-glyphs': 'none' },
    pulse: { '--look-comet': 'none', '--look-blocks': 'none', '--look-pulse': 'inline', '--look-glyphs': 'none' },
    glyphs: { '--look-comet': 'none', '--look-blocks': 'none', '--look-pulse': 'none', '--look-glyphs': 'inline' },
    none: { '--look-comet': 'none', '--look-blocks': 'none', '--look-pulse': 'none', '--look-glyphs': 'none' },
  },
  'wire-glyphs': {
    binary: { '--look-gs-binary': 'inline', '--look-gs-hex': 'none', '--look-gs-kana': 'none', '--look-gs-ascii': 'none' },
    hex: { '--look-gs-binary': 'none', '--look-gs-hex': 'inline', '--look-gs-kana': 'none', '--look-gs-ascii': 'none' },
    kana: { '--look-gs-binary': 'none', '--look-gs-hex': 'none', '--look-gs-kana': 'inline', '--look-gs-ascii': 'none' },
    ascii: { '--look-gs-binary': 'none', '--look-gs-hex': 'none', '--look-gs-kana': 'none', '--look-gs-ascii': 'inline' },
  },
  // roles: each packet in its wire's role color (initial makes the var() fall back to it); or one signal color for all
  'wire-signal': { roles: { '--look-signal': 'initial' }, lead: { '--look-signal': 'var(--role-lead)' }, bright: { '--look-signal': 'var(--bright)' }, tx: { '--look-signal': 'var(--tx)' } },
  // the rings turning round the lead and the comet on the inner one
  'tree-orbits': { off: { '--look-orbits': 'none' }, on: { '--look-orbits': 'inline' } },
  // the junction nodes swell and fade; the keyframes are the code's, the token names them or not
  'node-beat': { off: { '--look-beat': 'none' }, on: { '--look-beat': 'node' } },
}

/** The derived look declarations for a merged token set: the keyword tables, the scan strip's switch, and a lift of `none` made NULL */
export function lookDeclarations(all: TokenSet): string {
  const out: Record<string, string> = {}
  for (const [slot, table] of Object.entries(LOOK)) {
    const v = all[slot]
    if (v !== undefined && Object.hasOwn(table, v)) Object.assign(out, table[v])
  }
  // none cannot stand in a list of shadows: the card's list and the floating top bar's (with its line) would draw no shadow at all
  const liftNone = (all.lift ?? '').trim() === 'none'
  if (out['--look-shadow'] === 'var(--lift)' && liftNone) out['--look-shadow'] = NULL
  if (out['--look-head-lift'] === 'var(--lift)' && liftNone) out['--look-head-lift'] = NULL
  if (all['backdrop-scan'] !== undefined) out['--look-scan'] = Number(all['backdrop-scan']) > 0 ? 'block' : 'none'
  // a theme that tunes its glass (any glass slot off GLASS_DEFAULT, the glass of before) gets that glass on every surface,
  // including the ones whose markup sets a blur of its own (--look-surf-blur, read before that blur), and on the agent tree's
  // stage, whose usual fill is panel at 80%; a surface inside another does not filter again (--look-surf-blur-in). A theme of
  // the glass of before keeps those blurs and that stage
  if (all['card-fill'] !== undefined) {
    const off = (slot: string, value: number) => all[slot] !== undefined && Number.parseFloat(all[slot]) !== value
    const tuned = all['card-fill'] === 'glass' && (off('glass-tint', GLASS_DEFAULT.tint) || off('glass-blur', GLASS_DEFAULT.blur) || off('glass-saturate', GLASS_DEFAULT.saturate))
    out['--look-surf-blur'] = tuned ? 'var(--look-glass)' : 'initial'
    out['--look-surf-blur-in'] = tuned ? 'none' : 'initial'
    out['--look-stage-panel'] = tuned ? 'var(--look-fill)' : 'initial'
  }
  // the light along each card's edge and the highlight that follows the pointer, both scaled by their slot and their mode's
  // share (--look-edge-k, --look-edge-glow, --look-sheen-max: GLASS); the highlight is one image each surface moves
  if (all['glass-edge'] !== undefined) out['--look-edge-light'] = Number(all['glass-edge']) > 0 ? EDGE_LIGHT : NULL
  if (all['card-sheen'] !== undefined) out['--look-sheen'] = Number(all['card-sheen']) > 0 ? SHEEN : 'none'
  // the wallpaper's layer is drawn only while it has a strength
  if (all['wallpaper-strength'] !== undefined) out['--look-wall'] = Number(all['wallpaper-strength']) > 0 ? '""' : 'none'
  // numbers that switch a piece on: text glows only above 0, and wires and comets glow with the glow token
  if (all['text-glow'] !== undefined)
    out['--look-text-shadow'] = Number(all['text-glow']) > 0 ? '0 0 6px color-mix(in oklch, var(--tx) calc(var(--text-glow) * 100%), transparent), 0 0 1px color-mix(in oklch, var(--bright) min(100%, calc(var(--text-glow) * 150%)), transparent)' : 'none'
  if (all.glow !== undefined) {
    const on = Number(all.glow) > 0
    out['--look-wire'] = on ? 'drop-shadow(0 0 calc(3px * var(--glow)) currentColor)' : 'none'
    out['--look-comet-glow'] = on ? 'drop-shadow(0 0 calc(5px * var(--glow)) var(--look-signal, currentColor))' : 'none'
  }
  return Object.entries(out).map(([k, v]) => `${k}:${v};`).join('')
}

// For the fallback manifest only: a Default with no tokens for `mode` reads its other mode, as the
// contrast gate does for its fallback side. A theme's own tokens never cross modes.
const fallbackTokens = (m: ThemeManifest, mode: Mode): TokenSet => m.tokens[mode] ?? m.tokens.dark ?? m.tokens.light ?? {}

// An unset or unknown variant means the first one, so what the picker shows checked is what is painted; a theme with none has none.
export function variantOf(m: ThemeManifest, name: string | null | undefined): { name: string; tokens: TokenSet } | undefined {
  if (typeof m.accent !== 'object') return undefined
  return m.accent.variants.find((v) => v.name === name) ?? m.accent.variants[0]
}

// each layer drops its own bad values first, so a rejected value falls back to the layer below
function merge(m: ThemeManifest, mode: Mode, base: TokenSet, pick: Pick | undefined, derived: TokenSet = {}): TokenSet {
  const variant = variantOf(m, pick?.variant)
  const accent = m.accent === 'free' && pick?.accent ? accentTokens(mode, pick.accent.hue, pick.accent.chroma) : {}
  const ok = (t: TokenSet): TokenSet => Object.fromEntries(Object.entries(t).filter(([k, v]) => slotProblem(k, v) === null))
  return { ...ok(base), ...derived, ...ok(m.tokens[mode] ?? {}), ...ok(variant?.tokens ?? {}), ...ok(accent) }
}

// A length may be a bare 0 (the validator allows it), but a bare 0 is a number, not a length, to calc(): calc(0 + 14px) is
// invalid and its property falls to its initial value. So a bare 0 is written with the slot's unit.
const BARE_ZERO = /^[+-]?(?:0+(?:\.0*)?|\.0+)$/
const slotValue = (slot: string, v: string): string => {
  const spec = SLOTS[slot]
  return spec.kind === 'length' && BARE_ZERO.test(v.trim()) ? `0${spec.unit ?? 'px'}` : v
}

function declarations(m: ThemeManifest, mode: Mode, base: TokenSet, pick: Pick | undefined): string {
  const all = merge(m, mode, base, pick, FROM_VAR)
  return (
    Object.keys(SLOTS)
      .filter((k) => Object.hasOwn(all, k))
      .map((k) => `--${k}:${slotValue(k, all[k])};`)
      .join('') + lookDeclarations(all)
  )
}

/**
 * The values themeCss writes for `mode`, as data: what an effect reads for its colors. The FROM_VAR slots a theme leaves
 * to its own are here too, with each var() replaced by the value it names (the color the browser computes), so an effect
 * never sees the fallback's literal for them. A slot whose inputs are not set anywhere is left out, not invented.
 */
export function themeTokens(m: ThemeManifest, fallback: ThemeManifest, mode: Mode, pick?: Pick): Readonly<TokenSet> {
  const all = merge(m, mode, fallbackTokens(fallback, mode), pick)
  const own = merge(m, mode, {}, pick)
  for (const [k, form] of Object.entries(FROM_VAR)) {
    if (Object.hasOwn(own, k)) continue
    const names = [...form.matchAll(/var\(--([a-z-]+)\)/g)].map((x) => x[1])
    if (names.every((n) => Object.hasOwn(all, n))) all[k] = form.replace(/var\(--([a-z-]+)\)/g, (_, n: string) => all[n])
  }
  return all
}

export function themeCss(m: ThemeManifest, fallback: ThemeManifest, pick?: Pick): string {
  if (!ID.test(m.id)) return ''
  // only the two real modes become selectors: a mode string from a file is never written into one
  return MODES.filter((mode) => m.modes.includes(mode))
    .map((mode) => `[data-theme="${m.id}"][data-mode="${mode}"]{${declarations(m, mode, fallbackTokens(fallback, mode), pick)}}`)
    .join('\n')
}

// Default as the first-paint stylesheet: light on :root, dark under the OS preference, and the
// explicit blocks so a stored mode beats the OS one.
export function fallbackCss(def: ThemeManifest): string {
  if (!ID.test(def.id)) return ''
  const pick: Pick | undefined = def.accent === 'free' ? { accent: DEFAULT_ACCENT } : undefined
  const [light, dark] = MODES.map((mode) => declarations(def, mode, fallbackTokens(def, mode), pick))
  return [
    `:root{${light}}`,
    `@media (prefers-color-scheme: dark){:root{${dark}}}`,
    `[data-theme="${def.id}"][data-mode="light"]{${light}}`,
    `[data-theme="${def.id}"][data-mode="dark"]{${dark}}`,
  ].join('\n')
}
