// The theme judgment runs twice: here in the page (validate.ts) and in the Go server (internal/theme). This test holds the
// two to one set of answers: it builds a few thousand seeded cases (colors, contrast pairs, slot values and whole manifests),
// works out what validate.ts says of each, and keeps cases and answers in testdata/theme-cases.json. A normal run works the
// answers out again from the kept inputs and checks they are still the file's; the Go test reads the same file and gives the
// same answers. CONN_UPDATE_GOLDEN=1 builds the cases afresh (from today's built-in themes) and rewrites the file.
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { test } from 'node:test'
import { contrast, parseColor } from './color.ts'
import { ALLOWED_FONTS, FROM_VAR, KNOWN_EFFECTS, LIMITS, SLOTS } from './tokens.ts'
import type { ThemeSource } from './types.ts'
import { checkTheme, parseManifest, slotProblem } from './validate.ts'

const root = new URL('../../../', import.meta.url).pathname
const file = `${root}testdata/theme-cases.json`

// mulberry32: the same seed gives the same cases on every run
function random(seed: number) {
  let a = seed
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
const rand = random(20261103)
const pick = <T>(items: readonly T[]): T => items[Math.floor(rand() * items.length)]
const int = (lo: number, hi: number) => lo + Math.floor(rand() * (hi - lo + 1))
const decimal = (lo: number, hi: number, places: number) => (lo + rand() * (hi - lo)).toFixed(places)

const spaces = ['', ' ', '  ', '\t', '\n', '\u00a0']
const sp = () => pick(spaces)

function numberText(): string {
  return pick([
    () => String(int(0, 300)),
    () => decimal(-20, 300, int(1, 4)),
    () => `.${int(0, 99)}`,
    () => `+${int(0, 99)}`,
    () => `-${decimal(0, 2, 2)}`,
    () => `${int(0, 9)}.`,
    () => 'x',
    () => '1e2',
  ])()
}
const pct = () => `${pick([String(int(0, 120)), decimal(0, 100, 2), `-${int(0, 9)}`])}%`

function colorText(): string {
  const hexLength = pick([3, 4, 5, 6, 7, 8, 9])
  const hex = () => `#${Array.from({ length: hexLength }, () => pick([...'0123456789abcdefABCDEFg'])).join('')}`
  const name = () => pick(['rgb', 'rgba', 'RGB', 'Rgba', 'hsl', 'hsla', 'HSL', 'oklch', 'OKLCH', 'okLch', 'h\u017fl', 'lab'])
  const part = () => pick([numberText, pct, () => `${int(0, 400)}deg`, () => `${int(0, 400)}DEG`, () => 'none'])()
  const alpha = () => pick(['', `${sp()}/${sp()}${pick([decimal(0, 1, 2), pct(), '2', '-1'])}`])
  const spaced = () => `${name()}(${sp()}${part()} ${part()}${sp()} ${part()}${alpha()}${sp()})`
  const commas = () => `${name()}(${part()},${sp()}${part()}, ${part()}${pick(['', `,${sp()}${decimal(0, 1, 2)}`, `, ${pct()}`])})`
  const oklch = () => `oklch(${decimal(0, 1, 3)} ${decimal(0, 0.4, 3)} ${int(0, 720)}${pick(['', 'deg'])}${alpha()})`
  const hsl = () => `hsl(${int(-400, 800)}${pick(['', 'deg'])} ${pct()} ${pct()}${alpha()})`
  return `${sp()}${pick([hex, spaced, commas, oklch, oklch, hsl])()}${sp()}`
}

const handColors = [
  '#fff', '#FFFFFF', '#0000', '#12345678', 'rgb(1 2 3)', 'rgb(1,2,3)', 'rgba(1, 2, 3, .5)', 'rgb(10% 20% 30% / 50%)', 'rgb(300 -4 2)',
  'hsl(120 50% 50%)', 'hsl(120deg, 50%, 50%)', 'hsl(120 50 50)', 'hsl(-30 100% 50%)', 'hsl(390 100% 50%)', 'oklch(.5 .1 30)',
  'oklch(50% 50% 30deg / .5)', 'oklch(.5,.1,30)', 'oklch(1.2 -1 30)', 'oklch(0.627 0.258 29.23)', 'rgb(1 2)', 'rgb(1 2 3 4)',
  'rgb(1 2 3 / 4 / 5)', 'rgb()', 'rgb( 1 2 3 )', 'h\u017fl(1 2% 3%)', 'rgb(1\u00a02 3)', 'red', 'transparent', '', ' ', '#ggg',
]

function shadowText(): string {
  const layer = () => {
    const parts = [pick(['', 'inset ']) + `${int(-70, 70)}px`, `${int(-10, 10)}${pick(['px', 'rem', 'em', '', '%'])}`]
    for (let i = int(0, 3); i > 0; i--) parts.push(`${int(-5, 30)}${pick(['px', 'rem', 'em'])}`)
    parts.push(pick([colorText, () => 'oklch(.3 .05 260/.06)', () => '', () => 'blue', () => '#000 #fff'])())
    return parts.join(' ')
  }
  return Array.from({ length: int(1, 5) }, layer).join(pick([', ', ',', ' , ']))
}

function fontText(slot: string): string {
  const family = () => pick(["'IBM Plex Sans'", '"JetBrains Mono"', 'Chakra Petch', 'Comic Sans', 'system-ui', 'ui-sans-serif', 'sans-serif',
    'ui-monospace', 'monospace', "'system-ui'", 'serif', "'Bad;'", 'Share Tech Mono', '1bad'])
  const parts = Array.from({ length: int(0, 9) }, family)
  if (rand() < 0.6) parts.push(slot === 'font-mono' ? 'ui-monospace' : 'system-ui')
  return parts.join(pick([', ', ',']))
}

function valueFor(slot: string): string {
  const spec = SLOTS[slot]
  const forbidden = () => pick(['url(x)', 'URL(x)', 'a;b', '{', '@import', '@IMPORT', 'expression(1)', 'expre\u017f\u017fion', 'ExPrEsSiOn'])
  if (rand() < 0.08) return forbidden()
  if (rand() < 0.02) return 'x'.repeat(int(299, 301))
  switch (spec.kind) {
    case 'color':
      return pick([colorText, colorText, () => `color-mix(in oklch, ${colorText()} ${pick([String(int(0, 120)), decimal(0, 100, 1), '.5'])}%, transparent)`,
        () => 'color-mix(in oklch, var(--ac) 14%, transparent)', () => `color-mix(in oklch, #fff\r 10%, transparent)`])()
    case 'length':
      return pick([() => `${decimal(-1, 30, int(0, 2))}${pick(['px', 'rem', 'em', '', 'pt'])}`, () => '0', () => '-0', () => `${int(0, 99)}px`])()
    case 'number':
      return pick([() => decimal(-0.5, 4.5, int(0, 3)), () => String(int(0, 5)), () => 'one', () => '.5', () => '1.'])()
    case 'enum':
      return rand() < 0.8 ? pick(spec.values ?? []) : pick(['', 'None', 'other', ' on'])
    case 'shadow':
      return rand() < 0.1 ? 'none' : shadowText()
    case 'font':
      return fontText(slot)
  }
}

// a value the slot takes, so a case built from these reaches the contrast gate
function goodValueFor(slot: string): string {
  const spec = SLOTS[slot]
  switch (spec.kind) {
    case 'color':
      return `oklch(${decimal(0, 1, 2)} ${decimal(0, 0.3, 2)} ${int(0, 360)}${pick(['', '', ` / ${decimal(0.3, 1, 2)}`])})`
    case 'length':
      return spec.unit === 'em' ? `${decimal(spec.min ?? 0, spec.max ?? 0, 2)}em` : `${int(spec.min ?? 0, spec.max ?? 0)}px`
    case 'number':
      return decimal(spec.min ?? 0, spec.max ?? 0, 2)
    case 'enum':
      return pick(spec.values ?? [])
    case 'shadow':
      return 'none'
    case 'font':
      return spec.end ?? ''
  }
}

const slotNames = Object.keys(SLOTS)
const colorSlots = slotNames.filter(s => SLOTS[s].kind === 'color')

function tokenSet(count: number, contrastMinded: boolean): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (let i = 0; i < count; i++) {
    const slot = contrastMinded && rand() < 0.6 ? pick(['bg', 'panel', 'raise', 'tx', 'mu', 'ac', 'onac', 'line2', 'edge', 'warn', 'bad']) : pick(slotNames)
    out[slot] = contrastMinded ? goodValueFor(slot) : valueFor(slot)
  }
  if (rand() < 0.05) out[pick(['nope', '__proto__', 'constructor', '1', 'Bg'])] = '#fff'
  if (rand() < 0.05) out[pick(colorSlots)] = pick([1, null, ['#fff'], { a: 1 }])
  if (contrastMinded && rand() < 0.5) {
    out['card-fill'] = pick(['plain', 'wash', 'glass'])
    out.backdrop = pick(['none', 'grid', 'lines', 'dots'])
    out['backdrop-strength'] = decimal(0, 1, 2)
    out.orbs = pick(['none', 'soft', 'bright'])
    out['orbs-tint'] = pick(['roles', 'accent', 'edge'])
    out['card-bloom'] = pick(['0', decimal(0, 1, 2)])
  }
  return out
}

