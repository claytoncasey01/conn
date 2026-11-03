import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fontsHref } from './fonts.ts'
import { FONTS_HREF } from './storage.ts'
import { ALLOWED_FONTS } from './tokens.ts'

test('only allow-listed names appear and an unknown one is dropped', () => {
  const href = fontsHref(['IBM Plex Sans', 'Comic Sans MS', 'IBM Plex Mono'])
  assert.ok(href)
  assert.ok(href.startsWith('https://fonts.googleapis.com/css2?'))
  assert.ok(href.includes('family=IBM+Plex+Sans'))
  assert.ok(href.includes('family=IBM+Plex+Mono'))
  assert.ok(!href.includes('Comic'))
})

test('an empty list, or only unknown names, gives null', () => {
  assert.equal(fontsHref([]), null)
  assert.equal(fontsHref(['Papyrus', '</style><script>']), null)
})

test('the URL carries display=swap and each family once', () => {
  const href = fontsHref(['Chakra Petch', 'Chakra Petch', 'JetBrains Mono']) ?? ''
  assert.ok(href.endsWith('&display=swap'))
  assert.equal(href.match(/family=Chakra\+Petch/g)?.length, 1)
})

test('a font with one weight asks for that weight only', () => {
  assert.ok((fontsHref(['Share Tech Mono']) ?? '').includes('family=Share+Tech+Mono:wght@400&'))
})

test('every allow-listed font has a URL', () => {
  for (const f of ALLOWED_FONTS) assert.ok(fontsHref([f]), f)
})

test('the boot cache accepts whatever fontsHref builds: every subset of the allow-list, in any order', () => {
  const all = [...ALLOWED_FONTS]
  for (let mask = 1; mask < 1 << all.length; mask++) {
    const names = all.filter((_, i) => mask & (1 << i))
    for (const n of [names, [...names].reverse()]) assert.match(fontsHref(n) ?? '', FONTS_HREF, n.join())
  }
})
