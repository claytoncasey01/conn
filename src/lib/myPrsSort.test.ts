import assert from 'node:assert/strict'
import { test } from 'node:test'
import { loadSort, saveSort, sortRows } from './myPrsSort.ts'
import type { MyPrRow } from './types.ts'

const row = (number: number, createdAt: number, lastCommentAt: number): MyPrRow => ({
  repo: 'acme/widgets', number, title: `PR ${number}`, url: `https://github.com/acme/widgets/pull/${number}`, draft: false, createdAt, lastCommentAt,
  openBot: 0, openHuman: 0, outdated: 0, threads: [], partial: false, round: 0, status: 'idle', canStart: true, reason: null, label: 'Self-review', slackDraft: null,
})
const numbers = (rows: MyPrRow[]) => rows.map((r) => r.number)

test('sortRows by opened puts the newest createdAt first', () => {
  assert.deepEqual(numbers(sortRows([row(1, 100, 900), row(2, 300, 0), row(3, 200, 500)], 'opened')), [2, 3, 1])
})

test('sortRows by comment uses lastCommentAt', () => {
  assert.deepEqual(numbers(sortRows([row(1, 100, 900), row(2, 300, 0), row(3, 200, 500)], 'comment')), [1, 3, 2])
})

test('a PR with no comment (0) goes last when sorted by comment', () => {
  assert.deepEqual(numbers(sortRows([row(1, 100, 0), row(2, 300, 5)], 'comment')), [2, 1])
})

test('sortRows does not mutate its input', () => {
  const input = [row(1, 100, 0), row(2, 300, 0)]
  const out = sortRows(input, 'opened')
  assert.notEqual(out, input)
  assert.deepEqual(numbers(input), [1, 2])
})

test('equal keys keep input order', () => {
  assert.deepEqual(numbers(sortRows([row(5, 100, 0), row(3, 100, 0), row(9, 100, 0)], 'opened')), [5, 3, 9])
  assert.deepEqual(numbers(sortRows([row(5, 0, 70), row(3, 0, 70), row(9, 0, 70)], 'comment')), [5, 3, 9])
})

const store = (init: Record<string, string> = {}) => ({
  getItem: (k: string) => init[k] ?? null,
  setItem: (k: string, v: string) => void (init[k] = v),
})
const broken = {
  getItem: () => {
    throw new Error('blocked')
  },
  setItem: () => {
    throw new Error('blocked')
  },
}

test('the sort choice defaults to opened and is remembered', () => {
  const s = store()
  assert.equal(loadSort(s), 'opened')
  saveSort('comment', s)
  assert.equal(loadSort(s), 'comment')
})

test('a stale or hostile stored sort falls back to opened', () => {
  for (const v of ['', 'updated', 'COMMENT', '{"a":1}']) assert.equal(loadSort(store({ 'conn.myprs.sort': v })), 'opened', v)
})

test('storage that throws, or no storage at all, never breaks the choice', () => {
  assert.equal(loadSort(broken), 'opened')
  assert.doesNotThrow(() => saveSort('comment', broken))
  assert.equal(loadSort(null), 'opened')
  assert.doesNotThrow(() => saveSort('comment', null))
})
