// Manifest validation and the contrast gate. Pure: no DOM, no dependencies, no I/O.
import { composite, contrast, cssTrim, parseColor, type Rgba } from './color.ts'
import { ALLOWED_FONTS, FROM_VAR, GLASS, GLASS_DEFAULT, KNOWN_EFFECTS, LIMITS, SLOTS, type SlotSpec, WALLPAPER, WALLPAPER_FADE } from './tokens.ts'
import type { EffectSpec, Mode, ThemeManifest, ThemeSource, TokenSet } from './types.ts'

export type Ctx = { source: ThemeSource; builtinIds: ReadonlySet<string> }
type Result = { ok: true; manifest: ThemeManifest } | { ok: false; errors: string[] }

const ID = /^[a-z0-9][a-z0-9-]{0,39}$/
const MODES: Mode[] = ['light', 'dark']
const FORBIDDEN = /url\(|[;{}]|@import|expression/i
const MAX_SHADOW_PX = 64

// A name from the file is quoted and clipped before it goes into a message.
const show = (s: string) => JSON.stringify(s.length > 40 ? `${s.slice(0, 40)}...` : s)
const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

const LENGTH = /^([+-]?(?:\d+(?:\.\d+)?|\.\d+))(px|rem|em)?$/
// A length as a number of the slot's own unit; bare 0 is the only unitless length.
function lengthIn(v: string, unit: string): number | null {
  const m = LENGTH.exec(v)
  if (!m) return null
  const n = Number(m[1])
  if (!m[2]) return n === 0 ? 0 : null
  const px = m[2] === 'px' ? n : n * 16
  return unit === 'px' ? px : unit === 'em' ? (m[2] === 'px' ? n / 16 : n) : null
}

// Split on `sep` outside parentheses.
function splitTop(s: string, sep: RegExp): string[] {
  const out: string[] = []
  let depth = 0
  let cur = ''
  for (const ch of s) {
    if (ch === '(') depth++
    else if (ch === ')') depth--
    if (depth === 0 && sep.test(ch)) {
      out.push(cur)
      cur = ''
    } else cur += ch
  }
  out.push(cur)
  return out.map(cssTrim).filter((p) => p !== '')
}

function shadowProblem(v: string): string | null {
  if (v === 'none') return null
  const layers = splitTop(v, /,/)
  if (layers.length === 0 || layers.length > 4) return 'needs 1 to 4 shadows'
  for (const layer of layers) {
    let inset = 0
    let colors = 0
    const lens: number[] = []
    for (const t of splitTop(layer, /[ \t\n\r\f]/)) {
      if (t === 'inset') inset++
      else if (LENGTH.test(t)) {
        const px = lengthIn(t, 'px')
        if (px === null) return `length ${show(t)} needs px, rem or em`
        lens.push(px)
      } else if (parseColor(t)) colors++
      else return `${show(t)} is not a length or a color`
    }
    if (inset > 1 || colors !== 1) return 'each shadow needs exactly one color'
    if (lens.length < 2 || lens.length > 4) return 'each shadow needs 2 to 4 lengths'
    if (lens.some((n) => Math.abs(n) > MAX_SHADOW_PX)) return `a length is beyond ${MAX_SHADOW_PX}px`
    if (lens.length > 2 && lens[2] < 0) return 'blur cannot be negative'
  }
  return null
}

// Families are allow-listed names, plus the generics the slot's own spec lists; the stack must
// end in the slot's system stack so a font that fails to load never blocks rendering.
function fontProblem(spec: SlotSpec, v: string): string | null {
  const parts = splitTop(v, /,/)
  if (parts.length === 0 || parts.length > 8) return 'needs 1 to 8 families'
  for (const p of parts) {
    const m = /^(?:'([A-Za-z0-9 -]+)'|"([A-Za-z0-9 -]+)"|([A-Za-z][A-Za-z0-9-]*(?: [A-Za-z0-9-]+)*))$/.exec(p)
    const name = m && (m[1] ?? m[2] ?? m[3])
    if (!name || !(ALLOWED_FONTS.includes(name) || (m[3] && spec.values?.includes(name)))) return `family ${show(p)} is not on the allow-list for this slot`
  }
  if (parts[parts.length - 1] !== spec.end) return `must end in ${spec.end}`
  return null
}

const range = (spec: SlotSpec, n: number) => (spec.min !== undefined && n < spec.min) || (spec.max !== undefined && n > spec.max)
const rangeText = (spec: SlotSpec) => `${spec.min} to ${spec.max}${spec.unit ?? ''}`

// The one transparency form a theme may write: `ac` (or any color) at N% over transparent.
const COLOR_MIX = /^color-mix\(in oklch, (.+) (\d+(?:\.\d+)?|\.\d+)%, transparent\)$/
function colorMix(v: string): boolean {
  const m = COLOR_MIX.exec(v)
  return !!m && Number(m[2]) <= 100 && parseColor(m[1]) !== null
}
// what a color value paints: the color, or for the transparency form, the color at that share of its alpha
function paintOf(v: string): Rgba | null {
  const m = COLOR_MIX.exec(v.trim())
  if (!m) return parseColor(v)
  const c = parseColor(m[1])
  return c && Number(m[2]) <= 100 ? { ...c, a: (c.a * Number(m[2])) / 100 } : null
}

// null when `v` is a fine value for `spec`, else the reason.
function valueProblem(spec: SlotSpec, v: string): string | null {
  const bad = FORBIDDEN.exec(v)
  if (bad) return `contains ${show(bad[0])}`
  if (v.length > 300) return 'is too long'
  switch (spec.kind) {
    case 'color':
      return parseColor(v) || colorMix(v) ? null : 'is not a hex, rgb(), hsl(), oklch() or color-mix(in oklch, <color> N%, transparent) color'
    case 'length': {
      if (spec.pxOnly && !/px$/.test(v)) return 'is a length in px only'
      const n = lengthIn(v, spec.unit ?? 'px')
      if (n === null) return 'is not a number with px, rem or em'
      return range(spec, n) ? `is out of range (${rangeText(spec)})` : null
    }
    case 'number': {
      if (!/^[+-]?(?:\d+(?:\.\d+)?|\.\d+)$/.test(v)) return 'is not a number'
      return range(spec, Number(v)) ? `is out of range (${spec.min} to ${spec.max})` : null
    }
    case 'enum':
      return spec.values?.includes(v) ? null : `must be one of ${spec.values?.join(', ')}`
    case 'shadow':
      return shadowProblem(v)
    case 'font':
      return fontProblem(spec, v)
  }
}

// One value against one slot, for callers that build tokens themselves (the stylesheet generator).
export function slotProblem(slot: string, v: string): string | null {
  return Object.hasOwn(SLOTS, slot) ? valueProblem(SLOTS[slot], cssTrim(v)) : `unknown token ${show(slot)}`
}

// Own-property check on a null-prototype result, so `__proto__` and `constructor` are just unknown names.
function tokenSet(raw: unknown, path: string, errors: string[]): TokenSet {
  const out: TokenSet = Object.create(null)
  if (!isObject(raw)) {
    errors.push(`${path} must be an object of tokens`)
    return out
  }
  for (const slot of Object.keys(raw)) {
    if (!Object.hasOwn(SLOTS, slot)) {
      errors.push(`${path}: unknown token ${show(slot)}`)
      continue
    }
    const v = raw[slot]
    if (typeof v !== 'string') {
      errors.push(`${path}.${slot} must be a string`)
      continue
    }
    const value = cssTrim(v)
    const problem = valueProblem(SLOTS[slot], value)
    if (problem) errors.push(`${path}.${slot} ${problem}`)
    else out[slot] = value
  }
  return out
}

function text(raw: Record<string, unknown>, key: string, max: number, errors: string[], required: boolean): string {
  const v = raw[key]
  if (typeof v !== 'string') {
    errors.push(`${key} must be a string`)
    return ''
  }
  if (required && v.trim() === '') errors.push(`${key} must not be empty`)
  if (v.length > max) errors.push(`${key} is longer than ${max} characters`)
  return v
}

function effects(raw: unknown, errors: string[]): EffectSpec[] {
  if (!Array.isArray(raw)) {
    errors.push('effects must be a list')
    return []
  }
  if (raw.length > 8) errors.push('effects lists more than 8')
  const out: EffectSpec[] = []
  for (const e of raw.slice(0, 8)) {
    if (!isObject(e) || typeof e.name !== 'string' || !KNOWN_EFFECTS.includes(e.name)) {
      errors.push(`effects: ${show(String(isObject(e) ? e.name : e))} is not a known effect (${KNOWN_EFFECTS.join(', ')})`)
      continue
    }
    const spec: EffectSpec = { name: e.name }
    if (e.options !== undefined) {
      if (!isObject(e.options)) {
        errors.push(`effects.${e.name}.options must be an object`)
        continue
      }
      const options: Record<string, number | string | boolean> = Object.create(null)
      for (const k of Object.keys(e.options)) {
        const v = e.options[k]
        const where = `effects.${e.name}.options.${k}`
        if (!/^[a-z][A-Za-z0-9]{0,31}$/.test(k)) errors.push(`effects.${e.name}.options: bad name ${show(k)}`)
        else if (typeof v === 'number' && Number.isFinite(v)) options[k] = v
        else if (typeof v === 'boolean') options[k] = v
        else if (typeof v === 'string' && v.length <= 64 && !FORBIDDEN.test(v)) options[k] = v
        else errors.push(`${where} must be a number, boolean or plain string`)
      }
      spec.options = options
    }
    out.push(spec)
  }
  return out
}

export function validateManifest(raw: unknown, ctx: Ctx): Result {
  if (!isObject(raw)) return { ok: false, errors: ['theme.json must be a JSON object'] }
  const errors: string[] = []

  const id = typeof raw.id === 'string' ? raw.id : ''
  if (!ID.test(id)) errors.push('id must be 1 to 40 characters of a-z, 0-9 and -, not starting with -')
  else if (ctx.source !== 'builtin' && ctx.builtinIds.has(id)) errors.push(`id ${show(id)} belongs to a built-in theme`)
  const name = text(raw, 'name', 80, errors, true)
  const version = text(raw, 'version', 40, errors, true)
  const author = text(raw, 'author', 200, errors, false)
  const license = text(raw, 'license', 100, errors, false)
  const description = text(raw, 'description', 1000, errors, false)

  let modes: Mode[] = []
  if (!Array.isArray(raw.modes) || raw.modes.length === 0 || raw.modes.length > 2 || !raw.modes.every((m) => MODES.includes(m)) || new Set(raw.modes).size !== raw.modes.length) {
    errors.push('modes must be ["light"], ["dark"] or both')
  } else modes = raw.modes as Mode[]

  let accent: ThemeManifest['accent'] = 'fixed'
  if (raw.accent === 'free' || raw.accent === 'fixed') accent = raw.accent
  else if (isObject(raw.accent) && Array.isArray(raw.accent.variants) && raw.accent.variants.length >= 1 && raw.accent.variants.length <= 8) {
    const variants: { name: string; tokens: TokenSet }[] = []
    raw.accent.variants.forEach((v: unknown, i: number) => {
      if (!isObject(v) || typeof v.name !== 'string' || v.name.trim() === '' || v.name.length > 40) {
        errors.push(`accent.variants[${i}] needs a name of 1 to 40 characters`)
        return
      }
      // the stored choice names a variant, so a second one of a name could never be picked
      if (variants.some((w) => w.name === v.name)) {
        errors.push(`accent.variants: name ${show(v.name)} is used twice`)
        return
      }
      variants.push({ name: v.name, tokens: tokenSet(v.tokens, `accent variant ${show(v.name)} tokens`, errors) })
    })
    accent = { variants }
  } else errors.push('accent must be "free", "fixed" or { variants: [1 to 8 of { name, tokens }] }')

  const fonts: string[] = []
  if (!Array.isArray(raw.fonts) || raw.fonts.length > 8) errors.push('fonts must be a list of at most 8 names')
  else
    for (const f of raw.fonts) {
      if (typeof f === 'string' && ALLOWED_FONTS.includes(f)) fonts.push(f)
      else errors.push(`fonts: ${show(String(f))} is not allowed (${ALLOWED_FONTS.join(', ')})`)
    }

  const tokens: ThemeManifest['tokens'] = {}
  if (raw.tokens !== undefined && !isObject(raw.tokens)) errors.push('tokens must be an object keyed by mode')
  else
    for (const mode of Object.keys(raw.tokens ?? {})) {
      if (!MODES.includes(mode as Mode)) errors.push(`tokens: ${show(mode)} is not a mode`)
      else if (!modes.includes(mode as Mode)) errors.push(`tokens.${mode} is given but modes does not list ${mode}`)
      else tokens[mode as Mode] = tokenSet((raw.tokens as Record<string, unknown>)[mode], `tokens.${mode}`, errors)
    }

  const fx = effects(raw.effects ?? [], errors)
  // a theme made for a newer conn names tokens this one does not have yet: one line says so, after the names themselves
  if (errors.some((e) => e.includes(': unknown token '))) errors.push(NEWER)
  if (errors.length) return { ok: false, errors }
  return { ok: true, manifest: { id, name, version, author, license, description, modes, accent, fonts, tokens, effects: fx } }
}

export const NEWER = 'a token this conn does not know may come from a newer conn: update conn to use this theme'

export function parseManifest(text: string, ctx: Ctx): Result {
  if (new TextEncoder().encode(text).length > LIMITS.manifestBytes) return { ok: false, errors: [`theme.json is larger than ${LIMITS.manifestBytes / 1024} KB`] }
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    return { ok: false, errors: ['theme.json is not valid JSON'] }
  }
  return validateManifest(raw, ctx)
}

