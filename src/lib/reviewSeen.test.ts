import assert from 'node:assert/strict'
import { test } from 'node:test'
import { badgeCount } from './reviewGroups.ts'
import { fingerprint, markSeen, parseSeen, pruneSeen, unseenWaiting } from './reviewSeen.ts'
import type { ReviewRow, ReviewsView } from './types.ts'

const row = (number: number, state: ReviewRow['state'], reAsked: ReviewRow['reAsked'] = null, repo = 'acme/widgets'): ReviewRow => ({ key: `${repo}#${number}`, repo, number, state, reAsked }) as ReviewRow
const view = (rows: ReviewRow[]) => ({ rows }) as ReviewsView

test('a waiting PR counts until it has been on the Reviews page, then the badge clears', () => {
  const rows = [row(1, 'new'), row(2, 'rereview', 'github'), row(3, 'waiting'), row(4, 'done')]
  assert.equal(badgeCount(view(rows), {}), 2)
  const seen = markSeen({}, rows)
  assert.equal(badgeCount(view(rows), seen), 0)
  // only rows that wait on me are recorded
  assert.deepEqual(seen, { 'acme/widgets#1': 'new:', 'acme/widgets#2': 'rereview:github' })
})

test('the same number in two repos is two PRs: seeing one does not clear the other', () => {
  const rows = [row(7, 'new'), row(7, 'new', null, 'acme/gadgets')]
  const seen = markSeen({}, [rows[0]])
  assert.deepEqual(unseenWaiting(rows, seen).map((r) => r.key), ['acme/gadgets#7'])
  // marks kept by an older build under a bare number match nothing, and are dropped as soon as the page prunes
  assert.deepEqual(pruneSeen({ '7': 'new:' }, rows), {})
})

test('a PR that was not on screen (a filter hid it) is not marked seen and keeps the badge', () => {
  const rows = [row(1, 'new'), row(2, 'new')]
  const seen = markSeen({}, [rows[0]])
  assert.equal(badgeCount(view(rows), seen), 1)
  assert.deepEqual(unseenWaiting(rows, seen).map((r) => r.number), [2])
})

test('a seen PR raises the badge again when it changes to a state that waits on me', () => {
  const seen = markSeen({}, [row(1, 'new')])
  assert.equal(badgeCount(view([row(1, 'rereview', 'slack')]), seen), 1)
  assert.equal(badgeCount(view([row(1, 'rereview', 'github')]), markSeen(seen, [row(1, 'rereview', 'slack')])), 1)
  assert.notEqual(fingerprint(row(1, 'rereview', 'slack')), fingerprint(row(1, 'rereview', 'github')))
})

test('pruneSeen forgets PRs that stopped waiting, so a second round of the same state is a new notification', () => {
  const seen = markSeen({}, [row(1, 'rereview', 'github'), row(2, 'new')])
  // PR 1 was reviewed (waiting), PR 2 left the list
  const pruned = pruneSeen(seen, [row(1, 'waiting')])
  assert.deepEqual(pruned, {})
  assert.equal(badgeCount(view([row(1, 'rereview', 'github')]), pruned), 1)
})

test('markSeen and pruneSeen return the same object when nothing changes (no write, no effect loop)', () => {
  const seen = markSeen({}, [row(1, 'new')])
  assert.equal(markSeen(seen, [row(1, 'new'), row(5, 'done')]), seen)
  assert.equal(pruneSeen(seen, [row(1, 'new')]), seen)
  assert.equal(markSeen(seen, []), seen)
})

test('parseSeen takes only an object of strings; anything else is nothing seen', () => {
  assert.deepEqual(parseSeen('{"1":"new:","2":5,"3":null}'), { '1': 'new:' })
  for (const raw of [null, '', 'nope', '[]', '4', '"x"', 'null']) assert.deepEqual(parseSeen(raw), {}, String(raw))
})
