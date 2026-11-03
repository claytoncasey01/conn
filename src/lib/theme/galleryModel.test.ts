import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { IndexEntry } from './galleryTypes.ts'
import { cardsModel } from './galleryModel.ts'
import { previewDoc } from './preview.ts'
import type { ThemeEntry, ThemeManifest } from './types.ts'

const manifest = (id: string, over: Partial<ThemeManifest> = {}): ThemeManifest => ({
  id, name: id.toUpperCase(), version: '1.0.0', author: 'someone', license: 'MIT', description: '',
  modes: ['dark'], accent: 'fixed', fonts: [], tokens: { dark: { ac: '#00ff41' } }, effects: [], ...over,
})
const indexed = (id: string, over: Partial<ThemeManifest> = {}): IndexEntry => {
  const m = manifest(id, over)
  return { id, name: m.name, author: m.author, version: m.version, description: '', commit: 'a'.repeat(40), sha256: 'b'.repeat(64), manifest: m }
}
const entry = (id: string, source: ThemeEntry['source'], over: Partial<ThemeManifest> = {}): ThemeEntry => ({
  id, source, hasCss: false, cssSha256: null, manifest: manifest(id, over),
})

const builtin = entry('default', 'builtin', { name: 'Default', modes: ['light', 'dark'], accent: 'free', tokens: {} })

test('an index theme that is not installed is Install, from the community', () => {
  const [c] = cardsModel([indexed('aurora')], [builtin], 'community', '')
  assert.deepEqual({ id: c.id, source: c.source, action: c.action, accent: c.accent }, { id: 'aurora', source: 'community', action: 'Install', accent: '#00ff41' })
  assert.equal(c.entry?.id, 'aurora')
})

test('installed at the same version is Installed, at an older one Update, at a newer one Installed', () => {
  const act = (installed: string, index: string) =>
    cardsModel([indexed('aurora', { version: index })], [entry('aurora', 'gallery', { version: installed })], 'all', '').find((c) => c.id === 'aurora')?.action
  assert.equal(act('1.0.0', '1.0.0'), 'Installed')
  assert.equal(act('1.0.0', '1.1.0'), 'Update')
  assert.equal(act('1.9', '1.10'), 'Update')
  assert.equal(act('2', '1.5'), 'Installed')
  assert.equal(act('beta', 'gamma'), 'Update')
})

test('a version compares by dotted numbers only; anything else has no order and never reads as a downgrade offer', () => {
  const act = (installed: string, index: string) =>
    cardsModel([indexed('aurora', { version: index })], [entry('aurora', 'gallery', { version: installed })], 'all', '').find((c) => c.id === 'aurora')?.action
  assert.equal(act('1.9', '1.10'), 'Update')
  assert.equal(act('1.10', '1.9'), 'Installed')
  assert.equal(act('1.0', '1.0.0'), 'Installed')
  assert.equal(act('1.0.0', '1.0'), 'Installed')
  assert.equal(act('1.0', '1.0.1'), 'Update')
  // a prerelease, a v prefix, an exponent and a hex number are not numbers: a number against one is left alone
  assert.equal(act('1.0.0', '1.0.0-beta'), 'Installed')
  assert.equal(act('1.0.0-beta', '1.0.0'), 'Installed')
  assert.equal(act('1.0', 'v2'), 'Installed')
  assert.equal(act('1.0', '1e3'), 'Installed')
  assert.equal(act('1', '0x10'), 'Installed')
  assert.equal(act('1..0', '1.0'), 'Installed')
  assert.equal(act('v1', 'v1'), 'Installed')
  assert.equal(act('v1', 'v2'), 'Update')
})

test('a built-in or a user theme is Use', () => {
  const cards = cardsModel([], [builtin, entry('mine', 'user')], 'all', '')
  assert.deepEqual(cards.map((c) => [c.id, c.source, c.action]), [['default', 'builtin', 'Use'], ['mine', 'user', 'Use']])
})

test('an index entry never duplicates a built-in or a user theme of its id', () => {
  const cards = cardsModel([indexed('mine'), indexed('default')], [builtin, entry('mine', 'user')], 'all', '')
  assert.deepEqual(cards.map((c) => c.id), ['default', 'mine'])
})

