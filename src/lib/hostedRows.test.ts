// A hosted session's rows as the server builds them, read from testdata/answers/sessions/hosted.json, which the Go tests
// hold the server to: the page shows them like any other row, and the ask panel is the same one.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import hosted from '../../testdata/answers/sessions/hosted.json'
import { panelView } from './ask.ts'
import type { SessionDetail } from './types.ts'

const rows = hosted.rows as unknown as SessionDetail[]
const row = (name: string): SessionDetail => {
  const found = rows.find(r => r.name === name)
  if (!found) throw new Error(`no hosted row ${name} in testdata/answers/sessions/hosted.json`)
  return found
}

test('every hosted row is marked hosted', () => {
  assert.equal(rows.length, 3)
  for (const r of rows) assert.equal(r.hosted, true)
})

test("a waiting hosted session's permission prompt gets the same two buttons as any other", () => {
  const view = panelView(row('n55'), false)
  assert.equal(view.view, 'permission')
  assert.equal(view.allow?.key, '1')
  assert.equal(view.deny?.key, '3')
})

test('a running hosted session that asks nothing shows no panel, and can be replied to', () => {
  const running = row('running')
  assert.equal(panelView(running, false).view, 'none')
  assert.equal(running.canReply, true)
  assert.equal(running.exitCode, undefined)
})

test('an ended hosted session can no longer be replied to, and says how it ended', () => {
  const ended = row('ended')
  assert.equal(panelView(ended, false).view, 'none')
  assert.equal(ended.canReply, false)
  assert.equal(ended.exitCode, 143)
})

test('the reply box says an ended hosted session has ended, not that it has no cmux terminal', () => {
  const source = readFileSync(new URL('./ReplyBox.svelte', import.meta.url), 'utf8')
  const hostedBranch = source.indexOf('{#if s.hosted}')
  assert.ok(hostedBranch > source.indexOf('{#if !s.canReply}'), 'the hosted case is inside the no-reply case')
  assert.ok(source.indexOf('>Ended</span>', hostedBranch) > hostedBranch)
})
