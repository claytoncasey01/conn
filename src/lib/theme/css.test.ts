import assert from 'node:assert/strict'
import { test } from 'node:test'
import { DEFAULT_ACCENT, accentTokens } from './accent.ts'
import { LOOK, fallbackCss, lookDeclarations, themeCss, themeTokens } from './css.ts'
import { checkContrast, slotProblem } from './validate.ts'
import { SLOTS } from './tokens.ts'
import type { ThemeManifest, TokenSet } from './types.ts'

const make = (over: Partial<ThemeManifest> = {}): ThemeManifest => ({
  id: 'matrix', name: 'Matrix', version: '1', author: '', license: '', description: '',
  modes: ['dark'], accent: 'fixed', fonts: [], tokens: { dark: { bg: '#010a04', ac: '#00ff41' } }, effects: [],
  ...over,
})
const fallback = make({
  id: 'default', modes: ['light', 'dark'], accent: 'free',
  tokens: { light: { bg: '#ffffff', tx: '#111111', radius: '8px' }, dark: { bg: '#000000', tx: '#eeeeee', radius: '9px' } },
})

test('a dark-only manifest emits one dark block', () => {
  const css = themeCss(make(), fallback)
  assert.ok(css.startsWith('[data-theme="matrix"][data-mode="dark"]{'), css)
  assert.equal(css.match(/data-theme=/g)?.length, 1)
  assert.ok(css.includes('--bg:#010a04;'))
  assert.ok(css.includes('--ac:#00ff41;'))
})

