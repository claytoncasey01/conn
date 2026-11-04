// CRT's own rules: the spec's green values, the amber and white variants held to the same bar, and what the tube effects do to contrast.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { composite, contrast, parseColor, type Rgba } from '../src/lib/theme/color.ts'
import { checkTheme, parseManifest } from '../src/lib/theme/validate.ts'
import type { ThemeManifest, TokenSet } from '../src/lib/theme/types.ts'
import { AMOUNT_MAX } from '../src/effects/flicker.ts'
import { OPACITY_MAX } from '../src/effects/scanlines.ts'
import { STRENGTH_MAX } from '../src/effects/vignette.ts'

const root = new URL('.', import.meta.url).pathname
const ctx = { source: 'builtin' as const, builtinIds: new Set(['default', 'neon', 'matrix', 'crt']) }
const text = () => readFileSync(`${root}crt/theme.json`, 'utf8')

function load(): ThemeManifest {
  const r = parseManifest(text(), ctx)
  assert.ok(r.ok, r.ok ? '' : r.errors.join('; '))
  return r.manifest
}

const variants = (): { name: string; tokens: TokenSet }[] => {
  const a = load().accent
  assert.ok(typeof a === 'object', 'accent is { variants }')
  return a.variants
}
// the base tokens with a variant's applied over them, as the page paints it
const tokensOf = (name: string): TokenSet => {
  const v = variants().find((w) => w.name === name)
  assert.ok(v, `variant ${name}`)
  return { ...load().tokens.dark, ...v.tokens }
}
const BLACK: Rgba = { r: 0, g: 0, b: 0, a: 1 }
const color = (t: TokenSet, slot: string): Rgba => {
  const c = parseColor(t[slot])
  assert.ok(c, `${slot} is a color`)
  return c
}
const bgOf = (t: TokenSet) => composite(color(t, 'bg'), BLACK)
const panelOf = (t: TokenSet) => composite(color(t, 'panel'), bgOf(t))
const mix = (c: Rgba, toward: number): Rgba => ({ r: c.r * (1 - toward), g: c.g * (1 - toward), b: c.b * (1 - toward), a: 1 })
const NAMES = ['Green', 'Amber', 'White']

test('CRT is dark only, three phosphor variants, the whole tube as effects, mono type and a square corner', () => {
  const m = load()
  assert.equal(m.id, 'crt')
  assert.deepEqual(m.modes, ['dark'])
  assert.deepEqual(variants().map((v) => v.name), NAMES)
  assert.deepEqual(m.effects.map((e) => e.name), ['scanlines', 'vignette', 'flicker', 'roll', 'grain'])
  // options come back as null-prototype objects, so they are read key by key
  assert.deepEqual(Object.entries(m.effects[0].options ?? {}), [['opacity', 0.16], ['crawl', 3], ['grille', true]])
  assert.deepEqual(Object.entries(m.effects[1].options ?? {}), [['strength', 0.55], ['glass', true]])
  assert.deepEqual(Object.entries(m.effects[2].options ?? {}), [['amount', 0.03], ['hz', 4]])
  assert.deepEqual(Object.entries(m.effects[3].options ?? {}), [['strength', 0.04], ['period', 8]])
  assert.deepEqual(Object.entries(m.effects[4].options ?? {}), [['strength', 0.06], ['hz', 6]])
  assert.deepEqual(m.fonts, ['JetBrains Mono'])
  const t = load().tokens.dark ?? {}
  assert.equal(t.radius, '0px')
  assert.equal(t.led, 'square')
  assert.equal(t.motion, 'power')
  assert.equal(t.glow, '.36')
  assert.equal(t.blur, '0px')
  assert.equal(t.tracking, '.04em')
  assert.equal(t.case, 'uppercase')
})

test('the Green variant is the spec appendix verbatim', () => {
  const g = tokensOf('Green')
  const spec = { bg: '#030604', panel: 'rgba(10,30,16,.35)', line: '#12301c', line2: '#4e9864', tx: '#8bffad', bright: '#c8ffda', mu: '#4e9864', ac: '#8bffad', onac: '#030604', warn: '#ffd27a', bad: '#ff7a7a' }
  for (const [slot, v] of Object.entries(spec)) assert.equal(g[slot], v, slot)
})

test('every variant passes the contrast gate, each with the base under it', () => {
  const r = checkTheme(text(), ctx, JSON.parse(readFileSync(`${root}default/theme.json`, 'utf8')))
  assert.ok(r.ok, r.ok ? '' : r.errors.join('; '))
})

test('tx and mu on bg keep the spec\'s ratios: Green 16.4 and 5.7, Amber and White 12 and 4.5', () => {
  for (const name of NAMES) {
    const t = tokensOf(name)
    const [tx, mu] = name === 'Green' ? [16.4, 5.7] : [12, 4.5]
    assert.ok(contrast(color(t, 'tx'), bgOf(t)) >= tx, `${name} tx on bg ${contrast(color(t, 'tx'), bgOf(t)).toFixed(2)}`)
    assert.ok(contrast(color(t, 'mu'), bgOf(t)) >= mu, `${name} mu on bg ${contrast(color(t, 'mu'), bgOf(t)).toFixed(2)}`)
  }
})

