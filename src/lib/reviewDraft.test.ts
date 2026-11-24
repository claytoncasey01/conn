import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import {
  allTicked,
  APPROVE_BLOCKED,
  approveBlocked,
  draftId,
  draftKind,
  firstLine,
  placeNote,
  startingVerdict,
  tickedAfter,
  verdictAfter,
} from './reviewDraft.ts'
import { postReview } from './reviewsApi.ts'
import type { ReviewDraft, ReviewFinding } from './types.ts'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

const finding = (over: Partial<ReviewFinding> = {}): ReviewFinding => ({ path: 'a.go', line: 3, side: 'RIGHT', severity: 'P3', body: 'b', inline: true, ...over })
const draft = (over: Partial<ReviewDraft> = {}): ReviewDraft => ({
  launchAt: 1700000000000,
  fileAt: 1700000000500,
  commit: 'a'.repeat(40),
  headMoved: false,
  summary: 's',
  verdict: 'approve',
  findings: [finding({ severity: 'P3' }), finding({ severity: 'P1' }), finding({ severity: 'P2' })],
  dropped: 0,
  postedAt: null,
  url: null,
  problems: [],
  ...over,
})

test('allTicked is every index of the findings, in order', () => {
  assert.deepEqual(allTicked(draft()), [0, 1, 2])
  assert.deepEqual(allTicked(draft({ findings: [] })), [])
})

test('approve is blocked while a P0 or P1 is ticked, and only then', () => {
  const rows: [string, ReviewDraft, number[], boolean][] = [
    ['all ticked, with a P1', draft(), [0, 1, 2], true],
    ['the P1 unticked', draft(), [0, 2], false],
    ['nothing ticked', draft(), [], false],
    ['a ticked P0', draft({ findings: [finding({ severity: 'P0' })] }), [0], true],
    ['a ticked P2', draft({ findings: [finding({ severity: 'P2' })] }), [0], false],
    ['an index outside the findings', draft(), [7, -1], false],
  ]
  for (const [name, d, ticked, blocked] of rows) assert.equal(approveBlocked(d, ticked), blocked, name)
})

test('the verdict turns approve into comment while approve is blocked, and keeps the choice otherwise', () => {
  const rows: [string, number[], 'approve' | 'comment', 'approve' | 'comment'][] = [
    ['approve with a blocker ticked', [0, 1, 2], 'approve', 'comment'],
    ['approve with the blocker unticked', [0, 2], 'approve', 'approve'],
    ['comment with a blocker ticked', [0, 1, 2], 'comment', 'comment'],
    ['comment with nothing ticked', [], 'comment', 'comment'],
  ]
  for (const [name, ticked, chosen, want] of rows) assert.equal(verdictAfter(draft(), ticked, chosen), want, name)
})

test("the verdict starts at Claude's, and as a comment when Claude gave none", () => {
  assert.equal(startingVerdict(draft({ verdict: 'approve' })), 'approve')
  assert.equal(startingVerdict(draft({ verdict: 'comment' })), 'comment')
  assert.equal(startingVerdict(draft({ verdict: '' })), 'comment')
})

test('placeNote says where a finding goes when it is not an inline comment', () => {
  assert.equal(placeNote(finding({ inline: true })), '')
  assert.equal(placeNote(finding({ inline: false })), 'outside the diff: goes in the summary')
  assert.equal(placeNote(finding({ inline: null })), 'line not checked: goes in the summary')
})

test('the first line of a body is what shows until it is expanded', () => {
  assert.equal(firstLine('one\ntwo'), 'one')
  assert.equal(firstLine('\n\n  one  \ntwo'), 'one')
  assert.equal(firstLine(''), '')
})

test('ticking adds or removes one index and keeps the rest in order', () => {
  assert.deepEqual(tickedAfter([0, 2], 1, true), [0, 1, 2])
  assert.deepEqual(tickedAfter([0, 1, 2], 1, false), [0, 2])
  assert.deepEqual(tickedAfter([0, 1], 1, true), [0, 1])
})

