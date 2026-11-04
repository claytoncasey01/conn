import assert from 'node:assert/strict'
import { test } from 'node:test'
import { appendRows, historyBar, historyQueryString, refreshLimit } from './historyQuery.ts'
import type { Selection } from './filters.ts'

const none = { text: '', ref: '', minCost: '' }
const qs = (sel: Selection, o: Partial<Parameters<typeof historyQueryString>[1]> = {}) => historyQueryString(sel, { ...none, ...o })

test('no selection and no text give an empty string', () => {
  assert.equal(qs({}), '')
})

test('chips of a group join with commas', () => {
  assert.equal(qs({ kind: ['build', 'adhoc'] }), 'kind=build,adhoc')
  assert.equal(qs({ kind: ['build'], model: ['claude-opus-5-5', 'claude-haiku-5-5'] }), 'kind=build&model=claude-opus-5-5,claude-haiku-5-5')
  assert.equal(qs({ project: ['conn', 'my app'] }), 'project=conn,my%20app')
})

test('a chip id with a space or a reserved character is encoded', () => {
  assert.equal(qs({ outcome: ['PR opened', 'a&b=c'] }), 'outcome=PR%20opened,a%26b%3Dc')
})

test('the range group maps to range=today|7d|30d|custom with from and to', () => {
  assert.equal(qs({ range: ['today'] }), 'range=today')
  assert.equal(qs({ range: ['7d'] }), 'range=7d')
  assert.equal(qs({ range: ['30d'] }), 'range=30d')
  assert.equal(qs({ range: ['custom'] }, { from: '2026-10-01', to: '2026-10-29' }), 'range=custom&from=2026-10-01&to=2026-10-29')
})

test('from and to count only with the custom range', () => {
  assert.equal(qs({ range: ['7d'] }, { from: '2026-10-01', to: '2026-10-29' }), 'range=7d')
  assert.equal(qs({}, { from: '2026-10-01' }), '')
})

test('custom with one bound or none sends what it has', () => {
  assert.equal(qs({ range: ['custom'] }, { from: '2026-10-01' }), 'range=custom&from=2026-10-01')
  assert.equal(qs({ range: ['custom'] }, { to: '2026-10-29' }), 'range=custom&to=2026-10-29')
  assert.equal(qs({ range: ['custom'] }), 'range=custom')
})

test('dates go through as typed, so the server reads them as local days; anything that is not a date is dropped', () => {
  assert.equal(qs({ range: ['custom'] }, { from: '2026-02-30', to: 'tomorrow' }), 'range=custom')
  assert.equal(qs({ range: ['custom'] }, { from: '2026-13-01' }), 'range=custom')
  assert.equal(qs({ range: ['custom'] }, { from: ' 2026-10-01 ' }), 'range=custom&from=2026-10-01')
  assert.equal(qs({ range: ['custom'] }, { from: '2026-10-01T00:00:00Z' }), 'range=custom')
})

test('text, ref and minCost are encoded', () => {
  assert.equal(qs({}, { text: 'PROJ 1893 & more' }), 'q=PROJ%201893%20%26%20more')
  assert.equal(qs({}, { ref: '#42' }), 'ref=%2342')
  assert.equal(qs({}, { minCost: '1.5' }), 'minCost=1.5')
  assert.equal(qs({ kind: ['build'] }, { text: 'x', ref: 'PROJ-1', minCost: '2' }), 'kind=build&q=x&ref=PROJ-1&minCost=2')
})

test('the branch text is trimmed and encoded; blank is left out', () => {
  assert.equal(qs({}, { branch: ' feat/login ' }), 'branch=feat%2Flogin')
  assert.equal(qs({}, { branch: '   ' }), '')
  assert.equal(qs({}, {}), '')
  assert.equal(qs({ project: ['conn'] }, { branch: 'x', text: 'y', ref: 'PROJ-1', minCost: '2' }), 'project=conn&q=y&ref=PROJ-1&branch=x&minCost=2')
})

test('blank and whitespace inputs are left out', () => {
  assert.equal(qs({}, { text: '   ', ref: ' ', minCost: ' ' }), '')
  assert.equal(qs({ kind: [] }), '')
  assert.equal(qs({}, { text: '  hi  ' }), 'q=hi')
})

test('a minimum cost that is not a non-negative number is left out', () => {
  for (const bad of ['abc', '-1', 'NaN', 'Infinity', '1e999', '$5']) assert.equal(qs({}, { minCost: bad }), '', bad)
  assert.equal(qs({}, { minCost: '0' }), 'minCost=0')
  assert.equal(qs({}, { minCost: '.5' }), 'minCost=.5')
})

test('only the known groups are sent, and a hostile group name does not leak in', () => {
  assert.equal(qs({ evil: ['x'], __proto__: ['y'] } as Selection), '')
})

test('the bar has five groups, the range one exclusive with no counts', () => {
  const bar = historyBar({ kind: {}, outcome: {}, model: {} }, [], {})
  assert.deepEqual(bar.map(g => g.id), ['range', 'kind', 'outcome', 'model', 'project'])
  const range = bar[0]
  assert.equal(range.exclusive, true)
  assert.deepEqual(range.chips.map(c => [c.id, c.label, c.count]), [['today', 'Today', undefined], ['7d', '7 days', undefined], ['30d', '30 days', undefined], ['custom', 'Custom', undefined]])
})