const MIN = { tx: 4.5, onac: 4.5, line2: 1.5, mu: 3 }
const PAIRS: [fg: keyof typeof MIN, on: 'bg' | 'panel' | 'raise' | 'ac'][] = [
  ['tx', 'bg'], ['tx', 'panel'], ['tx', 'raise'], ['onac', 'ac'], ['line2', 'panel'], ['mu', 'bg'], ['mu', 'panel'],
]

// The look can lay color under text: a card's wash, a glass card over the page, the backdrop's pattern and orbs. tx and mu are
// measured over the worst of each: the strongest wash any card paints (WASH_MAX of its state color, a cap a source test holds
// the markup to), and the page where the pattern's line and the strongest orb meet (an orb is at most ORB_MAX of its hue).
export const WASH_MAX = 0.16
export const ORB_MAX = 0.22
const WASH_HUES = ['warn', 'bad', 'ok', 'ac', 'edge', 'role-lead', 'role-advisor', 'role-agent', 'tx']
const ORB_HUES: Record<string, string[]> = { roles: ['edge', 'role-advisor', 'role-lead', 'ac', 'warn'], accent: ['ac'], edge: ['edge'] }
const ORB_AMOUNT: Record<string, number> = { none: 0, soft: 0.5, bright: 1 }
const PATTERN_ALPHA: Record<string, number> = { none: 0, grid: 0.09, lines: 0.09, dots: 0.3 }
const alpha = (c: Rgba, a: number): Rgba => ({ ...c, a: c.a * a })
const WHITE: Rgba = { r: 255, g: 255, b: 255, a: 1 }
const channel = (n: number) => Math.min(255, Math.max(0, Math.round(n)))

