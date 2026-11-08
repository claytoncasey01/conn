import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { amend, applyResolved, cacheEntryOf, domFor, installFallback, reconcile, select } from './apply.ts'
import type { Dom, El } from './apply.ts'
import { fakeDoc } from './fakeDoc.ts'
import { fallbackCss } from './css.ts'
import { resolveTheme } from './resolve.ts'
import { DEFAULT_ID } from './storage.ts'
import type { Choice } from './storage.ts'
import type { ThemeEntry, ThemeManifest, ThemeSource } from './types.ts'

const def = JSON.parse(readFileSync(new URL('../../../themes/default/theme.json', import.meta.url), 'utf8')) as ThemeManifest
const make = (over: Partial<ThemeManifest> = {}): ThemeManifest => ({
  id: 'matrix', name: 'Matrix', version: '1', author: '', license: '', description: '',
  modes: ['dark'], accent: 'fixed', fonts: ['Share Tech Mono'], tokens: { dark: { bg: '#010a04', ac: '#00ff41', motion: 'power', led: 'square', blur: '4px' } }, effects: [],
  ...over,
})
const entry = (m: ThemeManifest, source: ThemeSource = 'builtin', css: string | null = null): ThemeEntry =>
  ({ id: m.id, source, hasCss: css !== null, cssSha256: css, manifest: m })
const bad = (id: string, source: ThemeSource): ThemeEntry => ({ id, source, hasCss: false, cssSha256: null, invalid: 'nope' })
const choice = (over: Partial<Choice> = {}): Choice => ({ id: 'matrix', mode: 'system', accent: null, variant: null, effects: 'auto', ...over })
const resolve = (list: ThemeEntry[], c: Choice, accepted: Record<string, string> = {}) =>
  resolveTheme({ list, choice: c, systemDark: true, accepted, fallback: def })

function recorder() {
  const calls: string[] = []
  const dom: Dom = {
    setAttr: (n, v) => void calls.push(`attr ${n}=${v}`),
    style: (css) => void calls.push(`style ${css === null ? 'null' : css}`),
    link: (id, href) => void calls.push(`link ${id} ${href}`),
    sheet: (css) => void calls.push(`sheet ${css}`),
  }
  return { calls, dom }
}

test('applyResolved sets the five attributes and the style text', () => {
  const r = resolve([entry(make())], choice())
  const { calls, dom } = recorder()
  applyResolved(dom, r, null)
  for (const a of ['attr data-theme=matrix', 'attr data-mode=dark', 'attr data-motion=power', 'attr data-led=square', 'attr data-glass=on'])
    assert.ok(calls.includes(a), a)
  assert.ok(calls.includes(`style ${r.css}`))
  assert.ok(r.css.includes('[data-theme="matrix"]'))
})

test('applyResolved links the fonts, and removes the fonts link for a theme with none', () => {
  const withFonts = recorder()
  applyResolved(withFonts.dom, resolve([entry(make())], choice()), null)
  assert.ok(withFonts.calls.some((c) => c.startsWith('link conn-theme-fonts https://fonts.googleapis.com/css2?family=Share+Tech+Mono')))
  const none = recorder()
  applyResolved(none.dom, resolve([entry(make({ fonts: [] }))], choice()), null)
  assert.ok(none.calls.includes('link conn-theme-fonts null'))
})

test('applyResolved: a null cssUrl removes the theme\'s stylesheet, a served one applies the text read from it', () => {
  const plain = recorder()
  applyResolved(plain.dom, resolve([entry(make())], choice()), 'a{}')
  assert.ok(plain.calls.includes('sheet null'), 'no cssUrl: nothing, whatever text is passed')
  const served = recorder()
  applyResolved(served.dom, resolve([entry(make(), 'builtin', 'abc')], choice()), 'a{}')
  assert.ok(served.calls.includes('sheet a{}'))
  const unread = recorder()
  applyResolved(unread.dom, resolve([entry(make(), 'builtin', 'abc')], choice()), null)
  assert.ok(unread.calls.includes('sheet null'), 'not read yet: no other theme\'s rules stay on')
  assert.ok(!served.calls.some((c) => c.startsWith('link conn-theme-css')), 'never a link: it is an /api route')
})

