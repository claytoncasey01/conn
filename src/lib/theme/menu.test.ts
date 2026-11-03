import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canAccept, menuModel, needsReview, pinNote, shownVariant } from './menu.ts'
import type { Mode, ThemeEntry, ThemeManifest, ThemeSource } from './types.ts'

const manifest = (id: string, over: Partial<ThemeManifest> = {}): ThemeManifest => ({
  id, name: id.toUpperCase(), version: '1.0.0', author: 'a', license: 'MIT', description: 'd',
  modes: ['light', 'dark'], accent: 'free', fonts: [], tokens: {}, effects: [], ...over,
})
const ok = (id: string, source: ThemeSource = 'builtin', over: Partial<ThemeManifest> = {}, hasCss = false): ThemeEntry => ({
  id, source, hasCss, cssSha256: hasCss ? 'abc' : null, manifest: manifest(id, over),
})
const bad = (id: string, source: ThemeSource, why: string): ThemeEntry => ({ id, source, hasCss: false, cssSha256: null, invalid: why })

test('an invalid entry is listed disabled with its reason as the hint', () => {
  const m = menuModel([ok('default'), bad('broken', 'user', 'tokens.bg: not a color')], 'default')
  const row = m.themes.find((t) => t.id === 'broken')!
  assert.equal(row.disabled, true)
  assert.equal(row.hint, 'tokens.bg: not a color')
  assert.equal(row.label, 'broken')
  const good = m.themes.find((t) => t.id === 'default')!
  assert.equal(good.disabled, false)
  assert.equal(good.hint, null)
  assert.equal(good.label, 'DEFAULT')
})

test('customCss is true for a user theme with CSS and false for a built-in one with CSS', () => {
  const m = menuModel([ok('neon', 'builtin', {}, true), ok('mine', 'user', {}, true), ok('plain', 'user')], 'neon')
  const by = (id: string) => m.themes.find((t) => t.id === id)!
  assert.equal(by('mine').customCss, true)
  assert.equal(by('neon').customCss, false)
  assert.equal(by('plain').customCss, false)
})

test('modeEnabled is false for a one-mode theme and true for a two-mode one', () => {
  const one: Mode[] = ['dark']
  const list = [ok('default'), ok('matrix', 'builtin', { modes: one })]
  assert.equal(menuModel(list, 'matrix').modeEnabled, false)
  assert.equal(menuModel(list, 'default').modeEnabled, true)
})

test('accent is free, variants or none from the active manifest, and variants lists the names', () => {
  const v = { variants: [{ name: 'Green', tokens: {} }, { name: 'Amber', tokens: {} }] }
  const list = [ok('default'), ok('crt', 'builtin', { accent: v }), ok('neon', 'builtin', { accent: 'fixed' })]
  const free = menuModel(list, 'default')
  assert.equal(free.accent, 'free')
  assert.deepEqual(free.variants, [])
  const withV = menuModel(list, 'crt')
  assert.equal(withV.accent, 'variants')
  assert.deepEqual(withV.variants, ['Green', 'Amber'])
  const fixed = menuModel(list, 'neon')
  assert.equal(fixed.accent, 'none')
  assert.deepEqual(fixed.variants, [])
})

test('a variant name listed twice (an old server, a list read before validation refused it) is shown once, first one kept', () => {
  const v = { variants: [{ name: 'Green', tokens: {} }, { name: 'Amber', tokens: {} }, { name: 'Green', tokens: {} }] }
  const m = menuModel([ok('default'), ok('crt', 'builtin', { accent: v })], 'crt')
  assert.equal(m.accent, 'variants')
  assert.deepEqual(m.variants, ['Green', 'Amber'])
})

test('row keys are unique even when the server lists one source and id twice (two long folder names clipped alike)', () => {
  const a = bad(`${'a'.repeat(40)}...`, 'user', 'folder name must be a-z, 0-9 and -')
  const b = bad(`${'a'.repeat(40)}...`, 'user', 'folder name must be a-z, 0-9 and -')
  const m = menuModel([ok('default'), a, b, bad('_limit', 'user', 'limit')], 'default')
  const keys = m.themes.map((t) => t.key)
  assert.equal(new Set(keys).size, keys.length)
  // the first of a kind keeps the plain source:id key
  assert.deepEqual(keys.slice(0, 2), ['builtin:default', `user:${'a'.repeat(40)}...`])
})

