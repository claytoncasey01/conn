import assert from 'node:assert/strict'
import { test } from 'node:test'
import { CSS_HREF, DEFAULT_ID, FALLBACK_ID, FONTS_HREF, KEYS, readCache, SHEET_CHARS_MAX, readChoice, readPin, removeCache, withPin, writeCache, writeChoice } from './storage.ts'
import type { CacheEntry, Choice } from './storage.ts'

const fake = (init: Record<string, string> = {}) => {
  const m = new Map(Object.entries(init))
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m }
}
const boom = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('quota') } }
const defaults: Choice = { id: DEFAULT_ID, mode: 'system', accent: null, variant: null, effects: 'auto' }
const entry: CacheEntry = { css: '[data-theme="x"]{--bg:#000;}', attrs: { motion: 'calm', led: 'circle', glass: 'off' }, only: null, cssUrl: null, sheet: null, fontsUrl: null }
const SHA = 'a'.repeat(64)
const CSS = `/api/themes/x/theme.css?v=${SHA}`
const FONTS = 'https://fonts.googleapis.com/css2?family=Chakra+Petch:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;700&display=swap'

test('FALLBACK_ID is the built-in that always exists, and today the first-run default is the same theme', () => {
  // FALLBACK_ID is what reconcile and the loader fall back to: it must be a theme that is always listed, never a setting.
  // A deliberate change of DEFAULT_ID (a first run on another built-in) must update the second line on purpose.
  assert.equal(FALLBACK_ID, 'default')
  assert.equal(DEFAULT_ID, FALLBACK_ID)
})

test('keys are the ones the boot script reads', () => {
  assert.equal(KEYS.choice, 'conn.theme')
  assert.equal(KEYS.css('matrix'), 'conn.theme.css.matrix')
  assert.equal(KEYS.cssOk, 'conn.theme.cssOk')
})

test('readChoice: missing, null storage, a throwing getItem, bad JSON and a wrong shape all give the defaults', () => {
  assert.deepEqual(readChoice(fake()), defaults)
  assert.deepEqual(readChoice(null), defaults)
  assert.deepEqual(readChoice(boom), defaults)
  for (const bad of ['{nope', 'null', '7', '"x"', '[]', '{}', '{"id":5,"mode":"loud"}'])
    assert.deepEqual(readChoice(fake({ [KEYS.choice]: bad })), defaults, bad)
})

test('readChoice round-trips a written choice', () => {
  const c: Choice = { id: 'matrix', mode: 'dark', accent: { name: 'Rose', hue: 355, chroma: 0.16 }, variant: 'green', effects: 'off' }
  const s = fake()
  writeChoice(s, c)
  assert.deepEqual(readChoice(s), c)
})

test('readChoice clamps the stored accent into range and wraps the hue', () => {
  const read = (accent: unknown) => readChoice(fake({ [KEYS.choice]: JSON.stringify({ ...defaults, accent }) })).accent
  assert.deepEqual(read({ name: 'X', hue: 725, chroma: 9 }), { name: 'X', hue: 5, chroma: 0.4 })
  assert.deepEqual(read({ name: 'X', hue: -30, chroma: -1 }), { name: 'X', hue: 330, chroma: 0 })
  assert.deepEqual(read({ name: 'X', hue: 360, chroma: 0.1 }), { name: 'X', hue: 0, chroma: 0.1 })
})

test('readChoice drops an accent whose hue, chroma or name is not usable', () => {
  const read = (accent: unknown) => readChoice(fake({ [KEYS.choice]: JSON.stringify({ ...defaults, accent }) })).accent
  for (const bad of [
    { name: 'X', hue: '200', chroma: 0.1 }, { name: 'X', hue: 200, chroma: null }, { name: 'X', hue: 200 },
    { name: 5, hue: 200, chroma: 0.1 }, { name: 'x'.repeat(200), hue: 200, chroma: 0.1 }, 'teal', [], 7,
  ]) assert.equal(read(bad), null, JSON.stringify(bad))
  // JSON has no NaN or Infinity; a huge exponent parses to Infinity
  const s = fake({ [KEYS.choice]: '{"id":"default","mode":"system","accent":{"name":"X","hue":1e999,"chroma":0.1}}' })
  assert.equal(readChoice(s).accent, null)
})

test('readChoice keeps an unknown mode or effects value out and a bad id on the default', () => {
  const c = readChoice(fake({ [KEYS.choice]: JSON.stringify({ id: '../x', mode: 'loud', effects: 'max', variant: 7 }) }))
  assert.deepEqual(c, defaults)
})