test('the installed filter keeps what is installed, community keeps the index and installed gallery themes', () => {
  const index = [indexed('aurora'), indexed('dusk')]
  const installed = [builtin, entry('mine', 'user'), entry('dusk', 'gallery')]
  assert.deepEqual(cardsModel(index, installed, 'installed', '').map((c) => c.id), ['default', 'mine', 'dusk'])
  assert.deepEqual(cardsModel(index, installed, 'community', '').map((c) => c.id), ['dusk', 'aurora'])
  assert.deepEqual(cardsModel(index, installed, 'all', '').map((c) => c.id), ['default', 'mine', 'dusk', 'aurora'])
})

test('an installed gallery theme missing from the index still appears, with no entry', () => {
  const [c] = cardsModel([], [entry('gone', 'gallery')], 'community', '')
  assert.equal(c.id, 'gone')
  assert.equal(c.entry, null)
  assert.equal(c.action, 'Installed')
  assert.equal(c.source, 'gallery')
})

test('the query matches name and author, case blind', () => {
  const index = [indexed('aurora', { name: 'Northern Lights', author: 'Ada' }), indexed('dusk', { name: 'Dusk', author: 'Grace' })]
  assert.deepEqual(cardsModel(index, [], 'all', 'NORTHERN').map((c) => c.id), ['aurora'])
  assert.deepEqual(cardsModel(index, [], 'all', ' grace ').map((c) => c.id), ['dusk'])
  assert.deepEqual(cardsModel(index, [], 'all', 'zzz'), [])
})

test('an invalid user theme has no card but still holds its id against an index entry', () => {
  const bad: ThemeEntry = { id: 'broken', source: 'user', hasCss: false, cssSha256: null, invalid: 'nope' }
  assert.deepEqual(cardsModel([], [bad], 'all', ''), [])
  assert.deepEqual(cardsModel([indexed('broken')], [bad], 'all', ''), [])
})

test('an invalid gallery install keeps a card so it can be removed, and is an Update when the index has it', () => {
  const bad: ThemeEntry = { id: 'broken', source: 'gallery', hasCss: false, cssSha256: null, invalid: 'theme.json is not valid JSON' }
  const [gone] = cardsModel([], [bad], 'installed', '')
  assert.deepEqual({ id: gone.id, name: gone.name, source: gone.source, action: gone.action, entry: gone.entry, invalid: gone.invalid }, {
    id: 'broken', name: 'broken', source: 'gallery', action: 'Installed', entry: null, invalid: 'theme.json is not valid JSON',
  })
  const listed = cardsModel([indexed('broken')], [bad], 'all', '')
  assert.equal(listed.length, 1)
  assert.equal(listed[0].action, 'Update')
  assert.equal(listed[0].entry?.id, 'broken')
  assert.equal(cardsModel([], [bad], 'community', '').length, 1)
})

test('the accent is what the preview paints: the first variant over the mode ac, dark first, and a free accent own ac', () => {
  const two = indexed('duo', { modes: ['light', 'dark'], tokens: { light: { ac: '#111111' }, dark: { ac: '#222222' } } })
  assert.equal(cardsModel([two], [], 'all', '')[0].accent, '#222222')
  const lightOnly = indexed('day', { modes: ['light'], tokens: { light: { ac: '#111111' } } })
  assert.equal(cardsModel([lightOnly], [], 'all', '')[0].accent, '#111111')
  const free = indexed('free', { accent: 'free', tokens: { dark: { ac: '#333333' } } })
  assert.equal(cardsModel([free], [], 'all', '')[0].accent, '#333333')
  assert.ok(previewDoc(free.manifest, 'dark', free.manifest).includes('--ac:#333333;'))
  const variants = indexed('multi', { accent: { variants: [{ name: 'One', tokens: { ac: '#112233' } }, { name: 'Two', tokens: { ac: '#445566' } }] }, tokens: { dark: { ac: '#999999' } } })
  const [c] = cardsModel([variants], [], 'all', '')
  assert.equal(c.accent, '#112233')
  // the same manifest the card reads is painted with that ac
  assert.ok(previewDoc(variants.manifest, 'dark', variants.manifest).includes(`--ac:${c.accent};`))
  const bare = indexed('bare', { tokens: { dark: {} } })
  assert.equal(cardsModel([bare], [], 'all', '')[0].accent, '')
})

