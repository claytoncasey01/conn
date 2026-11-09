import assert from 'node:assert/strict'
import { test } from 'node:test'
import { loadChoice, preselect, saveChoice, startBody, toggleRepo } from './repoChoice.ts'
import type { BuildRepo, WorkBuild } from './types.ts'

const repo = (name: string, ready = true): BuildRepo => ({ repo: name, base: ready ? 'main' : null, ready, reason: ready ? null : `No local clone of ${name}: set a folder in Settings.` })
const build = (repos: BuildRepo[], def: string | null = null): WorkBuild => ({ repos, default: def, layout: 'shared' })

const store = (init: Record<string, string> = {}) => ({
  data: init,
  getItem: (k: string) => init[k] ?? null,
  setItem: (k: string, v: string) => void (init[k] = v),
})
const broken = {
  getItem: () => {
    throw new Error('denied')
  },
  setItem: () => {
    throw new Error('denied')
  },
}

test('the remembered choice comes back as it was saved', () => {
  const s = store()
  saveChoice({ repos: ['acme/widgets', 'acme/gadgets'], layout: 'split' }, s)
  assert.deepEqual(loadChoice(s), { repos: ['acme/widgets', 'acme/gadgets'], layout: 'split' })
})

test('nothing remembered, something unreadable, or no storage at all is no choice', () => {
  const none = { repos: [], layout: null }
  assert.deepEqual(loadChoice(store()), none)
  assert.deepEqual(loadChoice(store({ 'conn.work.repos': 'not json', 'conn.work.layout': 'sideways' })), none)
  assert.deepEqual(loadChoice(store({ 'conn.work.repos': '[1,"acme/widgets"]' })), { repos: ['acme/widgets'], layout: null })
  assert.deepEqual(loadChoice(broken), none)
  assert.deepEqual(loadChoice(null), none)
  saveChoice({ repos: ['acme/widgets'], layout: 'shared' }, broken)
})

test('the preselection is what was remembered and is still ready, in its order and the list spelling', () => {
  const b = build([repo('acme/gadgets'), repo('acme/widgets'), repo('acme/broken', false)], 'acme/gadgets')
  assert.deepEqual(preselect(b, ['ACME/Widgets', 'acme/gone', 'acme/broken', 'acme/gadgets']), ['acme/widgets', 'acme/gadgets'])
})

test('with nothing remembered still ready the preselection is the default, else nothing', () => {
  assert.deepEqual(preselect(build([repo('acme/widgets')], 'acme/widgets'), ['acme/gone']), ['acme/widgets'])
  assert.deepEqual(preselect(build([repo('acme/widgets', false)], 'acme/widgets'), []), [])
  assert.deepEqual(preselect(build([repo('acme/widgets')]), []), [])
})

test('ticking adds a repository after the ones chosen; unticking takes it out', () => {
  assert.deepEqual(toggleRepo(['acme/widgets'], 'acme/gadgets'), ['acme/widgets', 'acme/gadgets'])
  assert.deepEqual(toggleRepo(['acme/widgets', 'acme/gadgets'], 'acme/widgets'), ['acme/gadgets'])
})

test('START sends the repositories in order, and the layout only when there are several', () => {
  assert.deepEqual(startBody('haiku', false, ['acme/widgets'], 'split'), { model: 'haiku', force: undefined, repos: ['acme/widgets'] })
  assert.deepEqual(startBody('sonnet', true, ['acme/widgets', 'acme/gadgets'], 'split'), { model: 'sonnet', force: true, repos: ['acme/widgets', 'acme/gadgets'], layout: 'split' })
})

test('START sends the advisor only when one was picked', () => {
  assert.deepEqual(startBody('haiku', false, ['acme/widgets'], 'shared', ''), { model: 'haiku', force: undefined, repos: ['acme/widgets'] })
  assert.deepEqual(startBody('haiku', false, ['acme/widgets'], 'shared', 'opus'), { model: 'haiku', force: undefined, repos: ['acme/widgets'], advisor: 'opus' })
})
