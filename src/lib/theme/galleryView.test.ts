import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { GalleryAnswer } from './galleryTypes.ts'
import type { IndexEntry } from './galleryTypes.ts'
import type { Card } from './galleryModel.ts'
import { accentNote, indexLine, isCurrent, manifestOf, modesNote, notice, previewMode, sourceNote } from './galleryView.ts'
import type { ThemeEntry, ThemeManifest } from './types.ts'

const manifest = (id: string, over: Partial<ThemeManifest> = {}): ThemeManifest => ({
  id, name: id, version: '1.0.0', author: 'someone', license: 'MIT', description: '',
  modes: ['dark'], accent: 'fixed', fonts: [], tokens: { dark: { ac: '#00ff41' } }, effects: [], ...over,
})
const entry = (id: string, source: ThemeEntry['source'], over: Partial<ThemeManifest> = {}): ThemeEntry => ({ id, source, hasCss: false, cssSha256: null, manifest: manifest(id, over) })
const indexed = (id: string, over: Partial<ThemeManifest> = {}): IndexEntry => ({ id, name: id, author: 'someone', version: '1.0.0', description: '', commit: 'a'.repeat(40), sha256: 'b'.repeat(64), manifest: manifest(id, over) })
const card = (over: Partial<Card>): Card => ({ id: 'x', name: 'X', author: 'a', version: '1.0.0', modes: ['dark'], accent: '', source: 'community', action: 'Install', entry: null, ...over })

test('a preview uses the installed manifest when there is one, else the index entry; an invalid card has none', () => {
  const inst = entry('frost', 'gallery', { version: '1.0.0' })
  const idx = indexed('frost', { version: '1.1.0' })
  assert.equal(manifestOf(card({ id: 'frost', source: 'gallery', action: 'Update', entry: idx }), [inst])?.version, '1.0.0')
  assert.equal(manifestOf(card({ id: 'frost', source: 'community', entry: idx }), [])?.version, '1.1.0')
  assert.equal(manifestOf(card({ id: 'default', source: 'builtin', action: 'Use' }), [entry('default', 'builtin')])?.id, 'default')
  assert.equal(manifestOf(card({ id: 'gone', source: 'gallery', modes: [], invalid: 'broken' }), [{ id: 'gone', source: 'gallery', hasCss: false, cssSha256: null, invalid: 'broken' }]), null)
  // a user folder and a gallery install can share an id in the list: the card's own source picks its manifest
  const both = [entry('dup', 'user', { version: '9.0.0' }), entry('dup', 'gallery', { version: '2.0.0' })]
  assert.equal(manifestOf(card({ id: 'dup', source: 'gallery' }), both)?.version, '2.0.0')
})

test('the preview shows the mode on screen when the theme has it, else a mode it has', () => {
  assert.equal(previewMode(manifest('a', { modes: ['light', 'dark'] }), 'light'), 'light')
  assert.equal(previewMode(manifest('a', { modes: ['dark'] }), 'light'), 'dark')
  assert.equal(previewMode(manifest('a', { modes: ['light'] }), 'dark'), 'light')
})

test('the card meta reads in plain words', () => {
  assert.equal(modesNote(['light', 'dark']), 'Light and dark')
  assert.equal(modesNote(['dark']), 'Dark only')
  assert.equal(modesNote(['light']), 'Light only')
  assert.equal(accentNote(manifest('a', { accent: 'free' })), 'Any accent')
  assert.equal(accentNote(manifest('a', { accent: 'fixed' })), 'Fixed accent')
  assert.equal(accentNote(manifest('a', { accent: { variants: [{ name: 'green', tokens: {} }, { name: 'amber', tokens: {} }] } })), 'Variants: green, amber')
  assert.equal(sourceNote('builtin'), 'Built in')
  assert.equal(sourceNote('community'), 'Community theme')
  assert.equal(sourceNote('gallery'), 'Installed from the index')
  assert.equal(sourceNote('user'), 'Your own theme')
})

test('the card on screen is the one with its id and its source', () => {
  const on = { id: 'dup', source: 'gallery' as const }
  assert.equal(isCurrent(card({ id: 'dup', source: 'gallery' }), on), true)
  assert.equal(isCurrent(card({ id: 'dup', source: 'user' }), on), false)
  assert.equal(isCurrent(card({ id: 'dup', source: 'community' }), on), false)
  assert.equal(isCurrent(card({ id: 'other', source: 'gallery' }), on), false)
})

const answer = (over: Partial<GalleryAnswer>): GalleryAnswer => ({ enabled: true, repo: 'me/conn-themes', themes: [], ...over })

test('the notice says why there is no index, in the brief\'s words, with the server\'s reason beneath', () => {
  assert.equal(notice(null), null)
  assert.equal(notice(answer({ checkedAt: 1 })), null)
  assert.deepEqual(notice(answer({ enabled: false, repo: null })), { kind: 'off', text: 'The gallery is turned off (CONN_THEME_INDEX=off).', reason: 'Installed themes still work. Installing and uninstalling are unavailable.' })
  assert.deepEqual(notice(answer({ error: 'GitHub is limiting requests (429); try the theme index again in a few minutes' })), {
    kind: 'error',
    text: 'The theme index cannot be reached. Installed themes still work.',
    reason: 'GitHub is limiting requests (429); try the theme index again in a few minutes',
  })
})

test('a repo with no index.json yet says so, naming it; offline and a server error keep the cannot-be-reached wording', () => {
  const missing = 'me/conn-themes has no index.json on its main branch (404 not found)'
  assert.deepEqual(notice(answer({ error: missing })), { kind: 'error', text: 'me/conn-themes has no theme index yet. Installed themes still work.', reason: missing })
  for (const error of ['Could not reach raw.githubusercontent.com to read the theme index', 'GitHub answered 500 for the theme index', 'The theme index timed out after 10 s']) {
    assert.equal(notice(answer({ error }))?.text, 'The theme index cannot be reached. Installed themes still work.')
  }
  // no repo to name: the general wording
  assert.equal(notice(answer({ error: missing, repo: null }))?.text, 'The theme index cannot be reached. Installed themes still work.')
})

test('the index line names the repo and when it was checked, and copes with an unknown repo and no check', () => {
  const now = 1_000_000
  assert.equal(indexLine(null, now), null)
  assert.equal(indexLine(answer({ enabled: false, repo: null }), now), null)
  assert.deepEqual(indexLine(answer({ checkedAt: now - 125_000 }), now), { repo: 'me/conn-themes', when: 'checked 2m 05s ago' })
  assert.deepEqual(indexLine(answer({ error: 'down' }), now), { repo: 'me/conn-themes', when: 'not read just now' })
  // a failed fetch in the browser has no repo: the line is left out rather than reading "from null"
  assert.equal(indexLine(answer({ repo: null, error: 'Failed to fetch' }), now), null)
})