test('a repeated index id is one card, the first', () => {
  const first = indexed('aurora', { name: 'First' })
  const cards = cardsModel([first, indexed('aurora', { name: 'Second' })], [], 'all', '')
  assert.deepEqual(cards.map((c) => c.name), ['First'])
  const inst = cardsModel([first, indexed('aurora', { name: 'Second', version: '9' })], [entry('aurora', 'gallery')], 'all', '')
  assert.equal(inst.length, 1)
  assert.equal(inst[0].action, 'Installed')
})

test('index-only themes come after the installed ones, sorted by name', () => {
  const index = [indexed('z1', { name: 'Zed' }), indexed('a1', { name: 'alpha' }), indexed('m1', { name: 'Mid' })]
  assert.deepEqual(cardsModel(index, [builtin], 'all', '').map((c) => c.id), ['default', 'a1', 'm1', 'z1'])
})

// A hostile list (the browser trusts nothing it is handed): none of these shapes may throw, and the installed themes keep their cards.
const withBad = (bad: unknown[]) => cardsModel([...(bad as IndexEntry[]), indexed('aurora')], [builtin, entry('frost', 'gallery')], 'all', '')

test('a null or non-object index entry is skipped, and installed themes still have cards', () => {
  const cards = withBad([null, undefined, 'x', 7, []])
  assert.deepEqual(cards.map((c) => `${c.source}:${c.id}`), ['builtin:default', 'gallery:frost', 'community:aurora'])
})

test('an index entry with no manifest, or one that is not an object or lacks what a card reads, is skipped', () => {
  const ok = indexed('ok')
  const cards = withBad([
    { ...ok, id: 'nomanifest', manifest: undefined },
    { ...ok, id: 'strmanifest', manifest: 'x' },
    { ...ok, id: 'notokens', manifest: { ...ok.manifest, tokens: undefined } },
    { ...ok, id: 'nomodes', manifest: { ...ok.manifest, modes: undefined } },
    { ...ok, id: 'numname', manifest: { ...ok.manifest, name: 3 } },
  ])
  assert.deepEqual(cards.map((c) => `${c.source}:${c.id}`), ['builtin:default', 'gallery:frost', 'community:aurora'])
  // the search reads names, so a hostile one must not reach it
  assert.doesNotThrow(() => cardsModel([{ ...ok, id: 'numname', manifest: { ...ok.manifest, name: 3 } } as unknown as IndexEntry], [builtin], 'all', 'x'))
})

test('two index entries with different ids but one manifest id are two cards keyed by the entry id, never a repeated key', () => {
  const m = indexed('shared').manifest
  const a = { ...indexed('a'), manifest: m }
  const b = { ...indexed('b'), manifest: m }
  const cards = cardsModel([a, b], [builtin], 'all', '')
  assert.deepEqual(cards.map((c) => `${c.source}:${c.id}`), ['builtin:default', 'community:a', 'community:b'])
  assert.equal(new Set(cards.map((c) => `${c.source}:${c.id}`)).size, cards.length)
  assert.equal(cards[1].entry?.id, 'a')
})

test('the final list never repeats a source and id', () => {
  const cards = cardsModel([], [entry('frost', 'gallery'), entry('frost', 'gallery'), builtin], 'all', '')
  assert.deepEqual(cards.map((c) => `${c.source}:${c.id}`), ['gallery:frost', 'builtin:default'])
})

test('an index entry with a numeric, missing or null commit (or a non-string version) is skipped; a null files list changes nothing', () => {
  const ok = indexed('ok')
  const cards = withBad([
    { ...ok, id: 'numcommit', commit: 12 },
    { ...ok, id: 'nocommit', commit: undefined },
    { ...ok, id: 'nullcommit', commit: null },
    { ...ok, id: 'numver', version: 2 },
    { ...ok, id: 'nullfiles', files: null, manifest: { ...ok.manifest, id: 'nullfiles' } },
  ])
  assert.deepEqual(cards.map((c) => `${c.source}:${c.id}`), ['builtin:default', 'gallery:frost', 'community:aurora', 'community:nullfiles'])
  // what the page reads off a community card's entry is a string, so it cannot throw
  for (const c of cards.filter((x) => x.entry)) assert.equal(typeof c.entry?.commit.slice(0, 7), 'string')
})
