import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ALLOWED_FONTS, KNOWN_EFFECTS, LIMITS, SLOTS } from './tokens.ts'
import type { Mode, ThemeManifest, TokenSet } from './types.ts'
import { NEWER, checkContrast, checkTheme, parseManifest, slotProblem, validateManifest, type Ctx } from './validate.ts'

const ctx: Ctx = { source: 'user', builtinIds: new Set(['default', 'neon']) }
const builtinCtx: Ctx = { source: 'builtin', builtinIds: ctx.builtinIds }

const manifest = (over: Record<string, unknown> = {}, dark: TokenSet = {}) => ({
  id: 'mine', name: 'Mine', version: '1.0.0', author: 'me', license: 'MIT', description: 'a test theme',
  modes: ['dark'], accent: 'fixed', fonts: [], effects: [],
  tokens: { dark: { bg: '#010a04', ...dark } },
  ...over,
})

const errorsOf = (raw: unknown, c: Ctx = ctx): string[] => {
  const r = validateManifest(raw, c)
  assert.equal(r.ok, false, 'expected a refusal')
  return r.ok ? [] : r.errors
}

const slotIn = (errors: string[], slot: string) => errors.some((e) => e.includes(slot))

test('a minimal valid manifest passes and comes back typed', () => {
  const r = validateManifest(manifest(), ctx)
  assert.equal(r.ok, true)
  if (r.ok) {
    assert.equal(r.manifest.id, 'mine')
    assert.deepEqual(r.manifest.modes, ['dark'])
    assert.equal(r.manifest.tokens.dark?.bg, '#010a04')
  }
})

test('every slot in the contract table exists', () => {
  const names = 'bg panel raise sunk hover line line2 tx bright mu onac ac ac-soft ac-line ok warn bad idle info k-bug k-epic k-task k-story k-subtask teammate role-lead role-agent role-advisor edge stall focus meter-track meter-lit meter-lead font-ui font-mono case tracking radius led border-style lift glow blur motion font-body chrome text-glow cursor hover-lift meter backdrop backdrop-size backdrop-strength orbs orbs-tint backdrop-scan header header-shape stage stage-backing card-fill glass-tint glass-blur glass-saturate glass-edge card-sheen wallpaper-strength wallpaper-1 wallpaper-2 wallpaper-3 wallpaper-4 wallpaper-5 card-corner card-cut card-frame card-marks card-stripe card-rim card-bloom card-glow card-shadow glyph-arrow glyph-bullet glyph-sep glyph-brand wire-style wire-width wire-head wire-crawl wire-flow wire-glyphs wire-signal wire-speed tree-orbits node-beat'.split(' ')
  for (const n of names) assert.ok(Object.hasOwn(SLOTS, n), n)
  assert.equal(Object.keys(SLOTS).length, names.length)
})

test('constants match the contract', () => {
  assert.deepEqual([...ALLOWED_FONTS], ['Chakra Petch', 'JetBrains Mono', 'IBM Plex Sans', 'IBM Plex Mono', 'Share Tech Mono'])
  assert.deepEqual([...KNOWN_EFFECTS], ['rain', 'scanlines', 'vignette', 'flicker', 'glow', 'roll', 'grain'])
  assert.deepEqual(LIMITS, { manifestBytes: 65536, cssBytes: 262144, userThemes: 100 })
})

test('a token value with url(, ;, {, }, @import or expression is refused, naming the slot', () => {
  for (const bad of ['url(x)', 'red;', '{', '}', '@import', 'expression', '#fff url(x)', 'URL(x)']) {
    const e = errorsOf(manifest({}, { ac: bad }))
    assert.ok(slotIn(e, 'ac'), `${bad}: ${e.join(' | ')}`)
  }
})

