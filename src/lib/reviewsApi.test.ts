import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { loadModel, placeLegacyReview, refreshReviews, saveModel, startReview } from './reviewsApi.ts'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

const seen: { url: string; init: RequestInit }[] = []
const reply = (status: number, body: unknown) => {
  seen.length = 0
  globalThis.fetch = (async (url: string, init: RequestInit) => {
    seen.push({ url, init })
    return new Response(typeof body === 'string' ? body : JSON.stringify(body), { status })
  }) as unknown as typeof fetch
}

test('it POSTs the repo, number and model to the start route with the x-conn header', async () => {
  reply(200, { ok: true, name: 'pr-acme-widgets-4101' })
  assert.deepEqual(await startReview('acme/widgets', 4101, 'opus'), { ok: true })
  assert.equal(seen[0].url, '/api/reviews/start')
  assert.equal(seen[0].init.method, 'POST')
  const headers = seen[0].init.headers as Record<string, string>
  assert.equal(headers['x-conn'], '1')
  assert.equal(headers['content-type'], 'application/json')
  assert.deepEqual(JSON.parse(seen[0].init.body as string), { repo: 'acme/widgets', number: 4101, model: 'opus' })
})

test('placing an old record POSTs its number and the repo, and a refusal carries the server message', async () => {
  reply(200, { ok: true })
  assert.deepEqual(await placeLegacyReview(41, 'acme/widgets'), { ok: true })
  assert.equal(seen[0].url, '/api/reviews/legacy')
  assert.equal((seen[0].init.headers as Record<string, string>)['x-conn'], '1')
  assert.deepEqual(JSON.parse(seen[0].init.body as string), { number: 41, repo: 'acme/widgets' })
  reply(200, { ok: true, placed: 3 })
  assert.deepEqual(await placeLegacyReview('all', 'acme/widgets'), { ok: true })
  assert.deepEqual(JSON.parse(seen[0].init.body as string), { number: 'all', repo: 'acme/widgets' })
  reply(400, { error: 'other/x is not an allowed repository' })
  assert.deepEqual(await placeLegacyReview(41, 'other/x'), { ok: false, error: 'other/x is not an allowed repository' })
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await placeLegacyReview(41, 'acme/widgets'), { ok: false, error: 'connection refused' })
})

test('HTTP 200 with ok:false is a failure that carries the command to paste', async () => {
  reply(200, { ok: false, error: 'cmux is not running', manualCommand: "cd '/w' && claude --name pr-4101" })
  assert.deepEqual(await startReview('acme/widgets', 4101, 'sonnet'), { ok: false, error: 'cmux is not running', manualCommand: "cd '/w' && claude --name pr-4101" })
})

test('a 409 shows the server message', async () => {
  reply(409, { error: 'a review of this PR was just started: give it a few seconds' })
  assert.deepEqual(await startReview('acme/widgets', 4101, 'sonnet'), { ok: false, error: 'a review of this PR was just started: give it a few seconds' })
})

test('an error status without a body says the status', async () => {
  reply(500, 'not json')
  assert.deepEqual(await startReview('acme/widgets', 4101, 'sonnet'), { ok: false, error: 'HTTP 500' })
})

test('a network failure is a failure with a message, never a throw', async () => {
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await startReview('acme/widgets', 4101, 'sonnet'), { ok: false, error: 'connection refused' })
})

test('the number is sent as an integer, whatever is passed', async () => {
  reply(200, { ok: true })
  await startReview('acme/widgets', 12.9, 'sonnet')
  assert.equal(JSON.parse(seen[0].init.body as string).number, 12)
})

test('refresh POSTs to /api/reviews/refresh with the x-conn header and answers ok', async () => {
  reply(200, { ok: true })
  assert.deepEqual(await refreshReviews(), { ok: true })
  assert.equal(seen[0].url, '/api/reviews/refresh')
  assert.equal(seen[0].init.method, 'POST')
  const headers = seen[0].init.headers as Record<string, string>
  assert.equal(headers['x-conn'], '1')
})

test('refresh: an error status says the server message or the status, and a network failure is a result, never a throw', async () => {
  reply(403, { error: 'forbidden origin' })
  assert.deepEqual(await refreshReviews(), { ok: false, error: 'forbidden origin' })
  reply(500, 'not json')
  assert.deepEqual(await refreshReviews(), { ok: false, error: 'HTTP 500' })
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await refreshReviews(), { ok: false, error: 'connection refused' })
})

const store = (init: Record<string, string> = {}) => ({
  getItem: (k: string) => init[k] ?? null,
  setItem: (k: string, v: string) => void (init[k] = v),
})
const broken = {
  getItem: () => {
    throw new Error('blocked')
  },
  setItem: () => {
    throw new Error('blocked')
  },
}

test('the model choice defaults to sonnet and is remembered', () => {
  const s = store()
  assert.equal(loadModel(s), 'sonnet')
  saveModel('opus', s)
  assert.equal(loadModel(s), 'opus')
})

test('a stale or hostile stored model falls back to sonnet', () => {
  for (const v of ['', 'haiku', 'OPUS', '{"a":1}']) assert.equal(loadModel(store({ 'conn.reviews.model': v })), 'sonnet', v)
})

test('storage that throws (a private window) never breaks the choice', () => {
  assert.equal(loadModel(broken), 'sonnet')
  assert.doesNotThrow(() => saveModel('opus', broken))
})

test('with no storage at all it still works', () => {
  assert.equal(loadModel(null), 'sonnet')
  assert.doesNotThrow(() => saveModel('opus', null))
})