test('an unlisted or empty-list active id leaves the controls plain: both modes, no accent', () => {
  for (const m of [menuModel([ok('default')], 'gone'), menuModel([], 'default')]) {
    assert.equal(m.modeEnabled, true)
    assert.equal(m.accent, 'none')
    assert.deepEqual(m.variants, [])
  }
})

for (const order of ['builtin first', 'user first'] as const) {
  test(`a rejected user theme named like a built-in is its own disabled row and the built-in stays selectable and active (${order})`, () => {
    const b = ok('default')
    const u = bad('default', 'user', 'duplicates a built-in id')
    const m = menuModel(order === 'builtin first' ? [b, u] : [u, b], 'default')
    assert.equal(m.themes.length, 2)
    const rows = m.themes.filter((t) => t.id === 'default')
    assert.deepEqual(rows.map((t) => t.disabled).sort(), [false, true])
    const sel = rows.find((t) => !t.disabled)!
    const dis = rows.find((t) => t.disabled)!
    assert.equal(sel.source, 'builtin')
    assert.equal(dis.source, 'user')
    assert.equal(dis.hint, 'duplicates a built-in id')
    assert.equal(sel.active, true)
    assert.equal(dis.active, false)
    // the controls follow the built-in's manifest, not the rejected entry
    assert.equal(m.accent, 'free')
    // (source, id) is unique, so a keyed list never sees a duplicate key
    assert.equal(new Set(m.themes.map((t) => t.key)).size, m.themes.length)
    assert.deepEqual(m.themes.map((t) => t.key).sort(), ['builtin:default', 'user:default'])
  })
}

test('the server\'s _limit note is a disabled row with its reason and is never active', () => {
  const note = bad('_limit', 'user', 'limit of 100 user themes reached, 3 folders not listed')
  const m = menuModel([ok('default'), note], '_limit')
  const row = m.themes.find((t) => t.id === '_limit')!
  assert.equal(row.disabled, true)
  assert.match(row.hint!, /100/)
  assert.equal(row.active, false)
})

test('exactly one row is active: the one the resolver would pick; none when the id is not listed', () => {
  const list = [ok('default'), ok('neon')]
  assert.deepEqual(menuModel(list, 'neon').themes.filter((t) => t.active).map((t) => t.id), ['neon'])
  assert.equal(menuModel(list, 'gone').themes.filter((t) => t.active).length, 0)
})

const css = (id: string, sha: string | null, source: ThemeSource = 'user'): ThemeEntry => ({ id, source, hasCss: sha !== null, cssSha256: sha, manifest: manifest(id) })
const asked = (sha: string | null) => ({ id: 'mine', label: 'MINE', sha })

test('a row carries the CSS hash it was listed with', () => {
  assert.equal(menuModel([ok('default'), css('mine', 'aaa')], 'default').themes.find((t) => t.id === 'mine')!.cssSha256, 'aaa')
  assert.equal(menuModel([ok('default')], 'default').themes[0].cssSha256, null)
})

test('canAccept: the CSS that was on screen is accepted', () => {
  assert.equal(canAccept(asked('aaa'), [ok('default'), css('mine', 'aaa')]), 'ok')
})

test('canAccept: CSS edited while the prompt was open is changed, not accepted', () => {
  assert.equal(canAccept(asked('aaa'), [ok('default'), css('mine', 'bbb')]), 'changed')
})

test('canAccept: CSS removed from the theme is changed', () => {
  assert.equal(canAccept(asked('aaa'), [ok('default'), css('mine', null)]), 'changed')
})

