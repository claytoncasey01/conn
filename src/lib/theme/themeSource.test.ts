// The store and main.ts are runes and DOM code, so what matters about their wiring is held as text, the repo's way.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const read = (p: string) => readFileSync(new URL(p, import.meta.url), 'utf8')
const store = read('./theme.svelte.ts')
const main = read('../../main.ts')
const html = read('../../../index.html')

test('main.ts starts the theme before it mounts the app', () => {
  const init = main.indexOf('initTheme()')
  const mount = main.indexOf('mount(App')
  assert.ok(init > 0 && mount > 0 && init < mount)
  assert.ok(main.includes("from './lib/theme/theme.svelte.ts'"))
})

test('the list is read again when the window regains focus', () => {
  assert.match(store, /window\.addEventListener\('focus',.*refreshThemes\(\)/)
})

test('system mode reads and follows prefers-color-scheme live', () => {
  assert.ok(store.includes("matchMedia('(prefers-color-scheme: dark)')"))
  assert.match(store, /addEventListener\('change'/)
})

test('every applied theme is cached, and the cache write has no condition on Default', () => {
  assert.ok(store.includes('writeCache('))
  assert.ok(store.includes('cacheEntryOf('))
  assert.ok(!/if \([^)]*default[^)]*\)[^\n]*writeCache/.test(store))
})

test('the store never writes localStorage itself: the choice goes through writeChoice', () => {
  assert.ok(store.includes('writeChoice('))
  assert.ok(!store.includes('localStorage.setItem') && !store.includes('localStorage.removeItem'))
})

test('theme css reaches the page as text, never as markup', () => {
  const apply = read('./apply.ts')
  assert.ok(apply.includes('textContent'))
  assert.ok(!apply.includes('innerHTML') && !store.includes('innerHTML'))
})

test('index.html has no static fonts link or preconnect: the loader links the active theme\'s fonts, with display=swap', () => {
  assert.ok(!html.includes('fonts.googleapis.com') && !html.includes('preconnect'))
  assert.ok(read('./fonts.ts').includes('&display=swap'))
  assert.ok(read('./apply.ts').includes('dom.link(IDS.fonts, fontsHref(r.fonts))'))
})
