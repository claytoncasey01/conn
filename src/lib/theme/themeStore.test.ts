// The store run for real against fakes: `$state` is only a compiler rune, so a pass-through stands in for it,
// and localStorage, fetch, matchMedia, window and document are fakes. No network, no browser.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { afterEach, test } from 'node:test'
import { fakeDoc } from './fakeDoc.ts'
import type { FakeDoc } from './fakeDoc.ts'
import { menuModel, shownVariant } from './menu.ts'
import { KEYS } from './storage.ts'
import type { Choice } from './storage.ts'
import type { ThemeEntry, ThemeManifest } from './types.ts'

const def = JSON.parse(readFileSync(new URL('../../../themes/default/theme.json', import.meta.url), 'utf8')) as ThemeManifest
const matrix: ThemeManifest = {
  id: 'matrix', name: 'Matrix', version: '1', author: '', license: '', description: '', modes: ['dark'], accent: 'fixed',
  fonts: ['Share Tech Mono'], tokens: { dark: { bg: '#010a04', ac: '#00ff41', motion: 'power', led: 'square', blur: '4px' } }, effects: [],
}
const entry = (m: ThemeManifest, source: ThemeEntry['source'] = 'builtin', css: string | null = null): ThemeEntry =>
  ({ id: m.id, source, hasCss: css !== null, cssSha256: css, manifest: m })
const choiceOf = (over: Partial<Choice> = {}) => JSON.stringify({ id: 'default', mode: 'light', accent: null, variant: null, effects: 'auto', ...over })

type Env = { ls: Map<string, string>; doc: FakeDoc; fetches: number; listeners: Record<string, (() => void)[]>; mq: { matches: boolean; fire(): void } }
const g = globalThis as unknown as Record<string, unknown>
const KEYS_SET = ['$state', 'localStorage', 'document', 'window', 'matchMedia', 'fetch', 'location']
// fetch is native, so what was there is put back rather than deleted, for the test files that run after this one
const originals = new Map(KEYS_SET.map((k) => [k, Object.hasOwn(g, k) ? { v: g[k] } : null]))
let n = 0

async function boot(init: Record<string, string>, list: ThemeEntry[] | 'fail', dark = false, search = '') {
  const ls = new Map(Object.entries(init))
  const doc = fakeDoc()
  const env: Env = { ls, doc, fetches: 0, listeners: {}, mq: { matches: dark, fire: () => {} } }
  const mqls: (() => void)[] = []
  env.mq.fire = () => mqls.forEach((f) => f())
  g.$state = (v: unknown) => v
  g.localStorage = { getItem: (k: string) => ls.get(k) ?? null, setItem: (k: string, v: string) => void ls.set(k, v), removeItem: (k: string) => void ls.delete(k) }
  g.document = doc
  g.location = { search }
  g.window = { addEventListener: (t: string, f: () => void) => void (env.listeners[t] ??= []).push(f) }
  g.matchMedia = () => Object.assign(env.mq, { addEventListener: (_: string, f: () => void) => void mqls.push(f) })
  g.fetch = async () => {
    env.fetches++
    if (list === 'fail') throw new Error('offline')
    return { ok: true, status: 200, json: async () => list }
  }
  const mod = (await import(`./theme.svelte.ts?${++n}`)) as typeof import('./theme.svelte.ts')
  return { mod, env }
}
const settle = () => new Promise((r) => setTimeout(r, 0))
const saved = (env: Env) => JSON.parse(env.ls.get(KEYS.choice) ?? 'null') as Choice

afterEach(() => {
  for (const k of KEYS_SET) {
    const o = originals.get(k)
    if (o) g[k] = o.v
    else delete g[k]
  }
})

test('(c) before the list loads, a Default choice applies Default with no error, and the cache is under default', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, [entry(def)])
  mod.initTheme()
  assert.equal(mod.theme.ready, false)
  assert.equal(mod.theme.resolved.fellBack, true)
  assert.equal(mod.theme.error, null)
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.ok(env.ls.has(KEYS.css('default')))
  await settle()
  assert.equal(mod.theme.ready, true)
  assert.equal(mod.theme.resolved.fellBack, false)
  assert.equal(mod.theme.error, null)
})

