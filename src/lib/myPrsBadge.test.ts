import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readyCount } from './myPrsBadge.ts'
import type { MyPrDraft, MyPrRow, MyPrsView } from './types.ts'

const draft = (over: Partial<MyPrDraft> = {}): MyPrDraft => ({ sha: 'a1', text: 'hello', state: 'saved', error: null, at: 0, tries: 1, ...over })
const row = (over: Partial<MyPrRow>): MyPrRow => ({ repo: 'org/repo', number: 1, title: 't', status: 'idle', slackDraft: null, ...over }) as MyPrRow
const view = (rows: MyPrRow[]): MyPrsView => ({ at: 0, me: 'me', error: null, capped: false, refresh: { seq: 0, done: 0 }, loaded: true, draftMaxTries: 3, channel: 'c', rows })

test('readyCount counts the rows that need me and is 0 for undefined', () => {
  assert.equal(readyCount(undefined), 0)
  assert.equal(readyCount(view([])), 0)
  const rows = [
    row({ number: 1, status: 'ready', slackDraft: draft() }),
    row({ number: 2, status: 'draft-failed', slackDraft: draft({ state: 'failed', error: 'x', tries: 3 }) }),
    row({ number: 3, status: 'findings' }),
    row({ number: 4, status: 'idle' }),
  ]
  assert.equal(readyCount(view(rows)), 2)
})

test('a ready row whose draft is not saved yet is not counted', () => {
  assert.equal(readyCount(view([row({ status: 'ready', slackDraft: null }), row({ number: 2, status: 'ready' })])), 0)
  // a draft kept for the head but failed shows as draft-failed, which counts; a failed draft on a ready row cannot come from the server but is not "there"
  assert.equal(readyCount(view([row({ status: 'ready', slackDraft: draft({ state: 'failed' }) })])), 0)
})

test('a failed draft counts only once its retries are used up', () => {
  const failed = (tries: number) => row({ status: 'draft-failed', slackDraft: draft({ state: 'failed', error: 'x', tries }) })
  assert.equal(readyCount(view([failed(1)])), 0)
  assert.equal(readyCount(view([failed(2)])), 0)
  assert.equal(readyCount(view([failed(3)])), 1)
  assert.equal(readyCount(view([failed(4)])), 1)
  // the cap is the server's, not a number of the page's own
  assert.equal(readyCount({ ...view([failed(2)]), draftMaxTries: 2 }), 1)
})

test('a draft-failed row with no draft read does not count', () => {
  assert.equal(readyCount(view([row({ status: 'draft-failed' })])), 0)
})
