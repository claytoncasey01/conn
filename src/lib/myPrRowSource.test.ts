// The tests run without a browser, so MyPrRow's DOM is not mounted here. Its decision is a pure helper in myPrsStarted.ts, tested
// there; these checks pin the wiring and the styles as text.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const src = readFileSync(new URL('./MyPrRow.svelte', import.meta.url), 'utf8')

test('the status tag wraps, so a long repo name in NO LOCAL CLONE OF <repo> never scrolls the page sideways', () => {
  const tag = /const tag = '([^']*)'/.exec(src)?.[1] ?? ''
  assert.match(tag, /white-space: normal/)
  assert.match(tag, /overflow-wrap: anywhere/)
  assert.doesNotMatch(tag, /nowrap/)
})

test('the "Started" note is shown only while the row still looks as it did at the press', () => {
  assert.match(src, /\{#if startedShows\(started, row\)\}/)
  const start = src.slice(src.indexOf('async function start('), src.indexOf('let cmd'))
  // what the row looked like is taken before the request, not after it
  assert.ok(start.indexOf('const at = { status: row.status, round: row.round }') < start.indexOf('await startSelf('), start)
  assert.match(start, /if \(r\.ok\) started = at/)
  // the status the row shows once the press's own round is on it is taken as it comes
  assert.match(src, /\$effect\(\(\) => \{\s*if \(started\) \{\s*const next = settleStarted\(started, row\)\s*if \(next !== started\) started = next/)
})

test('the Greptile ask error is the server\'s whole sentence, shown as it is (the server knows whether conn is still trying)', () => {
  assert.match(src, /\{#if row\.error\}\s*<div role="alert"[^>]*>\{row\.error\}<\/div>/)
  assert.doesNotMatch(src, /trying again/)
})

test('the Slack draft lines come from draftView: a retry time instead of "post this yourself" while conn will try again, and out of date once the row is not ready', () => {
  assert.match(src, /const dv = \$derived\(draftView\(row\)\)/)
  assert.match(src, /const statusLine = \$derived\(statusText\(row\)\)/)
  // "Post this yourself instead" only for a draft no retry will save
  const hand = src.indexOf('Post this yourself instead:')
  assert.ok(hand > 0)
  assert.match(src.slice(src.lastIndexOf('{#if', hand), hand), /dv\.byHand/)
  assert.match(src, /conn tries again at \{clock\(dv\.retryAt\)\}/)
  assert.match(src, /\(out of date\)/)
})