test('(a) the fallback sheet sits before the boot script style, and stays there after the theme is applied', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, [entry(def)])
  const boot1 = env.doc.createElement('style')
  boot1.id = 'conn-theme'
  env.doc.head.appendChild(boot1)
  mod.initTheme()
  await settle()
  const order = env.doc.order()
  assert.ok(order.indexOf('conn-theme-fallback') >= 0 && order.indexOf('conn-theme-fallback') < order.indexOf('conn-theme'), order.join())
  assert.equal(order.filter((i) => i === 'conn-theme').length, 1)
})

test('a stored non-default theme is left as the boot script painted it until the list arrives', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'matrix' }) }, [entry(def), entry(matrix)])
  mod.initTheme()
  assert.deepEqual(env.doc.attrs, {})
  assert.equal(env.ls.has(KEYS.css('default')), false)
  assert.equal(mod.theme.error, null)
  await settle()
  assert.equal(env.doc.attrs['data-theme'], 'matrix')
  assert.equal(env.doc.attrs['data-mode'], 'dark')
  assert.equal(env.doc.attrs['data-motion'], 'power')
  assert.ok(env.doc.el('conn-theme-fonts')?.href?.includes('Share+Tech+Mono'))
  const cached = JSON.parse(env.ls.get(KEYS.css('matrix')) ?? 'null')
  assert.ok(cached.css.startsWith('[data-theme="matrix"]'))
  assert.equal(env.ls.has(KEYS.css('default')), false)
})

test('(b) a deleted theme falls back to default, is saved as default, and its cache is dropped and never reused', async () => {
  const stale = JSON.stringify({ css: '[data-theme="gone"]{--bg:red;}', attrs: { motion: 'calm', led: 'circle', glass: 'off' }, only: null })
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'gone' }), [KEYS.css('gone')]: stale }, [entry(def)])
  mod.initTheme()
  await settle()
  assert.equal(env.ls.has(KEYS.css('gone')), false)
  assert.equal(saved(env).id, 'default')
  assert.equal(env.doc.attrs['data-theme'], 'default')
  const d = JSON.parse(env.ls.get(KEYS.css('default')) ?? 'null')
  assert.ok(d.css.includes('[data-theme="default"]') && !d.css.includes('gone'))
  // no cache is ever written under an id other than the data-theme applied
  for (const k of env.ls.keys()) if (k.startsWith('conn.theme.css.')) assert.equal(k, KEYS.css('default'))
})

test('(e) an invalid user theme named like the built-in does not shadow it', async () => {
  const bad: ThemeEntry = { id: 'default', source: 'user', hasCss: false, cssSha256: null, invalid: 'nope' }
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, [bad, entry(def)])
  mod.initTheme()
  await settle()
  assert.equal(mod.theme.resolved.entry.source, 'builtin')
  assert.equal(mod.theme.resolved.fellBack, false)
  assert.equal(env.doc.attrs['data-theme'], 'default')
})

test('a failed fetch sets error and keeps what is applied; the next focus tries again', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, 'fail')
  mod.initTheme()
  await settle()
  assert.match(mod.theme.error ?? '', /Could not load themes/)
  assert.equal(mod.theme.ready, false)
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.equal(env.fetches, 1)
  env.listeners.focus?.forEach((f) => f())
  await settle()
  assert.equal(env.fetches, 2)
})

test('(g) a user theme with CSS is not applied until acceptCss; a built-in with CSS is', async () => {
  const user = entry({ ...matrix, id: 'mine' }, 'user', 'h1')
  const builtin = entry({ ...matrix, id: 'neonish' }, 'builtin', 'h2')
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, [entry(def), user, builtin])
  mod.initTheme()
  await settle()
  mod.selectTheme('mine')
  assert.equal(mod.needsCssConsent('mine'), true)
  assert.equal(mod.theme.choice.id, 'default')
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.equal(env.doc.el('conn-theme-css'), undefined)
  mod.acceptCss('mine')
  assert.equal(mod.theme.choice.id, 'mine')
  assert.equal(saved(env).id, 'mine')
  assert.equal(env.doc.attrs['data-theme'], 'mine')
  assert.equal(env.doc.el('conn-theme-css')?.href, '/api/themes/mine/theme.css?v=h1')
  // nothing was cached for it while it waited, and now that its CSS is applied with consent the cache holds the link beside the tokens
  const cached = JSON.parse(env.ls.get(KEYS.css('mine')) ?? 'null')
  assert.equal(cached.cssUrl, '/api/themes/mine/theme.css?v=h1')
  assert.ok(!cached.css.includes('theme.css'), 'the token stylesheet is the generated one, never the theme.css text')
  mod.selectTheme('neonish')
  assert.equal(mod.theme.choice.id, 'neonish')
})

