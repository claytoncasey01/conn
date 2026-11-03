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
  }
  return { calls, dom }
}

test('applyResolved sets the five attributes and the style text', () => {
  const r = resolve([entry(make())], choice())
  const { calls, dom } = recorder()
  applyResolved(dom, r)
  for (const a of ['attr data-theme=matrix', 'attr data-mode=dark', 'attr data-motion=power', 'attr data-led=square', 'attr data-glass=on'])
    assert.ok(calls.includes(a), a)
  assert.ok(calls.includes(`style ${r.css}`))
  assert.ok(r.css.includes('[data-theme="matrix"]'))
})

test('applyResolved links the fonts, and removes the fonts link for a theme with none', () => {
  const withFonts = recorder()
  applyResolved(withFonts.dom, resolve([entry(make())], choice()))
  assert.ok(withFonts.calls.some((c) => c.startsWith('link conn-theme-fonts https://fonts.googleapis.com/css2?family=Share+Tech+Mono')))
  const none = recorder()
  applyResolved(none.dom, resolve([entry(make({ fonts: [] }))], choice()))
  assert.ok(none.calls.includes('link conn-theme-fonts null'))
})

test('applyResolved: a null cssUrl removes the theme.css link, a served one sets it', () => {
  const plain = recorder()
  applyResolved(plain.dom, resolve([entry(make())], choice()))
  assert.ok(plain.calls.includes('link conn-theme-css null'))
  const served = recorder()
  applyResolved(served.dom, resolve([entry(make(), 'builtin', 'abc')], choice()))
  assert.ok(served.calls.includes('link conn-theme-css /api/themes/matrix/theme.css?v=abc'))
})

test('domFor writes the style with textContent into one #conn-theme and removes a link on null', () => {
  const doc = fakeDoc()
  const dom = domFor(doc)
  dom.style('a{}')
  dom.style('b{}')
  assert.deepEqual(doc.order(), ['conn-theme'])
  assert.equal(doc.el('conn-theme')?.textContent, 'b{}')
  dom.link('conn-theme-css', '/x.css')
  assert.equal(doc.el('conn-theme-css')?.href, '/x.css')
  assert.equal(doc.el('conn-theme-css')?.rel, 'stylesheet')
  dom.link('conn-theme-css', null)
  assert.equal(doc.el('conn-theme-css'), undefined)
  dom.setAttr('data-theme', 'x')
  assert.equal(doc.attrs['data-theme'], 'x')
})

test('domFor adopts the link the boot script made: same href is left alone, a new version updates it, null removes it', () => {
  const doc = fakeDoc()
  const sha = 'a'.repeat(64)
  const boot = doc.createElement('link')
  boot.id = 'conn-theme-css'
  boot.rel = 'stylesheet'
  boot.href = `/api/themes/matrix/theme.css?v=${sha}`
  doc.head.appendChild(boot)
  const writes = doc.hrefWrites(boot)
  const dom = domFor(doc)
  dom.link('conn-theme-css', `/api/themes/matrix/theme.css?v=${sha}`)
  assert.deepEqual(doc.order(), ['conn-theme-css'], 'no second link')
  assert.equal(doc.hrefWrites(boot), writes, 'the same href is not written again (a reload of the sheet)')
  dom.link('conn-theme-css', `/api/themes/matrix/theme.css?v=${'c'.repeat(64)}`)
  assert.deepEqual(doc.order(), ['conn-theme-css'])
  assert.equal(doc.el('conn-theme-css')?.href, `/api/themes/matrix/theme.css?v=${'c'.repeat(64)}`)
  dom.link('conn-theme-css', null)
  assert.deepEqual(doc.order(), [])
})

// In dev the app's own CSS is added to the head by script, after the boot script ran, so the boot link is no longer the last
// stylesheet and Neon's rules would lose their ties with it. The loader puts an equal link last and drops the early one once it loads.
function bootedBehind(sha = 'a'.repeat(64)) {
  const doc = fakeDoc()
  const href = `/api/themes/matrix/theme.css?v=${sha}`
  const boot = doc.createElement('link')
  boot.id = 'conn-theme-css'
  boot.href = href
  doc.head.appendChild(boot)
  const later = doc.createElement('style')
  later.id = 'app-css'
  doc.head.appendChild(later)
  return { doc, boot, href }
}

test('domFor: a boot link with the app\'s CSS after it is replaced by an equal link at the end, once that has loaded', () => {
  const { doc, boot, href } = bootedBehind()
  const dom = domFor(doc)
  dom.link('conn-theme-css', href)
  assert.equal(doc.order().filter((i) => i === 'conn-theme-css').length, 1, 'the early link still serves until the new one has loaded')
  assert.equal(doc.el('conn-theme-css'), boot)
  const next = doc.head.lastElementChild as El
  assert.notEqual(next, boot)
  assert.equal(next.href, href)
  assert.equal(next.rel, 'stylesheet')
  next.onload?.()
  assert.deepEqual(doc.order(), ['app-css', 'conn-theme-css'])
  assert.equal(doc.el('conn-theme-css'), next)
  // later calls with the same href leave it alone; a new version updates it
  const writes = doc.hrefWrites(next)
  dom.link('conn-theme-css', href)
  assert.equal(doc.hrefWrites(next), writes)
  dom.link('conn-theme-css', href + 'x')
  assert.equal(next.href, href + 'x')
})

