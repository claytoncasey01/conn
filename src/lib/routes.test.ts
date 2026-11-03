import assert from 'node:assert/strict'
import { test } from 'node:test'
import { href, isAgentId, isHistoryId, needsSetup, parseRoute, type Route } from './routes.ts'

const UUID = '0f3c2a9e-1b2d-4c5e-8f70-123456789abc'

test('the live routes parse as before', () => {
  assert.deepEqual(parseRoute(''), { page: 'sessions' })
  assert.deepEqual(parseRoute('#/'), { page: 'sessions' })
  assert.deepEqual(parseRoute('#/nowhere'), { page: 'sessions' })
  assert.deepEqual(parseRoute('#/s/abc'), { page: 'overview', sid: 'abc' })
  assert.deepEqual(parseRoute('#/s/abc/advisor'), { page: 'advisor', sid: 'abc' })
  assert.deepEqual(parseRoute('#/s/abc/a'), { page: 'agent', sid: 'abc', aid: null })
  assert.deepEqual(parseRoute('#/s/abc/a/lead'), { page: 'agent', sid: 'abc', aid: 'lead' })
  assert.deepEqual(parseRoute('#/work'), { page: 'work' })
  assert.deepEqual(parseRoute('#/work/PROJ-12'), { page: 'run', key: 'PROJ-12' })
  assert.deepEqual(parseRoute('#/work/proj-12'), { page: 'work' })
  assert.deepEqual(parseRoute('#/work/PROJ-12-x'), { page: 'work' })
  assert.deepEqual(parseRoute('#/work/../x'), { page: 'work' })
  assert.deepEqual(parseRoute('#/work/nope'), { page: 'work' })
})

test('the History routes parse', () => {
  assert.deepEqual(parseRoute('#/history'), { page: 'history' })
  assert.deepEqual(parseRoute(`#/h/${UUID}`), { page: 'entry', uuid: UUID })
  assert.deepEqual(parseRoute(`#/h/${UUID}/advisor`), { page: 'entryAdvisor', uuid: UUID })
  assert.deepEqual(parseRoute(`#/h/${UUID}/a`), { page: 'entryAgent', uuid: UUID, aid: null })
  assert.deepEqual(parseRoute(`#/h/${UUID}/a/agent-7`), { page: 'entryAgent', uuid: UUID, aid: 'agent-7' })
  // #/h with no id is no entry at all
  assert.deepEqual(parseRoute('#/h'), { page: 'sessions' })
})

test('the Reviews route parses', () => {
  assert.deepEqual(parseRoute('#/reviews'), { page: 'reviews' })
  assert.deepEqual(parseRoute('#/reviews/extra'), { page: 'reviews' })
  assert.equal(href.reviews(), '#/reviews')
  assert.deepEqual(parseRoute(href.reviews()), { page: 'reviews' })
})

test('#/myprs parses and href.myprs round-trips', () => {
  assert.deepEqual(parseRoute('#/myprs'), { page: 'myprs' })
  assert.deepEqual(parseRoute('#/myprs/extra'), { page: 'myprs' })
  assert.equal(href.myprs(), '#/myprs')
  assert.deepEqual(parseRoute(href.myprs()), { page: 'myprs' })
})

test('#/themes parses and href.themes round-trips', () => {
  assert.deepEqual(parseRoute('#/themes'), { page: 'themes' })
  assert.deepEqual(parseRoute('#/themes/extra'), { page: 'themes' })
  assert.equal(href.themes(), '#/themes')
  assert.deepEqual(parseRoute(href.themes()), { page: 'themes' })
})

test('#/settings parses and href.settings round-trips', () => {
  assert.deepEqual(parseRoute('#/settings'), { page: 'settings' })
  assert.deepEqual(parseRoute('#/settings/extra'), { page: 'settings' })
  assert.equal(href.settings(), '#/settings')
  assert.deepEqual(parseRoute(href.settings()), { page: 'settings' })
})

test('adding #/settings changes no other route', () => {
  assert.deepEqual(parseRoute('#/setting'), { page: 'sessions' })
  assert.deepEqual(parseRoute('#/themes'), { page: 'themes' })
  assert.deepEqual(parseRoute('#/reviews'), { page: 'reviews' })
  assert.deepEqual(parseRoute('#/s/settings'), { page: 'overview', sid: 'settings' })
  assert.deepEqual(parseRoute('#/work/settings'), { page: 'work' })
})

