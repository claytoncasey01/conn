// The tests run without a browser, so the block is not mounted here. These checks pin, as text, the rule it exists to keep: a skill
// is installed only by a press. Reading the state may happen on mount; changeSkill may be called only from press(), and press()
// only from a button's onclick.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const src = readFileSync(new URL('../pages/SettingsProvided.svelte', import.meta.url), 'utf8')

test('nothing is installed or removed except by a press', () => {
  const calls = [...src.matchAll(/changeSkill\(/g)].length
  assert.equal(calls, 1, 'changeSkill is called in one place')
  const body = src.slice(src.indexOf('async function press'), src.indexOf('const word'))
  assert.ok(body.includes('changeSkill('), 'and that place is press()')
  const presses = [...src.matchAll(/press\(/g)].length
  const clicks = [...src.matchAll(/onclick=\{\(\) => press\(/g)].length
  assert.equal(presses, clicks + 1, 'press() is called only from onclick (the +1 is its definition)')
})

test('mounting only reads', () => {
  const mount = src.slice(src.indexOf('onMount('), src.indexOf('async function press'))
  assert.ok(mount.includes('loadProvided()'))
  assert.ok(!mount.includes('changeSkill'))
})
