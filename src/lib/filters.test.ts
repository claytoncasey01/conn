import assert from 'node:assert/strict'
import { test } from 'node:test'
import { barGroups, visibleChips, chipCount, emptySelection, isActive, matches, repoBarGroups, repoChoices, repoGroups, toggle, type Group, type Selection } from './filters.ts'

interface Item { status: string; kind: string }

const groups: Group<Item>[] = [
  {
    id: 'status',
    label: 'Status',
    chips: [
      { id: 'needs', label: 'Needs you', test: (i) => i.status === 'needs' },
      { id: 'live', label: 'Live', test: (i) => i.status === 'live' },
      { id: 'idle', label: 'Idle', test: (i) => i.status === 'idle' },
    ],
  },
  {
    id: 'kind',
    label: 'Kind',
    exclusive: true,
    chips: [
      { id: 'build', label: 'Build', test: (i) => i.kind === 'build' },
      { id: 'review', label: 'Review', test: (i) => i.kind === 'review' },
    ],
  },
]
const items: Item[] = [
  { status: 'needs', kind: 'build' },
  { status: 'live', kind: 'build' },
  { status: 'live', kind: 'review' },
  { status: 'idle', kind: 'build' },
]
const pick = (sel: Selection) => items.filter((i) => matches(i, groups, sel))

test('no selection matches everything', () => {
  const sel = emptySelection()
  assert.equal(isActive(sel), false)
  assert.equal(pick(sel).length, items.length)
})

test('chips in one group are OR', () => {
  const sel: Selection = { status: ['needs', 'live'] }
  assert.equal(isActive(sel), true)
  assert.deepEqual(pick(sel), [items[0], items[1], items[2]])
})

test('groups are AND', () => {
  const sel: Selection = { status: ['live'], kind: ['build'] }
  assert.deepEqual(pick(sel), [items[1]])
})

test('an exclusive group replaces its choice and a second click clears it', () => {
  const kind = groups[1]
  let sel = toggle(emptySelection(), kind, 'build')
  assert.deepEqual(sel.kind, ['build'])
  sel = toggle(sel, kind, 'review')
  assert.deepEqual(sel.kind, ['review'])
  sel = toggle(sel, kind, 'review')
  assert.equal(isActive(sel), false)
})

test("chipCount ignores the chip's own group and respects the others", () => {
  const sel: Selection = { status: ['needs'], kind: ['build'] }
  // status group is ignored, so live counts live builds only
  assert.equal(chipCount(items, groups, sel, 'status', 'live'), 1)
  // kind group is ignored, so review counts items matching status needs: none
  assert.equal(chipCount(items, groups, sel, 'kind', 'review'), 0)
  assert.equal(chipCount(items, groups, sel, 'kind', 'build'), 1)
})

test('a selected chip that no longer exists matches nothing', () => {
  const sel: Selection = { status: ['gone'] }
  assert.equal(pick(sel).length, 0)
  assert.equal(pick({ status: ['live', 'gone'] }).length, 2)
})

test('toggle returns a new object and leaves the old one alone', () => {
  const before: Selection = { status: ['live'] }
  const after = toggle(before, groups[0], 'needs')
  assert.notEqual(after, before)
  assert.deepEqual(before, { status: ['live'] })
  assert.deepEqual(after.status, ['live', 'needs'])
  const removed = toggle(after, groups[0], 'live')
  assert.deepEqual(removed.status, ['needs'])
  assert.deepEqual(after.status, ['live', 'needs'])
})

test("barGroups fills each chip's count from the other groups", () => {
  const sel: Selection = { status: ['needs'], kind: ['build'] }
  const bar = barGroups(items, groups, sel)
  assert.deepEqual(bar.map((g) => g.id), ['status', 'kind'])
  assert.equal(bar[0].label, 'Status')
  assert.equal(bar[0].exclusive, undefined)
  assert.equal(bar[1].exclusive, true)
  // status ignores itself: live builds = 1
  assert.deepEqual(bar[0].chips, [
    { id: 'needs', label: 'Needs you', count: 1 },
    { id: 'live', label: 'Live', count: 1 },
    { id: 'idle', label: 'Idle', count: 1 },
  ])
  // kind ignores itself, status needs applies: only the build is needs
  assert.deepEqual(bar[1].chips, [
    { id: 'build', label: 'Build', count: 1 },
    { id: 'review', label: 'Review', count: 0 },
  ])
})

test('barGroups keeps zero-count chips', () => {
  const bar = barGroups(items, groups, { status: ['needs'] })
  const review = bar[1].chips.find((c) => c.id === 'review')
  assert.deepEqual(review, { id: 'review', label: 'Review', count: 0 })
  assert.equal(bar[1].chips.length, 2)
})