const EVERY: Route[] = [
  { page: 'sessions' },
  { page: 'overview', sid: 'abc' },
  { page: 'advisor', sid: 'abc' },
  { page: 'agent', sid: 'abc', aid: null },
  { page: 'work' },
  { page: 'run', key: 'PROJ-1' },
  { page: 'reviews' },
  { page: 'myprs' },
  { page: 'history' },
  { page: 'themes' },
  { page: 'settings' },
  { page: 'entry', uuid: UUID },
  { page: 'entryAgent', uuid: UUID, aid: null },
  { page: 'entryAdvisor', uuid: UUID },
]

test('needsSetup: while setup is true every route but the theme gallery and Settings redirects', () => {
  for (const r of EVERY) assert.equal(needsSetup(r, { setup: true }), r.page !== 'themes' && r.page !== 'settings', r.page)
})

test('needsSetup: once setup is false no route redirects', () => {
  for (const r of EVERY) assert.equal(needsSetup(r, { setup: false }), false, r.page)
})

test('needsSetup: no snapshot yet, or a server that does not report setup, never redirects', () => {
  for (const r of EVERY) {
    assert.equal(needsSetup(r, null), false, r.page)
    assert.equal(needsSetup(r, undefined), false, r.page)
    assert.equal(needsSetup(r, {}), false, r.page)
  }
})

test('every href parses back to its route', () => {
  assert.deepEqual(parseRoute(href.sessions()), { page: 'sessions' })
  assert.deepEqual(parseRoute(href.overview('abc')), { page: 'overview', sid: 'abc' })
  assert.deepEqual(parseRoute(href.advisor('abc')), { page: 'advisor', sid: 'abc' })
  assert.deepEqual(parseRoute(href.agent('abc', 'lead')), { page: 'agent', sid: 'abc', aid: 'lead' })
  assert.deepEqual(parseRoute(href.agent('abc', null)), { page: 'agent', sid: 'abc', aid: null })
  assert.deepEqual(parseRoute(href.history()), { page: 'history' })
  assert.deepEqual(parseRoute(href.entry(UUID)), { page: 'entry', uuid: UUID })
  assert.deepEqual(parseRoute(href.entryAdvisor(UUID)), { page: 'entryAdvisor', uuid: UUID })
  assert.deepEqual(parseRoute(href.entryAgent(UUID, 'a1')), { page: 'entryAgent', uuid: UUID, aid: 'a1' })
  assert.deepEqual(parseRoute(href.entryAgent(UUID)), { page: 'entryAgent', uuid: UUID, aid: null })
})

test('the live hrefs are unchanged', () => {
  assert.equal(href.sessions(), '#/')
  assert.equal(href.overview('abc'), '#/s/abc')
  assert.equal(href.advisor('abc'), '#/s/abc/advisor')
  assert.equal(href.agent('abc', 'x'), '#/s/abc/a/x')
  assert.equal(href.agent('abc'), '#/s/abc/a')
})

test('an entry href cannot be steered by a slash or a hash in an id', () => {
  assert.equal(href.entry('a/b#c'), '#/h/a%2Fb%23c')
  assert.equal(href.entryAgent(UUID, '../x'), `#/h/${UUID}/a/..%2Fx`)
})

test('isHistoryId and isAgentId accept only the shapes the server accepts', () => {
  assert.equal(isHistoryId(UUID), true)
  assert.equal(isHistoryId(UUID.toUpperCase()), true)
  for (const bad of ['', 'abc', `${UUID}x`, `../${UUID}`, UUID.replace(/-/g, ''), '%2e%2e', `${UUID}/agents/lead`]) assert.equal(isHistoryId(bad), false, bad)
  assert.equal(isAgentId('lead'), true)
  assert.equal(isAgentId('a1b2-c3_d4'), true)
  for (const bad of ['', '../x', 'a/b', 'a b', 'x'.repeat(129), '%2F']) assert.equal(isAgentId(bad), false, bad)
})
