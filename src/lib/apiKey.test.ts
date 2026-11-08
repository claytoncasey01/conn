import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { api } from './api.ts'
import { header, KEY, takeFromFragment } from './apiKey.ts'

const SECRET = 'A'.repeat(42) + '_'

function memory(initial: Record<string, string> = {}) {
  const items = { ...initial }
  return { items, getItem: (k: string) => items[k] ?? null, setItem: (k: string, v: string) => void (items[k] = v) }
}

function at(hash: string) {
  const replaced: string[] = []
  return { place: { hash, pathname: '/', search: '?theme=neon' }, hist: { replaceState: (_d: unknown, _u: string, url?: string) => void replaced.push(url ?? '') }, replaced }
}

test('a #k= fragment is kept in the store and removed from the address, the rest of the address kept', () => {
  const store = memory()
  const { place, hist, replaced } = at(`#k=${SECRET}`)
  assert.equal(takeFromFragment(place, hist, store), true)
  assert.equal(store.items[KEY], SECRET)
  assert.deepEqual(replaced, ['/?theme=neon'])
})

test('a route fragment is left alone', () => {
  const store = memory()
  const { place, hist, replaced } = at('#/history')
  assert.equal(takeFromFragment(place, hist, store), false)
  assert.deepEqual(store.items, {})
  assert.deepEqual(replaced, [])
})

test('a #k= fragment that is not a secret is removed and not kept, and the old secret stays', () => {
  for (const bad of ['', 'short', `${SECRET}A`, `${SECRET.slice(1)}+`, `${SECRET.slice(1)}=`, `${SECRET}&x=1`]) {
    const store = memory({ [KEY]: SECRET })
    const { place, hist, replaced } = at(`#k=${bad}`)
    assert.equal(takeFromFragment(place, hist, store), false, bad)
    assert.equal(store.items[KEY], SECRET, bad)
    assert.deepEqual(replaced, ['/?theme=neon'], bad)
  }
})

test('a new secret replaces the old one', () => {
  const store = memory({ [KEY]: 'B'.repeat(43) })
  const { place, hist } = at(`#k=${SECRET}`)
  takeFromFragment(place, hist, store)
  assert.equal(store.items[KEY], SECRET)
})

test('header is the bearer of the stored secret, read at each call, and nothing without one', () => {
  const store = memory()
  assert.deepEqual(header(store), {})
  store.setItem(KEY, SECRET)
  assert.deepEqual(header(store), { authorization: `Bearer ${SECRET}` })
  store.setItem(KEY, 'not a secret')
  assert.deepEqual(header(store), {})
  assert.deepEqual(header(null), {})
  assert.deepEqual(header({ getItem: () => { throw new Error('denied') }, setItem: () => {} }), {})
})

const realFetch = globalThis.fetch
const realStorage = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage')
afterEach(() => {
  globalThis.fetch = realFetch
  if (realStorage) Object.defineProperty(globalThis, 'sessionStorage', realStorage)
  else delete (globalThis as { sessionStorage?: unknown }).sessionStorage
})

test('api sends the request with its own headers and the secret', async () => {
  Object.defineProperty(globalThis, 'sessionStorage', { value: memory({ [KEY]: SECRET }), configurable: true })
  const seen: { url: string; init: RequestInit }[] = []
  globalThis.fetch = (async (url: string, init: RequestInit) => {
    seen.push({ url, init })
    return new Response('{}')
  }) as unknown as typeof fetch
  await api('/api/work/refresh', { method: 'POST', headers: { 'x-conn': '1' }, body: '{}' })
  assert.equal(seen[0].url, '/api/work/refresh')
  assert.equal(seen[0].init.method, 'POST')
  assert.equal(seen[0].init.body, '{}')
  assert.deepEqual(seen[0].init.headers, { 'x-conn': '1', authorization: `Bearer ${SECRET}` })
})

test('a caller cannot replace the secret with a header of its own', async () => {
  Object.defineProperty(globalThis, 'sessionStorage', { value: memory({ [KEY]: SECRET }), configurable: true })
  const seen: RequestInit[] = []
  globalThis.fetch = (async (_url: string, init: RequestInit) => {
    seen.push(init)
    return new Response('{}')
  }) as unknown as typeof fetch
  await api('/api/snapshot', { headers: { authorization: 'Bearer other' } })
  assert.deepEqual(seen[0].headers, { authorization: `Bearer ${SECRET}` })
})

test('api sends nothing to a path that is not one of the server\'s own /api routes', async () => {
  Object.defineProperty(globalThis, 'sessionStorage', { value: memory({ [KEY]: SECRET }), configurable: true })
  const seen: string[] = []
  globalThis.fetch = (async (url: string) => {
    seen.push(url)
    return new Response('{}')
  }) as unknown as typeof fetch
  for (const path of ['https://evil.example/api/x', '//evil.example/api/x', '/apix', '/api', 'api/x', '/themes/x.css', '', ' /api/x']) {
    await assert.rejects(api(path), /not an \/api route/, path)
  }
  assert.deepEqual(seen, [])
})
