// Interrupt: the one way to send Ctrl-C to a hosted program whose terminal panel has closed (it fell behind a flood of output).
// One body-less POST through api(), only for an id the host issues; offered on a closed panel, never on a live or ended one.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { afterEach, test } from 'node:test'
import { INTERRUPT_HELP, interruptOffered, interruptSession } from './interrupt.ts'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

const ID = '0123456789abcdef0123456789abcdef'

test('an interrupt is offered on a closed panel only: live keys work, and an ended program has nothing to interrupt', () => {
  for (const [phase, offered] of [['connecting', false], ['replaying', false], ['live', false], ['ended', false], ['closed', true]] as const) {
    assert.equal(interruptOffered({ phase }), offered, phase)
  }
})

test('it says what it does', () => {
  assert.equal(INTERRUPT_HELP, 'Sends Ctrl-C to the program')
})

test('an interrupt is one POST with the page header and no body, and the server’s words when it is refused', async () => {
  const seen: { url: string; init?: RequestInit }[] = []
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    seen.push({ url, init })
    return new Response('{"ok":true}', { status: 200 })
  }) as unknown as typeof fetch
  assert.deepEqual(await interruptSession(ID), { ok: true })
  assert.equal(seen.length, 1)
  assert.equal(seen[0].url, `/api/term/${ID}/interrupt`)
  assert.equal(seen[0].init?.method, 'POST')
  assert.equal((seen[0].init?.headers as Record<string, string>)['x-conn'], '1')
  assert.equal(seen[0].init?.body, undefined)

  globalThis.fetch = (async () => new Response('{"error":"the session has ended"}', { status: 409 })) as unknown as typeof fetch
  assert.deepEqual(await interruptSession(ID), { ok: false, error: 'the session has ended' })

  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await interruptSession(ID), { ok: false, error: 'connection refused' })
})

test('an id the host does not issue sends nothing', async () => {
  let called = false
  globalThis.fetch = (async () => {
    called = true
    return new Response('{}')
  }) as unknown as typeof fetch
  for (const id of ['', '123', `${ID}/../x`, ID.toUpperCase()]) assert.equal((await interruptSession(id)).ok, false)
  assert.equal(called, false)
})

test('the panel offers it while closed, and the hosted Sessions card always', () => {
  const panel = readFileSync(new URL('./Terminal.svelte', import.meta.url), 'utf8')
  assert.match(panel, /\{#if interruptOffered\(view\)\}\s*<InterruptButton id=\{sessionId\} \/>/)
  const sessions = readFileSync(new URL('../pages/Sessions.svelte', import.meta.url), 'utf8')
  assert.match(sessions, /\{#if s\.hosted\}[\s\S]*?<InterruptButton id=\{s\.id\} compact \/>/)
})