test('domFor writes the style with textContent into one #conn-theme and removes a link on null', () => {
  const doc = fakeDoc()
  const dom = domFor(doc)
  dom.style('a{}')
  dom.style('b{}')
  assert.deepEqual(doc.order(), ['conn-theme'])
  assert.equal(doc.el('conn-theme')?.textContent, 'b{}')
  dom.link('conn-theme-fonts', '/x.css')
  assert.equal(doc.el('conn-theme-fonts')?.href, '/x.css')
  assert.equal(doc.el('conn-theme-fonts')?.rel, 'stylesheet')
  dom.link('conn-theme-fonts', null)
  assert.equal(doc.el('conn-theme-fonts'), undefined)
  dom.setAttr('data-theme', 'x')
  assert.equal(doc.attrs['data-theme'], 'x')
})

test('domFor adopts the fonts link the boot script made: the same href is left alone, a new one updates it', () => {
  const doc = fakeDoc()
  const href = 'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&display=swap'
  const boot = doc.createElement('link')
  boot.id = 'conn-theme-fonts'
  boot.rel = 'stylesheet'
  boot.href = href
  doc.head.appendChild(boot)
  const writes = doc.hrefWrites(boot)
  const dom = domFor(doc)
  dom.link('conn-theme-fonts', href)
  assert.deepEqual(doc.order(), ['conn-theme-fonts'], 'no second link')
  assert.equal(doc.hrefWrites(boot), writes, 'the same href is not written again (a reload of the sheet)')
  dom.link('conn-theme-fonts', href + 'x')
  assert.equal(boot.href, href + 'x')
})

test('domFor: the theme\'s stylesheet is one style element, its text written only when it changed, null removes it', () => {
  const doc = fakeDoc()
  const dom = domFor(doc)
  dom.sheet('a{}')
  const el = doc.el('conn-theme-css')
  assert.equal(el?.textContent, 'a{}')
  assert.equal(el?.href, undefined, 'a style element, never a link')
  dom.sheet('a{}')
  dom.sheet('b{}')
  assert.deepEqual(doc.order(), ['conn-theme-css'])
  assert.equal(doc.el('conn-theme-css'), el)
  assert.equal(el?.textContent, 'b{}')
  dom.sheet(null)
  assert.deepEqual(doc.order(), [])
})

// In dev the app's own CSS is added to the head by script, after the boot script ran, so the boot style is no longer the last
// one and Neon's rules would lose their ties with it. The loader moves it to the end.
test('domFor adopts the style the boot script painted, and keeps it last in the head', () => {
  const doc = fakeDoc()
  const boot = doc.createElement('style')
  boot.id = 'conn-theme-css'
  boot.textContent = 'a{}'
  doc.head.appendChild(boot)
  const later = doc.createElement('style')
  later.id = 'app-css'
  doc.head.appendChild(later)
  const dom = domFor(doc)
  dom.sheet('a{}')
  assert.deepEqual(doc.order(), ['app-css', 'conn-theme-css'])
  assert.equal(doc.el('conn-theme-css'), boot, 'adopted, not made again')
  dom.link('conn-theme-fonts', 'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&display=swap')
  dom.sheet('a{}')
  assert.deepEqual(doc.order(), ['app-css', 'conn-theme-fonts', 'conn-theme-css'])
})

