import assert from 'node:assert/strict'
import { test } from 'node:test'
import { refreshBusy } from './refreshBusy.ts'

test('nothing pending and nothing waited for is not busy', () => {
  assert.equal(refreshBusy({ seq: 0, done: 0 }, null), false)
  assert.equal(refreshBusy({ seq: 3, done: 3 }, null), false)
})

test('a refresh the server has started and not finished is busy, whoever asked for it (another tab, the page just opened)', () => {
  assert.equal(refreshBusy({ seq: 4, done: 3 }, null), true)
  assert.equal(refreshBusy({ seq: 4, done: 0 }, null), true)
})

test('a refresh this page asked for is busy until the server says it is done, even before a snapshot shows it started', () => {
  assert.equal(refreshBusy({ seq: 2, done: 2 }, 3), false, 'the snapshot is older than the refresh: nothing is known yet')
  assert.equal(refreshBusy({ seq: 3, done: 2 }, 3), true)
  assert.equal(refreshBusy({ seq: 3, done: 3 }, 3), false)
  assert.equal(refreshBusy({ seq: 5, done: 5 }, 3), false, 'everything up to 5 is done, so the wait for 3 is over')
  assert.equal(refreshBusy({ seq: 5, done: 4 }, 3), true, 'a later refresh (another tab) is still going')
})

test('a snapshot older than the press is busy only while the grace for the next frame lasts', () => {
  assert.equal(refreshBusy({ seq: 2, done: 2 }, 3, true), true, 'the frame that shows the press has not arrived yet')
  assert.equal(refreshBusy({ seq: 2, done: 2 }, 3, false), false, 'the grace is over: the server restarted')
  assert.equal(refreshBusy({ seq: 3, done: 3 }, 3, true), false, 'the grace never keeps a finished press busy')
  assert.equal(refreshBusy({ seq: 4, done: 4 }, 3, true), false)
})

test('a server that restarted (its counters are lower than the wait) is not waited for', () => {
  assert.equal(refreshBusy({ seq: 1, done: 1 }, 7), false)
  assert.equal(refreshBusy({ seq: 0, done: 0 }, 7), false)
})

test('a snapshot with no reviews view, or an old server with no refresh field, is not busy', () => {
  assert.equal(refreshBusy(undefined, null), false)
  assert.equal(refreshBusy(undefined, 2), false)
})
