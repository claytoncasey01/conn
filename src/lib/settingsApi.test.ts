import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { browseGithub, checkModule, getSettings, putSection, withSavedRepos, withSavedSection, withStatus, type SettingsView } from './settingsApi.ts'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

const seen: { url: string; init?: RequestInit }[] = []
const reply = (status: number, body: unknown) => {
  seen.length = 0
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    seen.push({ url, init })
    return new Response(typeof body === 'string' ? body : JSON.stringify(body), { status })
  }) as unknown as typeof fetch
}
const lost = () => {
  seen.length = 0
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
}

const SETTINGS = {
  version: 1,
  repos: { allow: ['acme/*', 'jane/widgets'], clones: { 'acme/infra': '/w/infra' }, cloneRoot: '/w' },
  github: { enabled: true },
  slack: { enabled: false, channels: [], dms: false, draftChannel: null },
  atlassian: { enabled: false, jira: { projects: [], buildRepo: null, baseBranch: null, baseBranches: {}, reviewMove: 'all', sessionLayout: 'shared' } },
  reviews: { requested: true, allOpen: false },
}
const VIEW = {
  settings: SETTINGS,
  setup: false,
  seen: [
    { repo: 'acme/infra', from: 'session' },
    { repo: 'jane/widgets', from: 'myprs' },
  ],
  clones: [
    { repo: 'acme/infra', path: '/w/infra', how: 'settings' },
    { repo: 'jane/widgets', path: null, how: null },
  ],
  matched: { 'acme/*': 1, 'jane/widgets': 1 },
  dropped: [],
  backedUp: null,
}

test('getSettings GETs /api/settings and reads the view', async () => {
  reply(200, VIEW)
  const r = await getSettings()
  assert.equal(seen[0].url, '/api/settings')
  assert.ok(r.ok)
  if (!r.ok) return
  assert.deepEqual(r.view.settings.repos, SETTINGS.repos)
  assert.equal(r.view.setup, false)
  assert.deepEqual(r.view.seen, VIEW.seen)
  assert.deepEqual(r.view.clones, VIEW.clones)
  assert.deepEqual(r.view.matched, VIEW.matched)
  assert.deepEqual(r.view.dropped, [])
  assert.equal(r.view.backedUp, null)
})

test('the file a save kept comes through as its path, and anything else is none', async () => {
  reply(200, { ...VIEW, backedUp: '/data/settings.json.bak' })
  const r = await getSettings()
  assert.ok(r.ok && r.view.backedUp === '/data/settings.json.bak')
  for (const bad of [7, '', {}, undefined]) {
    reply(200, { ...VIEW, backedUp: bad })
    const x = await getSettings()
    assert.ok(x.ok && x.view.backedUp === null)
  }
})

test('what the server dropped from a hand-edited file comes through as text, and a server that does not say has none', async () => {
  reply(200, { ...VIEW, dropped: ['repos.allow "acme/re*" dropped: * only as owner/*', 7, '', 'settings.json is not valid JSON'] })
  const r = await getSettings()
  assert.ok(r.ok && r.view.dropped.length === 2 && r.view.dropped[0].startsWith('repos.allow'))
  const { dropped: _, ...old } = VIEW
  reply(200, old)
  const o = await getSettings()
  assert.ok(o.ok && o.view.dropped.length === 0)
})

test('in setup the allow list is null and the view says so', async () => {
  reply(200, { ...VIEW, setup: true, settings: { ...SETTINGS, repos: { allow: null, clones: {}, cloneRoot: null } }, seen: [], clones: [], matched: {} })
  const r = await getSettings()
  assert.ok(r.ok && r.view.setup && r.view.settings.repos.allow === null)
})

