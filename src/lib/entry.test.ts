import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fetchEntry, fetchEntryAgent } from './entry.ts'

const UUID = '0f3c2a9e-1b2d-4c5e-8f70-123456789abc'

/** A fetch that records the URLs it was asked for and answers with `reply`. */
const fake = (reply: () => Response | Promise<Response>) => {
  const urls: string[] = []
  const f = async (url: string) => {
    urls.push(url)
    return reply()
  }
  return { f, urls }
}
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

test('fetchEntry reads the entry', async () => {
  const entry = { row: { id: UUID, endedAt: 5 }, detail: { id: UUID, archived: { endedAt: 5 } }, summary: {} }
  const { f, urls } = fake(() => json(entry))
  assert.deepEqual(await fetchEntry(UUID, f), { kind: 'ready', entry })
  assert.deepEqual(urls, [`/api/history/${UUID}`])
})

test('fetchEntry never requests an id that is not a session id', async () => {
  const { f, urls } = fake(() => json({}))
  for (const bad of ['', '..', '../../api/work', `${UUID}/resume`, 'abc%2F']) assert.deepEqual(await fetchEntry(bad, f), { kind: 'missing' })
  assert.deepEqual(urls, [])
})

test('fetchEntry: 404 is missing, other failures are errors with the reason', async () => {
  assert.deepEqual(await fetchEntry(UUID, fake(() => json({ error: 'not found' }, 404)).f), { kind: 'missing' })
  assert.deepEqual(await fetchEntry(UUID, fake(() => json({ error: 'database is locked' }, 500)).f), { kind: 'error', error: 'database is locked' })
  assert.deepEqual(await fetchEntry(UUID, fake(() => new Response('oops', { status: 502 })).f), { kind: 'error', error: 'HTTP 502' })
  assert.deepEqual(await fetchEntry(UUID, fake(() => Promise.reject(new Error('Failed to fetch'))).f), { kind: 'error', error: 'Failed to fetch' })
  assert.equal((await fetchEntry(UUID, fake(() => json({ row: {} })).f)).kind, 'error')
})

test('fetchEntryAgent reads one agent and checks both ids first', async () => {
  const detail = { agent: { id: 'lead' }, toolCalls: [] }
  const { f, urls } = fake(() => json(detail))
  assert.deepEqual(await fetchEntryAgent(UUID, 'lead', f), detail)
  assert.equal(await fetchEntryAgent(UUID, '../x', f), null)
  assert.equal(await fetchEntryAgent('nope', 'lead', f), null)
  assert.deepEqual(urls, [`/api/history/${UUID}/agents/lead`])
})

test('fetchEntryAgent: no such agent is null, a failed request throws', async () => {
  assert.equal(await fetchEntryAgent(UUID, 'a1', fake(() => json({ error: 'not found' }, 404)).f), null)
  await assert.rejects(fetchEntryAgent(UUID, 'a1', fake(() => json({}, 500)).f), /HTTP 500/)
})
