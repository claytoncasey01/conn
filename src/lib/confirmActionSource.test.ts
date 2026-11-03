// The component is not mounted here (no DOM library); the focus rules are in confirmFocus.ts and tested there. These pin, as text,
// that every step which removes the focused button hands focus to the next control, and that its words follow the theme's case.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const src = readFileSync(new URL('./ConfirmAction.svelte', import.meta.url), 'utf8')
const script = src.slice(0, src.indexOf('</script>'))
const markup = src.slice(src.indexOf('</script>'))

test('every swap of the buttons moves focus on: idle to confirm, Cancel, confirm to working, and working to done or error', () => {
  const m = script.match(/async function move\([\s\S]*?\n  \}/)?.[0] ?? ''
  assert.match(m, /step = next\s+await tick\(\)/)
  assert.match(m, /onlyIfAdrift && !adrift\(document\.activeElement, document\.body, root\)/)
  // the press that opens the prompt
  assert.match(markup, /onclick=\{ask\}/)
  assert.match(script, /function ask\(\) \{\s+void move\('confirm', \(\) => \(firstFocus\(tone\) === 'cancel' \? cancelBtn : confirmBtn\)\)\s+\}/)
  // Cancel and OK put it back on the button that asked
  assert.equal(markup.match(/onclick=\{back\}/g)?.length, 2)
  assert.match(script, /function back\(\) \{\s+void move\('idle', \(\) => askBtn\)\s+\}/)
  // the confirming press removes the focused button: the status line holds focus while it works, then it goes to the result unless the page took it
  // the status line exists only once the block has rendered, so it is focused after a tick; the action itself starts first
  assert.match(script, /step = 'working'\s+const started = run\(\)\s+await tick\(\)\s+statusEl\?\.focus\(\)\s+const r = await started/)
  assert.match(script, /await move\('idle', \(\) => askBtn, true\)/)
  assert.match(script, /await move\('error', \(\) => okBtn, true\)/)
  for (const ref of ['askBtn', 'confirmBtn', 'cancelBtn', 'okBtn']) assert.match(markup, new RegExp(`bind:this=\\{${ref}\\}`))
  assert.match(markup, /role="status" tabindex="-1" bind:this=\{statusEl\}/)
})

test('the working line follows the theme\'s case token, and nothing here sets an uppercase transform', () => {
  assert.match(markup, /role="status"[^>]*text-transform: var\(--case\)[^>]*>Working…/)
  assert.doesNotMatch(src, /text-transform\s*:\s*uppercase/i)
})