test('getSettings drops entries it cannot read instead of failing the page', async () => {
  reply(200, {
    ...VIEW,
    seen: [{ repo: 'acme/infra', from: 'session' }, { repo: 5, from: 'session' }, { repo: 'x/y', from: 'carrier pigeon' }, null],
    clones: [{ repo: 'acme/infra', path: '/w/infra', how: 'settings' }, { repo: 'a/b', path: 7, how: null }, 'x'],
    matched: { 'acme/*': 1, bad: 'many' },
  })
  const r = await getSettings()
  assert.ok(r.ok)
  if (!r.ok) return
  assert.deepEqual(r.view.seen, [{ repo: 'acme/infra', from: 'session' }])
  assert.deepEqual(r.view.clones, [{ repo: 'acme/infra', path: '/w/infra', how: 'settings' }])
  assert.deepEqual(r.view.matched, { 'acme/*': 1 })
})

test('getSettings: an answer with no settings in it, an old server and a lost request are errors, never throws', async () => {
  reply(200, { nothing: true })
  const bad = await getSettings()
  assert.ok(!bad.ok && /read/i.test(bad.error))
  reply(404, 'not found')
  const old = await getSettings()
  assert.ok(!old.ok && /restart/i.test(old.error))
  reply(500, { error: 'disk on fire' })
  assert.deepEqual(await getSettings(), { ok: false, error: 'disk on fire' })
  reply(500, 'not json')
  assert.deepEqual(await getSettings(), { ok: false, error: 'HTTP 500' })
  lost()
  assert.deepEqual(await getSettings(), { ok: false, error: 'connection refused' })
})

test('putSection PUTs the section body to /api/settings/<section> with x-conn and a JSON body', async () => {
  reply(200, { ok: true })
  const body = { allow: ['acme/*'], clones: {}, cloneRoot: null }
  assert.deepEqual(await putSection('repos', body), { ok: true })
  assert.equal(seen[0].url, '/api/settings/repos')
  assert.equal(seen[0].init?.method, 'PUT')
  const headers = seen[0].init?.headers as Record<string, string>
  assert.equal(headers['x-conn'], '1')
  assert.equal(headers['content-type'], 'application/json')
  assert.deepEqual(JSON.parse(String(seen[0].init?.body)), body)
})

test('a refused save returns the server\'s words, so the page keeps the draft and shows them', async () => {
  reply(400, { error: 'repos.allow[1]: "acme/re*" is not owner/repo or owner/*' })
  assert.deepEqual(await putSection('repos', { allow: ['acme/re*'], clones: {}, cloneRoot: null }), { ok: false, error: 'repos.allow[1]: "acme/re*" is not owner/repo or owner/*' })
  reply(403, { error: 'forbidden origin' })
  assert.deepEqual(await putSection('repos', {}), { ok: false, error: 'forbidden origin' })
  reply(500, 'oops')
  assert.deepEqual(await putSection('repos', {}), { ok: false, error: 'HTTP 500' })
  reply(200, { fine: 1 })
  const noOk = await putSection('repos', {})
  assert.equal(noOk.ok, false)
  lost()
  assert.deepEqual(await putSection('repos', {}), { ok: false, error: 'connection refused' })
})

test('browseGithub POSTs to /api/settings/browse with x-conn, only when called', async () => {
  reply(200, { owners: [{ owner: 'acme', repos: ['acme/infra', 'acme/app'] }, { owner: 'jane', repos: ['jane/widgets'] }] })
  assert.equal(seen.length, 0)
  const r = await browseGithub()
  assert.equal(seen.length, 1)
  assert.equal(seen[0].url, '/api/settings/browse')
  assert.equal(seen[0].init?.method, 'POST')
  assert.equal((seen[0].init?.headers as Record<string, string>)['x-conn'], '1')
  assert.deepEqual(r, { ok: true, owners: [{ owner: 'acme', repos: ['acme/infra', 'acme/app'] }, { owner: 'jane', repos: ['jane/widgets'] }], failed: [] })
})