test('(f) setAccent clamps what it is given, and refuses the unusable', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, [entry(def)])
  mod.initTheme()
  await settle()
  mod.setAccent({ name: 'Wild', hue: 400, chroma: 9 })
  assert.deepEqual(saved(env).accent, { name: 'Wild', hue: 40, chroma: 0.4 })
  mod.setAccent({ name: 'Bad', hue: Number.NaN, chroma: 0.1 })
  assert.equal(saved(env).accent?.name, 'Wild')
  const css = JSON.parse(env.ls.get(KEYS.css('default')) ?? 'null').css as string
  assert.ok(css.includes('--ac:'))
})

test('system mode follows prefers-color-scheme live, a stored mode does not, and a one-mode theme keeps its mode', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ mode: 'system' }) }, [entry(def), entry(matrix)], false)
  mod.initTheme()
  await settle()
  assert.equal(env.doc.attrs['data-mode'], 'light')
  env.mq.matches = true
  env.mq.fire()
  assert.equal(env.doc.attrs['data-mode'], 'dark')
  mod.setMode('light')
  assert.equal(env.doc.attrs['data-mode'], 'light')
  env.mq.fire()
  assert.equal(env.doc.attrs['data-mode'], 'light')
  mod.selectTheme('matrix')
  mod.setMode('light')
  assert.equal(env.doc.attrs['data-mode'], 'dark')
})

test('(b) a theme deleted while the app is open falls back on the next focus, with its cache dropped', async () => {
  const list = [entry(def), entry(matrix)]
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'matrix' }) }, list)
  mod.initTheme()
  await settle()
  assert.ok(env.ls.has(KEYS.css('matrix')))
  list.splice(1, 1)
  env.listeners.focus?.forEach((f) => f())
  await settle()
  assert.equal(env.ls.has(KEYS.css('matrix')), false)
  assert.equal(saved(env).id, 'default')
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.equal(env.doc.el('conn-theme-fonts')?.href?.includes('Share'), false)
  assert.equal(mod.theme.error, null)
})

test('(I1) a ?theme= and ?mode= pin drives the look for this load only, and storage is not touched', async () => {
  const init = { [KEYS.choice]: choiceOf({ mode: 'light' }) }
  const { mod, env } = await boot({ ...init }, [entry(def), entry(matrix)], false, '?theme=matrix&mode=dark')
  mod.initTheme()
  assert.equal(env.doc.attrs['data-theme'], undefined)
  await settle()
  assert.equal(env.doc.attrs['data-theme'], 'matrix')
  assert.equal(env.doc.attrs['data-mode'], 'dark')
  assert.equal(env.doc.attrs['data-motion'], 'power')
  assert.equal(env.ls.get(KEYS.choice), init[KEYS.choice])
  // the mode alone pins too, on the stored theme
  const m = await boot({ [KEYS.choice]: choiceOf({ mode: 'light' }) }, [entry(def)], false, '?mode=dark')
  m.mod.initTheme()
  assert.equal(m.env.doc.attrs['data-mode'], 'dark')
  await settle()
  assert.equal(m.env.doc.attrs['data-mode'], 'dark')
})

test('(I1) a pinned id that is not listed shows Default and leaves the stored choice and caches alone', async () => {
  const ghost = JSON.stringify({ css: '[data-theme="ghost"]{--bg:red;}', attrs: { motion: 'calm', led: 'circle', glass: 'off' }, only: null })
  const init = { [KEYS.choice]: choiceOf({ id: 'matrix' }), [KEYS.css('ghost')]: ghost }
  const { mod, env } = await boot({ ...init }, [entry(def), entry(matrix)], false, '?theme=ghost')
  mod.initTheme()
  await settle()
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.equal(env.ls.get(KEYS.choice), init[KEYS.choice])
  assert.equal(env.ls.get(KEYS.css('ghost')), ghost)
  assert.equal(mod.theme.error, null)
})

