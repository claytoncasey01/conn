import assert from 'node:assert/strict'
import { test } from 'node:test'
import { adrift, firstFocus } from './confirmFocus.ts'

const node = (inside: Node[] = []) => ({ contains: (n: Node) => inside.includes(n) })
const el = {} as Element
const body = {} as Element

test('the confirm button takes focus first, but a destructive (red) action puts it on Cancel, so a held Enter cannot confirm it', () => {
  assert.equal(firstFocus('green'), 'confirm')
  assert.equal(firstFocus('cyan'), 'confirm')
  assert.equal(firstFocus('red'), 'cancel')
})

test('focus is adrift when nothing holds it, the page body does, or the removed button of this component did', () => {
  assert.equal(adrift(null, body, node()), true)
  assert.equal(adrift(body, body, node()), true)
  assert.equal(adrift(el, body, node([el])), true)
  // an unmounted component has no root: only the body or null count
  assert.equal(adrift(body, body, null), true)
})

test('focus somewhere else is left alone: another control, or one the page placed itself (ThemesPage refocuses after an uninstall)', () => {
  assert.equal(adrift(el, body, node()), false)
  assert.equal(adrift(el, body, null), false)
})