test('browseGithub keeps only owners and repos that are names, and reads errors as results', async () => {
  reply(200, { owners: [{ owner: 'acme', repos: ['acme/infra', 'acme/wid gets', 7, '../x'] }, { owner: 'bad owner', repos: [] }, { owner: 'jane' }, 'x'] })
  assert.deepEqual(await browseGithub(), { ok: true, owners: [{ owner: 'acme', repos: ['acme/infra'] }], failed: [] })
  reply(200, { owners: [{ owner: 'jane', repos: ['jane/widgets'] }], failed: ['acme', 'bad owner', 3] })
  assert.deepEqual(await browseGithub(), { ok: true, owners: [{ owner: 'jane', repos: ['jane/widgets'] }], failed: ['acme'] })
  reply(200, { nope: 1 })
  const bad = await browseGithub()
  assert.ok(!bad.ok)
  reply(502, { error: 'gh is not signed in' })
  assert.deepEqual(await browseGithub(), { ok: false, error: 'gh is not signed in' })
  lost()
  assert.deepEqual(await browseGithub(), { ok: false, error: 'connection refused' })
})

test('withSavedRepos puts what was sent in the view, ends setup for a list, and leaves the rest as it was', async () => {
  reply(200, { ...VIEW, setup: true, settings: { ...SETTINGS, repos: { allow: null, clones: {}, cloneRoot: null } } })
  const r = await getSettings()
  assert.ok(r.ok)
  if (!r.ok) return
  const sent = { allow: ['jane/widgets'], clones: { 'jane/widgets': '/w/widgets' }, cloneRoot: '/w' }
  const next: SettingsView = withSavedRepos(r.view, sent)
  assert.deepEqual(next.settings.repos, sent)
  assert.equal(next.setup, false)
  assert.deepEqual(next.settings.slack, r.view.settings.slack)
  assert.deepEqual(next.seen, r.view.seen)
  assert.deepEqual(r.view.settings.repos.allow, null, 'the old view is not changed')
  // an empty list does not end setup: the server refuses it, and the page never sends one
  assert.equal(withSavedRepos(r.view, { allow: [], clones: {}, cloneRoot: null }).setup, true)
})

test('modules: each status comes through, a server that sends none is every module not checked, and an unknown state is not checked', async () => {
  reply(200, { ...VIEW, modules: { github: { state: 'ok', detail: 'Signed in as jane', at: 5 }, slack: { state: 'off', detail: 'Off: conn makes no calls', at: null }, atlassian: { state: 'bogus', detail: 'x', at: 'soon' } } })
  const r = await getSettings()
  assert.ok(r.ok)
  assert.deepEqual(r.view.modules.github, { state: 'ok', detail: 'Signed in as jane', at: 5 })
  assert.deepEqual(r.view.modules.slack, { state: 'off', detail: 'Off: conn makes no calls', at: null })
  assert.deepEqual(r.view.modules.atlassian, { state: 'unchecked', detail: 'Not checked yet', at: null })
  reply(200, VIEW)
  const none = await getSettings()
  assert.ok(none.ok)
  for (const m of ['github', 'slack', 'atlassian'] as const) assert.deepEqual(none.view.modules[m], { state: 'unchecked', detail: 'Not checked yet', at: null })
})

test('the sections the page edits are read field by field: a wrong type is the default, a draft channel that is not a channel is none', async () => {
  reply(200, { ...VIEW, settings: { ...SETTINGS, github: { enabled: 'yes' }, slack: { enabled: true, channels: ['#a', 7, null], dms: 'x', draftChannel: '#gone' }, atlassian: { enabled: 1, jira: { projects: ['PROJ', 3], buildRepo: 'acme/*', baseBranch: '--upload-pack=x' } }, reviews: 'nope' } })
  const r = await getSettings()
  assert.ok(r.ok)
  assert.deepEqual(r.view.settings.github, { enabled: true })
  assert.deepEqual(r.view.settings.slack, { enabled: true, channels: ['#a'], dms: false, draftChannel: null })
  assert.deepEqual(r.view.settings.atlassian, { enabled: false, jira: { projects: ['PROJ'], buildRepo: null, baseBranch: null, baseBranches: {}, reviewMove: 'all', sessionLayout: 'shared' } })
  assert.deepEqual(r.view.settings.reviews, { requested: true, allOpen: false })
})