test('counts come from the server counts; a value the server did not count is zero', () => {
  const bar = historyBar({ kind: { build: 3 }, outcome: { merged: 2, ended: 1 }, model: { 'claude-opus-5-5': 3 } }, ['claude-opus-5-5', 'claude-haiku-5-5'], {})
  const get = (g: string) => Object.fromEntries(bar.find(x => x.id === g)!.chips.map(c => [c.id, c.count]))
  assert.deepEqual(get('kind'), { build: 3, adhoc: 0, review: 0, selfreview: 0 })
  assert.deepEqual(get('outcome'), { merged: 2, 'PR opened': 0, ended: 1 })
  assert.deepEqual(get('model'), { 'claude-haiku-5-5': 0, 'claude-opus-5-5': 3 })
})

test('the kind options include review and selfreview', () => {
  const bar = historyBar({ kind: { review: 2, selfreview: 1 }, outcome: {}, model: {} }, [], {})
  const chips = bar.find(g => g.id === 'kind')!.chips
  assert.deepEqual(chips.map(c => c.id), ['build', 'adhoc', 'review', 'selfreview'])
  assert.deepEqual(chips.find(c => c.id === 'review'), { id: 'review', label: 'Review', count: 2 })
  assert.deepEqual(chips.find(c => c.id === 'selfreview'), { id: 'selfreview', label: 'Self-review', count: 1 })
})

test('an outcome or model that is selected stays on the bar even when nothing has it, so it can be unselected', () => {
  const bar = historyBar({ kind: {}, outcome: {}, model: {} }, [], { outcome: ['reviewed'], model: ['claude-old-1'] })
  assert.ok(bar.find(g => g.id === 'outcome')!.chips.some(c => c.id === 'reviewed' && c.count === 0))
  assert.ok(bar.find(g => g.id === 'model')!.chips.some(c => c.id === 'claude-old-1' && c.count === 0))
})

test('models are labelled like the Sessions page, and a blank model is not a chip', () => {
  const bar = historyBar({ kind: {}, outcome: {}, model: { 'claude-opus-5-5': 1, '': 4 } }, [''], {})
  assert.deepEqual(bar.find(g => g.id === 'model')!.chips.map(c => c.label), ['OPUS 5.5'])
})

test('appendRows keeps the first occurrence of a repeated id and the order', () => {
  const r = (id: string, n = 0) => ({ id, n })
  assert.deepEqual(appendRows([r('a'), r('b')], [r('b', 1), r('c'), r('c', 2)]), [r('a'), r('b'), r('c')])
  assert.deepEqual(appendRows([], [r('a')]), [r('a')])
  assert.deepEqual(appendRows([r('a')], []), [r('a')])
})

test('refreshLimit keeps loaded rows, never below a page or above the server cap', () => {
  assert.equal(refreshLimit(0, 100), 100)
  assert.equal(refreshLimit(100, 100), 100)
  assert.equal(refreshLimit(300, 100), 300)
  assert.equal(refreshLimit(500, 100), 500)
  assert.equal(refreshLimit(800, 100), 500)
})

test('projects come from the facets with the server counts; a project not counted is zero, a selected one stays', () => {
  const bar = historyBar({ project: { conn: 3, web: 1 } }, [], { project: ['gone'] }, ['web', 'conn', 'idle'])
  const g = bar.find(x => x.id === 'project')!
  assert.equal(g.label, 'Project')
  assert.deepEqual(g.chips.map(c => [c.id, c.label, c.count]), [['conn', 'conn', 3], ['gone', 'gone', 0], ['idle', 'idle', 0], ['web', 'web', 1]])
  assert.ok(g.cap && g.cap > 0)
})

test('a blank project is not a chip, and no projects gives an empty group', () => {
  const bar = historyBar({ project: { '': 2 } }, [], {}, [''])
  assert.deepEqual(bar.find(x => x.id === 'project')!.chips, [])
})

test('the repo group is sent as repos=, apart from project=, each id encoded', () => {
  assert.equal(qs({ repo: ['acme/widgets'] }), 'repos=acme%2Fwidgets')
  assert.equal(qs({ repo: ['acme/widgets', 'acme/gadgets'], project: ['conn'] }), 'project=conn&repos=acme%2Fwidgets,acme%2Fgadgets')
  assert.equal(qs({ repo: [] }), '')
})

test('the repo group is there only for more than one repo (or a selected one); counts are the server\'s, a repo not counted is zero', () => {
  assert.equal(historyBar({}, [], {}, [], []).find(x => x.id === 'repo'), undefined)
  assert.equal(historyBar({ repo: { 'acme/widgets': 4 } }, [], {}, [], ['acme/widgets']).find(x => x.id === 'repo'), undefined)
  const bar = historyBar({ repo: { 'acme/widgets': 4 } }, [], {}, [], ['acme/widgets', 'acme/gadgets'])
  const g = bar.find(x => x.id === 'repo')!
  assert.equal(g.label, 'Repo')
  assert.deepEqual(g.chips.map(c => [c.id, c.label, c.count]), [['acme/gadgets', 'acme/gadgets', 0], ['acme/widgets', 'acme/widgets', 4]])
  // the five groups before it are unchanged, and a selected repo keeps the group when the archive holds one
  assert.deepEqual(bar.slice(0, 5).map(x => x.id), ['range', 'kind', 'outcome', 'model', 'project'])
  assert.deepEqual(historyBar({}, [], { repo: ['acme/gone'] }, [], ['acme/widgets']).find(x => x.id === 'repo')!.chips.map(c => c.id), ['acme/gone', 'acme/widgets'])
})