test('(a) the fallback style goes BEFORE the boot script style, so the theme wins at equal specificity', () => {
  const css = fallbackCss(def)
  // the boot script already put #conn-theme in the head
  const boot = fakeDoc()
  const bootStyle = boot.createElement('style')
  bootStyle.id = 'conn-theme'
  boot.head.appendChild(bootStyle)
  installFallback(boot, css)
  assert.deepEqual(boot.order(), ['conn-theme-fallback', 'conn-theme'])
  assert.equal(boot.el('conn-theme-fallback')?.textContent, css)
  // no cache, so no boot style: the style the loader makes later still comes after
  const fresh = fakeDoc()
  installFallback(fresh, css)
  domFor(fresh).style('x{}')
  assert.deepEqual(fresh.order(), ['conn-theme-fallback', 'conn-theme'])
  // twice is still one fallback
  installFallback(fresh, css)
  assert.equal(fresh.order().filter((i) => i === 'conn-theme-fallback').length, 1)
})

test('(d) the cache entry is the generated token stylesheet, the fonts href and the theme\'s own stylesheet', () => {
  const r = resolve([entry(make(), 'builtin', 'abc')], choice())
  assert.ok(r.cssUrl)
  const c = cacheEntryOf(r, 'a{}')
  assert.equal(c.css, r.css)
  assert.deepEqual(c.attrs, r.attrs)
  assert.equal(c.only, 'dark')
  assert.deepEqual(Object.keys(c).sort(), ['attrs', 'css', 'cssUrl', 'fontsUrl', 'only', 'sheet'])
  assert.ok(!c.css.includes('cssUrl') && !c.css.includes('/api/themes'))
})

test('the cache entry carries what the app applied: the stylesheet with its address, and the fonts href', () => {
  const r = resolve([entry(make(), 'builtin', 'a'.repeat(64))], choice())
  const c = cacheEntryOf(r, 'a{}')
  assert.deepEqual([c.cssUrl, c.sheet], [`/api/themes/matrix/theme.css?v=${'a'.repeat(64)}`, 'a{}'])
  assert.equal(c.fontsUrl, 'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&display=swap')
  const unread = cacheEntryOf(r, null)
  assert.deepEqual([unread.cssUrl, unread.sheet], [null, null], 'an address without its text is not kept')
  const none = cacheEntryOf(resolve([entry(make({ fonts: [] }))], choice()), 'a{}')
  assert.deepEqual([none.cssUrl, none.sheet, none.fontsUrl], [null, null, null])
})

test('a stylesheet is cached only for CSS the app served: a user theme asks first, a gallery theme never has one', () => {
  const sha = 'b'.repeat(64)
  const user = entry(make(), 'user', sha)
  assert.equal(cacheEntryOf(resolve([user], choice()), 'a{}').sheet, null, 'not accepted yet')
  assert.equal(cacheEntryOf(resolve([user], choice(), { matrix: 'c'.repeat(64) }), 'a{}').sheet, null, 'accepted for other CSS')
  assert.equal(cacheEntryOf(resolve([user], choice(), { matrix: sha }), 'a{}').cssUrl, `/api/themes/matrix/theme.css?v=${sha}`)
  // a gallery install is a user folder with .source.json: the server lists no CSS for it, and even a hand-forged entry gets none
  assert.equal(cacheEntryOf(resolve([entry(make(), 'gallery')], choice()), 'a{}').sheet, null)
  assert.equal(cacheEntryOf(resolve([entry(make(), 'gallery', sha)], choice(), { matrix: sha }), 'a{}').sheet, null)
})

test('reconcile: a theme that is no longer listed becomes default and its cache is dropped', () => {
  const out = reconcile([entry(def)], choice({ id: 'gone', mode: 'dark', variant: 'v' }))
  assert.equal(out.choice.id, 'default')
  assert.equal(out.choice.id, DEFAULT_ID)
  assert.equal(out.dropCache, 'gone')
  assert.equal(out.choice.mode, 'dark')
  assert.equal(out.choice.variant, null)
})

test('reconcile: an invalid theme is the same as a missing one', () => {
  const out = reconcile([entry(def), bad('matrix', 'user')], choice())
  assert.deepEqual([out.choice.id, out.dropCache], ['default', 'matrix'])
})