// The glass's filter on the opaque page behind it, as the browser applies it: saturate() by the Filter Effects matrix, then
// brightness(), each to a whole 8-bit value. 1 is no change, so a glass of before is measured exactly as it was.
export function saturate(c: Rgba, amount: number): Rgba {
  if (amount === 1) return c
  const s = amount
  return {
    r: channel((0.213 + 0.787 * s) * c.r + (0.715 - 0.715 * s) * c.g + (0.072 - 0.072 * s) * c.b),
    g: channel((0.213 - 0.213 * s) * c.r + (0.715 + 0.285 * s) * c.g + (0.072 - 0.072 * s) * c.b),
    b: channel((0.213 - 0.213 * s) * c.r + (0.715 - 0.715 * s) * c.g + (0.072 + 0.928 * s) * c.b),
    a: 1,
  }
}
export function brightness(c: Rgba, amount: number): Rgba {
  if (amount === 1) return c
  return { r: channel(c.r * amount), g: channel(c.g * amount), b: channel(c.b * amount), a: 1 }
}

// The wallpaper's samples: a grid of WALLPAPER_STEPS by WALLPAPER_STEPS over the window, and each field's center, where it is
// strongest. The fields are placed in shares of the window, so these points cover every window's shape.
export const WALLPAPER_STEPS = 24
const WALLPAPER_POINTS: readonly [number, number][] = [
  ...Array.from({ length: (WALLPAPER_STEPS + 1) ** 2 }, (_, i): [number, number] => [(i % (WALLPAPER_STEPS + 1)) / WALLPAPER_STEPS, Math.floor(i / (WALLPAPER_STEPS + 1)) / WALLPAPER_STEPS]),
  ...WALLPAPER.map((f): [number, number] => [f.x, f.y]),
]