test('(I1) a pin cannot bypass consent: a pinned user theme with unaccepted CSS gets no theme.css link', async () => {
  const user = entry({ ...matrix, id: 'mine' }, 'user', 'h1')
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, [entry(def), user], false, '?theme=mine')
  mod.initTheme()
  await settle()
  assert.equal(env.doc.attrs['data-theme'], 'mine')
  assert.equal(env.doc.el('conn-theme-css'), undefined)
  assert.equal(JSON.parse(env.ls.get(KEYS.css('mine')) ?? 'null').cssUrl, null, 'and no stylesheet link is cached for the next cold start')
})

test('a link the boot script made is adopted once the list arrives, and a changed theme version updates it', async () => {
  const sha = 'a'.repeat(64)
  const href = (v: string) => `/api/themes/neonish/theme.css?v=${v}`
  const neonish = (v: string) => entry({ ...matrix, id: 'neonish' }, 'builtin', v)
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'neonish' }) }, [entry(def), neonish(sha)])
  const booted = env.doc.createElement('link')
  booted.id = 'conn-theme-css'
  booted.href = href(sha)
  env.doc.head.appendChild(booted)
  const writes = env.doc.hrefWrites(booted)
  mod.initTheme()
  await settle()
  assert.equal(env.doc.order().filter((i) => i === 'conn-theme-css').length, 1)
  assert.equal(env.doc.el('conn-theme-css'), booted)
  assert.equal(env.doc.hrefWrites(booted), writes, 'adopted, not reloaded')
  assert.equal(JSON.parse(env.ls.get(KEYS.css('neonish')) ?? 'null').cssUrl, href(sha))
  // the theme's CSS changed on the server: the next list read points the same link at the new version
  const next = 'c'.repeat(64)
  const body = [entry(def), neonish(next)]
  g.fetch = async () => ({ ok: true, status: 200, json: async () => body })
  await mod.refreshThemes()
  assert.equal(env.doc.el('conn-theme-css'), booted)
  assert.equal(booted.href, href(next))
  assert.equal(JSON.parse(env.ls.get(KEYS.css('neonish')) ?? 'null').cssUrl, href(next))
})

test('a user theme whose CSS changed after it was accepted loses its cached link at the next list read', async () => {
  const old = 'a'.repeat(64)
  const user = (v: string) => entry({ ...matrix, id: 'mine' }, 'user', v)
  const init = {
    [KEYS.choice]: choiceOf({ id: 'mine' }),
    [KEYS.cssOk]: JSON.stringify({ mine: old }),
    [KEYS.css('mine')]: JSON.stringify({ css: '[data-theme="mine"]{}', attrs: { motion: 'power', led: 'square', glass: 'on' }, only: 'dark', cssUrl: `/api/themes/mine/theme.css?v=${old}`, fontsUrl: null }),
  }
  const { mod, env } = await boot(init, [entry(def), user('b'.repeat(64))])
  const booted = env.doc.createElement('link')
  booted.id = 'conn-theme-css'
  booted.href = `/api/themes/mine/theme.css?v=${old}`
  env.doc.head.appendChild(booted)
  mod.initTheme()
  await settle()
  assert.equal(env.doc.el('conn-theme-css'), undefined, 'the stale link is removed')
  assert.equal(JSON.parse(env.ls.get(KEYS.css('mine')) ?? 'null').cssUrl, null)
})

test('(I1) an explicit choice drops the pin and persists normally', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ mode: 'light' }) }, [entry(def), entry(matrix)], false, '?theme=matrix&mode=dark')
  mod.initTheme()
  await settle()
  mod.setMode('light')
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.equal(env.doc.attrs['data-mode'], 'light')
  assert.equal(saved(env).id, 'default')
})

test('(I1) the store exposes the pin, so the menu can say what the page shows; an explicit choice clears it', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ mode: 'light' }) }, [entry(def), entry(matrix)], false, '?theme=matrix&mode=dark')
  assert.equal(mod.theme.pin, null)
  mod.initTheme()
  await settle()
  assert.deepEqual(mod.theme.pin, { id: 'matrix', mode: 'dark' })
  // the stored choice is still the saved one: that is what a change goes back to
  assert.equal(mod.theme.choice.id, 'default')
  // picking the saved theme from the pinned one is a choice like any other: it applies and drops the pin
  mod.selectTheme('default')
  assert.equal(mod.theme.pin, null)
  assert.equal(env.doc.attrs['data-theme'], 'default')
  assert.equal(saved(env).id, 'default')
  const none = await boot({ [KEYS.choice]: choiceOf() }, [entry(def)])
  none.mod.initTheme()
  assert.equal(none.mod.theme.pin, null)
})

