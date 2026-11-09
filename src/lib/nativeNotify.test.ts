import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import type { Alert } from './alerts.ts'
import { findBridge, fitNotification, nativeRequest, nativeShow, nativeStatus, NOTIFICATION_LIMITS, permissionOf, type Bridge } from './nativeNotify.ts'

// The app's limits and cases (internal/desk/notify_test.go reads the same file), so the page never sends what the app refuses.
const shared = JSON.parse(readFileSync(new URL('../../testdata/notification-cases.json', import.meta.url), 'utf8')) as {
  limits: typeof NOTIFICATION_LIMITS
  cases: { name: string; sent: Alert; ok: boolean }[]
}

test('the limits are the app’s, from testdata/notification-cases.json', () => {
  assert.deepEqual({ ...NOTIFICATION_LIMITS }, shared.limits)
})

for (const c of shared.cases.filter((c) => c.ok)) {
  test(`a notification the app shows is sent as it is: ${c.name}`, () => {
    assert.deepEqual(fitNotification(c.sent), c.sent)
  })
}

const chars = (s: string) => [...s].length
const controls = /[\u0000-\u001F\u007F-\u009F]/

test('fitting cuts each field to its limit and leaves no control character', () => {
  const long = { title: 'é'.repeat(5000), body: 'line\n'.repeat(5000), tag: 's'.repeat(5000), href: '#/s/1' }
  const fit = fitNotification(long)
  assert.ok(chars(fit.title) <= NOTIFICATION_LIMITS.title, `title ${chars(fit.title)}`)
  assert.ok(chars(fit.body) <= NOTIFICATION_LIMITS.body, `body ${chars(fit.body)}`)
  assert.ok(chars(fit.tag) <= NOTIFICATION_LIMITS.tag, `tag ${chars(fit.tag)}`)
  assert.ok(!controls.test(fit.title + fit.body), 'no control character')
})

test('fitting flattens a terminal escape and a newline to text on one line', () => {
  const fit = fitNotification({ title: '\u001b[31mconn\u001b[0m finished', body: 'Question:\nwhich one?', tag: 's1', href: '#/s/1' })
  assert.ok(!controls.test(fit.title + fit.body), JSON.stringify(fit))
  assert.equal(fit.body, 'Question: which one?')
})

test('the bridge is found only where WebKit put the app’s handler', () => {
  const postMessage = async () => 'default'
  const rows: [string, unknown, boolean][] = [
    ['a browser', {}, false],
    ['WebKit with no handler of Conn’s', { webkit: { messageHandlers: {} } }, false],
    ['a handler that is not one', { webkit: { messageHandlers: { connNotify: {} } } }, false],
    ['Conn.app', { webkit: { messageHandlers: { connNotify: { postMessage } } } }, true],
  ]
  for (const [name, page, found] of rows) assert.equal(findBridge(page) !== null, found, name)
})

test('an answer the page does not know is unavailable', () => {
  const rows: [unknown, string][] = [
    ['default', 'default'],
    ['granted', 'granted'],
    ['denied', 'denied'],
    ['unavailable', 'unavailable'],
    ['provisional', 'unavailable'],
    [undefined, 'unavailable'],
    [{ status: 'granted' }, 'unavailable'],
  ]
  for (const [answer, want] of rows) assert.equal(permissionOf(answer), want, String(answer))
})

/** A bridge that records what it was sent and answers with `answer`, or rejects when it is an Error. */
function fake(answer: unknown): Bridge & { sent: unknown[] } {
  const sent: unknown[] = []
  return {
    sent,
    postMessage: async (message: unknown) => {
      sent.push(message)
      if (answer instanceof Error) throw answer
      return answer
    },
  }
}

test('status and request ask the app by kind and read its answer', async () => {
  const bridge = fake('granted')
  assert.equal(await nativeStatus(bridge), 'granted')
  assert.equal(await nativeRequest(bridge), 'granted')
  assert.deepEqual(bridge.sent, [{ kind: 'status' }, { kind: 'request' }])
})

test('a bridge that refuses is unavailable, never an error the page throws', async () => {
  assert.equal(await nativeStatus(fake(new Error('only a page of the server may ask'))), 'unavailable')
  await nativeShow(fake(new Error('the app refused the notification')), { title: 't', body: 'b', tag: 'g', href: '#/s/1' })
})

test('show sends the fitted alert', async () => {
  const bridge = fake('shown')
  await nativeShow(bridge, { title: 'conn finished', body: 'idle\nnow', tag: 's1', href: '#/s/1' })
  assert.deepEqual(bridge.sent, [{ kind: 'show', title: 'conn finished', body: 'idle now', tag: 's1', href: '#/s/1' }])
})
