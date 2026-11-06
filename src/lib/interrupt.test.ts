// Interrupt: the one way to send Ctrl-C to a hosted program whose terminal panel has closed (it fell behind a flood of output).
// One body-less POST through api(), only for an id the host issues; offered on a closed panel, never on a live or ended one.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { afterEach, test } from 'node:test'
import { INTERRUPT_HELP, INTERRUPT_REPEAT_MS, INTERRUPT_SENT, interruptOffered, interruptOnRow, interruptSession } from './interrupt.ts'

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

test('it says what it does, and that a second press soon after is not sent', () => {
  assert.equal(INTERRUPT_HELP, 'Sends Ctrl-C to the program. A second press within 3 seconds is not sent: two could make it quit')
})

test('the page waits as long as the server before offering a second press', () => {
  const server = readFileSync(new URL('../../internal/server/terminterrupt.go', import.meta.url), 'utf8')
  const window = /interruptRepeatWindow = (\d+) \* time\.Second/.exec(server)
  assert.ok(window, 'the server names its repeat window in seconds')
  assert.equal(Number(window[1]) * 1000, INTERRUPT_REPEAT_MS)
})

test('a Sessions card offers it for a hosted program still running, never for one that ended', () => {
  for (const [row, offered] of [
    [{ hosted: true }, true],
    [{ hosted: true, exitCode: 0 }, false],
    [{ hosted: true, exitCode: 130 }, false],
    [{ hosted: false }, false],
    [{}, false],
  ] as const) {
    assert.equal(interruptOnRow(row), offered, JSON.stringify(row))
  }
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

test('once sent, the words on the page say a second press soon after is not sent', () => {
  assert.equal(INTERRUPT_SENT, 'Ctrl-C sent. Another press within 3 seconds is not sent')
  const button = readFileSync(new URL('./InterruptButton.svelte', import.meta.url), 'utf8')
  assert.match(button, /\{#if step === 'sent'\}\s*<span role="status"[^>]*>\{INTERRUPT_SENT\}<\/span>/)
})

test('the button rests for the window after Ctrl-C was sent', () => {
  const button = readFileSync(new URL('./InterruptButton.svelte', import.meta.url), 'utf8')
  assert.match(button, /setTimeout\([^\n]*resting = false[^\n]*step = 'idle'[^\n]*INTERRUPT_REPEAT_MS\)/)
  assert.match(button, /disabled=\{resting\}/)
})

test('the panel offers it while closed, and the Sessions card while the hosted program runs', () => {
  const panel = readFileSync(new URL('./Terminal.svelte', import.meta.url), 'utf8')
  assert.match(panel, /\{#if interruptOffered\(view\)\}\s*<InterruptButton id=\{sessionId\} \/>/)
  const sessions = readFileSync(new URL('../pages/Sessions.svelte', import.meta.url), 'utf8')
  assert.match(sessions, /\{#if interruptOnRow\(s\)\}\s*<InterruptButton id=\{s\.id\} compact \/>/)
})