test('(I2) an empty list is a failed read: error set, storage and cache untouched, a later good list works', async () => {
  const cache = JSON.stringify({ css: '[data-theme="matrix"]{--bg:#010a04;}', attrs: { motion: 'power', led: 'square', glass: 'on' }, only: 'dark' })
  const init = { [KEYS.choice]: choiceOf({ id: 'matrix' }), [KEYS.css('matrix')]: cache }
  const list: ThemeEntry[] = []
  const { mod, env } = await boot({ ...init }, list)
  mod.initTheme()
  await settle()
  assert.match(mod.theme.error ?? '', /Could not load themes/)
  assert.equal(mod.theme.ready, false)
  assert.equal(env.ls.get(KEYS.choice), init[KEYS.choice])
  assert.equal(env.ls.get(KEYS.css('matrix')), cache)
  list.push(entry(def), entry(matrix))
  env.listeners.focus?.forEach((f) => f())
  await settle()
  assert.equal(mod.theme.error, null)
  assert.equal(mod.theme.ready, true)
  assert.equal(env.doc.attrs['data-theme'], 'matrix')
})

// The effects host stood in for by a recorder: what the store hands it is what the page would get
type Applied = { names: string[]; tokens: Readonly<Record<string, string>>; enabled: boolean }
const recorder = () => {
  const calls: Applied[] = []
  let disposed = 0
  return {
    calls,
    disposed: () => disposed,
    host: {
      apply: (e: { name: string }[], tokens: Readonly<Record<string, string>>, enabled: boolean) => void calls.push({ names: e.map((x) => x.name), tokens, enabled }),
      dispose: () => void disposed++,
    },
    last: () => calls[calls.length - 1],
  }
}
const rainy: ThemeManifest = { ...matrix, id: 'rainy', effects: [{ name: 'rain', options: { speed: 2 } }, { name: 'scanlines' }] }

test('effects are driven from the resolved theme: its names, its merged tokens, on for auto', async () => {
  const rec = recorder()
  const { mod } = await boot({ [KEYS.choice]: choiceOf({ id: 'rainy', mode: 'dark' }) }, [entry(def), entry(rainy)])
  mod.initTheme({ effects: rec.host })
  assert.equal(rec.calls.length, 0, 'a stored non-default theme waits for the list, as its stylesheet does')
  await settle()
  assert.deepEqual(rec.last().names, ['rain', 'scanlines'])
  assert.equal(rec.last().tokens.bg, '#010a04')
  assert.equal(rec.last().tokens.ac, '#00ff41')
  assert.equal(rec.last().enabled, true)
})

test('effects off never enables, on enables under reduced motion, auto does not', async () => {
  // the fake matchMedia answers every query with the same flag, so dark=true is also "reduced motion"
  const run = async (effects: Choice['effects']) => {
    const rec = recorder()
    const { mod } = await boot({ [KEYS.choice]: choiceOf({ id: 'rainy', mode: 'dark', effects }) }, [entry(def), entry(rainy)], true)
    mod.initTheme({ effects: rec.host })
    await settle()
    return rec.last().enabled
  }
  assert.equal(await run('auto'), false)
  assert.equal(await run('on'), true)
  assert.equal(await run('off'), false)
})

// every theme's motion (Neon's ring, the tree's packets, CSS keyframes) keys off one root marker, from the same decision as the host
test('the root is marked data-fx from the effects decision: on, off, and auto off under reduced motion; setEffects re-marks it', async () => {
  const run = async (effects: Choice['effects'], reduced: boolean) => {
    const rec = recorder()
    const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'rainy', mode: 'dark', effects }) }, [entry(def), entry(rainy)], reduced)
    mod.initTheme({ effects: rec.host })
    await settle()
    return { mod, env, rec }
  }
  for (const [effects, reduced, fx] of [['auto', false, 'on'], ['auto', true, 'off'], ['on', true, 'on'], ['off', false, 'off']] as const) {
    const { env, rec } = await run(effects, reduced)
    assert.equal(env.doc.attrs['data-fx'], fx, `${effects}, reduced ${reduced}`)
    assert.equal(rec.last().enabled, fx === 'on', 'the marker and the host agree')
  }
  const { mod, env } = await run('auto', false)
  mod.setEffects('off')
  assert.equal(env.doc.attrs['data-fx'], 'off')
  mod.setEffects('on')
  assert.equal(env.doc.attrs['data-fx'], 'on')
})

