// The Dock icon a theme means: the drawing its header uses, in its dark colors, and the bundle's own icon for Default.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import crt from '../../../themes/crt/theme.json'
import defaultTheme from '../../../themes/default/theme.json'
import matrix from '../../../themes/matrix/theme.json'
import neon from '../../../themes/neon/theme.json'
import { CLASSIC, ICON_LIMITS, iconChoiceOf, iconChoiceText, iconKey, iconSpecFor, iconSpecOf, iconSvg, isClassic, pinnable, type IconChoice, type IconSpec } from './appIcon.ts'
import { BRAND_MARK_SHAPES, BRAND_PIXEL_SHAPES, BRAND_TUBE_SHAPES } from './brandMark.ts'
import type { Choice } from './storage.ts'
import type { ThemeEntry, ThemeManifest } from './types.ts'

const fallback = defaultTheme as unknown as ThemeManifest
const entry = (m: unknown, source: ThemeEntry['source'] = 'builtin'): ThemeEntry => {
  const manifest = m as ThemeManifest
  return { id: manifest.id, source, hasCss: false, cssSha256: null, manifest }
}
const list = [entry(defaultTheme), entry(neon), entry(matrix), entry(crt)]
const person: Choice = { id: 'default', mode: 'light', accent: null, variant: null, effects: 'auto' }
const spec = (choice: IconChoice, theme: Partial<Choice> = {}) => iconSpecFor(choice, { list, theme: { ...person, ...theme }, fallback })

test('the limits are the app’s, from testdata/app-icon.json', () => {
  const shared = JSON.parse(readFileSync(new URL('../../../testdata/app-icon.json', import.meta.url), 'utf8')) as unknown
  assert.deepEqual({ ...ICON_LIMITS }, shared)
})

test('Default at its own accent is the bundle’s icon, in light mode too', () => {
  assert.deepEqual(spec('match'), CLASSIC)
  assert.deepEqual(spec('match', { mode: 'dark' }), CLASSIC)
  assert.equal(iconKey(spec('match')), 'classic')
})

test('a picked accent colors the solid mark, on the classic black', () => {
  const violet = spec('match', { accent: { name: 'Violet', hue: 295, chroma: 0.15 } })
  assert.equal(violet.drawing, 'solid')
  assert.equal(violet.ground, CLASSIC.ground)
  assert.notEqual(violet.mark, CLASSIC.mark)
  assert.equal(isClassic(violet), false)
})

const rows: { theme: string; drawing: IconSpec['drawing']; mark: string; ground: string }[] = [
  { theme: 'neon', drawing: 'tube', mark: '#5ad7ff', ground: '#05060d' },
  { theme: 'matrix', drawing: 'pixel', mark: '#00ff41', ground: '#010a04' },
  { theme: 'crt', drawing: 'phosphor', mark: '#8bffad', ground: '#030604' },
]
for (const row of rows) {
  test(`matching ${row.theme} draws its header’s mark in its colors`, () => {
    assert.deepEqual(spec('match', { id: row.theme }), { drawing: row.drawing, mark: row.mark, ground: row.ground })
  })
}

test('matching follows the variant', () => {
  assert.equal(spec('match', { id: 'crt', variant: 'Amber' }).mark, '#ffbc00')
  assert.equal(spec('match', { id: 'crt', variant: 'Amber' }).ground, '#070502')
})

test('a pinned theme is its own look, whatever the theme, accent or variant on screen', () => {
  const pinned = { theme: 'crt' }
  assert.deepEqual(spec(pinned), spec('match', { id: 'crt' }))
  assert.deepEqual(spec(pinned, { id: 'crt', variant: 'Amber', accent: { name: 'Rose', hue: 355, chroma: 0.16 } }), spec('match', { id: 'crt' }))
})

test('Classic is the bundle’s icon on any theme, and a pinned theme no longer listed falls back to it', () => {
  assert.deepEqual(spec('classic', { id: 'neon' }), CLASSIC)
  assert.deepEqual(spec({ theme: 'gone' }, { id: 'neon' }), CLASSIC)
})

test('a text glyph has no drawing of the mark, so it gets the solid one', () => {
  assert.equal(iconSpecOf({ 'glyph-brand': 'prompt', ac: '#ff0000', bg: '#ffffff' }).drawing, 'solid')
  assert.equal(iconSpecOf({ 'glyph-brand': 'conn-tube', ac: 'rgba(255,0,0,.5)', bg: '#000' }).mark, '#800000')
})

test('an accent or background the icon cannot read gives the classic, never a black mark on black', () => {
  const mixed = 'color-mix(in oklch, #ff0000 50%, transparent)'
  assert.deepEqual(iconSpecOf({ 'glyph-brand': 'conn-tube', ac: mixed, bg: '#000000' }), CLASSIC)
  assert.deepEqual(iconSpecOf({ 'glyph-brand': 'conn-tube', ac: '#ff0000', bg: mixed }), CLASSIC)
  assert.deepEqual(iconSpecOf({ 'glyph-brand': 'conn' }), CLASSIC)
})

const choices: [unknown, IconChoice][] = [
  ['match', 'match'],
  ['classic', 'classic'],
  ['theme:crt', { theme: 'crt' }],
  ['theme:Bad Id', 'match'],
  ['theme:', 'match'],
  ['crt', 'match'],
  [null, 'match'],
  [7, 'match'],
]
for (const [stored, want] of choices) {
  test(`a stored choice of ${JSON.stringify(stored)} reads as ${JSON.stringify(want)}`, () => {
    assert.deepEqual(iconChoiceOf(stored), want)
  })
}

test('a choice survives being stored', () => {
  for (const c of ['match', 'classic', { theme: 'neon' }] as IconChoice[]) assert.deepEqual(iconChoiceOf(iconChoiceText(c)), c)
})

test('Settings offers each listed theme once to pin, but Default, whose icon is the classic', () => {
  const broken = { ...entry(neon, 'user'), manifest: undefined, invalid: 'bad' } as ThemeEntry
  assert.deepEqual(pinnable([...list, entry(neon, 'user'), broken]), [
    { id: 'neon', name: 'Neon' },
    { id: 'matrix', name: 'Matrix' },
    { id: 'crt', name: 'CRT' },
  ])
})

const drawings: [IconSpec['drawing'], string][] = [
  ['solid', BRAND_MARK_SHAPES],
  ['tube', BRAND_TUBE_SHAPES],
  ['pixel', BRAND_PIXEL_SHAPES],
  ['phosphor', BRAND_MARK_SHAPES],
]
for (const [drawing, shapes] of drawings) {
  test(`the ${drawing} icon is the header’s drawing on the app’s 1024 grid, in its own colors only`, () => {
    const svg = iconSvg({ drawing, mark: '#123456', ground: '#0a0b0c' })
    assert.match(svg, /^<svg xmlns="http:\/\/www.w3.org\/2000\/svg" width="1024" height="1024" viewBox="0 0 1024 1024">.*<\/svg>$/)
    assert.ok(svg.includes(shapes), 'the icon has drifted from brandMark.ts')
    assert.ok(svg.includes('color="#123456"'))
    // Nothing is fetched: every url() names something in the icon itself
    assert.deepEqual([...svg.matchAll(/url\(([^)]*)\)/g)].filter((m) => !m[1].startsWith('#')), [])
    assert.ok(!/<script|href=/i.test(svg))
  })
}