// The tube effects draw over the page and darken it. At their option caps (scanlines .2, vignette .6, flicker .06) the worst pixel loses .70 of its light
// (.62 at the defaults), but no color can read through that: pure white on black is 3.39:1 at .62 and 2.48:1 at .70.
// What each variant can bear, tx held to 4.5 and mu to 3 (the figures this test's .30 is picked from): Green tx .49 mu .33, Amber tx .40 mu .34, White tx .48 mu .32.
// A scanline row (OPACITY_MAX, .2) and the flicker veil (AMOUNT_MAX, .06) are over every pixel: 1 - .8 * .94 = .25, so .30 leaves room for a little vignette.
// The vignette's own cap (STRENGTH_MAX, .6) is what the outer ring takes at its very corner; it is read here only to say what the corner costs.
// The vignette's outer ring (it clears only to about 55% of the way out, so a header row, a footer and a right column are in it) is knowingly dimmer,
// like the edge of a real tube; the screenshots of every route are where that is judged.
const TUBE = 0.3
test('tx stays 4.5:1 and mu 3:1 on bg when both are darkened .30 toward black (the scanline and flicker caps, with room for the vignette\'s start)', () => {
  assert.ok(1 - (1 - OPACITY_MAX) * (1 - AMOUNT_MAX) <= TUBE, 'the caps of the scanline and flicker effects fit under the figure held')
  assert.ok(STRENGTH_MAX > 0, 'the vignette has a cap')
  for (const name of NAMES) {
    const t = tokensOf(name)
    const bg = mix(bgOf(t), TUBE)
    const tx = contrast(mix(color(t, 'tx'), TUBE), bg)
    const mu = contrast(mix(color(t, 'mu'), TUBE), bg)
    assert.ok(tx >= 4.5, `${name} tx under the tube ${tx.toFixed(2)}`)
    assert.ok(mu >= 3, `${name} mu under the tube ${mu.toFixed(2)}`)
  }
})

test('the variants override the same keys, and those include the six a phosphor is made of', () => {
  const [first, ...rest] = variants().map((v) => Object.keys(v.tokens).sort())
  for (const k of ['ac', 'tx', 'mu', 'line', 'line2', 'bright']) assert.ok(first.includes(k), `${k} is overridden`)
  for (const keys of rest) assert.deepEqual(keys, first)
})

test('every status and category color is at least 3:1 on the panel, in every variant', () => {
  const slots = ['ok', 'warn', 'bad', 'idle', 'info', 'k-bug', 'k-epic', 'k-task', 'k-story', 'k-subtask', 'teammate', 'role-lead', 'role-agent', 'role-advisor', 'edge', 'stall']
  for (const name of NAMES) {
    const t = tokensOf(name)
    for (const s of slots) {
      assert.ok(t[s], `${name} ${s} is set`)
      assert.ok(contrast(color(t, s), panelOf(t)) >= 3, `${name} ${s} on panel ${contrast(color(t, s), panelOf(t)).toFixed(2)}:1`)
    }
  }
})

const css = () => readFileSync(`${root}crt/theme.css`, 'utf8')

test('the block cursor and the phosphor glow on text are tokens; theme.css only blinks the cursor, and only when motion is allowed', () => {
  const t = load().tokens.dark ?? {}
  assert.deepEqual([t.cursor, t['text-glow']], ['block', t.glow], 'a block cursor, and text glowing as much as the dots')
  const c = css()
  assert.ok(/\.cursor\s*\{/.test(c), 'a .cursor rule')
  assert.ok(!/text-shadow|width|height|background/.test(c), 'nothing but the blink: the rest is tokens')
  assert.ok(!/url\(|@import/i.test(c), 'no url( or @import')
  // every `animation` sits inside the no-preference block, and none outside it
  const inside = /@media \(prefers-reduced-motion: no-preference\)\s*\{((?:[^{}]|\{[^{}]*\})*)\}/.exec(c)
  assert.ok(inside, 'a no-preference block')
  assert.ok(/animation/.test(inside[1]), 'the blink is in it')
  assert.ok(!/animation/.test(c.replace(inside[0], '')), 'no animation outside it')
})

// Status colors must not be the phosphor in another coat. Hue is compared in OKLCH when both colors carry a hue (chroma >= .05),
// otherwise (a near-gray phosphor has none) by the distance in OKLab.
function oklab(c: Rgba): { L: number; a: number; b: number } {
  const lin = (v: number) => ((v / 255) <= 0.04045 ? v / 255 / 12.92 : ((v / 255 + 0.055) / 1.055) ** 2.4)
  const [r, g, b] = [lin(c.r), lin(c.g), lin(c.b)]
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  return { L: 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s, a: 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s, b: 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s }
}
const hueChroma = (c: Rgba) => {
  const o = oklab(c)
  return { h: (Math.atan2(o.b, o.a) * 180) / Math.PI, chroma: Math.hypot(o.a, o.b) }
}
function apart(x: Rgba, y: Rgba): boolean {
  const a = hueChroma(x)
  const b = hueChroma(y)
  if (a.chroma >= 0.05 && b.chroma >= 0.05) {
    const d = Math.abs(a.h - b.h) % 360
    return Math.min(d, 360 - d) >= 25
  }
  const p = oklab(x)
  const q = oklab(y)
  return Math.hypot(p.L - q.L, p.a - q.a, p.b - q.b) >= 0.1 // the old Amber warn on its tx was .073, a yellow on near-white White is .115
}

test('in every variant warn, k-epic and role-advisor are told apart from tx, and stall from teammate', () => {
  for (const name of NAMES) {
    const t = tokensOf(name)
    for (const [s, other] of [['warn', 'tx'], ['k-epic', 'tx'], ['role-advisor', 'tx'], ['stall', 'teammate'], ['warn', 'bad'], ['stall', 'bad']] as const) {
      assert.ok(apart(color(t, s), color(t, other)), `${name} ${s} ${t[s]} is too close to ${other} ${t[other]}`)
    }
  }
})

test('warn, k-epic, role-advisor and stall are set by every variant, so a switch never keeps another phosphor\'s', () => {
  for (const v of variants()) for (const k of ['warn', 'k-epic', 'role-advisor', 'stall']) assert.ok(v.tokens[k], `${v.name} overrides ${k}`)
})
