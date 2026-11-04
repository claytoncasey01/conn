import assert from 'node:assert/strict'
import { test } from 'node:test'
import { draftView, statusText } from './myPrsDraftView.ts'
import type { MyPrDraft, MyPrRow } from './types.ts'

const draft = (over: Partial<MyPrDraft> = {}): MyPrDraft => ({ sha: 'a1', text: 'hello', state: 'saved', error: null, at: 0, tries: 1, ...over })
const row = (over: Partial<MyPrRow>): MyPrRow => ({ repo: 'org/repo', number: 1, title: 't', status: 'idle', openBot: 0, openHuman: 0, slackDraft: null, draftRetryAt: null, ...over }) as MyPrRow

test('a ready row says drafting until its draft is saved, and draft ready once it is', () => {
  assert.equal(statusText(row({ status: 'ready' })), 'Clean, drafting…')
  assert.equal(statusText(row({ status: 'ready', slackDraft: draft({ state: 'failed' }) })), 'Clean, drafting…')
  assert.equal(statusText(row({ status: 'ready', slackDraft: draft() })), 'Clean, draft ready')
})

test('the other statuses keep their words, in sentence case: the row cases them with the theme', () => {
  assert.equal(statusText(row({ status: 'findings', openBot: 2, openHuman: 1 })), '3 findings to fix')
  assert.equal(statusText(row({ status: 'findings', openBot: 1 })), '1 finding to fix')
  assert.equal(statusText(row({ status: 'no-clone', repo: 'org/web' })), 'No local clone of org/web')
  assert.equal(statusText(row({ status: 'running' })), 'Session running')
  assert.equal(statusText(row({ status: 'draft-failed' })), 'Draft failed')
  // A status this build does not know (a newer server)
  assert.equal(statusText(row({ status: 'later' as MyPrRow['status'] })), 'Idle')
})

test('a saved draft is out of date only once a later review found something on its head', () => {
  assert.deepEqual(draftView(row({ status: 'ready', slackDraft: draft() })), { kind: 'saved', stale: false })
  assert.deepEqual(draftView(row({ status: 'findings', slackDraft: draft() })), { kind: 'saved', stale: true })
  // A new round at work on the head, one launched and not seen yet (idle), the wait for an ask, no clone, a draft PR: nothing was found
  for (const status of ['running', 'idle', 'waiting', 'no-clone', 'draft-pr'] as const) {
    assert.deepEqual(draftView(row({ status, slackDraft: draft() })), { kind: 'saved', stale: false }, status)
  }
  assert.deepEqual(draftView(row({ status: 'ready' })), { kind: 'none' })
})

test('a failed draft that will be tried again says when, and only a failed draft that will not asks to be posted by hand', () => {
  const failed = draft({ state: 'failed', error: 'x' })
  assert.deepEqual(draftView(row({ status: 'draft-failed', slackDraft: failed, draftRetryAt: 900_000 })), { kind: 'failed', retryAt: 900_000, byHand: false, stale: false })
  assert.deepEqual(draftView(row({ status: 'draft-failed', slackDraft: { ...failed, tries: 3 } })), { kind: 'failed', retryAt: null, byHand: true, stale: false })
  // No longer clean: nothing will retry it, and it is not the text to post
  assert.deepEqual(draftView(row({ status: 'findings', slackDraft: failed, draftRetryAt: null })), { kind: 'failed', retryAt: null, byHand: false, stale: true })
  for (const status of ['running', 'idle', 'waiting', 'no-clone', 'draft-pr'] as const) {
    assert.deepEqual(draftView(row({ status, slackDraft: failed })), { kind: 'failed', retryAt: null, byHand: false, stale: false }, status)
  }
  // An older server sends no retry time: the draft reads as one to post by hand, as before
  assert.deepEqual(draftView(row({ status: 'draft-failed', slackDraft: failed, draftRetryAt: undefined })), { kind: 'failed', retryAt: null, byHand: true, stale: false })
})

test('a clean row with no draft being made says only that it is clean', () => {
  assert.equal(statusText(row({ status: 'ready', draftOff: true })), 'Clean')
  assert.equal(statusText(row({ status: 'ready', draftOff: true, slackDraft: draft() })), 'Clean, draft ready')
})
