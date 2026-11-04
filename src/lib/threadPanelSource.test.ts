// The tests run without a browser, so the panel's DOM is not mounted here. Its decisions are pure helpers in threadPanel.ts and
// tested there; these checks pin, as text, what the markup must never do: render GitHub's text as HTML, or post for several threads at once.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const panel = readFileSync(new URL('./ThreadPanel.svelte', import.meta.url), 'utf8')
const row = readFileSync(new URL('./MyPrRow.svelte', import.meta.url), 'utf8')

test('markup, control and bidi characters in a comment render as inert text: neither the panel nor the row uses {@html}', () => {
  assert.doesNotMatch(panel, /\{@html/)
  assert.doesNotMatch(row, /\{@html/)
  assert.doesNotMatch(panel, /innerHTML|outerHTML|insertAdjacentHTML/)
  // the first comment and the author are plain interpolations
  assert.match(panel, /white-space: pre-wrap">\{t\.text\}<\/div>/)
  assert.match(panel, /\{threadLabel\(t\)\}/)
})

test('one click posts for one thread: postThread is called once, in press(), which has no loop, and only a per-thread button calls press', () => {
  assert.equal(panel.match(/postThread\(/g)?.length, 1)
  assert.doesNotMatch(row, /postThread/)
  const start = panel.indexOf('async function press(')
  const press = panel.slice(start, panel.indexOf('\n  }\n', start))
  assert.ok(start > 0 && press.includes('postThread(row.repo, row.number, t.id, s.text)'))
  assert.doesNotMatch(press, /\bfor\s*\(|\bwhile\s*\(|forEach|\.map\(|Promise\.all|\.filter\(|\.reduce\(/)
  // press is only ever reached from the click of one thread's own button
  assert.deepEqual(panel.match(/\bpress\(/g)?.length, 2) // the definition and the one call
  assert.match(panel, /onclick=\{\(\) => press\(t\)\}/)
})

test('there is no bulk control: every button of the panel sits inside the per-thread block', () => {
  const body = panel.slice(panel.indexOf('</script>'))
  const each = body.indexOf('{#each row.threads as t (t.id)}')
  const endEach = body.indexOf('{/each}')
  assert.ok(each >= 0 && endEach > each)
  for (const m of body.matchAll(/<button\b/g)) assert.ok(m.index > each && m.index < endEach, 'a button outside the thread loop')
  assert.doesNotMatch(body, /\ball\b|every thread|ALL\b/)
})

test('every thread has a labelled box, and the post button is off while a request is out or the box is over the limit', () => {
  assert.match(panel, /<textarea[\s\S]*?aria-label="Reply to the thread by/)
  assert.match(panel, /<label for="\{uid\}-\{t\.id\}"/)
  assert.match(panel, /disabled=\{!canPress\(s, held\(t\)\)\}/)
})

test('SKIP is kept nowhere: no request, no storage, no cookie', () => {
  assert.doesNotMatch(panel, /localStorage|sessionStorage|indexedDB|document\.cookie|fetch\(/)
})

test('hiding the panel does not unmount it: the row toggles visibility, so edits, requests in flight and SKIPs survive HIDE', () => {
  // not wrapped in an {#if} that the toggle controls
  assert.doesNotMatch(row, /\{#if threadsOpen\}/)
  assert.match(row, /<div id="\{uid\}-threads" hidden=\{!threadsOpen\}><ThreadPanel \{row\} \/><\/div>/)
  assert.match(row, /aria-expanded=\{threadsOpen\} aria-controls="\{uid\}-threads"/)
  // the only conditional around the panel is "the row has threads"
  const before = row.slice(0, row.indexOf('<ThreadPanel {row} />'))
  assert.match(before.slice(before.lastIndexOf('{#if')), /^\{#if row\.threads\.length > 0\}/)
})

test('an outdated thread is listed with an OUTDATED tag beside its label, and the row counts unresolved threads, not open ones', () => {
  assert.match(panel, /\{threadLabel\(t\)\}[\s\S]{0,400}\{#if t\.outdated\}<span[^>]*>Outdated<\/span>\{\/if\}/)
  assert.match(panel, /aria-label="Unresolved threads on PR \{row\.number\}"/)
  assert.match(row, /\{row\.threads\.length\} unresolved \{row\.threads\.length === 1 \? 'thread' : 'threads'\}/)
  assert.doesNotMatch(row, /OPEN \{row\.threads/)
})

test('the row shows how many proposed replies were ignored, as a status line of its own', () => {
  assert.match(row, /\{#if ignored\}<div role="status"[^>]*>\{ignored\}<\/div>\{\/if\}/)
  assert.match(row, /const ignored = \$derived\(ignoredNote\(row\.ignoredReplies\)\)/)
})

test('a newer proposal is offered beside the box with USE IT, which drops the draft so the box takes the newer text', () => {
  assert.match(panel, /\{@const newer = newerProposal\(t, drafts\[t\.id\]\)\}/)
  assert.match(panel, /\{#if newer !== null[^}]*\}[\s\S]{0,600}A newer proposal arrived[\s\S]{0,300}white-space: pre-wrap">\{newer\}<\/div>[\s\S]{0,600}onclick=\{\(\) => delete drafts\[t\.id\]\}[^>]*>Use it<\/button>/)
})

test('a click keeps the text it sent as the box draft before the request goes out, so a failure never loses it', () => {
  const start = panel.indexOf('async function press(')
  const press = panel.slice(start, panel.indexOf('\n  }\n', start))
  assert.ok(press.indexOf('keep(t, s.text)') > 0 && press.indexOf('keep(t, s.text)') < press.indexOf('postThread('))
})

test('after POST AND RESOLVE goes through, and after SKIP, focus moves on to the next thread (or the panel heading), never to the page', () => {
  assert.match(panel, /if \(r\.ok\) \{\s*sent\[t\.id\] = true\s*void focusAfter\(t\)/)
  assert.match(panel, /function skip\(t: RowThread\) \{\s*skipped\[t\.id\] = true\s*void focusAfter\(t\)/)
  assert.match(panel, /onclick=\{\(\) => skip\(t\)\}/)
  assert.match(panel, /await tick\(\)[\s\S]{0,300}nextFocus\(/)
  assert.match(panel, /<h3 bind:this=\{heading\} tabindex="-1"/)
  assert.match(panel, /\(box \?\? heading\)\?\.focus\(\)/)
})
