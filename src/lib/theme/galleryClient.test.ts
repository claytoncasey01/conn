import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { installTheme, loadGallery, uninstallTheme } from './galleryClient.ts'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

const seen: { url: string; init: RequestInit | undefined }[] = []
const reply = (status: number, body: unknown) => {
  seen.length = 0
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    seen.push({ url, init })
    return new Response(typeof body === 'string' ? body : JSON.stringify(body), { status })
  }) as unknown as typeof fetch
}
const offline = () => {
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
}

test('install and uninstall POST the id as JSON with x-conn', async () => {
  reply(200, { ok: true, id: 'aurora', version: '1.0.0' })
  assert.deepEqual(await installTheme('aurora'), { ok: true })
  assert.equal(seen[0].url, '/api/gallery/install')
  assert.equal(seen[0].init?.method, 'POST')
  const h = seen[0].init?.headers as Record<string, string>
  assert.equal(h['x-conn'], '1')
  assert.equal(h['content-type'], 'application/json')
  assert.deepEqual(JSON.parse(String(seen[0].init?.body)), { id: 'aurora' })
  reply(200, { ok: true, id: 'aurora', version: '1.0.0' })
  assert.deepEqual(await uninstallTheme('aurora'), { ok: true })
  assert.equal(seen[0].url, '/api/gallery/uninstall')
  assert.equal((seen[0].init?.headers as Record<string, string>)['x-conn'], '1')
})

test('a refusal says the server message, or the status when there is none', async () => {
  reply(404, { error: 'aurora is not in the gallery' })
  assert.deepEqual(await installTheme('aurora'), { ok: false, error: 'aurora is not in the gallery' })
  reply(500, 'not json')
  assert.deepEqual(await uninstallTheme('aurora'), { ok: false, error: 'HTTP 500' })
})

test('a network error is a result, never a throw', async () => {
  offline()
  assert.deepEqual(await installTheme('aurora'), { ok: false, error: 'connection refused' })
  assert.deepEqual(await uninstallTheme('aurora'), { ok: false, error: 'connection refused' })
  const a = await loadGallery()
  assert.equal(a.error, 'connection refused')
  assert.deepEqual(a.themes, [])
  assert.equal(a.enabled, true)
})

test('loadGallery GETs /api/gallery and passes the answer through', async () => {
  reply(200, { enabled: true, repo: 'o/r', checkedAt: 5, themes: [], dropped: 1 })
  const a = await loadGallery()
  assert.equal(seen[0].url, '/api/gallery')
  assert.deepEqual(a, { enabled: true, repo: 'o/r', checkedAt: 5, themes: [], dropped: 1 })
  reply(200, { enabled: false, repo: null, themes: [] })
  assert.equal((await loadGallery()).enabled, false)
})

test('a bad status or a body of the wrong shape is an answer with an error and no themes', async () => {
  reply(500, { error: 'boom' })
  assert.deepEqual(await loadGallery(), { enabled: true, repo: null, themes: [], error: 'boom' })
  reply(200, 'not json')
  assert.equal((await loadGallery()).themes.length, 0)
  assert.ok((await loadGallery()).error)
  reply(200, { enabled: true, themes: 'x' })
  assert.ok((await loadGallery()).error)
})

const good = (id: string, over: Record<string, unknown> = {}) => ({ id, name: id, author: 'a', version: '1.0.0', description: '', commit: 'a'.repeat(40), sha256: 'b'.repeat(64), manifest: { id, name: id, author: 'a', version: '1.0.0', modes: ['dark'], accent: 'fixed', tokens: {}, effects: [] }, ...over })

test('an index entry that is not an object, has no manifest or whose manifest names another id is dropped and counted; the good ones stay', async () => {
  reply(200, {
    enabled: true, repo: 'me/conn-themes', checkedAt: 5, dropped: 1,
    themes: [null, 'x', 7, [], good('ok'), good('nomanifest', { manifest: undefined }), good('strmanifest', { manifest: 'x' }), good('a', { manifest: { id: 'b' } }), good('noid', { id: 3 }), good('two')],
  })
  const a = await loadGallery()
  assert.deepEqual(a.themes.map((t) => t.id), ['ok', 'two'])
  assert.equal(a.enabled, true)
  assert.equal(a.repo, 'me/conn-themes')
  assert.equal(a.checkedAt, 5)
  // the server's own count and what this dropped
  assert.equal(a.dropped, 9)
})

test('a clean list is passed through with no dropped count added', async () => {
  reply(200, { enabled: true, repo: 'me/conn-themes', themes: [good('ok')] })
  const a = await loadGallery()
  assert.equal(a.themes.length, 1)
  assert.equal(a.dropped, undefined)
})

test('an entry whose commit or version is not a string is dropped, and a null files list is not read at all', async () => {
  reply(200, { enabled: true, repo: 'me/conn-themes', themes: [good('numcommit', { commit: 12 }), good('nocommit', { commit: undefined }), good('nullcommit', { commit: null }), good('numver', { version: 2 }), good('nullfiles', { files: null })] })
  const a = await loadGallery()
  assert.deepEqual(a.themes.map((t) => t.id), ['nullfiles'])
  assert.equal(a.dropped, 4)
})