test('writeChoice into a throwing or null storage does not throw', () => {
  writeChoice(boom, defaults)
  writeChoice(null, defaults)
})

test('readCache: a written entry comes back; anything else is null and never throws', () => {
  const s = fake()
  writeCache(s, 'x', entry)
  assert.deepEqual(readCache(s, 'x'), entry)
  assert.equal(readCache(s, 'y'), null)
  assert.equal(readCache(null, 'x'), null)
  assert.equal(readCache(boom, 'x'), null)
  writeCache(boom, 'x', entry)
})

test('readCache rejects corrupt JSON and every wrong shape', () => {
  const bad = (v: unknown) => readCache(fake({ [KEYS.css('x')]: typeof v === 'string' ? v : JSON.stringify(v) }), 'x')
  assert.equal(bad('{oops'), null)
  assert.equal(bad(null), null)
  assert.equal(bad([]), null)
  assert.equal(bad({ ...entry, css: 5 }), null)
  assert.equal(bad({ ...entry, attrs: null }), null)
  assert.equal(bad({ ...entry, attrs: { ...entry.attrs, motion: 'wild' } }), null)
  assert.equal(bad({ ...entry, attrs: { ...entry.attrs, led: 'star' } }), null)
  assert.equal(bad({ ...entry, attrs: { ...entry.attrs, glass: 'maybe' } }), null)
  assert.equal(bad({ ...entry, only: 'sepia' }), null)
  assert.equal(bad({ css: entry.css, attrs: entry.attrs }), null)
  assert.deepEqual(bad({ ...entry, only: 'dark' }), { ...entry, only: 'dark' })
})

test('readCache keeps a valid stylesheet with its address and a fonts href, and an entry written before they existed reads as null', () => {
  const s = fake()
  writeCache(s, 'x', { ...entry, cssUrl: CSS, sheet: 'a{}', fontsUrl: FONTS })
  assert.deepEqual(readCache(s, 'x'), { ...entry, cssUrl: CSS, sheet: 'a{}', fontsUrl: FONTS })
  const old = fake({ [KEYS.css('x')]: JSON.stringify({ css: entry.css, attrs: entry.attrs, only: null }) })
  assert.deepEqual(readCache(old, 'x'), entry)
  // an address cached before the text was kept (the stylesheet used to be a link) is dropped: it needs the secret now
  const linked = fake({ [KEYS.css('x')]: JSON.stringify({ ...entry, cssUrl: CSS }) })
  assert.deepEqual(readCache(linked, 'x'), entry)
})

test('readCache keeps a stylesheet only as a string up to the limit, beside its address', () => {
  const got = (over: object) => readCache(fake({ [KEYS.css('x')]: JSON.stringify({ ...entry, ...over }) }), 'x')
  for (const bad of [5, {}, ['a{}'], true, 'a'.repeat(SHEET_CHARS_MAX + 1)]) assert.deepEqual(got({ cssUrl: CSS, sheet: bad }), entry, String(bad).slice(0, 20))
  assert.deepEqual(got({ sheet: 'a{}' }), entry, 'no address')
  assert.deepEqual(got({ cssUrl: CSS, sheet: 'a'.repeat(SHEET_CHARS_MAX) }), { ...entry, cssUrl: CSS, sheet: 'a'.repeat(SHEET_CHARS_MAX) })
})