test('a group with a cap shows that many chips plus any selected one, and says how many are hidden', () => {
  const g = { id: 'p', label: 'P', cap: 2, chips: ['a', 'b', 'c', 'd'].map(id => ({ id, label: id })) }
  const ids = (r: ReturnType<typeof visibleChips>) => r.chips.map(c => c.id)
  assert.deepEqual(ids(visibleChips(g, [], false)), ['a', 'b'])
  assert.equal(visibleChips(g, [], false).hidden, 2)
  assert.deepEqual(ids(visibleChips(g, ['d'], false)), ['a', 'b', 'd'])
  assert.equal(visibleChips(g, ['d'], false).hidden, 1)
  assert.deepEqual(ids(visibleChips(g, [], true)), ['a', 'b', 'c', 'd'])
  assert.equal(visibleChips(g, [], true).hidden, 0)
  // At or under the cap there is nothing to hide, and no cap shows everything
  assert.equal(visibleChips({ ...g, cap: 4 }, [], false).hidden, 0)
  assert.equal(visibleChips({ ...g, cap: undefined }, [], false).chips.length, 4)
})

// ---- The repo group ----


test('repoChoices: distinct repos sorted by lowercase id, labelled with the first spelling; hidden for one or none', () => {
  assert.deepEqual(repoChoices([]), [])
  assert.deepEqual(repoChoices([null, undefined, '']), [])
  assert.deepEqual(repoChoices(['acme/widgets', 'acme/widgets']), [], 'one repo filters nothing')
  assert.deepEqual(repoChoices(['Acme/Widgets', 'acme/widgets']), [], 'a case difference is one repo')
  assert.deepEqual(repoChoices(['acme/widgets', null, 'Acme/Gadgets', 'acme/gadgets']), [
    { id: 'acme/gadgets', label: 'Acme/Gadgets' },
    { id: 'acme/widgets', label: 'acme/widgets' },
  ])
})

test('repoChoices keeps a selected id the data no longer has, and shows the group for a lone repo while one is selected', () => {
  assert.deepEqual(repoChoices(['acme/widgets'], ['acme/gone']).map((c) => c.id), ['acme/gone', 'acme/widgets'])
  assert.deepEqual(repoChoices([], ['acme/gone']), [{ id: 'acme/gone', label: 'acme/gone' }])
  assert.deepEqual(repoChoices(['acme/widgets'], ['acme/widgets']), [{ id: 'acme/widgets', label: 'acme/widgets' }])
})

interface Row { name: string; repo: string | null }
const rows: Row[] = [
  { name: 'a', repo: 'acme/widgets' },
  { name: 'b', repo: 'Acme/Gadgets' },
  { name: 'c', repo: 'acme/gadgets' },
  { name: 'd', repo: null },
]

test('repoGroups: none for one repo; otherwise a repo group whose chips match case-blind and leave out items in no repo', () => {
  assert.deepEqual(repoGroups([rows[0], rows[3]], emptySelection()), [])
  assert.deepEqual(repoGroups([], emptySelection()), [])
  const [g] = repoGroups(rows, emptySelection())
  assert.equal(g.id, 'repo')
  assert.equal(g.label, 'Repo')
  assert.deepEqual(g.chips.map((c) => c.id), ['acme/gadgets', 'acme/widgets'])
  assert.deepEqual(rows.filter((r) => matches(r, [g], { repo: ['acme/gadgets'] })).map((r) => r.name), ['b', 'c'])
  assert.deepEqual(rows.filter((r) => matches(r, [g], { repo: ['acme/gadgets', 'acme/widgets'] })).map((r) => r.name), ['a', 'b', 'c'])
  assert.deepEqual(rows.filter((r) => matches(r, [g], emptySelection())).map((r) => r.name), ['a', 'b', 'c', 'd'])
})

test('repoGroups counts through barGroups like any group, and keeps a selected repo whose items are gone', () => {
  const sel: Selection = { repo: ['acme/widgets'] }
  const gs = repoGroups(rows, sel)
  const bar = barGroups(rows, gs, sel)
  assert.deepEqual(bar[0].chips, [
    { id: 'acme/gadgets', label: 'Acme/Gadgets', count: 2 },
    { id: 'acme/widgets', label: 'acme/widgets', count: 1 },
  ])
  const only = repoGroups([rows[0]], { repo: ['acme/old'] })
  assert.deepEqual(only[0].chips.map((c) => c.id), ['acme/old', 'acme/widgets'])
})

test('repoGroups does not read a prototype key as a selection', () => {
  assert.deepEqual(repoGroups(rows, JSON.parse('{"__proto__":["x"]}') as Selection)[0].chips.map((c) => c.id), ['acme/gadgets', 'acme/widgets'])
})

test('repoBarGroups: the facet repos with the server counts; zero for a repo with none, none for one repo, a selected one stays', () => {
  assert.deepEqual(repoBarGroups(['acme/widgets'], { 'acme/widgets': 4 }, []), [])
  assert.deepEqual(repoBarGroups([], {}, []), [])
  const [g] = repoBarGroups(['acme/widgets', 'acme/gadgets'], { 'acme/widgets': 3 }, [], 8)
  assert.equal(g.id, 'repo')
  assert.equal(g.cap, 8)
  assert.deepEqual(g.chips, [
    { id: 'acme/gadgets', label: 'acme/gadgets', count: 0 },
    { id: 'acme/widgets', label: 'acme/widgets', count: 3 },
  ])
  assert.deepEqual(repoBarGroups(['acme/widgets'], {}, ['acme/gone'])[0].chips.map((c) => c.id), ['acme/gone', 'acme/widgets'])
  // a key such as constructor is not a count
  assert.equal(repoBarGroups(['acme/widgets', 'constructor/x'], {}, [])[0].chips[0].count, 0)
})
