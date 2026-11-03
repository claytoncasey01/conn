import assert from 'node:assert/strict'
import { test } from 'node:test'
import { allowed as serverAllowed, parsePattern as serverParse } from './serverAnswers.ts'
import {
  MAX_PATTERNS,
  addCheck,
  addPattern,
  clonesFrom,
  covered,
  folderProblem,
  howText,
  matchedCount,
  matchedText,
  nextFocusIndex,
  ownerCountText,
  parsePattern,
  removePattern,
  repoName,
  saveClones,
  savePatterns,
  saveProblem,
  sameList,
  suggestions,
  wroteClones,
  wrotePatterns,
  type ReposSettings,
} from './settingsModel.ts'

// One table for the grammar: the same cases the server's parsePattern is held to (testdata/answers/repomatch.json)
const GOOD: [string, string][] = [
  ['acme/widgets', 'acme/widgets'],
  ['acme/*', 'acme/*'],
  ['ACME/Widgets', 'acme/widgets'],
  ['  acme/widgets  ', 'acme/widgets'],
  ['acme/my.repo-2_x', 'acme/my.repo-2_x'],
  ['acme-labs/*', 'acme-labs/*'],
  ['jane/.github', 'jane/.github'],
]
const BAD = ['', '   ', '*', '*/*', 'acme/re*', 'acme/*x', 'a/b/c', '../x', 'acme/..', 'acme/.', './x', 'acme', 'acme/', '/widgets', 'acme /widgets', 'acme/wid gets', 'acme/wid?gets', 'acme/wid#gets', 'acme/widégets', 'acme/аpp', 'acme\\widgets', 'https://github.com/acme/widgets', '-acme/widgets', '--help/x', 'acme/-widgets', '_acme/widgets', '-acme/*']

test('parsePattern accepts owner/repo and owner/*, lowercases and trims', () => {
  for (const [raw, want] of GOOD) assert.deepEqual(parsePattern(raw), { ok: true, pattern: want }, raw)
})

test('parsePattern refuses everything else and says why', () => {
  for (const raw of BAD) {
    const r = parsePattern(raw)
    assert.equal(r.ok, false, JSON.stringify(raw))
    if (!r.ok) assert.ok(r.reason.length > 4, raw)
  }
  assert.match((parsePattern('*/*') as { reason: string }).reason, /owner/i)
  assert.match((parsePattern('acme/re*') as { reason: string }).reason, /whole/i)
  assert.match((parsePattern('acme/wid gets') as { reason: string }).reason, /space/i)
})

test('covered mirrors allowed(): case, owner wildcard, no prefix match, malformed and empty lists', () => {
  assert.equal(covered('Acme/Widgets', ['acme/widgets']), true)
  assert.equal(covered('acme/widgets', ['acme/*']), true)
  assert.equal(covered('acme/widgets', ['acme-labs/*']), false)
  assert.equal(covered('acme-labs/widgets', ['acme/*']), false)
  assert.equal(covered('acme/my.repo-2', ['acme/my.repo-2']), true)
  assert.equal(covered('acme/widgets', []), false)
  assert.equal(covered('acme/widgets', null), false)
  assert.equal(covered('acme', ['acme/*']), false)
  assert.equal(covered('acme/../x', ['acme/*']), false)
  assert.equal(covered('acme/wid?gets', ['acme/*']), false)
  assert.equal(covered('', ['acme/*']), false)
})

test('repoName accepts what a repo is called and nothing else', () => {
  for (const ok of ['acme/widgets', 'Acme/Widgets', 'a/b', 'acme/my.repo-2_x']) assert.equal(repoName(ok), true, ok)
  for (const bad of ['acme/*', 'acme', 'a/b/c', '../x', 'acme/..', 'acme/wid gets', '']) assert.equal(repoName(bad), false, bad)
})

test('addPattern appends a valid pattern and keeps the order', () => {
  assert.deepEqual(addPattern(['acme/*'], 'Jane/Widgets'), { ok: true, list: ['acme/*', 'jane/widgets'], pattern: 'jane/widgets' })
  assert.deepEqual(addPattern([], 'acme/*'), { ok: true, list: ['acme/*'], pattern: 'acme/*' })
})

test('addPattern refuses a duplicate (any casing), a bad pattern and a 26th', () => {
  const dup = addPattern(['acme/widgets'], 'ACME/Widgets')
  assert.equal(dup.ok, false)
  assert.match((dup as { reason: string }).reason, /already/i)
  assert.equal(addPattern([], '*/*').ok, false)
  const full = Array.from({ length: MAX_PATTERNS }, (_, i) => `acme/r${i}`)
  const over = addPattern(full, 'acme/more')
  assert.equal(over.ok, false)
  assert.match((over as { reason: string }).reason, /25/)
})