test('readCache drops a hostile or malformed href but keeps the tokens (today\'s behavior for those fields)', () => {
  const got = (over: object) => readCache(fake({ [KEYS.css('x')]: JSON.stringify({ ...entry, ...over }) }), 'x')
  const hostileCss = [
    `//evil.com/x.css?v=${SHA}`, `https://evil.com/api/themes/x/theme.css?v=${SHA}`, `javascript:alert(1)`, `data:text/css,a{}`,
    `/api/themes/../x/theme.css?v=${SHA}`, `/api/themes/X/theme.css?v=${SHA}`, `/api/themes/x/theme.css?v=${'A'.repeat(64)}`,
    `/api/themes/x/theme.css?v=${'a'.repeat(63)}`, `${CSS}&x=1`, `${CSS}\n`, `/api/themes/x/theme.css?v=${SHA}#`, `/api/themes/x/other.css?v=${SHA}`,
    `/api/themes/x/y/theme.css?v=${SHA}`, 5, {}, [CSS], `/api/themes/${'a'.repeat(5000)}/theme.css?v=${SHA}`,
  ]
  for (const h of hostileCss) assert.deepEqual(got({ cssUrl: h, sheet: 'a{}' }), entry, String(h).slice(0, 60))
  const hostileFonts = [
    'https://evil.com/css2?family=A:wght@400&display=swap', 'http://fonts.googleapis.com/css2?family=A:wght@400&display=swap',
    'https://fonts.googleapis.com.evil.com/css2?family=A:wght@400&display=swap', 'https://fonts.googleapis.com/css2?family=A:wght@400&display=swap&x=<',
    'https://fonts.googleapis.com/css2?family=A:wght@400&display=swap#', '//fonts.googleapis.com/css2?family=A:wght@400&display=swap', 'javascript:1', 7,
    'https://fonts.googleapis.com/css2?family=Evil+Font:wght@400&display=swap', FONTS + 'x', `https://fonts.googleapis.com/css2?family=${'A'.repeat(5000)}:wght@400&display=swap`,
  ]
  for (const h of hostileFonts) assert.deepEqual(got({ fontsUrl: h }), entry, String(h).slice(0, 60))
  // a well-formed href for ANOTHER theme's stylesheet is as bad as a malformed one
  assert.deepEqual(got({ cssUrl: `/api/themes/y/theme.css?v=${SHA}`, sheet: 'a{}' }), entry)
  // each field stands alone: a bad one never takes the good one down with it
  assert.deepEqual(got({ cssUrl: '//evil.com/x.css', sheet: 'a{}', fontsUrl: FONTS }), { ...entry, fontsUrl: FONTS })
  assert.deepEqual(got({ cssUrl: CSS, sheet: 'a{}', fontsUrl: 'javascript:1' }), { ...entry, cssUrl: CSS, sheet: 'a{}' })
})

test('CSS_HREF and FONTS_HREF are anchored, and the fonts pattern takes what fontsHref builds for every allowed name', () => {
  assert.equal(CSS_HREF.source, '^\\/api\\/themes\\/[a-z0-9-]+\\/theme\\.css\\?v=[0-9a-f]{64}$')
  assert.ok(CSS_HREF.test(CSS) && !CSS_HREF.test(CSS + '\n'))
  assert.ok(FONTS_HREF.test(FONTS))
})

test('removeCache drops one theme entry, and a throwing store is fine', () => {
  const m = new Map([[KEYS.css('gone'), '{}'], [KEYS.css('keep'), '{}']])
  removeCache({ removeItem: (k: string) => void m.delete(k) }, 'gone')
  assert.deepEqual([...m.keys()], [KEYS.css('keep')])
  removeCache(null, 'gone')
  removeCache({ removeItem: () => { throw new Error('denied') } }, 'gone')
})

test('readPin: theme and mode from the query, the same checks as the boot script, else null', () => {
  assert.deepEqual(readPin('?theme=matrix&mode=dark'), { id: 'matrix', mode: 'dark' })
  assert.deepEqual(readPin('?mode=system'), { mode: 'system' })
  assert.deepEqual(readPin('?theme=matrix'), { id: 'matrix' })
  assert.deepEqual(readPin('?theme=../x&mode=loud'), null)
  assert.deepEqual(readPin('?theme=Matrix'), null)
  assert.deepEqual(readPin(''), null)
  assert.deepEqual(readPin('?x=1&theme=ok-1'), { id: 'ok-1' })
})

test('withPin: the pin overrides the stored choice key by key, and no pin is the choice itself', () => {
  assert.equal(withPin(defaults, null), defaults)
  assert.deepEqual(withPin(defaults, { id: 'matrix' }), { ...defaults, id: 'matrix' })
  assert.deepEqual(withPin(defaults, { mode: 'dark' }), { ...defaults, mode: 'dark' })
  assert.deepEqual(withPin(defaults, { id: 'matrix', mode: 'dark' }), { ...defaults, id: 'matrix', mode: 'dark' })
  // the stored choice is not touched
  assert.equal(defaults.id, DEFAULT_ID)
})

test('readCache refuses a stylesheet that would load something (url(), @import, expression), as the boot script does', () => {
  for (const css of ['[data-theme="x"]{--look-marks:url(https://x/y.svg)}', '@import "https://x/y.css";', 'a{width:expression(1)}', 'A{BACKGROUND:URL(x)}'])
    assert.equal(readCache(fake({ [KEYS.css('x')]: JSON.stringify({ ...entry, css }) }), 'x'), null, css)
  assert.deepEqual(readCache(fake({ [KEYS.css('x')]: JSON.stringify(entry) }), 'x')?.css, entry.css)
})