test('setEffects and a theme change re-drive the host', async () => {
  const rec = recorder()
  const { mod } = await boot({ [KEYS.choice]: choiceOf({ id: 'rainy', mode: 'dark' }) }, [entry(def), entry(rainy), entry(matrix)])
  mod.initTheme({ effects: rec.host })
  await settle()
  mod.setEffects('off')
  assert.equal(rec.last().enabled, false)
  assert.deepEqual(rec.last().names, ['rain', 'scanlines'])
  mod.setEffects('on')
  assert.equal(rec.last().enabled, true)
  mod.selectTheme('matrix')
  assert.deepEqual(rec.last().names, [])
  assert.equal(rec.last().tokens.bg, '#010a04')
})

test('a change of accent reaches the effects as new tokens', async () => {
  const rec = recorder()
  const free: ThemeManifest = { ...rainy, id: 'freebie', accent: 'free', modes: ['light', 'dark'], tokens: { light: { bg: '#fff' }, dark: { bg: '#000' } } }
  const { mod } = await boot({ [KEYS.choice]: choiceOf({ id: 'freebie', mode: 'dark' }) }, [entry(def), entry(free)])
  mod.initTheme({ effects: rec.host })
  await settle()
  const before = rec.last().tokens.ac
  mod.setAccent({ name: 'Blue', hue: 255, chroma: 0.15 })
  assert.notEqual(rec.last().tokens.ac, before)
})

test('a URL pin drives the effects too: they follow the look on screen, the choice only supplies the switch', async () => {
  const rec = recorder()
  const { mod } = await boot({ [KEYS.choice]: choiceOf({ id: 'default', effects: 'on' }) }, [entry(def), entry(rainy)], false, '?theme=rainy')
  mod.initTheme({ effects: rec.host })
  await settle()
  assert.deepEqual(rec.last().names, ['rain', 'scanlines'])
  assert.equal(rec.last().enabled, true)
})

test('the OS reduced-motion setting changing re-drives the host, so auto follows it live', async () => {
  const rec = recorder()
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'rainy', mode: 'dark' }) }, [entry(def), entry(rainy)])
  mod.initTheme({ effects: rec.host })
  await settle()
  assert.equal(rec.last().enabled, true)
  env.mq.matches = true
  env.mq.fire()
  assert.equal(rec.last().enabled, false)
})

test('the real host is built when none is given, and with no effects registered it mounts nothing and never touches the body', async () => {
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf({ id: 'rainy', mode: 'dark' }) }, [entry(def), entry(rainy)])
  mod.initTheme()
  await settle()
  assert.equal(env.doc.attrs['data-theme'], 'rainy')
  assert.equal(env.doc.el('conn-effects'), undefined)
  assert.equal(env.doc.el('conn-effects-above'), undefined)
})

test('picking CRT from Default shows its first variant checked, and an unknown stored variant shows it too', async () => {
  const crt: ThemeManifest = {
    ...matrix, id: 'crt', name: 'CRT',
    accent: { variants: [{ name: 'Green', tokens: { ac: '#8bffad' } }, { name: 'Amber', tokens: { ac: '#ffb000' } }] },
  }
  const list = [entry(def), entry(crt)]
  const shown = (mod: typeof import('./theme.svelte.ts')) => {
    const m = menuModel(mod.theme.list, mod.theme.choice.id)
    return shownVariant(m.variants, mod.theme.choice.variant)
  }
  const { mod, env } = await boot({ [KEYS.choice]: choiceOf() }, list)
  mod.initTheme()
  await settle()
  mod.selectTheme('crt')
  assert.equal(saved(env).variant, null)
  assert.equal(shown(mod), 'Green')
  assert.equal(mod.theme.resolved.tokens.ac, '#8bffad')
  mod.setVariant('Amber')
  assert.equal(shown(mod), 'Amber')
  const stale = await boot({ [KEYS.choice]: choiceOf({ id: 'crt', mode: 'dark', variant: 'Gone' }) }, list)
  stale.mod.initTheme()
  await settle()
  assert.equal(shown(stale.mod), 'Green')
  assert.equal(stale.mod.theme.resolved.tokens.ac, '#8bffad')
})