/** The page at (x, y) under the wallpaper: each field from the bottom layer up, fading from its center to nothing at FADE */
export function wallpaperAt(x: number, y: number, hues: readonly (Rgba | null)[], strength: number, bg: Rgba): Rgba {
  let out = bg
  for (let i = WALLPAPER.length - 1; i >= 0; i--) {
    const hue = hues[i]
    if (!hue) continue
    const f = WALLPAPER[i]
    const dx = (x - f.x) / f.rx
    const dy = (y - f.y) / f.ry
    const share = strength * Math.max(0, 1 - Math.sqrt(dx * dx + dy * dy) / WALLPAPER_FADE)
    if (share > 0) out = composite(alpha(hue, share), out)
  }
  return out
}

// a number slot as the look reads it, or its default when the theme leaves it out
function amount(t: Record<string, string>, slot: string, fallback: number): number {
  const n = Number(t[slot])
  return t[slot] !== undefined && Number.isFinite(n) ? n : fallback
}

function lookContrast(t: Record<string, string>, mode: Mode, where: string, read: (slot: string) => Rgba | null, s: { bg: Rgba | null; panel: Rgba | null }): string[] {
  const out: string[] = []
  const tx = read('tx')
  const mu = read('mu')
  if (!tx || !mu || !s.bg || !s.panel) return out
  // each of tx and mu over the worst of the places a layer can sit
  const check = (ons: Rgba[], what: string) => {
    for (const [name, fg] of [['tx', tx], ['mu', mu]] as const) {
      const ratio = Math.min(...ons.map((on) => contrast(fg, on)))
      if (ratio < MIN[name]) out.push(`${where}: ${name} on ${what} is ${ratio.toFixed(2)}:1, needs at least ${MIN[name]}:1`)
    }
  }
  const hueOf = (hue: string): Rgba | null => {
    const c = read(hue)
    if (!c && t[hue] !== undefined) out.push(`${where}: the look's layers cannot be measured, ${hue} has no usable value`)
    return c
  }
  // the page as text and see-through layers find it: plain, all over the wallpaper, and where the pattern's line and each orb
  // are strongest, over each of those
  const bg = s.bg
  const bases: Rgba[] = [bg]
  const wall = amount(t, 'wallpaper-strength', 0)
  if (wall > 0) {
    const hues = WALLPAPER.map((_, i) => (t[`wallpaper-${i + 1}`] !== undefined ? hueOf(`wallpaper-${i + 1}`) : null))
    if (hues.some((h) => h)) {
      const walled = WALLPAPER_POINTS.map(([x, y]) => wallpaperAt(x, y, hues, wall, bg))
      bases.push(...walled)
      check(walled, "the page's wallpaper at its strongest")
    }
  }
  const pattern = PATTERN_ALPHA[t.backdrop] ?? 0
  const strength = Math.min(1, Math.max(0, Number(t['backdrop-strength'] ?? 0) || 0))
  const orb = ORB_AMOUNT[t.orbs] ?? 0
  const edge = read('edge') ?? mu
  const pages: Rgba[] = [...bases]
  if (pattern * strength > 0 || orb > 0) {
    const hues = orb > 0 ? ORB_HUES[t['orbs-tint']] ?? ORB_HUES.roles : ['edge']
    for (const hue of hues) {
      const c = hueOf(hue)
      if (!c) continue
      const lit = bases.map((base) => composite(alpha(edge, pattern * strength), orb > 0 ? composite(alpha(c, ORB_MAX * orb), base) : base))
      pages.push(...lit)
      check(lit, `the page's backdrop at its strongest (${t.backdrop ?? 'none'}${orb > 0 ? `, ${hue} orb` : ''})`)
    }
  }
  const fill = t['card-fill']
  // every card's fill as text finds it, for the light laid over it below
  const cards: Rgba[] = fill === 'wash' || fill === 'glass' ? [] : [s.panel]
  if (fill === 'wash') {
    // a wash paints no panel under its start: the hue lies over the page there, and over the panel where it ends
    for (const hue of WASH_HUES) {
      const c = hueOf(hue)
      if (!c) continue
      const washed = [s.panel, ...pages].map((on) => composite(alpha(c, WASH_MAX), on))
      cards.push(...washed)
      check(washed, `the strongest card wash (${hue})`)
    }
  }
  const panelRaw = read('panel')
  if (fill === 'glass' && panelRaw) {
    // panel at glass-tint over the page behind, which the glass boosts and lifts (or deepens) by its mode's tone
    const tint = amount(t, 'glass-tint', GLASS_DEFAULT.tint)
    const boost = amount(t, 'glass-saturate', GLASS_DEFAULT.saturate)
    const tone = 1 + (boost - 1) * GLASS[mode].tone
    const glassed = pages.map((on) => composite(alpha(panelRaw, tint), brightness(saturate(on, boost), tone)))
    cards.push(...glassed)
    check(glassed, 'a glass card over the page')
  }
  // the glow inside a card's edge and the highlight at its center, both white, at their strongest over every fill
  const edgeLight = amount(t, 'glass-edge', 0) * GLASS[mode].edgeGlow
  const sheen = amount(t, 'card-sheen', 0) * GLASS[mode].sheen
  if (edgeLight > 0 || sheen > 0) {
    check(cards.map((on) => composite(alpha(WHITE, sheen), composite(alpha(WHITE, edgeLight), on))), "a card under its edge light and highlight")
  }
  // the bloom glows tx in from a card's edges (at most 20% of tx at full strength), under the text nearest the edge
  const bloom = Math.min(1, Math.max(0, Number(t['card-bloom'] ?? 0) || 0))
  if (bloom > 0) check([composite(alpha(tx, 0.2 * bloom), s.panel)], "a card's edge under its bloom")
  return out
}

