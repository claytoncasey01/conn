// The tests run without a browser, so AskPanel's DOM is not mounted here. Its decisions are pure helpers in ask.ts and
// tested there; these checks pin the wiring those tests cannot see, as text: they fail if the keyed body, the pre-paint
// sync or the click-time check is taken out, not if the behaviour around them breaks.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const src = readFileSync(new URL('./AskPanel.svelte', import.meta.url), 'utf8')

test('a new prompt is new DOM: the panel body is keyed by the id of the prompt on show', () => {
  assert.match(src, /\{#key ask\?\.id \?\? ''\}/)
})

test('the panel state follows the snapshot before the DOM is drawn ($effect.pre, not $effect), snapshot `at` included', () => {
  assert.match(src, /\$effect\.pre\(\(\) => \{\s*const next = sync\(untrack\(\(\) => st\), current, waiting, mono\(\), snapAt\)/)
})

test('a click checks again, when it lands, that the prompt on show is the one armed', () => {
  const answer = src.slice(src.indexOf('async function answer('), src.indexOf('async function terminal('))
  assert.match(answer, /const id = pressable\(st, ask, sending, mono\(\)\)\s*\n\s*if \(!id \|\| \(allow && unread\)\) return/)
  assert.match(answer, /answerSession\(session\.id, id, key\)/)
})

test('no answer button is focused or pre-selected', () => {
  assert.doesNotMatch(src, /autofocus|\.focus\(/)
})

test('ALLOW ONCE stays disabled until a long command has been read to its end; DENY does not wait', () => {
  assert.match(src, /class="btn allow cap" disabled=\{busy \|\| unread\}/)
  assert.match(src, /class="btn deny cap" disabled=\{busy\}/)
})
