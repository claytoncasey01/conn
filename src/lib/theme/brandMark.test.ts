// The Conn mark is one drawing in three places (the header tile, the gallery preview, the favicon), with two more of the same
// C and pill a theme can draw it as, shown by one token; every bundled theme shows one of them.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import crt from '../../../themes/crt/theme.json'
import defaultTheme from '../../../themes/default/theme.json'
import matrix from '../../../themes/matrix/theme.json'
import neon from '../../../themes/neon/theme.json'
import { BRAND_MARK, BRAND_MARK_SHAPES, BRAND_PIXEL_SHAPES, BRAND_TUBE_SHAPES } from './brandMark.ts'
import { LOOK, lookDeclarations } from './css.ts'
import { LOOK_RULES } from './lookCss.ts'
import { SLOTS } from './tokens.ts'
import type { ThemeManifest } from './types.ts'

const file = (rel: string) => readFileSync(new URL(`../../../${rel}`, import.meta.url), 'utf8')
const glyph = (m: unknown, mode: 'light' | 'dark') => (m as ThemeManifest).tokens[mode]?.['glyph-brand']

test('the mark is a decorative SVG painted only in currentColor, so the tile and the favicon color it', () => {
  assert.match(BRAND_MARK, /^<svg class="g-brand-mark" viewBox="0 0 32 32" aria-hidden="true" focusable="false">.*<\/svg>$/)
  assert.ok(BRAND_MARK.includes(`<g class="bm-solid">${BRAND_MARK_SHAPES}</g>`))
  assert.ok(BRAND_MARK.includes(`<g class="bm-tube">${BRAND_TUBE_SHAPES}</g>`))
  assert.ok(BRAND_MARK.includes(`<g class="bm-pixel">${BRAND_PIXEL_SHAPES}</g>`))
  assert.ok(!/#[0-9a-f]{3,8}|rgb|hsl|oklch|url\(|<script|xmlns/i.test(BRAND_MARK), BRAND_MARK)
  assert.equal([...BRAND_MARK.matchAll(/(?:fill|stroke)="([^"]+)"/g)].filter((m) => m[1] !== 'none' && m[1] !== 'currentColor').length, 0)
})

test('the favicon draws the same shapes as the header tile, and the page links it and is named Conn', () => {
  assert.ok(file('public/favicon.svg').includes(BRAND_MARK_SHAPES), 'public/favicon.svg and brandMark.ts have drifted apart')
  const html = file('index.html')
  assert.match(html, /<link rel="icon" type="image\/svg\+xml" href="\/favicon\.svg" \/>/)
  assert.match(html, /<title>Conn<\/title>/)
})

const MARKS: Record<string, string> = { conn: 'solid', 'conn-tube': 'tube', 'conn-pixel': 'pixel', 'conn-phosphor': 'solid' }
const DRAWINGS = ['solid', 'tube', 'pixel']

test('each conn keyword shows the mark in its one drawing with no text glyph, and every other keyword shows only its glyph', () => {
  assert.deepEqual(
    SLOTS['glyph-brand'].values?.filter((value) => value.startsWith('conn')),
    Object.keys(MARKS),
  )
  for (const [key, row] of Object.entries(LOOK['glyph-brand'])) {
    const drawing = MARKS[key]
    assert.equal(row['--look-brand-mark'], drawing ? 'block' : 'none', key)
    if (drawing) assert.equal(row['--look-brand'], '""', key)
    for (const name of DRAWINGS) assert.equal(row[`--look-brand-${name}`], name === drawing ? 'inline' : 'none', `${key} ${name}`)
  }
  assert.ok(lookDeclarations({ 'glyph-brand': 'conn-pixel' }).includes('--look-brand-mark:block;--look-brand-solid:none;--look-brand-tube:none;--look-brand-pixel:inline;'))
  assert.ok(lookDeclarations({ 'glyph-brand': 'prompt' }).includes('--look-brand-mark:none;'))
})

test('every bundled theme shows the Conn mark in each mode and variant: Default as the app icon, Neon as a tube, Matrix in pixels, CRT as phosphor', () => {
  for (const mode of ['light', 'dark'] as const) assert.equal(glyph(defaultTheme, mode), 'conn', mode)
  assert.equal(glyph(neon, 'dark'), 'conn-tube')
  assert.equal(glyph(matrix, 'dark'), 'conn-pixel')
  assert.equal(glyph(crt, 'dark'), 'conn-phosphor')
  for (const theme of [defaultTheme, neon, matrix, crt] as unknown as ThemeManifest[]) {
    if (typeof theme.accent !== 'object') continue
    for (const variant of theme.accent.variants) assert.equal(variant.tokens['glyph-brand'], undefined, `${theme.id} ${variant.name}`)
  }
})

test('the header tile is the accent with its ink, or lit (reversed, ringed and glowing) for a tube or phosphor mark', () => {
  const brand = file('src/lib/Brand.svelte')
  assert.match(brand, /<div class="g-brand-tile" style="[^"]*box-shadow: 0 0 22px/)
  assert.ok(!/background:|color: var/.test(brand.match(/<div class="g-brand-tile" style="([^"]*)"/)?.[1] ?? ''), 'the tile takes its colors from .g-brand-tile')
  assert.ok(!/linear-gradient\(135deg/.test(brand), 'the tile is one accent color, not a gradient of two roles')
  assert.match(file('src/lib/theme/preview.ts'), /<span class="brand g-brand-tile"/)
  assert.match(LOOK_RULES, /\.g-brand-tile\{background:var\(--look-brand-tile, var\(--ac\)\);color:var\(--look-brand-ink, var\(--onac\)\);outline:var\(--look-brand-ring, none\)/)
  for (const [key, row] of Object.entries(LOOK['glyph-brand'])) {
    const lit = key === 'conn-tube' || key === 'conn-phosphor'
    assert.equal(row['--look-brand-tile'], lit ? 'var(--onac)' : 'var(--ac)', key)
    assert.equal(row['--look-brand-ink'], lit ? 'var(--ac)' : 'var(--onac)', key)
    assert.equal(row['--look-brand-bloom'] === 'none', !lit, key)
  }
  assert.match(LOOK['glyph-brand']['conn-phosphor']['--look-brand-lines'], /^repeating-linear-gradient\(/)
})

test('the favicon is a solid teal tile with a black mark, the app icon reversed', () => {
  const svg = file('public/favicon.svg')
  assert.match(svg, /<rect [^>]*fill="#3bcfcf"/)
  assert.match(svg, /<g color="#000"/)
  assert.ok(!svg.includes('Gradient'), 'no gradient')
})