test('addPattern does not change the list it is given', () => {
  const list = ['acme/*']
  addPattern(list, 'jane/widgets')
  assert.deepEqual(list, ['acme/*'])
})

test('removePattern drops one entry and leaves the rest', () => {
  assert.deepEqual(removePattern(['acme/*', 'jane/widgets'], 'acme/*'), ['jane/widgets'])
  assert.deepEqual(removePattern(['acme/*'], 'nope/x'), ['acme/*'])
})

test('saveProblem refuses an empty list and one over 25', () => {
  assert.match(saveProblem([]) ?? '', /at least one/i)
  assert.equal(saveProblem(['acme/*']), null)
  assert.equal(saveProblem(Array.from({ length: MAX_PATTERNS }, (_, i) => `acme/r${i}`)), null)
  assert.match(saveProblem(Array.from({ length: MAX_PATTERNS + 1 }, (_, i) => `acme/r${i}`)) ?? '', /25/)
})

test('addCheck says nothing for an empty box, the pattern for a good one, the reason for a bad one', () => {
  assert.deepEqual(addCheck('', []), { state: 'empty' })
  assert.deepEqual(addCheck('   ', []), { state: 'empty' })
  assert.deepEqual(addCheck('Acme/*', []), { state: 'ok', pattern: 'acme/*' })
  const bad = addCheck('acme/re*', [])
  assert.equal(bad.state, 'error')
  const dup = addCheck('acme/*', ['acme/*'])
  assert.equal(dup.state, 'error')
})

test('matchedText: "N repos matched", singular for one', () => {
  assert.equal(matchedText(0), '0 repos matched')
  assert.equal(matchedText(1), '1 repo matched')
  assert.equal(matchedText(14), '14 repos matched')
})

test('matchedCount counts the distinct seen repos a pattern covers', () => {
  const seen = ['acme/a', 'acme/b', 'ACME/b', 'jane/widgets', 'acme-labs/x']
  assert.equal(matchedCount('acme/*', seen), 2)
  assert.equal(matchedCount('jane/widgets', seen), 1)
  assert.equal(matchedCount('zed/*', seen), 0)
})

test('suggestions: seen repos not covered by the list, once each, in order', () => {
  const seen = [{ repo: 'acme/a' }, { repo: 'jane/widgets' }, { repo: 'jane/notes' }, { repo: 'jane/widgets' }, { repo: 'acme/b' }]
  assert.deepEqual(suggestions(seen, ['acme/*']), ['jane/notes', 'jane/widgets'])
  assert.deepEqual(suggestions(seen, ['acme/*', 'jane/widgets']), ['jane/notes'])
  assert.deepEqual(suggestions(seen, []), ['acme/a', 'acme/b', 'jane/notes', 'jane/widgets'])
  assert.deepEqual(suggestions([{ repo: 'not a repo' }, { repo: 'acme/../x' }], []), [])
})

test('sameList compares in order', () => {
  assert.equal(sameList(['a/b'], ['a/b']), true)
  assert.equal(sameList(['a/b', 'c/d'], ['c/d', 'a/b']), false)
  assert.equal(sameList([], []), true)
})

test('folderProblem: empty clears, an absolute path is fine, anything else is said', () => {
  assert.equal(folderProblem(''), null)
  assert.equal(folderProblem('   '), null)
  assert.equal(folderProblem('/Users/jane/work/widgets'), null)
  assert.equal(folderProblem('/Users/jane/work with space/x'), null)
  assert.match(folderProblem('work/widgets') ?? '', /absolute/i)
  assert.match(folderProblem('~/work') ?? '', /absolute/i)
  assert.match(folderProblem('/a\nb') ?? '', /line/i)
  assert.match(folderProblem('/' + 'x'.repeat(1100)) ?? '', /long/i)
})

const SAVED: ReposSettings = { allow: ['acme/*', 'jane/widgets'], clones: { 'acme/infra': '/w/infra', 'jane/old': '/w/old' }, cloneRoot: '/w' }

test('saving the patterns sends the draft list with the saved clones and root untouched', () => {
  assert.deepEqual(savePatterns(SAVED, ['jane/widgets', 'zed/*']), { allow: ['jane/widgets', 'zed/*'], clones: SAVED.clones, cloneRoot: '/w' })
})

test('saving the clones sends the saved patterns, never an unsaved edit of them', () => {
  const out = saveClones(SAVED, ['acme/infra', 'jane/widgets'], { 'acme/infra': ' /w/infra2 ', 'jane/widgets': '/w/widgets' }, '/w2')
  assert.deepEqual(out.allow, ['acme/*', 'jane/widgets'])
  assert.equal(out.cloneRoot, '/w2')
  // a repo that has no row keeps its saved folder; a row's draft replaces it, trimmed
  assert.deepEqual(out.clones, { 'jane/old': '/w/old', 'acme/infra': '/w/infra2', 'jane/widgets': '/w/widgets' })
})