test('reconcile: a valid theme, and Default itself (even before it is listed), change nothing', () => {
  const c = choice()
  assert.deepEqual(reconcile([entry(make())], c), { choice: c, dropCache: null })
  const d = choice({ id: 'default' })
  assert.deepEqual(reconcile([], d), { choice: d, dropCache: null })
})

test('(e) reconcile: an invalid user theme named like a built-in never shadows the valid one, in either order', () => {
  const d = choice({ id: 'default' })
  for (const list of [[bad('default', 'user'), entry(def)], [entry(def), bad('default', 'user')]]) {
    assert.deepEqual(reconcile(list, d), { choice: d, dropCache: null })
    const m = choice({ id: 'matrix' })
    const l2 = [...list, entry(make()), bad('matrix', 'user')]
    assert.deepEqual(reconcile(l2, m), { choice: m, dropCache: null })
    assert.equal(select(l2, m, 'default', {}).kind, 'apply')
  }
  // and the built-in is picked over a valid user theme of the same id
  const user = make({ id: 'default', name: 'Mine' })
  assert.equal(resolve([entry(user, 'user'), entry(def)], choice({ id: 'default' })).entry.manifest?.name, 'Default')
})

test('(g) select: a user theme with CSS needs consent until accepted; a built-in with CSS does not', () => {
  const list = [entry(make(), 'user', 'h1'), entry(make({ id: 'neon' }), 'builtin', 'h2'), entry(make({ id: 'plain' }), 'user')]
  assert.equal(select(list, choice({ id: 'default' }), 'matrix', {}).kind, 'consent')
  assert.equal(select(list, choice({ id: 'default' }), 'matrix', { matrix: 'old' }).kind, 'consent')
  const ok = select(list, choice({ id: 'default', accent: { name: 'A', hue: 1, chroma: 0.1 } }), 'matrix', { matrix: 'h1' })
  assert.equal(ok.kind, 'apply')
  assert.equal(ok.kind === 'apply' && ok.choice.id, 'matrix')
  assert.equal(ok.kind === 'apply' && ok.choice.accent?.name, 'A')
  assert.equal(select(list, choice({ id: 'default' }), 'neon', {}).kind, 'apply')
  assert.equal(select(list, choice({ id: 'default' }), 'plain', {}).kind, 'apply')
  assert.equal(select(list, choice({ id: 'default' }), 'nope', {}).kind, 'none')
  assert.equal(select(list, choice({ id: 'default' }), '../x', {}).kind, 'none')
})

test('select: moving to another theme drops the old theme variant; staying keeps it', () => {
  const list = [entry(make()), entry(make({ id: 'other' }))]
  const moved = select(list, choice({ variant: 'v' }), 'other', {})
  assert.equal(moved.kind === 'apply' && moved.choice.variant, null)
  const same = select(list, choice({ variant: 'v' }), 'matrix', {})
  assert.equal(same.kind === 'apply' && same.choice.variant, 'v')
})

test('(f) amend clamps an accent, and refuses a bad one, a bad mode, effects or variant', () => {
  const c = choice()
  assert.deepEqual(amend(c, { accent: { name: 'X', hue: 400, chroma: 9 } })?.accent, { name: 'X', hue: 40, chroma: 0.4 })
  assert.deepEqual(amend(c, { accent: { name: 'X', hue: -10, chroma: -1 } })?.accent, { name: 'X', hue: 350, chroma: 0 })
  assert.equal(amend(c, { accent: { name: 'X', hue: Number.NaN, chroma: 0.1 } }), null)
  assert.equal(amend(c, { accent: { name: 'x'.repeat(33), hue: 1, chroma: 0.1 } }), null)
  assert.equal(amend(c, { mode: 'loud' as never }), null)
  assert.equal(amend(c, { effects: 'x' as never }), null)
  assert.equal(amend(c, { variant: 'y'.repeat(41) }), null)
  assert.equal(amend(c, { mode: 'dark' })?.mode, 'dark')
  assert.equal(amend(c, { effects: 'off' })?.effects, 'off')
  assert.equal(amend(c, { variant: 'green' })?.variant, 'green')
})