// Most cases are near-valid, so they reach the contrast gate; a share carry the structural mistakes validateManifest names.
function manifestCase(): unknown {
  const noisy = rand() < 0.3
  const sometimes = <T>(good: T, bad: () => T): T => (noisy && rand() < 0.3 ? bad() : good)
  const modes = sometimes(pick([['dark'], ['light'], ['light', 'dark'], ['dark', 'light']]) as unknown, () => pick([[], ['dark', 'dark'], ['dim'], 'dark', null]))
  const tokens: Record<string, unknown> = {}
  for (const mode of Array.isArray(modes) ? modes : ['dark']) {
    if (typeof mode === 'string' && rand() < 0.9) tokens[mode] = tokenSet(int(0, 10), !noisy || rand() < 0.5)
  }
  if (noisy && rand() < 0.1) tokens[pick(['dim', 'light', 'dark'])] = tokenSet(2, false)
  const variant = () => ({
    name: sometimes(pick(['Green', 'Blue', 'Red', 'Amber']) as unknown, () => pick(['', ' ', 'n'.repeat(41), 5, '\u{1F600}'.repeat(21), 'Green'])),
    tokens: sometimes(tokenSet(int(0, 4), true) as unknown, () => pick([null, 'x', []])),
  })
  const base: Record<string, unknown> = {
    id: sometimes(pick(['mine', 'm-2', 'a1']) as unknown, () => pick(['neon', 'default', 'Bad', '-x', 'a'.repeat(41), 7])),
    name: sometimes('Mine' as unknown, () => pick(['', '   ', 'n'.repeat(81), 3])),
    version: sometimes('1.0.0' as unknown, () => pick(['', 'v'.repeat(41)])),
    author: sometimes('me' as unknown, () => pick(['', 'a'.repeat(201), null])),
    license: 'MIT',
    description: sometimes('a theme' as unknown, () => pick(['', 'd'.repeat(1001)])),
    modes,
    accent: sometimes(pick(['free', 'fixed', 'fixed', { variants: Array.from({ length: int(1, 3) }, variant) }]) as unknown,
      () => pick(['other', null, { variants: [] }, { variants: Array.from({ length: int(1, 9) }, variant) }])),
    fonts: sometimes(pick([[], ['IBM Plex Sans'], ['JetBrains Mono', 'Chakra Petch']]) as unknown,
      () => pick([['Comic Sans', 4, null, { a: 1 }, [1, [2]]], 'IBM Plex Sans', Array(9).fill('IBM Plex Mono')])),
    tokens: sometimes(tokens as unknown, () => pick([null, [], 'x'])),
    effects: sometimes(pick([[], [{ name: 'rain' }], [{ name: 'scanlines' }, { name: 'grain', options: { amount: 0.5 } }]]) as unknown, () => pick([
      [{ name: 'rain', options: { speed: 2, on: true, label: 'x', bad: 'url(x)', Bad: 1, '1': 2, big: 1e400 } }],
      [{ name: 'nope' }, { name: 4 }, {}, 'rain', null, [1, null], { name: 'grain', options: null }, { name: 'roll', options: [] }],
      Array(9).fill({ name: 'glow' }), null, 'rain', [{ name: 'vignette', options: { k: 'x'.repeat(65), l: 'y'.repeat(64), z: -0 } }]])),
  }
  if (noisy) for (const key of Object.keys(base)) if (rand() < 0.05) delete base[key]
  return base
}