test('canAccept: a theme that left the list is gone, and so is one now rejected as invalid', () => {
  assert.equal(canAccept(asked('aaa'), [ok('default')]), 'gone')
  assert.equal(canAccept(asked('aaa'), []), 'gone')
  assert.equal(canAccept(asked('aaa'), [ok('default'), bad('mine', 'user', 'tokens.bg: not a color')]), 'gone')
})

test('canAccept: a built-in that took the id is another theme (changed), whatever the old hash was', () => {
  assert.equal(canAccept(asked('aaa'), [css('mine', 'aaa', 'builtin')]), 'changed')
  assert.equal(canAccept(asked('aaa'), [css('mine', 'aaa', 'user'), css('mine', 'aaa', 'builtin')]), 'changed')
})

test('canAccept: a prompt opened with no hash never accepts', () => {
  assert.equal(canAccept(asked(null), [css('mine', null)]), 'changed')
})

test('needsReview: the active user theme whose CSS is not the accepted hash is the row to review', () => {
  const list = [ok('default'), css('mine', 'bbb')]
  const row = needsReview(menuModel(list, 'mine'), list, { mine: 'aaa' })
  assert.equal(row?.key, 'user:mine')
  assert.equal(row?.cssSha256, 'bbb')
  // never accepted at all (storage cleared) asks the same way
  assert.equal(needsReview(menuModel(list, 'mine'), list, {})?.key, 'user:mine')
})

test('needsReview: nothing to review when the hash is accepted, the theme is not active, or its CSS is not a user\'s', () => {
  const list = [ok('default'), css('mine', 'bbb'), ok('neon', 'builtin', {}, true)]
  assert.equal(needsReview(menuModel(list, 'mine'), list, { mine: 'bbb' }), null)
  assert.equal(needsReview(menuModel(list, 'default'), list, {}), null)
  assert.equal(needsReview(menuModel(list, 'neon'), list, {}), null)
  assert.equal(needsReview(menuModel(list, 'gone'), list, {}), null)
  // a user theme without CSS never asks
  const plain = [ok('default'), css('mine', null)]
  assert.equal(needsReview(menuModel(plain, 'mine'), plain, {}), null)
})

test('the shown variant is the stored one when listed, else the first', () => {
  const vs = ['Green', 'Amber', 'White']
  assert.equal(shownVariant(vs, 'Amber'), 'Amber')
  assert.equal(shownVariant(vs, null), 'Green')
  assert.equal(shownVariant(vs, 'Gone'), 'Green')
  assert.equal(shownVariant([], 'Amber'), null)
})

test('pinNote: says what the link shows and what a change does, or nothing when the link adds nothing', () => {
  const saved = { id: 'default', theme: 'Default', mode: 'light' as const }
  const note = (pin: Parameters<typeof pinNote>[0], theme: string, mode: 'light' | 'dark') => pinNote(pin, { theme, mode }, saved)
  assert.equal(note(null, 'Default', 'light'), null)
  assert.equal(
    note({ id: 'matrix' }, 'Matrix', 'light'),
    'The link you opened shows Matrix. Changing a setting here goes back to your saved theme, Default.',
  )
  assert.equal(
    note({ mode: 'dark' }, 'Default', 'dark'),
    'The link you opened shows dark mode. Changing a setting here goes back to your saved mode, light.',
  )
  assert.equal(
    note({ id: 'matrix', mode: 'dark' }, 'Matrix', 'dark'),
    'The link you opened shows Matrix in dark mode. Changing a setting here goes back to your saved choice, Default in light mode.',
  )
  // a pin that equals what is saved changes nothing, so there is nothing to explain
  assert.equal(note({ id: 'default' }, 'Default', 'light'), null)
  assert.equal(note({ mode: 'light' }, 'Default', 'light'), null)
  // one key that differs is enough; the one that matches is not named
  assert.equal(
    note({ id: 'default', mode: 'dark' }, 'Default', 'dark'),
    'The link you opened shows dark mode. Changing a setting here goes back to your saved mode, light.',
  )
  assert.equal(
    note({ id: 'matrix', mode: 'light' }, 'Matrix', 'light'),
    'The link you opened shows Matrix. Changing a setting here goes back to your saved theme, Default.',
  )
})
