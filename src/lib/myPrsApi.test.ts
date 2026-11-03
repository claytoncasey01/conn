import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { postThread, refreshMyPrs, startSelf } from './myPrsApi.ts'

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

test('refreshMyPrs posts to /api/myprs/refresh with x-conn', async () => {
  reply(200, { ok: true, seq: 4 })
  assert.deepEqual(await refreshMyPrs(), { ok: true, seq: 4 })
  assert.equal(seen[0].url, '/api/myprs/refresh')
  assert.equal(seen[0].init.method, 'POST')
  assert.equal((seen[0].init.headers as Record<string, string>)['x-conn'], '1')
})

test('an answer without a usable seq is ok with no seq', async () => {
  reply(200, { ok: true, seq: 'x' })
  assert.deepEqual(await refreshMyPrs(), { ok: true })
})

test('an error status says the server message or the status, and a network failure is a result, never a throw', async () => {
  reply(403, { error: 'forbidden origin' })
  assert.deepEqual(await refreshMyPrs(), { ok: false, error: 'forbidden origin' })
  reply(500, 'not json')
  assert.deepEqual(await refreshMyPrs(), { ok: false, error: 'HTTP 500' })
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await refreshMyPrs(), { ok: false, error: 'connection refused' })
})

test('startSelf POSTs the repo, number and model to /api/myprs/start with x-conn and a JSON body', async () => {
  reply(200, { ok: true, name: 'self-repo-7' })
  assert.deepEqual(await startSelf('org/repo', 7, 'opus'), { ok: true })
  assert.equal(seen[0].url, '/api/myprs/start')
  assert.equal(seen[0].init.method, 'POST')
  const headers = seen[0].init.headers as Record<string, string>
  assert.equal(headers['x-conn'], '1')
  assert.equal(headers['content-type'], 'application/json')
  assert.deepEqual(JSON.parse(seen[0].init.body as string), { repo: 'org/repo', number: 7, model: 'opus' })
})

test('startSelf: HTTP 200 with ok:false carries the error and the command to paste', async () => {
  reply(200, { ok: false, error: 'cmux is not running', manualCommand: "cd '/w' && claude --name self-repo-7" })
  assert.deepEqual(await startSelf('org/repo', 7, 'sonnet'), { ok: false, error: 'cmux is not running', manualCommand: "cd '/w' && claude --name self-repo-7" })
})

test('startSelf: a refusal shows the server message, a missing body the status, a network failure a message, and none throws', async () => {
  reply(409, { error: 'no local clone of org/repo' })
  assert.deepEqual(await startSelf('org/repo', 7, 'sonnet'), { ok: false, error: 'no local clone of org/repo' })
  reply(500, 'not json')
  assert.deepEqual(await startSelf('org/repo', 7, 'sonnet'), { ok: false, error: 'HTTP 500' })
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await startSelf('org/repo', 7, 'sonnet'), { ok: false, error: 'connection refused' })
})

test('postThread posts the four values to /api/myprs/thread with x-conn and answers ok', async () => {
  reply(200, { ok: true })
  assert.deepEqual(await postThread('org/repo', 7, 'PRRT_kwDOabc123', 'Fixed.'), { ok: true })
  assert.equal(seen[0].url, '/api/myprs/thread')
  assert.equal(seen[0].init.method, 'POST')
  assert.equal((seen[0].init.headers as Record<string, string>)['x-conn'], '1')
  assert.deepEqual(JSON.parse(String(seen[0].init.body)), { repo: 'org/repo', number: 7, threadId: 'PRRT_kwDOabc123', text: 'Fixed.' })
})

test('postThread reads a refused write (HTTP 200 ok:false), an error status and a lost request as results, never a throw', async () => {
  reply(200, { ok: false, error: 'the reply is posted, but the thread was not resolved: HTTP 502' })
  assert.deepEqual(await postThread('org/repo', 7, 'PRRT_kwDOabc123', ''), { ok: false, error: 'the reply is posted, but the thread was not resolved: HTTP 502' })
  reply(409, { error: 'that thread is already resolved' })
  assert.deepEqual(await postThread('org/repo', 7, 'PRRT_kwDOabc123', ''), { ok: false, error: 'that thread is already resolved' })
  reply(500, 'not json')
  assert.deepEqual(await postThread('org/repo', 7, 'PRRT_kwDOabc123', ''), { ok: false, error: 'HTTP 500' })
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await postThread('org/repo', 7, 'PRRT_kwDOabc123', ''), { ok: false, error: 'connection refused' })
})
