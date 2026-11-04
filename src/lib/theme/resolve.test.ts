import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { accentTokens } from './accent.ts'
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
const run = (list: ThemeEntry[], c: Choice, systemDark = false, accepted: Record<string, string> = {}) =>
  resolveTheme({ list, choice: c, systemDark, accepted, fallback: def })

test('system follows the OS for a two-mode theme; an explicit mode wins', () => {
  const list = [entry(def)]
  assert.equal(run(list, choice({ id: 'default' }), true).mode, 'dark')
  assert.equal(run(list, choice({ id: 'default' }), false).mode, 'light')
  assert.equal(run(list, choice({ id: 'default', mode: 'light' }), true).mode, 'light')
  assert.equal(run(list, choice({ id: 'default' }), true).only, null)
})

test('a one-mode theme ignores the setting and takes its mode from the theme', () => {
  const light = make({ id: 'paper', modes: ['light'], tokens: { light: { bg: '#fff' } } })
  const r = run([entry(light)], choice({ id: 'paper', mode: 'dark' }), true)
  assert.equal(r.mode, 'light')
  assert.equal(r.only, 'light')
  assert.equal(r.fellBack, false)
})

test("a one-mode theme's gaps come from Default's same mode", () => {
  const r = run([entry(make())], choice(), false)
  assert.equal(r.mode, 'dark')
  assert.ok(r.css.includes('--bg:#010a04;'))
  assert.ok(r.css.includes(`--tx:${def.tokens.dark?.tx};`))
  assert.ok(!r.css.includes('data-mode="light"'))
})

test('an id missing from the list resolves to Default with fellBack', () => {
  const r = run([entry(make())], choice({ id: 'gone' }), true)
  assert.equal(r.entry.id, 'default')
  assert.equal(r.fellBack, true)
  assert.equal(r.mode, 'dark')
  assert.ok(r.css.includes('[data-theme="default"]'))
})

test('an invalid entry resolves to Default with fellBack', () => {
  const r = run([bad('matrix', 'user')], choice(), false)
  assert.equal(r.entry.id, 'default')
  assert.equal(r.fellBack, true)
})

test('an id that is not a theme id (the server lists _limit) is unselectable', () => {
  const r = run([bad('_limit', 'user'), entry(def)], choice({ id: '_limit' }), false)
  assert.equal(r.entry.id, 'default')
  assert.equal(r.fellBack, true)
  const odd = run([{ ...entry(make({ id: 'Bad_Id' })) }], choice({ id: 'Bad_Id' }), false)
  assert.equal(odd.fellBack, true)
})

test('a rejected user or gallery folder named like a built-in never shadows the valid built-in, in either order', () => {
  for (const source of ['user', 'gallery'] as const) {
    for (const list of [[entry(def), bad('default', source)], [bad('default', source), entry(def)]]) {
      const r = run(list, choice({ id: 'default' }), false)
      assert.equal(r.entry.source, 'builtin')
      assert.equal(r.fellBack, false)
      assert.equal(r.entry.manifest, def)
    }
  }
})

test('with a valid user entry and a built-in of one id, the built-in wins', () => {
  const user = entry(make({ id: 'default', name: 'Mine' }), 'user')
  const r = run([user, entry(def)], choice({ id: 'default' }), false)
  assert.equal(r.entry.source, 'builtin')
})

test('cssUrl: built-in with CSS, accepted user CSS; otherwise null', () => {
  const m = make()
  assert.equal(run([entry(m, 'builtin', 'abc')], choice()).cssUrl, '/api/themes/matrix/theme.css?v=abc')
  assert.equal(run([entry(m, 'user', 'abc')], choice(), false, { matrix: 'abc' }).cssUrl, '/api/themes/matrix/theme.css?v=abc')
  assert.equal(run([entry(m, 'user', 'abc')], choice(), false, {}).cssUrl, null)
  assert.equal(run([entry(m, 'user', 'abc')], choice(), false, { matrix: 'old' }).cssUrl, null)
  assert.equal(run([entry(m, 'user')], choice(), false, { matrix: 'abc' }).cssUrl, null)
  assert.equal(run([entry(m, 'gallery', 'abc')], choice(), false, { matrix: 'abc' }).cssUrl, null)
  assert.equal(run([entry(m)], choice()).cssUrl, null)
})

test('attrs come from the motion, led and blur tokens; glass is on only when blur is above 0', () => {
  assert.deepEqual(run([entry(make())], choice()).attrs, { motion: 'power', led: 'square', glass: 'on' })
  const calm = make({ tokens: { dark: { bg: '#000', blur: '0px' } } })
  assert.deepEqual(run([entry(calm)], choice()).attrs, { motion: 'calm', led: 'circle', glass: 'off' })
})

test('fonts come from the manifest', () => {
  assert.deepEqual(run([entry(make())], choice()).fonts, ['Share Tech Mono'])
})

test('a free-accent theme gets the picked accent in its CSS; no pick leaves the theme as written', () => {
  const pick = { name: 'Rose', hue: 355, chroma: 0.16 }
  const picked = run([entry(def)], choice({ id: 'default', mode: 'dark', accent: pick }))
  assert.ok(picked.css.includes(`--ac:${accentTokens('dark', 355, 0.16).ac};`))
  const plain = run([entry(def)], choice({ id: 'default', mode: 'dark' }))
  assert.ok(plain.css.includes(`--ac:${def.tokens.dark?.ac};`))
})

test('the default id matches the one the boot script paints', () => {
  assert.equal(run([], choice({ id: DEFAULT_ID })).entry.id, 'default')
})

test('tokens are the merged set for the resolved mode, accent pick and variant included', () => {
  const m = make({ accent: 'free', modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#010a04', ac: '#00ff41' } } })
  const r = run([entry(m)], choice({ mode: 'dark', accent: { name: 'Blue', hue: 255, chroma: 0.15 } }))
  assert.equal(r.tokens.bg, '#010a04')
  assert.equal(r.tokens.ac, accentTokens('dark', 255, 0.15).ac)
  assert.equal(r.tokens.radius, def.tokens.dark?.radius)
  assert.equal(run([entry(m)], choice({ mode: 'light' })).tokens.bg, '#fff')
})

test('an unset or unknown variant resolves to variants[0]; a theme with no variants is untouched', () => {
  const crt = make({
    id: 'crt', accent: { variants: [{ name: 'Green', tokens: { ac: '#8bffad', motion: 'calm' } }, { name: 'Amber', tokens: { ac: '#ffb000' } }] },
    tokens: { dark: { bg: '#030604', ac: '#111111', motion: 'power' } },
  })
  const ac = (v: string | null) => run([entry(crt)], choice({ id: 'crt', mode: 'dark', variant: v }))
  for (const v of [null, 'Gone']) {
    const r = ac(v)
    assert.equal(r.tokens.ac, '#8bffad')
    assert.ok(r.css.includes('--ac:#8bffad;') && !r.css.includes('#111111'), r.css)
    assert.equal(r.attrs.motion, 'calm', 'attrs follow the variant too')
  }
  assert.equal(ac('Amber').tokens.ac, '#ffb000')
  const plain = run([entry(make())], choice({ variant: 'Amber' }))
  assert.equal(plain.tokens.ac, '#00ff41')
  assert.equal(plain.css, run([entry(make())], choice()).css)
})
