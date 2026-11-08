// Every request the page makes goes through api() (api.ts), which adds the secret: a request made any other way would be
// refused by the server, or, worse, would be a way round the rule. Pinned as text, as the other source tests are.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'

const root = new URL('../../', import.meta.url).pathname
const read = (rel: string) => readFileSync(root + rel, 'utf8')
const sources = [...new Glob('{src,public}/**/*.{ts,js,svelte}').scanSync(root)].filter((f) => !/\.test\.ts$/.test(f)).sort()

test('no module but api.ts sends a request: no fetch, EventSource, XMLHttpRequest, WebSocket or beacon', () => {
  assert.ok(sources.length > 50, 'the sources were found')
  for (const file of sources) {
    if (file === 'src/lib/api.ts') continue
    const text = read(file)
    assert.doesNotMatch(text, /(?<![.\w])fetch\(|globalThis\.fetch|window\.fetch|new\s+EventSource|XMLHttpRequest|new\s+WebSocket|sendBeacon/, file)
  }
})

test('api.ts adds the secret to every request it sends', () => {
  assert.match(read('src/lib/api.ts'), /globalThis\.fetch\(path, \{ \.\.\.init, headers: \{ \.\.\.init\.headers, \.\.\.header\(\) \} \}\)/)
})

test('main.ts takes the secret from the fragment before anything else runs', () => {
  const main = read('src/main.ts')
  const firstImport = main.split('\n').find((l) => l.startsWith('import '))
  assert.equal(firstImport, "import './lib/keyBoot.ts'")
})