test('a slot the manifest omits comes from the fallback in the same mode', () => {
  const css = themeCss(make(), fallback)
  assert.ok(css.includes('--tx:#eeeeee;'), css)
  assert.ok(css.includes('--radius:9px;'))
  assert.ok(!css.includes('#111111'))
  const both = themeCss(make({ modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#000' } } }), fallback)
  assert.ok(both.includes('[data-mode="light"]{') && both.includes('--tx:#111111;'))
})

test('a one-mode fallback backs the other mode too', () => {
  const darkOnly = make({ id: 'default', accent: 'free', tokens: { dark: { tx: '#eeeeee' } } })
  assert.ok(themeCss(make({ modes: ['light'], tokens: { light: { bg: '#fff' } } }), darkOnly).includes('--tx:#eeeeee;'))
})

test('a fixed theme without ac-soft or ac-line derives them from var(--ac)', () => {
  const css = themeCss(make(), fallback)
  assert.ok(css.includes('--ac-soft:color-mix(in oklch, var(--ac) 14%, transparent);'), css)
  assert.ok(css.includes('--ac-line:color-mix(in oklch, var(--ac) 40%, transparent);'))
})

test('a theme that omits edge or stall derives them from its own mu, warn and bad, even over a fallback that sets them', () => {
  const full = make({ id: 'default', modes: ['light', 'dark'], accent: 'free', tokens: {
    light: { bg: '#ffffff', tx: '#111111', edge: '#888888', stall: '#cc6600', 'ac-soft': 'rgba(0,128,128,.14)', 'ac-line': 'rgba(0,128,128,.4)' },
    dark: { bg: '#000000', tx: '#eeeeee', edge: '#999999', stall: '#dd7700', 'ac-soft': 'rgba(0,200,200,.14)', 'ac-line': 'rgba(0,200,200,.4)' },
  } })
  const css = themeCss(make({ tokens: { dark: { bg: '#010a04', ac: '#00ff41', mu: '#6fa8ff' } } }), full)
  assert.ok(css.includes('--edge:var(--mu);'), css)
  assert.ok(css.includes('--stall:color-mix(in oklch, var(--warn) 50%, var(--bad));'), css)
  assert.ok(css.includes('--ac-soft:color-mix(in oklch, var(--ac) 14%, transparent);'), 'the fallback\'s teal ac-soft never shows through')
  assert.ok(!css.includes('#999999') && !css.includes('#dd7700') && !css.includes('rgba(0,200,200'), css)
  const own = themeCss(make({ tokens: { dark: { bg: '#010a04', edge: '#7f9cff', stall: '#ff9a5a' } } }), full)
  assert.ok(own.includes('--edge:#7f9cff;') && own.includes('--stall:#ff9a5a;'), own)
  assert.ok(fallbackCss(full).includes('--edge:#999999;'), 'Default itself keeps its own values')
})

test('a fixed theme that sets ac-soft keeps its own', () => {
  const css = themeCss(make({ tokens: { dark: { bg: '#000', ac: '#0f0', 'ac-soft': 'rgba(0,255,65,.1)' } } }), fallback)
  assert.ok(css.includes('--ac-soft:rgba(0,255,65,.1);'))
  assert.ok(!css.includes('var(--ac) 14%'))
})

test('a free theme with a pick emits the derived accent tokens', () => {
  const m = make({ accent: 'free', modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#000' } } })
  const css = themeCss(m, fallback, { accent: { hue: 255, chroma: 0.15 } })
  for (const mode of ['light', 'dark'] as const) {
    const t = accentTokens(mode, 255, 0.15)
    const block = css.split('\n').find((b) => b.includes(`data-mode="${mode}"`)) ?? ''
    assert.ok(block.includes(`--ac:${t.ac};`), block)
    assert.ok(block.includes(`--onac:${t.onac};`))
    assert.ok(block.includes(`--ac-soft:${t['ac-soft']};`))
  }
})

test('a pick is ignored by a fixed theme and a free theme without one keeps its own ac', () => {
  assert.ok(themeCss(make(), fallback, { accent: { hue: 255, chroma: 0.15 } }).includes('--ac:#00ff41;'))
  const free = make({ accent: 'free', tokens: { dark: { bg: '#000', ac: '#abcdef' } } })
  assert.ok(themeCss(free, fallback).includes('--ac:#abcdef;'))
})

test('a variants theme with a pick lays that variant over the base', () => {
  const v = make({
    id: 'crt',
    accent: { variants: [{ name: 'Green', tokens: { ac: '#8bffad' } }, { name: 'Amber', tokens: { ac: '#ffb000', onac: '#030604' } }] },
    tokens: { dark: { bg: '#030604', ac: '#111111' } },
  })
  const css = themeCss(v, fallback, { variant: 'Amber' })
  assert.ok(css.includes('--ac:#ffb000;') && css.includes('--onac:#030604;') && css.includes('--bg:#030604;'), css)
  assert.ok(!css.includes('#8bffad') && !css.includes('#111111'))
  // an unset or unknown name means the first variant, not the bare base: the picker shows that one checked
  for (const pick of [undefined, {}, { variant: 'Nope' }]) {
    const css = themeCss(v, fallback, pick)
    assert.ok(css.includes('--ac:#8bffad;') && !css.includes('#111111') && css.includes('--bg:#030604;'), css)
  }
})

test('a theme with no variants ignores a variant pick, and its base is unchanged', () => {
  const plain = make({ tokens: { dark: { bg: '#010a04', ac: '#00ff41' } } })
  const base = themeCss(plain, fallback)
  assert.equal(themeCss(plain, fallback, { variant: 'Amber' }), base)
  assert.ok(base.includes('--ac:#00ff41;'))
  const empty = make({ accent: { variants: [] }, tokens: { dark: { bg: '#010a04', ac: '#00ff41' } } })
  assert.equal(themeCss(empty, fallback, { variant: 'Amber' }), base)
})

test('themeTokens takes variants[0] for a null or unknown variant', () => {
  const v = make({
    accent: { variants: [{ name: 'Green', tokens: { ac: '#8bffad' } }, { name: 'Amber', tokens: { ac: '#ffb000' } }] },
    tokens: { dark: { bg: '#030604', ac: '#111111' } },
  })
  assert.equal(themeTokens(v, fallback, 'dark').ac, '#8bffad')
  assert.equal(themeTokens(v, fallback, 'dark', { variant: 'Gone' }).ac, '#8bffad')
  assert.equal(themeTokens(v, fallback, 'dark', { variant: 'Amber' }).ac, '#ffb000')
})

test('a value that fails the validator is dropped, never emitted', () => {
  const bad: TokenSet = {
    bg: 'url(http://x)', tx: 'red', ac: '#0f0;}body{display:none', onac: 'rgb(1, 2)', radius: '99px', 'font-ui': 'Comic Sans', nope: '#fff', lift: 'none',
  }
  const css = themeCss(make({ tokens: { dark: bad } }), fallback)
  assert.ok(!/url\(|Comic|body\{|--nope|--radius:99px|--tx:red|rgb\(1/.test(css), css)
  assert.ok(css.includes('--lift:none;'))
  // the dropped bg falls back rather than disappearing
  assert.ok(css.includes('--bg:#000000;'), css)
})

test('an id outside the id grammar emits nothing', () => {
  assert.equal(themeCss(make({ id: 'a"]{}x' }), fallback), '')
})

test('fallbackCss has the light :root, the dark one in the media query, and the explicit blocks', () => {
  const css = fallbackCss(fallback)
  const root = css.indexOf(':root{')
  const media = css.indexOf('@media (prefers-color-scheme: dark){:root{')
  assert.ok(root === 0 && media > root, css)
  assert.ok(css.slice(root, media).includes('--bg:#ffffff;') && !css.slice(root, media).includes('--bg:#000000;'))
  assert.ok(css.slice(media).includes('--bg:#000000;'))
  assert.ok(css.includes('[data-theme="default"][data-mode="light"]{'))
  assert.ok(css.includes('[data-theme="default"][data-mode="dark"]{'))
})

test('fallbackCss fills the free accent from the default swatch in each mode', () => {
  const css = fallbackCss(fallback)
  for (const mode of ['light', 'dark'] as const) {
    const t = accentTokens(mode, DEFAULT_ACCENT.hue, DEFAULT_ACCENT.chroma)
    const block = css.split('\n').find((b) => b.startsWith(`[data-theme="default"][data-mode="${mode}"]`)) ?? ''
    assert.ok(block.includes(`--ac:${t.ac};`) && block.includes(`--onac:${t.onac};`), block)
  }
})

test('a bad accent pick emits no accent tokens at all, onac included', () => {
  const free = make({ accent: 'free', tokens: { dark: { bg: '#000', ac: '#abcdef' } } })
  for (const accent of [{ hue: NaN, chroma: 0.1 }, { hue: 195, chroma: Infinity }]) {
    const css = themeCss(free, fallback, { accent })
    assert.ok(!css.includes('--onac'), css)
    assert.ok(css.includes('--ac:#abcdef;'))
  }
})

test('every emitted value passes the slot grammar, bar the code-owned var() derivations', () => {
  const CODE_OWNED = new Set(['--ac-soft:color-mix(in oklch, var(--ac) 14%, transparent)', '--ac-line:color-mix(in oklch, var(--ac) 40%, transparent)', '--edge:var(--mu)', '--stall:color-mix(in oklch, var(--warn) 50%, var(--bad))', '--card-glow:var(--glow)', '--focus:var(--ac)', '--meter-track:var(--line)', '--meter-lit:var(--ac)', '--meter-lead:var(--role-lead)'])
  const variants = make({
    id: 'crt', accent: { variants: [{ name: 'Amber', tokens: { ac: '#ffb000', onac: '#030604' } }] }, tokens: { dark: { bg: '#030604', ac: '#8bffad' } },
  })
  const free = make({ accent: 'free', modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#000' } } })
  const bad = make({ tokens: { dark: { bg: 'url(x)', radius: '99px', ac: '#0f0' } } })
  const sheets = [
    themeCss(make(), fallback), themeCss(variants, fallback, { variant: 'Amber' }), themeCss(bad, fallback),
    themeCss(free, fallback, { accent: { hue: 355, chroma: 0.16 } }), fallbackCss(fallback),
  ]
  let seen = 0
  for (const css of sheets)
    for (const [, k, v] of css.matchAll(/--([a-z0-9-]+):([^;]*);/g)) {
      seen++
      if (CODE_OWNED.has(`--${k}:${v}`)) continue
      assert.equal(slotProblem(k, v), null, `--${k}:${v}`)
    }
  assert.ok(seen > 30)
  // the exemption is exactly the var(--ac) form, nothing else slips through it
  assert.ok(sheets.every((css) => [...css.matchAll(/--[a-z0-9-]+:[^;]*var\(--ac\)[^;]*;/g)].every((m) => CODE_OWNED.has(m[0].slice(0, -1)))))
})

test('a mode the theme lists but gives no tokens for renders from the fallback, never from its other mode', () => {
  const lightT = { bg: '#ffffff', panel: '#f4f4f4', raise: '#ffffff', tx: '#111111', mu: '#555555', line2: '#999999', ac: '#006680', onac: '#ffffff' }
  const darkT = { bg: '#000000', panel: '#0a0a0a', raise: '#141414', tx: '#eeeeee', mu: '#aaaaaa', line2: '#666666', ac: '#5ad7ff', onac: '#000000' }
  const def = make({ id: 'default', modes: ['light', 'dark'], accent: 'free', tokens: { light: lightT, dark: darkT } })
  const gated = ['bg', 'panel', 'raise', 'tx', 'mu', 'line2', 'ac', 'onac']
  const cases: ThemeManifest[] = [
    make({ modes: ['light', 'dark'], tokens: { dark: { tx: '#eeeeee', mu: '#cccccc' } } }),
    make({ modes: ['light', 'dark'], tokens: { light: { tx: '#222222' } } }),
    make({ modes: ['light'], tokens: { dark: { tx: '#eeeeee' } } }),
    make({ modes: ['light', 'dark'], tokens: { light: { tx: '#222222' }, dark: { tx: '#eeeeee', mu: '#cccccc' } } }),
    make({
      modes: ['light', 'dark'], tokens: { dark: { tx: '#eeeeee' } },
      accent: { variants: [{ name: 'Amber', tokens: { ac: '#ffb000', onac: '#030604' } }] },
    }),
  ]
  for (const m of cases) {
    const pick = { variant: 'Amber' }
    assert.deepEqual(checkContrast(m, def), [], 'the gate accepts it')
    const css = themeCss(m, def, pick)
    for (const mode of m.modes) {
      // the gate's inputs, worked out here on their own: Default's same mode, then the theme's own tokens for that mode, then the variant
      const variant = typeof m.accent === 'object' ? m.accent.variants[0].tokens : {}
      const expected: TokenSet = { ...def.tokens[mode], ...(m.tokens[mode] ?? {}), ...variant }
      const block = css.split('\n').find((b) => b.startsWith(`[data-theme="${m.id}"][data-mode="${mode}"]{`)) ?? ''
      assert.ok(block, `${mode} block exists`)
      for (const slot of gated) assert.ok(block.includes(`--${slot}:${expected[slot]};`), `${mode} ${slot} should be ${expected[slot]} in ${block}`)
    }
  }
})

test('only light and dark become selectors; a hostile mode string emits nothing', () => {
  const hostile = make({ modes: ['dark"]{}body{display:none}[a="', 'dark'] as never })
  const css = themeCss(hostile, fallback)
  assert.ok(!css.includes('body{') && !css.includes('display'), css)
  assert.equal(css.match(/\{/g)?.length, 1)
  assert.ok(css.startsWith('[data-theme="matrix"][data-mode="dark"]{'))
  assert.equal(themeCss(make({ modes: ['x'] as never }), fallback), '')
})

test('themeTokens is the merged set themeCss writes for a mode, as data an effect can read', () => {
  const t = themeTokens(make(), fallback, 'dark')
  assert.equal(t.bg, '#010a04')
  assert.equal(t.ac, '#00ff41')
  assert.equal(t.tx, '#eeeeee')
  assert.equal(t.radius, '9px')
  // the same value the stylesheet declares for each of them (a derived slot is declared in var() form: the next test)
  const css = themeCss(make(), fallback)
  for (const [k, v] of Object.entries(t)) {
    const declared = new RegExp(`--${k}:([^;]*);`).exec(css)?.[1]
    assert.equal(declared?.replace(/var\(--([a-z-]+)\)/g, (_, n: string) => t[n]), v, k)
  }
})

test('themeTokens drops a rejected value to the layer below, and an unknown key never appears', () => {
  const m = make({ tokens: { dark: { bg: 'url(http://x)', ac: '#0f0', nope: '1' } as TokenSet } })
  const t = themeTokens(m, fallback, 'dark')
  assert.equal(t.bg, '#000000')
  assert.equal(t.ac, '#0f0')
  assert.equal(Object.hasOwn(t, 'nope'), false)
})

test('themeTokens follows the accent pick and the variant like the stylesheet does, and a mode the fallback lacks reads its other', () => {
  const free = make({ accent: 'free', modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#000' } } })
  assert.equal(themeTokens(free, fallback, 'light', { accent: { hue: 255, chroma: 0.15 } }).ac, accentTokens('light', 255, 0.15).ac)
  const v = make({ accent: { variants: [{ name: 'Amber', tokens: { ac: '#ffb000' } }] } })
  assert.equal(themeTokens(v, fallback, 'dark', { variant: 'Amber' }).ac, '#ffb000')
  assert.equal(themeTokens(make({ modes: ['light'], tokens: { light: { bg: '#fff' } } }), make({ id: 'default', tokens: { dark: { tx: '#eeeeee' } } }), 'light').tx, '#eeeeee')
})

// An effect reads colors as data, so the slots the stylesheet derives with var() reach it resolved against the theme's own
// values: the same color the browser computes, never the fallback's literal for a slot the theme leaves to its own.
const defaultLike = make({ id: 'default', modes: ['light', 'dark'], accent: 'free', tokens: {
  light: { bg: '#ffffff', tx: '#111111', edge: '#888888', stall: '#cc6600', 'ac-soft': '#aaeeee', 'ac-line': '#88dddd' },
  dark: { bg: '#000000', tx: '#eeeeee', edge: '#999999', stall: '#dd7700', 'ac-soft': '#116666', 'ac-line': '#228888' },
} })

test('themeTokens derives edge, stall, ac-soft and ac-line from the theme\'s own values, not the fallback\'s literals', () => {
  const m = make({ tokens: { dark: { bg: '#010a04', ac: '#00ff41', mu: '#6fa8ff', warn: '#ffaa00', bad: '#ff3355' } } })
  const t = themeTokens(m, defaultLike, 'dark')
  assert.equal(t.edge, '#6fa8ff')
  assert.equal(t.stall, 'color-mix(in oklch, #ffaa00 50%, #ff3355)')
  assert.equal(t['ac-soft'], 'color-mix(in oklch, #00ff41 14%, transparent)')
  assert.equal(t['ac-line'], 'color-mix(in oklch, #00ff41 40%, transparent)')
  // what the stylesheet says in var() form is what the data says resolved: one substitution apart
  const css = themeCss(m, defaultLike)
  for (const k of ['edge', 'stall', 'ac-soft', 'ac-line']) {
    const declared = new RegExp(`--${k}:([^;]*);`).exec(css)![1]
    assert.equal(declared.replace(/var\(--([a-z-]+)\)/g, (_, n: string) => t[n]), t[k], k)
  }
})

test('themeTokens: a slot the theme sets keeps its own value, and a derived one follows the variant and the accent pick', () => {
  const own = make({ tokens: { dark: { bg: '#010a04', ac: '#00ff41', edge: '#7f9cff', stall: '#ff9a5a', 'ac-soft': '#003311' } } })
  const t = themeTokens(own, defaultLike, 'dark')
  assert.deepEqual([t.edge, t.stall, t['ac-soft']], ['#7f9cff', '#ff9a5a', '#003311'])
  assert.equal(t['ac-line'], 'color-mix(in oklch, #00ff41 40%, transparent)')
  const v = make({ accent: { variants: [{ name: 'Amber', tokens: { ac: '#ffb000' } }] } })
  assert.equal(themeTokens(v, defaultLike, 'dark', { variant: 'Amber' })['ac-line'], 'color-mix(in oklch, #ffb000 40%, transparent)')
  // a free accent supplies its own soft and line forms, which a derivation never replaces
  const free = make({ accent: 'free', modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#000' } } })
  const pick = { accent: { hue: 255, chroma: 0.15 } }
  assert.equal(themeTokens(free, defaultLike, 'dark', pick)['ac-soft'], accentTokens('dark', 255, 0.15)['ac-soft'])
})

test('themeTokens: Default itself keeps its own derived values, and a fallback without the slots leaves them out if the theme lacks the inputs', () => {
  assert.equal(themeTokens(defaultLike, defaultLike, 'dark').edge, '#999999')
  const bare = make({ id: 'default', accent: 'free', tokens: { dark: { bg: '#000000' } } })
  const t = themeTokens(make({ tokens: { dark: { bg: '#010a04', ac: '#00ff41' } } }), bare, 'dark')
  // no mu, warn or bad anywhere: nothing to derive from, so nothing is invented
  assert.equal(Object.hasOwn(t, 'edge'), false)
  assert.equal(Object.hasOwn(t, 'stall'), false)
  assert.equal(t['ac-soft'], 'color-mix(in oklch, #00ff41 14%, transparent)')
})

// The look slots reach CSS only through LOOK: a keyword picks code-owned values, so the table must cover each slot's keywords exactly
test('LOOK maps every keyword of every enum look slot, and nothing else', () => {
  for (const [slot, table] of Object.entries(LOOK)) {
    assert.equal(SLOTS[slot]?.kind, 'enum', slot)
    assert.deepEqual(Object.keys(table).sort(), [...(SLOTS[slot].values ?? [])].sort(), slot)
    // each keyword sets the same variables, so switching themes never leaves one behind
    const names = Object.values(table).map((row) => Object.keys(row).sort().join(' '))
    assert.equal(new Set(names).size, 1, `${slot} sets different variables per keyword`)
    for (const row of Object.values(table)) for (const [k, v] of Object.entries(row)) {
      assert.match(k, /^--look-[a-z-]+$/)
      assert.ok(!/url\(|[;{}<>]|@import|expression|\/\*/i.test(v), `${k}:${v}`)
    }
  }
  const looks = ['font-body', 'chrome', 'cursor', 'hover-lift', 'meter', 'backdrop', 'orbs', 'orbs-tint', 'header', 'header-shape', 'stage', 'card-fill', 'card-corner', 'card-frame', 'card-marks', 'card-stripe', 'card-shadow', 'glyph-arrow', 'glyph-bullet', 'glyph-sep', 'glyph-brand', 'wire-style', 'wire-head', 'wire-crawl', 'wire-flow', 'wire-glyphs', 'wire-signal', 'tree-orbits', 'node-beat']
  assert.deepEqual(Object.keys(LOOK).sort(), looks.sort())
})

test('lookDeclarations writes the row for each keyword, makes a lift of none a shadow that draws nothing, and switches the scan strip', () => {
  const base: TokenSet = { backdrop: 'grid', 'card-shadow': 'lift', lift: '0 1px 2px #0002', 'backdrop-scan': '.4', 'glyph-sep': 'bar' }
  const css = lookDeclarations(base)
  assert.ok(css.includes('--look-grid-h:1;--look-grid-v:1;--look-dots:0;'), css)
  assert.ok(css.includes('--look-shadow:var(--lift);'))
  assert.ok(css.includes('--look-scan:block;'))
  assert.ok(css.includes('--look-sep:"|";'))
  const flat = lookDeclarations({ ...base, lift: 'none', 'backdrop-scan': '0' })
  assert.ok(flat.includes('--look-shadow:0 0 transparent;') && flat.includes('--look-scan:none;'), flat)
  // the floating top bar's shadow sits in a list with its line (header rule), so a lift of none there is the same empty shadow
  assert.ok(lookDeclarations({ 'header-shape': 'float', lift: '0 1px 2px #0002' }).includes('--look-head-lift:var(--lift);'))
  assert.ok(lookDeclarations({ 'header-shape': 'float', lift: ' none ' }).includes('--look-head-lift:0 0 transparent;'))
  // a keyword the table does not have writes nothing for that slot (the validator has dropped it before this in practice)
  assert.ok(!lookDeclarations({ backdrop: 'url(x)' }).includes('--look-grid'))
})

test('the look reaches a theme block through the fallback, and never reaches themeTokens', () => {
  const look = make({ id: 'default', modes: ['dark'], accent: 'free', tokens: { dark: { bg: '#000000', tx: '#eeeeee', 'card-fill': 'plain', 'glyph-arrow': 'chevron' } } })
  const css = themeCss(make({ tokens: { dark: { bg: '#010a04', 'glyph-arrow': 'arrow' } } }), look)
  assert.ok(css.includes('--look-fill:var(--panel);'), 'from the fallback')
  assert.ok(css.includes('--look-fwd:"→";--look-back:"←";'), 'the theme overrides the fallback')
  assert.ok(Object.keys(themeTokens(make(), look, 'dark')).every((k) => !k.startsWith('--')))
})

// a bare 0 is a number to calc(), so calc(var(--radius) + 14px), the floating top bar's corner, would be invalid with it
test('a length of bare 0 is written with its slot unit, so calc() can add to it', () => {
  const css = themeCss(make({ tokens: { dark: { radius: '0', tracking: '0', 'card-cut': '8px' } } }), fallback)
  assert.ok(css.includes('--radius:0px;'), css)
  assert.ok(css.includes('--tracking:0em;'), css)
  assert.ok(css.includes('--card-cut:8px;'), css)
})
