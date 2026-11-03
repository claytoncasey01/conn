import assert from 'node:assert/strict'
import { test } from 'node:test'
import { barGroups, emptySelection, matches } from './filters.ts'
import { badgeCount, reviewGroups, safeUrl } from './reviewGroups.ts'
import type { ReviewRow, ReviewsView } from './types.ts'

const row = (over: Partial<ReviewRow>): ReviewRow =>
  ({ number: 1, title: 't', author: 'sam-dev', state: 'new', ...over }) as ReviewRow

const group = (rows: ReviewRow[], id: string, sel = emptySelection()) => reviewGroups(rows, sel).find((g) => g.id === id)!

test('status chips map the five states, in the order the page lists them', () => {
  const g = group([], 'status')
  assert.deepEqual(g.chips.map((c) => c.id), ['new', 'reviewing', 'waiting', 'rereview', 'done'])
  assert.deepEqual(g.chips.map((c) => c.label), ['New', 'Reviewing', 'Waiting', 'Re-review', 'Done'])
  for (const state of ['new', 'reviewing', 'waiting', 'rereview', 'done'] as const) {
    assert.deepEqual(g.chips.filter((c) => c.test(row({ state }))).map((c) => c.id), [state])
  }
})

test('author chips list each author once, from the rows present', () => {
  const rows = [row({ author: 'maria-k' }), row({ author: 'li-wen' }), row({ author: 'maria-k' }), row({ author: 'dependabot[bot]' })]
  const g = group(rows, 'author')
  assert.deepEqual(g.chips.map((c) => c.label), ['dependabot[bot]', 'li-wen', 'maria-k'])
  const bar = barGroups(rows, reviewGroups(rows, emptySelection()), emptySelection()).find((b) => b.id === 'author')!
  assert.deepEqual(bar.chips.map((c) => [c.label, c.count]), [['dependabot[bot]', 1], ['li-wen', 1], ['maria-k', 2]])
})

test('an author chip tests the author exactly', () => {
  const g = group([row({ author: 'li-wen' })], 'author')
  assert.equal(g.chips[0].test(row({ author: 'li-wen' })), true)
  assert.equal(g.chips[0].test(row({ author: 'li-wen2' })), false)
})

test('a selected author that is gone stays as a chip at 0, so it can be unselected', () => {
  const rows = [row({ author: 'maria-k' })]
  const sel = { author: ['li-wen'] }
  const groups = reviewGroups(rows, sel)
  const bar = barGroups(rows, groups, sel).find((b) => b.id === 'author')!
  assert.equal(bar.chips.find((c) => c.id === 'li-wen')!.count, 0)
  assert.equal(bar.chips.length, 2)
  assert.equal(matches(rows[0], groups, sel), false)
})

test('status and author filters combine', () => {
  const rows = [row({ author: 'a', state: 'new' }), row({ author: 'b', state: 'new' }), row({ author: 'a', state: 'done' })]
  const sel = { status: ['new'], author: ['a'] }
  const groups = reviewGroups(rows, sel)
  assert.deepEqual(rows.map((r) => matches(r, groups, sel)), [true, false, false])
})

test('a repo group appears only when the rows are in more than one repo; its chips are the repos present and filter by repo', () => {
  const widgets = row({ number: 7, repo: 'Acme/Widgets' })
  const gadgets = row({ number: 7, repo: 'acme/gadgets' })
  assert.equal(reviewGroups([widgets, row({ number: 8, repo: 'acme/widgets' })], emptySelection()).some((g) => g.id === 'repo'), false)
  assert.equal(reviewGroups([], emptySelection()).some((g) => g.id === 'repo'), false)
  const rows = [widgets, gadgets, row({ number: 9, repo: 'acme/widgets' })]
  const groups = reviewGroups(rows, emptySelection())
  assert.deepEqual(groups.map((g) => g.id), ['status', 'author', 'repo'])
  const bar = barGroups(rows, groups, emptySelection()).find((b) => b.id === 'repo')!
  // ids are lowercase, labels keep the first spelling GitHub sent
  assert.deepEqual(bar.chips.map((c) => [c.id, c.label, c.count]), [['acme/gadgets', 'acme/gadgets', 1], ['acme/widgets', 'Acme/Widgets', 2]])
  const sel = { repo: ['acme/widgets'] }
  const picked = reviewGroups(rows, sel)
  assert.deepEqual(rows.filter((r) => matches(r, picked, sel)).map((r) => [r.repo, r.number]), [['Acme/Widgets', 7], ['acme/widgets', 9]])
})

test('a selected repo whose rows are gone stays as a chip at 0, so it can be unselected', () => {
  const sel = { repo: ['jane/tools'] }
  const rows = [row({ repo: 'acme/widgets' })]
  const bar = barGroups(rows, reviewGroups(rows, sel), sel).find((b) => b.id === 'repo')!
  assert.deepEqual(bar.chips.map((c) => [c.id, c.count]), [['acme/widgets', 1], ['jane/tools', 0]])
})

test('badgeCount counts only new and re-review rows', () => {
  const view = (rows: ReviewRow[]) => ({ rows }) as ReviewsView
  const rows = (['new', 'rereview', 'reviewing', 'waiting', 'done', 'new'] as const).map((state) => row({ state }))
  assert.equal(badgeCount(view(rows)), 3)
  assert.equal(badgeCount(view([])), 0)
  assert.equal(badgeCount(undefined), 0)
})

test('safeUrl keeps only https links, so a hostile permalink is never an href', () => {
  assert.equal(safeUrl('https://github.com/org/repo/pull/7'), 'https://github.com/org/repo/pull/7')
  assert.equal(safeUrl('https://example.slack.com/archives/C0/p1'), 'https://example.slack.com/archives/C0/p1')
  for (const bad of [null, '', 'javascript:alert(1)', 'http://github.com/x', 'data:text/html,x', ' https://x', '//x', 'HTTPS://x']) assert.equal(safeUrl(bad), null, String(bad))
})