test('a draft is a posted one, an unreadable file, or a draft to post', () => {
  const marker = draft({ commit: '', findings: [], summary: '', verdict: '', postedAt: 5, url: 'https://x' })
  const unreadable = draft({ commit: '', findings: [], summary: '', verdict: '', problems: ['commit is not a sha'] })
  const rows: [string, ReviewDraft, string][] = [
    ['a posted marker', marker, 'posted'],
    ['a posted draft that kept its findings', draft({ postedAt: 5 }), 'posted'],
    ['no commit and no findings, with problems', unreadable, 'unreadable'],
    ['findings with problems from a newer write', draft({ problems: ['x'] }), 'draft'],
    ['a plain draft', draft(), 'draft'],
  ]
  for (const [name, d, kind] of rows) assert.equal(draftKind(d), kind, name)
})

test('a draft keeps its identity while it is posted or its head moves, and changes when the file is written again', () => {
  const same = draftId(draft())
  assert.equal(draftId(draft({ headMoved: true, postedAt: 9, url: 'https://x', problems: ['p'] })), same)
  assert.notEqual(draftId(draft({ launchAt: 2 })), same)
  assert.notEqual(draftId(draft({ summary: 'other' })), same)
  assert.notEqual(draftId(draft({ findings: [finding({ body: 'changed' })] })), same)
})

test('the blocked note says a P0 or P1 is a blocker and to post a comment', () => {
  assert.equal(APPROVE_BLOCKED, 'A ticked P0 or P1 is a blocker: post it as a comment')
})

const stub = (status: number, body: unknown, seen: { url: string; init?: RequestInit }[] = []) => {
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    seen.push({ url, init })
    return new Response(JSON.stringify(body), { status })
  }) as unknown as typeof fetch
  return seen
}

test('postReview sends the key, the launch, the write of the file shown, the ticked indexes and the verdict with the page header', async () => {
  const link = 'https://github.com/acme/app/pull/12#pullrequestreview-1'
  const seen = stub(200, { ok: true, url: link })
  assert.deepEqual(await postReview('acme/app#12', 1700000000000, 1700000000500, [0, 2], 'comment'), { ok: true, url: link })
  assert.equal(seen.length, 1)
  assert.equal(seen[0].url, '/api/reviews/post')
  assert.equal(seen[0].init?.method, 'POST')
  const headers = seen[0].init?.headers as Record<string, string>
  assert.equal(headers['x-conn'], '1')
  assert.equal(headers['content-type'], 'application/json')
  assert.deepEqual(JSON.parse(seen[0].init?.body as string), { key: 'acme/app#12', launchAt: 1700000000000, fileAt: 1700000000500, findings: [0, 2], verdict: 'comment' })
})

test("postReview gives the server's words for a refusal, whatever the status", async () => {
  const held = 'checking whether GitHub took the review: try again after the next poll'
  for (const status of [400, 403, 404, 409, 502]) {
    stub(status, { error: held })
    assert.deepEqual(await postReview('acme/app#12', 1, 2, [], 'approve'), { ok: false, error: held }, String(status))
  }
})

test('postReview names the status when the answer has no error text, and the failure when there is no answer', async () => {
  globalThis.fetch = (async () => new Response('<html>', { status: 502 })) as unknown as typeof fetch
  assert.deepEqual(await postReview('acme/app#12', 1, 2, [], 'approve'), { ok: false, error: 'HTTP 502' })
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  assert.deepEqual(await postReview('acme/app#12', 1, 2, [], 'approve'), { ok: false, error: 'connection refused' })
})

test('postReview takes a 200 with ok false as the failure it says, and a 200 with no link as posted without one', async () => {
  stub(200, { ok: false, error: 'nope' })
  assert.deepEqual(await postReview('acme/app#12', 1, 2, [], 'approve'), { ok: false, error: 'nope' })
  stub(200, { ok: true })
  assert.deepEqual(await postReview('acme/app#12', 1, 2, [], 'approve'), { ok: true, url: '' })
})