// built-in folders as the server reads them: Default is the fallback every theme's missing slots read from. Read only when the
// cases are built afresh.
const themesDir = `${root}themes`

function build() {
  const builtinIds = readdirSync(themesDir, { withFileTypes: true }).filter(d => d.isDirectory() && /^[a-z0-9-]+$/.test(d.name)).map(d => d.name).sort()
  const defaultText = readFileSync(`${themesDir}/default/theme.json`, 'utf8')
  const ids = new Set(builtinIds)
  const parsedDefault = parseManifest(defaultText, { source: 'builtin', builtinIds: ids })
  if (!parsedDefault.ok) throw new Error('the Default theme does not parse')
  const fallback = parsedDefault.manifest
  const colors = [...handColors, ...Array.from({ length: 2500 }, colorText)].map(c => [c, parseColor(c)])
  const opaque = () => `oklch(${decimal(0, 1, 3)} ${decimal(0, 0.37, 3)} ${int(0, 360)}${pick(['', ` / ${decimal(0, 1, 2)}`])})`
  const pairs = Array.from({ length: 1200 }, () => [opaque(), opaque()]).map(([a, b]) => [a, b, contrast(parseColor(a)!, parseColor(b)!)])
  const values = slotNames.flatMap(slot => Array.from({ length: 24 }, () => valueFor(slot)).map(v => [slot, v, slotProblem(slot, v)]))

  const texts: { source: ThemeSource; text: string }[] = []
  for (const id of builtinIds) {
    const text = readFileSync(`${themesDir}/${id}/theme.json`, 'utf8')
    texts.push({ source: 'builtin', text }, { source: 'user', text })
  }
  texts.push({ source: 'user', text: readFileSync(`${themesDir}/_template/theme.json`, 'utf8') })
  texts.push(
    { source: 'user', text: 'not json' }, { source: 'user', text: '[]' }, { source: 'user', text: 'null' }, { source: 'user', text: '"x"' },
    { source: 'user', text: `\ufeff${defaultText}` },
    { source: 'user', text: '{"id":"a","id":"b"}' },
  )
  for (let i = 0; i < 500; i++) texts.push({ source: pick(['user', 'user', 'gallery', 'builtin'] as const), text: JSON.stringify(manifestCase()) })
  const manifests = texts.map(({ source, text }) => {
    const r = checkTheme(text, { source, builtinIds: ids }, fallback)
    return [source, text, r.ok ? { ok: true, manifest: r.manifest } : { ok: false, errors: r.errors }]
  })

  return {
    slots: Object.entries(SLOTS),
    derived: Object.entries(FROM_VAR),
    fonts: ALLOWED_FONTS,
    effects: KNOWN_EFFECTS,
    limits: LIMITS,
    builtinIds,
    default: defaultText,
    colors,
    pairs,
    values,
    manifests,
  }
}