test('numeric ranges: radius 24px ok, 25px refused; glow, tracking and blur limits', () => {
  assert.equal(validateManifest(manifest({}, { radius: '24px' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { radius: '0' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { radius: '1.5rem' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { radius: '25px' })), 'radius'))
  assert.ok(slotIn(errorsOf(manifest({}, { radius: '2rem' })), 'radius'))
  assert.ok(slotIn(errorsOf(manifest({}, { radius: '-1px' })), 'radius'))
  assert.ok(slotIn(errorsOf(manifest({}, { radius: '12' })), 'radius'))
  assert.equal(validateManifest(manifest({}, { glow: '1' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { glow: '1.2' })), 'glow'))
  assert.equal(validateManifest(manifest({}, { tracking: '.3em' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { tracking: '-0.05em' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { tracking: '.4em' })), 'tracking'))
  assert.equal(validateManifest(manifest({}, { blur: '16px' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { blur: '17px' })), 'blur'))
})

// a rem or em grows with the reader's font size, so 1.25rem is 40px at a 32px font: past the cut every card's padding clears
test('card-cut is px only, so the reader\'s font size cannot take a cut past its 20px bound', () => {
  assert.equal(validateManifest(manifest({}, { 'card-cut': '20px' }), ctx).ok, true)
  for (const v of ['1.25rem', '1rem', '.5em', '1em']) {
    const errors = errorsOf(manifest({}, { 'card-cut': v }))
    assert.ok(errors.some((e) => e.includes('card-cut') && e.includes('px only')), `${v}: ${errors.join(' | ')}`)
  }
  assert.equal(validateManifest(manifest({}, { radius: '1.5rem' }), ctx).ok, true, 'other lengths keep rem and em')
})

test('enums are closed lists', () => {
  assert.equal(validateManifest(manifest({}, { case: 'uppercase', led: 'diamond', motion: 'power', 'border-style': 'dashed' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { case: 'capitalize' })), 'case'))
  assert.ok(slotIn(errorsOf(manifest({}, { led: 'star' })), 'led'))
  assert.ok(slotIn(errorsOf(manifest({}, { motion: 'wild' })), 'motion'))
})

test('the slots Neon added (edge: the chrome hue, stall: a stalled agent) are colors', () => {
  assert.equal(SLOTS.edge.kind, 'color')
  assert.equal(SLOTS.stall.kind, 'color')
  assert.equal(validateManifest(manifest({}, { edge: 'rgba(127,156,255,.5)', stall: 'oklch(.77 .14 51)' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { edge: '4px' })), 'edge'))
  assert.ok(slotIn(errorsOf(manifest({}, { stall: 'orange' })), 'stall'))
})

test('a color slot refuses a name or a variable', () => {
  assert.ok(slotIn(errorsOf(manifest({}, { ac: 'red' })), 'ac'))
  assert.ok(slotIn(errorsOf(manifest({}, { ac: 'var(--x)' })), 'ac'))
})

test('lift takes shadows and refuses url( in one', () => {
  assert.equal(validateManifest(manifest({}, { lift: '0 1px 2px oklch(.3 .05 260/.07)' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { lift: 'inset 0 1px 0 oklch(1 0 0/.05)' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { lift: '0 1px 2px #0003, 0 8px 24px rgba(0,0,0,.4)' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { lift: 'none' }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { lift: '0 1px 2px url(x)' })), 'lift'))
  assert.ok(slotIn(errorsOf(manifest({}, { lift: '0 1px 2px' })), 'lift'))
  assert.ok(slotIn(errorsOf(manifest({}, { lift: '0 1px 2px blue' })), 'lift'))
  assert.ok(slotIn(errorsOf(manifest({}, { lift: '0 1px 999px #000' })), 'lift'))
})

test('font tokens take allow-listed families and end in their slot\'s system stack', () => {
  assert.equal(validateManifest(manifest({}, { 'font-ui': "'IBM Plex Sans', system-ui" }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { 'font-ui': "'IBM Plex Sans', sans-serif, system-ui" }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { 'font-mono': '"JetBrains Mono", ui-monospace' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({}, { 'font-mono': "'Share Tech Mono', monospace, ui-monospace" }), ctx).ok, true)
  assert.ok(slotIn(errorsOf(manifest({}, { 'font-ui': "'Comic Sans MS', system-ui" })), 'font-ui'))
})

test('a font stack missing its ending is refused, and a generic in the wrong slot too', () => {
  assert.ok(errorsOf(manifest({}, { 'font-ui': "'IBM Plex Sans'" })).some((e) => e.includes('font-ui') && e.includes('system-ui')))
  assert.ok(errorsOf(manifest({}, { 'font-ui': "'IBM Plex Sans', sans-serif" })).some((e) => e.includes('font-ui') && e.includes('system-ui')))
  assert.ok(errorsOf(manifest({}, { 'font-mono': "'JetBrains Mono'" })).some((e) => e.includes('font-mono') && e.includes('ui-monospace')))
  assert.ok(errorsOf(manifest({}, { 'font-mono': "'JetBrains Mono', monospace" })).some((e) => e.includes('font-mono') && e.includes('ui-monospace')))
  assert.ok(slotIn(errorsOf(manifest({}, { 'font-mono': 'system-ui' })), 'font-mono'))
  assert.ok(slotIn(errorsOf(manifest({}, { 'font-ui': 'ui-monospace' })), 'font-ui'))
  assert.ok(slotIn(errorsOf(manifest({}, { 'font-ui': "monospace, system-ui" })), 'font-ui'))
})

test('a color CSS would drop is refused in a token, so it cannot pass the gate unseen', () => {
  for (const bad of ['rgb(1 2 3 4)', 'rgb(1/2/3)', 'oklch(.5, .1, 20)', 'hsl(120 50% 50% 1)', 'rgb(1. 2 3)', 'rgb(1\u00a02\u00a03)']) {
    assert.ok(slotIn(errorsOf(manifest({}, { ac: bad })), 'ac'), bad)
    assert.ok(slotIn(errorsOf(manifest({}, { lift: `0 1px 2px ${bad}` })), 'lift'), bad)
  }
  assert.ok(slotIn(errorsOf(manifest({}, { radius: '1.px' })), 'radius'))
  assert.ok(slotIn(errorsOf(manifest({}, { lift: '0\u00a01px 2px #000' })), 'lift'))
})

test('ids: pattern and length', () => {
  assert.equal(validateManifest(manifest({ id: 'a' }), ctx).ok, true)
  assert.equal(validateManifest(manifest({ id: 'a'.repeat(40) }), ctx).ok, true)
  assert.ok(errorsOf(manifest({ id: 'Bad Id' })).some((e) => e.includes('id')))
  assert.ok(errorsOf(manifest({ id: 'a'.repeat(41) })).some((e) => e.includes('id')))
  assert.ok(errorsOf(manifest({ id: '-a' })).some((e) => e.includes('id')))
  assert.ok(errorsOf(manifest({ id: '../x' })).some((e) => e.includes('id')))
})

test('an unknown slot, __proto__, constructor, an unknown font or effect, and empty modes are refused', () => {
  assert.ok(slotIn(errorsOf(manifest({}, { nope: '#fff' })), 'nope'))
  const proto = JSON.parse('{"__proto__": "#fff"}') as TokenSet
  assert.ok(slotIn(errorsOf(manifest({ tokens: { dark: proto } })), '__proto__'))
  assert.ok(slotIn(errorsOf(manifest({}, { constructor: '#fff' })), 'constructor'))
  assert.ok(errorsOf(manifest({ fonts: ['Comic Sans MS'] })).some((e) => e.includes('Comic Sans MS')))
  assert.ok(errorsOf(manifest({ effects: [{ name: 'fireworks' }] })).some((e) => e.includes('fireworks')))
  assert.ok(errorsOf(manifest({ modes: [] })).some((e) => e.includes('modes')))
  assert.ok(errorsOf(manifest({ modes: ['sepia'] })).some((e) => e.includes('modes')))
})

test('a valid font, effect and option pass; a bad option value is refused', () => {
  const ok = manifest({ fonts: ['Share Tech Mono'], effects: [{ name: 'rain', options: { density: 0.6, color: '#8bffad' } }] })
  assert.equal(validateManifest(ok, ctx).ok, true)
  assert.ok(errorsOf(manifest({ effects: [{ name: 'rain', options: { color: 'url(x)' } }] })).some((e) => e.includes('color')))
})

test('accent: free, fixed, variants; a variant carries tokens that are checked', () => {
  assert.equal(validateManifest(manifest({ accent: 'free' }), ctx).ok, true)
  const v = manifest({ accent: { variants: [{ name: 'Green', tokens: { ac: '#8bffad' } }, { name: 'Amber', tokens: { ac: '#ffb000' } }] } })
  assert.equal(validateManifest(v, ctx).ok, true)
  const bad = manifest({ accent: { variants: [{ name: 'Green', tokens: { ac: 'url(x)' } }] } })
  assert.ok(slotIn(errorsOf(bad), 'ac'))
  assert.ok(errorsOf(manifest({ accent: 'rainbow' })).some((e) => e.includes('accent')))
  assert.ok(errorsOf(manifest({ accent: { variants: [] } })).some((e) => e.includes('accent')))
})

test('accent: a variant name used twice is refused, so the menu never lists two of one name', () => {
  const twice = manifest({ accent: { variants: [{ name: 'Green', tokens: {} }, { name: 'Amber', tokens: {} }, { name: 'Green', tokens: {} }] } })
  assert.deepEqual(errorsOf(twice), ['accent.variants: name "Green" is used twice'])
  // the name is clipped like any other name from the file
  const long = 'G'.repeat(40)
  const clipped = errorsOf(manifest({ accent: { variants: [{ name: long, tokens: {} }, { name: long, tokens: {} }] } }))
  assert.deepEqual(clipped, [`accent.variants: name ${JSON.stringify(long)} is used twice`])
  // names are compared exactly: case or spacing makes another name
  assert.equal(validateManifest(manifest({ accent: { variants: [{ name: 'Green', tokens: {} }, { name: 'green', tokens: {} }] } }), ctx).ok, true)
})

test('tokens for a mode the theme does not list are refused', () => {
  assert.ok(errorsOf(manifest({ tokens: { dark: { bg: '#000' }, light: { bg: '#fff' } } })).some((e) => e.includes('light')))
})

test('a user or gallery theme cannot take a built-in id; a built-in can', () => {
  for (const source of ['user', 'gallery'] as const) {
    assert.ok(errorsOf(manifest({ id: 'neon' }), { source, builtinIds: ctx.builtinIds }).some((e) => e.includes('neon')))
  }
  assert.equal(validateManifest(manifest({ id: 'neon' }), builtinCtx).ok, true)
})

test('non-object manifests are refused', () => {
  for (const raw of [null, [], 'x', 3]) assert.equal(validateManifest(raw, ctx).ok, false)
})

test('parseManifest refuses text over 64 KB and text that is not JSON', () => {
  const big = JSON.stringify(manifest({ description: 'x'.repeat(LIMITS.manifestBytes) }))
  const r = parseManifest(big, ctx)
  assert.equal(r.ok, false)
  assert.ok(!r.ok && r.errors[0].includes('64'))
  const nj = parseManifest('{ not json', ctx)
  assert.equal(nj.ok, false)
  assert.equal(parseManifest(JSON.stringify(manifest()), ctx).ok, true)
})

// contrast ---------------------------------------------------------------

const full = (over: TokenSet = {}): TokenSet => ({
  bg: '#05060d', panel: '#0a0c18', raise: '#10142a', line2: '#3a4160', tx: '#d6dcf0', mu: '#98a2bd', ac: '#6ee7ff', onac: '#05060d', ...over,
})
const asManifest = (dark: TokenSet, extra: Partial<ThemeManifest> = {}): ThemeManifest => {
  const r = validateManifest(manifest({ tokens: { dark }, ...extra }), builtinCtx)
  assert.ok(r.ok, r.ok ? '' : r.errors.join('|'))
  return r.manifest
}
const fallback = asManifest(full())

test('checkContrast passes a palette that clears every bar', () => {
  assert.deepEqual(checkContrast(asManifest(full()), fallback), [])
})

test('checkContrast names tx and panel when they are equal', () => {
  const m = asManifest(full({ tx: '#0a0c18' }))
  const msgs = checkContrast(m, fallback)
  assert.ok(msgs.some((e) => e.includes('tx') && e.includes('panel')), msgs.join(' | '))
})

test('checkContrast flags onac on ac, line2 on panel and mu on bg', () => {
  const msgs = checkContrast(asManifest(full({ onac: '#6ee7ff', line2: '#0b0d1a', mu: '#0f1224' })), fallback)
  assert.ok(msgs.some((e) => e.includes('onac') && e.includes('ac')))
  assert.ok(msgs.some((e) => e.includes('line2') && e.includes('panel')))
  assert.ok(msgs.some((e) => e.includes('mu') && e.includes('bg')))
})

test('checkContrast measures omitted slots with the fallback values', () => {
  const sparse = asManifest({ tx: '#0a0c18' })
  // panel comes from the fallback, so tx equal to it still fails
  assert.ok(checkContrast(sparse, fallback).some((e) => e.includes('tx') && e.includes('panel')))
  // and a sparse manifest that only brightens text passes against the fallback
  assert.deepEqual(checkContrast(asManifest({ tx: '#ffffff' }), fallback), [])
})

test('checkContrast composites a translucent surface over bg before measuring', () => {
  // panel at 0 alpha shows bg through; tx equal to bg then fails against it
  const m = asManifest(full({ panel: 'rgba(0,0,0,0)', tx: '#05060d' }))
  assert.ok(checkContrast(m, fallback).some((e) => e.includes('tx') && e.includes('panel')))
})

test('checkContrast checks each accent variant over the base tokens', () => {
  const m = asManifest(full(), { accent: { variants: [{ name: 'Washed', tokens: { ac: '#6ee7ff', onac: '#6ee7ff' } }] } })
  const msgs = checkContrast(m, fallback)
  assert.ok(msgs.some((e) => e.includes('Washed') && e.includes('onac')), msgs.join(' | '))
})

test('checkContrast checks each listed mode, using the fallback for the same mode', () => {
  const both: Record<Mode, TokenSet> = {
    dark: full(),
    light: { bg: '#ffffff', panel: '#f4f4f8', raise: '#ececf2', line2: '#9aa0b4', tx: '#14161f', mu: '#555b70', ac: '#2a5bd7', onac: '#ffffff' },
  }
  const fb = asManifest(full(), { modes: ['light', 'dark'], tokens: both } as Partial<ThemeManifest>)
  assert.deepEqual(checkContrast(fb, fb), [])
  const bad = asManifest(full(), { modes: ['light', 'dark'], tokens: { dark: full(), light: { ...both.light, tx: '#f4f4f8' } } } as Partial<ThemeManifest>)
  assert.ok(checkContrast(bad, fb).some((e) => e.includes('light') && e.includes('tx')))
})

test('checkTheme chains parse, validate and contrast', () => {
  const good = checkTheme(JSON.stringify(manifest({ tokens: { dark: full() } })), ctx, fallback)
  assert.equal(good.ok, true)
  const poor = checkTheme(JSON.stringify(manifest({ tokens: { dark: full({ tx: '#0a0c18' }) } })), ctx, fallback)
  assert.equal(poor.ok, false)
  assert.equal(checkTheme('nope', ctx, fallback).ok, false)
})

test('color-mix(in oklch, <color> N%, transparent) is a color, and only that shape', () => {
  const ok = (v: string) => assert.equal(validateManifest(manifest({}, { 'ac-soft': v }), ctx).ok, true, v)
  const no = (v: string) => assert.ok(slotIn(errorsOf(manifest({}, { 'ac-soft': v })), 'ac-soft'), v)
  ok('color-mix(in oklch, oklch(0.78 0.12 195) 14%, transparent)')
  ok('color-mix(in oklch, #00ff41 40%, transparent)')
  no('color-mix(in srgb, #00ff41 40%, transparent)')
  no('color-mix(in oklch, #00ff41 40%, red)')
  no('color-mix(in oklch, var(--ac) 14%, transparent)')
  no('color-mix(in oklch, #00ff41 140%, transparent)')
  no('color-mix(in oklch, nope 14%, transparent)')
  no('color-mix(in oklch, #00ff41 14%, transparent) 3px')
})

test('slotProblem checks one value against its slot', () => {
  assert.equal(slotProblem('radius', '8px'), null)
  assert.ok(slotProblem('radius', '99px'))
  assert.ok(slotProblem('nope', '#fff'))
  assert.ok(slotProblem('constructor', '#fff'))
})

// the look's worst cases: a wash, a glass card, the backdrop's pattern and orbs can sit under text
const dim = (over: TokenSet = {}) => full({ bg: '#000000', panel: '#000000', raise: '#000000', tx: '#777777', mu: '#777777', line2: '#777777', warn: '#ffffff', edge: '#ffffff', ...over })

test('checkContrast measures tx and mu over the strongest card wash, so a wash that drowns the text is refused', () => {
  assert.deepEqual(checkContrast(asManifest(dim({ 'card-fill': 'plain' })), fallback), [], 'the same palette on a plain card passes')
  const msgs = checkContrast(asManifest(dim({ 'card-fill': 'wash' })), fallback)
  assert.ok(msgs.some((e) => /tx on the strongest card wash \(warn\)/.test(e)), msgs.join(' | '))
  const r = checkTheme(JSON.stringify(manifest({ tokens: { dark: dim({ 'card-fill': 'wash' }) } })), { source: 'gallery', builtinIds: new Set(['default']) }, fallback)
  assert.equal(r.ok, false, 'a gallery install refuses it')
})

test('checkContrast measures a wash over the page it is painted on, not over a panel the wash does not paint', () => {
  // a dark panel under a lighter page: the wash's gradient is see-through at its start, so the page shows under the text there
  const page = dim({ bg: '#353535', panel: '#000000', raise: '#000000', tx: '#a8a8a8', mu: '#a8a8a8', edge: '#000000' })
  assert.deepEqual(checkContrast(asManifest({ ...page, 'card-fill': 'plain' }), fallback), [], 'the same palette on plain cards passes')
  const msgs = checkContrast(asManifest({ ...page, 'card-fill': 'wash' }), fallback)
  assert.ok(msgs.some((e) => /tx on the strongest card wash \(warn\)/.test(e)), msgs.join(' | '))
  // and the other way round: where the wash ends, it lies over the panel
  const panel = { ...page, bg: '#000000', panel: '#353535', raise: '#353535', 'card-fill': 'wash' }
  const ends = checkContrast(asManifest(panel), fallback)
  assert.ok(ends.some((e) => /tx on the strongest card wash \(warn\)/.test(e)), ends.join(' | '))
})

test('checkContrast reads a hue written as color-mix over transparent, so the form cannot slip a wash, orb or pattern past it', () => {
  const white = 'color-mix(in oklch, #ffffff 100%, transparent)'
  const wash = checkContrast(asManifest(dim({ edge: '#000000', warn: white, 'card-fill': 'wash' })), fallback)
  assert.ok(wash.some((e) => /tx on the strongest card wash \(warn\)/.test(e)), wash.join(' | '))
  const orb = checkContrast(asManifest(dim({ edge: white, orbs: 'bright', 'orbs-tint': 'edge' })), fallback)
  assert.ok(orb.some((e) => /edge orb/.test(e)), orb.join(' | '))
  const dots = checkContrast(asManifest(dim({ edge: white, backdrop: 'dots', 'backdrop-strength': '1' })), fallback)
  assert.ok(dots.some((e) => /backdrop at its strongest \(dots\)/.test(e)), dots.join(' | '))
})

test('checkContrast measures a slot the theme leaves unset as the page paints it (edge follows the theme\'s mu), not as the fallback writes it', () => {
  const quiet = asManifest(full({ edge: '#000000' }))
  const own = { ...dim({ tx: '#9a9a9a', mu: '#ffffff', backdrop: 'dots', 'backdrop-strength': '1' }) }
  delete own.edge
  const msgs = checkContrast(asManifest(own), quiet)
  assert.ok(msgs.some((e) => /tx on the page's backdrop at its strongest \(dots\)/.test(e)), msgs.join(' | '))
})

test('checkContrast measures the page where the pattern and the strongest orb meet', () => {
  assert.deepEqual(checkContrast(asManifest(dim({ backdrop: 'dots', 'backdrop-strength': '0' })), fallback), [], 'a pattern at strength 0 lays nothing')
  const dots = checkContrast(asManifest(dim({ backdrop: 'dots', 'backdrop-strength': '1' })), fallback)
  assert.ok(dots.some((e) => /tx on the page's backdrop at its strongest \(dots\)/.test(e)), dots.join(' | '))
  const orbs = checkContrast(asManifest(dim({ orbs: 'bright', 'orbs-tint': 'edge' })), fallback)
  assert.ok(orbs.some((e) => /edge orb/.test(e)), orbs.join(' | '))
  const glass = checkContrast(asManifest(dim({ bg: '#ffffff', 'card-fill': 'glass', tx: '#ffffff' })), fallback)
  assert.ok(glass.some((e) => /a glass card over the page/.test(e)), glass.join(' | '))
})

test('checkContrast measures the text nearest a card edge under its bloom, so a bloom that washes the text out is refused', () => {
  const lit = dim({ bg: '#000000', panel: '#000000', tx: '#8a8a8a', mu: '#5e5e5e' })
  assert.deepEqual(checkContrast(asManifest({ ...lit, 'card-bloom': '0' }), fallback), [], 'no bloom, no loss')
  const msgs = checkContrast(asManifest({ ...lit, 'card-bloom': '1' }), fallback)
  assert.ok(msgs.some((e) => /mu on a card's edge under its bloom/.test(e)), msgs.join(' | '))
})

test('an unknown token gets one line saying the theme may be for a newer conn; a bad value alone does not', () => {
  const r = validateManifest(manifest({ tokens: { dark: { 'card-shape': 'wavy', 'card-sheen': '1' } } }), ctx)
  assert.equal(r.ok, false)
  if (!r.ok) {
    assert.equal(r.errors.filter((e) => e === NEWER).length, 1)
    assert.equal(r.errors.at(-1), NEWER)
  }
  const bad = validateManifest(manifest({ tokens: { dark: { radius: '99px' } } }), ctx)
  assert.ok(!bad.ok && !bad.errors.includes(NEWER))
})