test('an empty folder draft removes that repo\'s override, and an empty root clears the root', () => {
  const out = saveClones(SAVED, ['acme/infra'], { 'acme/infra': '  ' }, '  ')
  assert.deepEqual(out.clones, { 'jane/old': '/w/old' })
  assert.equal(out.cloneRoot, null)
})

test('clonesFrom keys are lowercase, like the file', () => {
  assert.deepEqual(clonesFrom({}, ['Acme/Infra'], { 'Acme/Infra': '/w/i' }), { 'acme/infra': '/w/i' })
})

test('what was written names the entries as the server stored them', () => {
  assert.equal(wrotePatterns(['acme/*']), 'Saved 1 repository pattern: acme/*')
  assert.equal(wrotePatterns(['acme/*', 'jane/widgets']), 'Saved 2 repository patterns: acme/*, jane/widgets')
  assert.equal(wroteClones({}, null), 'Saved: no clone folders set, no clones folder')
  assert.equal(wroteClones({ 'acme/infra': '/w/infra' }, '/w'), 'Saved 1 clone folder (acme/infra) and the clones folder /w')
  assert.equal(wroteClones({ 'a/b': '/x', 'c/d': '/y' }, null), 'Saved 2 clone folders (a/b, c/d), no clones folder')
})

test('howText says where a clone was found', () => {
  assert.equal(howText('settings'), 'set in Settings')
  assert.equal(howText('root'), 'in the clones folder')
  assert.equal(howText('projects'), 'in ~/projects')
  assert.equal(howText(null), '')
  assert.equal(howText('something else'), '')
})

// The page mirrors the server's grammar so it can say "no" while the person types; the server stays the judge. Both are held to
// one table here (the server's answers are in testdata/answers/repomatch.json).
const PATTERN_CASES = [
  ...GOOD.map(([raw]) => raw),
  ...BAD,
  '*', '*/*', '*/widgets', 'acme/**', '.../*', '.../x', 'acme/...', 'a/b...c', 'acme/.github', ' ACME/* ', 'acme/wid\ngets', 'acme/widgets/',
  'ac\u212Ae/widgets', 'ac\u212B/widgets', '\u0430cme/*', 'acme/wid\u0435gets', 'acme/widgets\n', '\tacme/widgets', 'jane/widgets2', 'a_b/c.d', '-/-', '_/_', '-a/b', '--help/x', 'acme/-x', 'acme/--x', 'a/_x', 'a/.x', '.x/y', '-a/*', '_a/*', '.a/*', 'a-/b-',
]
const REPO_CASES = ['acme/widgets', 'Acme/Widgets', 'acme', 'acme/', '/widgets', 'a/b/c', '../x', 'acme/..', 'acme/*', 'acme/wid gets', 'acme/widgets?x=1', 'acme/widgets#1', 'acme/widgets\n', 'acme/wid\u0435gets', 'ac\u212Ae/widgets', ' acme/widgets', 'acme/.github', 'acme/...', '...', '', '-a/b', 'acme/-x', '_a/b', 'a/_b']

test('parsePattern agrees with the server\'s on every case, accepted or refused and the stored form', () => {
  for (const raw of PATTERN_CASES) {
    const mine = parsePattern(raw)
    const theirs = serverParse(raw)
    assert.equal(mine.ok, theirs.ok, JSON.stringify(raw))
    if (mine.ok && theirs.ok) assert.equal(mine.pattern, theirs.pattern, JSON.stringify(raw))
  }
})

test('covered agrees with the server\'s allowed() for every repo against several lists', () => {
  const lists = [['acme/*'], ['acme/widgets'], ['acme/*', 'jane/widgets'], ['ACME/Widgets'], ['acme-labs/*'], ['ack/*'], [], null]
  for (const list of lists) for (const repo of REPO_CASES) assert.equal(covered(repo, list), serverAllowed(repo, list), `${JSON.stringify(repo)} in ${JSON.stringify(list)}`)
})

test('after a row is removed, focus goes to the row that took its place, else the one before, else nowhere (the add box)', () => {
  // `left` is how many rows remain, `at` the index the removed row had
  assert.equal(nextFocusIndex(3, 0), 0)
  assert.equal(nextFocusIndex(3, 1), 1)
  assert.equal(nextFocusIndex(2, 2), 1)
  assert.equal(nextFocusIndex(1, 0), 0)
  assert.equal(nextFocusIndex(0, 0), -1)
  assert.equal(nextFocusIndex(0, 5), -1)
})

test('ownerCountText says "first 100" when the list is as long as the most GitHub is asked for', () => {
  assert.equal(ownerCountText(1), '1 repo')
  assert.equal(ownerCountText(4), '4 repos')
  assert.equal(ownerCountText(99), '99 repos')
  assert.equal(ownerCountText(100), 'first 100 repos')
  assert.equal(ownerCountText(0), '0 repos')
})