// Each kept case's answer, worked out again by validate.ts from the inputs the file holds. Built-in ids and Default come from the
// file too, so an edit to a built-in theme changes nothing here until the file is regenerated on purpose.
function answers(kept: ReturnType<typeof build>) {
  const ids = new Set(kept.builtinIds)
  const base = parseManifest(kept.default, { source: 'builtin', builtinIds: ids })
  if (!base.ok) throw new Error('the kept Default does not parse')
  return {
    ...kept,
    slots: Object.entries(SLOTS),
    derived: Object.entries(FROM_VAR),
    fonts: ALLOWED_FONTS,
    effects: KNOWN_EFFECTS,
    limits: LIMITS,
    colors: kept.colors.map(([c]) => [c, parseColor(c as string)]),
    pairs: kept.pairs.map(([a, b]) => [a, b, contrast(parseColor(a as string)!, parseColor(b as string)!)]),
    values: kept.values.map(([slot, v]) => [slot, v, slotProblem(slot as string, v as string)]),
    manifests: kept.manifests.map(([source, text]) => {
      const r = checkTheme(text as string, { source: source as ThemeSource, builtinIds: ids }, base.manifest)
      return [source, text, r.ok ? { ok: true, manifest: r.manifest } : { ok: false, errors: r.errors }]
    }),
  }
}

test('validate.ts gives the answers in testdata/theme-cases.json, which the Go server is held to', () => {
  if (process.env.CONN_UPDATE_GOLDEN === '1') writeFileSync(file, `${JSON.stringify(build())}\n`)
  const text = readFileSync(file, 'utf8')
  const kept = JSON.parse(text) as ReturnType<typeof build>
  assert.ok(text === `${JSON.stringify(answers(kept))}\n`, 'testdata/theme-cases.json is out of date: run CONN_UPDATE_GOLDEN=1 bun test src/lib/theme/validateCases.test.ts')
})