// The gate: one message per failing pair, naming the tokens. No waiver.
export function checkContrast(m: ThemeManifest, fallback: ThemeManifest): string[] {
  const out: string[] = []
  const variants = typeof m.accent === 'object' ? m.accent.variants : []
  for (const mode of m.modes) {
    // a missing mode in the fallback reads its other one, so a one-mode Default still backs a theme
    const base = fallback.tokens[mode] ?? fallback.tokens.dark ?? fallback.tokens.light ?? {}
    const own = m.tokens[mode] ?? {}
    for (const variant of [null, ...variants]) {
      const where = `${mode}${variant ? ` (${variant.name})` : ''}`
      const t: Record<string, string> = { ...base, ...own, ...variant?.tokens }
      // a derived slot the theme leaves unset is painted from the theme's own values (edge is its mu), not the fallback's literal
      for (const [k, form] of Object.entries(FROM_VAR)) {
        if (Object.hasOwn(own, k) || (variant && Object.hasOwn(variant.tokens, k))) continue
        const names = [...form.matchAll(/var\(--([a-z-]+)\)/g)].map((x) => x[1])
        if (names.every((n) => t[n] !== undefined)) t[k] = form.replace(/var\(--([a-z-]+)\)/g, (_, n: string) => t[n])
        else delete t[k]
      }
      const read = (slot: string): Rgba | null => (t[slot] ? paintOf(t[slot]) : null)
      const page = read('bg')
      const floor: Rgba = { r: mode === 'light' ? 255 : 0, g: mode === 'light' ? 255 : 0, b: mode === 'light' ? 255 : 0, a: 1 }
      const bg = page && composite(page, floor)
      // a translucent surface sits on the one under it: panel on bg, raise on panel, ac on bg
      const panelRaw = read('panel')
      const panel = bg && panelRaw && composite(panelRaw, bg)
      const raiseRaw = read('raise')
      const raise = panel && raiseRaw && composite(raiseRaw, panel)
      const ac = bg && (read('ac') ? composite(read('ac') as Rgba, bg) : null)
      const surface = { bg, panel, raise, ac }
      out.push(...lookContrast(t, mode, where, read, { bg, panel }))
      for (const [fgName, onName] of PAIRS) {
        const fg = read(fgName)
        const on = surface[onName]
        if (!fg || !on) {
          const missing = !fg ? fgName : onName
          out.push(`${where}: ${fgName} on ${onName} cannot be measured, ${missing} has no usable value`)
          continue
        }
        const ratio = contrast(fg, on)
        if (ratio < MIN[fgName]) out.push(`${where}: ${fgName} on ${onName} is ${ratio.toFixed(2)}:1, needs at least ${MIN[fgName]}:1`)
      }
    }
  }
  return out
}

export function checkTheme(text: string, ctx: Ctx, fallback: ThemeManifest): Result {
  const r = parseManifest(text, ctx)
  if (!r.ok) return r
  const errors = checkContrast(r.manifest, fallback)
  return errors.length ? { ok: false, errors } : r
}
