import assert from 'node:assert/strict'
import { test } from 'node:test'
import { alertRow, type AlertAction, type Where } from './alertSettings.ts'
import type { Permission } from './nativeNotify.ts'

test('the Alerts row offers the one thing that can change each state', () => {
  const rows: [string, Where, boolean, Permission, boolean, AlertAction][] = [
    ['a browser with no notifications', 'browser', false, 'default', false, 'none'],
    ['nothing asked yet, in the app', 'app', true, 'default', false, 'ask'],
    ['nothing asked yet, in a browser', 'browser', true, 'default', false, 'ask'],
    ['allowed and on', 'app', true, 'granted', true, 'off'],
    ['allowed and off', 'app', true, 'granted', false, 'on'],
    // macOS shows its prompt once: after a no, only System Settings can change it
    ['denied in macOS', 'app', true, 'denied', false, 'settings'],
    // a browser asks once too, and the page cannot open its site settings
    ['blocked by the browser', 'browser', true, 'denied', false, 'none'],
    // an app run from a temporary folder, or with no bundle: nothing the page can do but say where to run it
    ['unavailable in the app', 'app', true, 'unavailable', false, 'none'],
  ]
  for (const [name, where, supported, permission, enabled, action] of rows) {
    assert.equal(alertRow(where, supported, permission, enabled).action, action, name)
  }
})

test('a state that blocks alerts reads as bad, and alerts on as on', () => {
  assert.equal(alertRow('app', true, 'denied', false).tone, 'bad')
  assert.equal(alertRow('browser', true, 'denied', false).tone, 'bad')
  assert.equal(alertRow('app', true, 'unavailable', false).tone, 'bad')
  assert.equal(alertRow('app', true, 'granted', true).tone, 'on')
  assert.equal(alertRow('app', true, 'default', false).tone, 'plain')
})

test('a denial in the app points to System Settings, and one in a browser to its site settings', () => {
  assert.match(alertRow('app', true, 'denied', false).text, /System Settings/)
  assert.match(alertRow('browser', true, 'denied', false).text, /site settings/)
})