test('the base branch is read as a branch name or none, and an older server that sends none is none', async () => {
  reply(200, { ...VIEW, settings: { ...SETTINGS, atlassian: { enabled: true, jira: { projects: ['PROJ'], buildRepo: null, baseBranch: 'release/2.1' } } } })
  const good = await getSettings()
  assert.ok(good.ok)
  assert.equal(good.view.settings.atlassian.jira.baseBranch, 'release/2.1')
  reply(200, { ...VIEW, settings: { ...SETTINGS, atlassian: { enabled: true, jira: { projects: ['PROJ'], buildRepo: null } } } })
  const old = await getSettings()
  assert.ok(old.ok)
  assert.equal(old.view.settings.atlassian.jira.baseBranch, null)
})

test('the per-repository branches, the review move and the layout are read as sent, a wrong value is the default, and an older server that sends none is the defaults', async () => {
  const jira = { projects: ['PROJ'], buildRepo: null, baseBranch: null }
  reply(200, { ...VIEW, settings: { ...SETTINGS, atlassian: { enabled: true, jira: { ...jira, baseBranches: { 'Acme/Api': 'develop', 'acme/web': '--x', '../x': 'main', 'acme/db': 7 }, reviewMove: 'first', sessionLayout: 'split' } } } })
  const good = await getSettings()
  assert.ok(good.ok)
  assert.deepEqual(good.view.settings.atlassian.jira.baseBranches, { 'acme/api': 'develop' })
  assert.equal(good.view.settings.atlassian.jira.reviewMove, 'first')
  assert.equal(good.view.settings.atlassian.jira.sessionLayout, 'split')
  reply(200, { ...VIEW, settings: { ...SETTINGS, atlassian: { enabled: true, jira: { ...jira, baseBranches: ['main'], reviewMove: 'sometimes', sessionLayout: 3 } } } })
  const wrong = await getSettings()
  assert.ok(wrong.ok)
  assert.deepEqual(wrong.view.settings.atlassian.jira, { ...jira, baseBranches: {}, reviewMove: 'all', sessionLayout: 'shared' })
  reply(200, { ...VIEW, settings: { ...SETTINGS, atlassian: { enabled: true, jira } } })
  const old = await getSettings()
  assert.ok(old.ok)
  assert.deepEqual(old.view.settings.atlassian.jira, { ...jira, baseBranches: {}, reviewMove: 'all', sessionLayout: 'shared' })
})

test('checkModule: a POST with the header, no body, the server’s status; a refusal is its words; a lost connection is an error', async () => {
  reply(200, { status: { state: 'ok', detail: 'Slack connector works', at: 9 } })
  const r = await checkModule('slack')
  assert.deepEqual(r, { ok: true, status: { state: 'ok', detail: 'Slack connector works', at: 9 } })
  assert.equal(seen[0].url, '/api/settings/check/slack')
  assert.equal(seen[0].init?.method, 'POST')
  assert.deepEqual(seen[0].init?.headers, { 'x-conn': '1' })
  reply(409, { error: 'Slack is off: save its switch on before checking it' })
  assert.deepEqual(await checkModule('slack'), { ok: false, error: 'Slack is off: save its switch on before checking it' })
  reply(200, { status: { state: 'weird' } })
  assert.equal((await checkModule('github')).ok, false)
  reply(500, 'not json')
  assert.deepEqual(await checkModule('github'), { ok: false, error: 'HTTP 500' })
  lost()
  assert.deepEqual(await checkModule('atlassian'), { ok: false, error: 'connection refused' })
})

test('a saved section and a new status are put into the view without touching the rest', async () => {
  reply(200, VIEW)
  const r = await getSettings()
  assert.ok(r.ok)
  const v = withSavedSection(r.view, 'reviews', { requested: false, allOpen: true })
  assert.deepEqual(v.settings.reviews, { requested: false, allOpen: true })
  assert.deepEqual(v.settings.repos, r.view.settings.repos)
  assert.deepEqual(r.view.settings.reviews, { requested: true, allOpen: false }, 'the view given is not changed')
  const w = withStatus(v, 'slack', { state: 'failing', detail: 'down', at: 1 })
  assert.equal(w.modules.slack.state, 'failing')
  assert.equal(w.modules.github.state, 'unchecked')
})