test('domFor: the swap is dropped if the href changed or the link was removed before the new one loaded', () => {
  const a = bootedBehind()
  const domA = domFor(a.doc)
  domA.link('conn-theme-css', a.href)
  const nextA = a.doc.head.lastElementChild as El
  domA.link('conn-theme-css', a.href + 'v2')
  nextA.onload?.()
  assert.deepEqual(a.doc.order(), ['conn-theme-css', 'app-css'], 'the early link stays and holds the new href')
  assert.equal(a.doc.el('conn-theme-css')?.href, a.href + 'v2')
  const b = bootedBehind()
  const domB = domFor(b.doc)
  domB.link('conn-theme-css', b.href)
  const nextB = b.doc.head.lastElementChild as El
  domB.link('conn-theme-css', null)
  nextB.onload?.()
  assert.deepEqual(b.doc.order(), ['app-css'], 'a removed link is not brought back')
})

test('domFor: a boot link that is already last (the built page) is not swapped, and the fonts link never is', () => {
  const { doc, boot, href } = bootedBehind()
  doc.el('app-css')?.remove()
  const fonts = doc.createElement('link')
  fonts.id = 'conn-theme-fonts'
  fonts.href = 'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&display=swap'
  doc.head.insertBefore(fonts, boot)
  const later = doc.createElement('style')
  later.id = 'later'
  doc.head.appendChild(later)
  const dom = domFor(doc)
  dom.link('conn-theme-fonts', fonts.href as string)
  assert.deepEqual(doc.order(), ['conn-theme-fonts', 'conn-theme-css', 'later'])
  doc.el('later')?.remove()
  dom.link('conn-theme-css', href)
  assert.deepEqual(doc.order(), ['conn-theme-fonts', 'conn-theme-css'])
})

test('domFor: a link the loader made itself is never swapped, whatever is added after it', () => {
  const doc = fakeDoc()
  const dom = domFor(doc)
  dom.link('conn-theme-css', '/x.css')
  const later = doc.createElement('style')
  later.id = 'later'
  doc.head.appendChild(later)
  dom.link('conn-theme-css', '/x.css')
  assert.deepEqual(doc.order(), ['conn-theme-css', 'later'])
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

test('(d) the cache entry is the generated token stylesheet and nothing else', () => {
  const r = resolve([entry(make(), 'builtin', 'abc')], choice())
  assert.ok(r.cssUrl)
  const c = cacheEntryOf(r)
  assert.equal(c.css, r.css)
  assert.deepEqual(c.attrs, r.attrs)
  assert.equal(c.only, 'dark')
  assert.deepEqual(Object.keys(c).sort(), ['attrs', 'css', 'cssUrl', 'fontsUrl', 'only'])
  assert.ok(!c.css.includes('cssUrl') && !c.css.includes('/api/themes'))
})

test('the cache entry carries the links the app applied: the served stylesheet and the fonts href', () => {
  const r = resolve([entry(make(), 'builtin', 'a'.repeat(64))], choice())
  const c = cacheEntryOf(r)
  assert.equal(c.cssUrl, `/api/themes/matrix/theme.css?v=${'a'.repeat(64)}`)
  assert.equal(c.fontsUrl, 'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&display=swap')
  const none = cacheEntryOf(resolve([entry(make({ fonts: [] }))], choice()))
  assert.deepEqual([none.cssUrl, none.fontsUrl], [null, null])
})

test('a stylesheet link is cached only for CSS the app served: a user theme asks first, a gallery theme never has one', () => {
  const sha = 'b'.repeat(64)
  const user = entry(make(), 'user', sha)
  assert.equal(cacheEntryOf(resolve([user], choice())).cssUrl, null, 'not accepted yet')
  assert.equal(cacheEntryOf(resolve([user], choice(), { matrix: 'c'.repeat(64) })).cssUrl, null, 'accepted for other CSS')
  assert.equal(cacheEntryOf(resolve([user], choice(), { matrix: sha })).cssUrl, `/api/themes/matrix/theme.css?v=${sha}`)
  // a gallery install is a user folder with .source.json: the server lists no CSS for it, and even a hand-forged entry gets no link
  assert.equal(cacheEntryOf(resolve([entry(make(), 'gallery')], choice())).cssUrl, null)
  assert.equal(cacheEntryOf(resolve([entry(make(), 'gallery', sha)], choice(), { matrix: sha })).cssUrl, null)
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
