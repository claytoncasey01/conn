// The Conn mark is one drawing in three places (the header tile, the gallery preview, the favicon), shown by one token.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import crt from '../../../themes/crt/theme.json'
import defaultTheme from '../../../themes/default/theme.json'
import matrix from '../../../themes/matrix/theme.json'
import neon from '../../../themes/neon/theme.json'
import { BRAND_MARK, BRAND_MARK_SHAPES } from './brandMark.ts'
import { LOOK, lookDeclarations } from './css.ts'
import { SLOTS } from './tokens.ts'
import type { ThemeManifest } from './types.ts'

const file = (rel: string) => readFileSync(new URL(`../../../${rel}`, import.meta.url), 'utf8')
const glyph = (m: unknown, mode: 'light' | 'dark') => (m as ThemeManifest).tokens[mode]?.['glyph-brand']

test('the mark is a decorative SVG painted only in currentColor, so the tile and the favicon color it', () => {
  assert.match(BRAND_MARK, /^<svg class="g-brand-mark" viewBox="0 0 32 32" aria-hidden="true" focusable="false">.*<\/svg>$/)
  assert.ok(BRAND_MARK.includes(BRAND_MARK_SHAPES))
  assert.ok(!/#[0-9a-f]{3,8}|rgb|hsl|oklch|url\(|<script|xmlns/i.test(BRAND_MARK), BRAND_MARK)
  assert.equal([...BRAND_MARK.matchAll(/(?:fill|stroke)="([^"]+)"/g)].filter((m) => m[1] !== 'none' && m[1] !== 'currentColor').length, 0)
})

test('the favicon draws the same shapes as the header tile, and the page links it and is named Conn', () => {
  assert.ok(file('public/favicon.svg').includes(BRAND_MARK_SHAPES), 'public/favicon.svg and brandMark.ts have drifted apart')
  const html = file('index.html')
  assert.match(html, /<link rel="icon" type="image\/svg\+xml" href="\/favicon\.svg" \/>/)
  assert.match(html, /<title>Conn<\/title>/)
})

test('glyph-brand has a conn keyword that shows the mark with no text glyph, and every other keyword hides it', () => {
  assert.ok(SLOTS['glyph-brand'].values?.includes('conn'))
  assert.deepEqual(LOOK['glyph-brand'].conn, { '--look-brand': '""', '--look-brand-size': '1em', '--look-brand-mark': 'block' })
  for (const [key, row] of Object.entries(LOOK['glyph-brand'])) if (key !== 'conn') assert.equal(row['--look-brand-mark'], 'none', key)
  assert.ok(lookDeclarations({ 'glyph-brand': 'conn' }).includes('--look-brand-mark:block;'))
  assert.ok(lookDeclarations({ 'glyph-brand': 'prompt' }).includes('--look-brand-mark:none;'))
})

test('Default wears the Conn mark in both modes, and Neon, Matrix and CRT keep the glyphs that were theirs', () => {
  for (const mode of ['light', 'dark'] as const) assert.equal(glyph(defaultTheme, mode), 'conn', mode)
  assert.equal(glyph(neon, 'dark'), 'blocks')
  assert.equal(glyph(matrix, 'dark'), 'prompt')
  assert.equal(glyph(crt, 'dark'), 'none')
})

test('the header tile is the theme accent with the ink that reads on it, so every theme and accent colors it', () => {
  const brand = file('src/lib/Brand.svelte')
  assert.match(brand, /background: var\(--ac\); color: var\(--onac\);/)
  assert.ok(!/linear-gradient\(135deg/.test(brand), 'the tile is one accent color, not a gradient of two roles')
  assert.match(file('src/lib/theme/preview.ts'), /\.brand\{[^}]*background:var\(--ac\);color:var\(--onac\)/)
})

test('the favicon is a solid teal tile with a black mark, the app icon reversed', () => {
  const svg = file('public/favicon.svg')
  assert.match(svg, /<rect [^>]*fill="#3bcfcf"/)
  assert.match(svg, /<g color="#000"/)
  assert.ok(!svg.includes('Gradient'), 'no gradient')
})
