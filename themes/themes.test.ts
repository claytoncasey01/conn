// Every built-in theme folder under themes/ is held to the same bar the server will hold it to.
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import { contrast, parseColor, type Rgba } from '../src/lib/theme/color.ts'
import { LIMITS, SLOTS } from '../src/lib/theme/tokens.ts'
import { checkTheme, parseManifest } from '../src/lib/theme/validate.ts'
import type { Mode, ThemeManifest } from '../src/lib/theme/types.ts'

const root = new URL('.', import.meta.url).pathname
const folders = readdirSync(root, { withFileTypes: true })
  .filter((d) => d.isDirectory() && !d.name.startsWith('_'))
  .map((d) => d.name)
  .sort()
const read = (id: string) => readFileSync(`${root}${id}/theme.json`, 'utf8')
const ctx = { source: 'builtin' as const, builtinIds: new Set(folders) }

function load(id: string): ThemeManifest {
  const r = parseManifest(read(id), ctx)
  assert.ok(r.ok, r.ok ? '' : `${id}: ${r.errors.join('; ')}`)
  return r.manifest
}

test('Default is there', () => {
  assert.ok(folders.includes('default'), `built-in folders: ${folders.join(', ')}`)
})

test('Default defines every slot in both modes: it is the fallback, so it has no gaps', () => {
  const d = load('default')
  assert.deepEqual(d.modes, ['light', 'dark'])
  for (const mode of ['light', 'dark'] as Mode[]) {
    const missing = Object.keys(SLOTS).filter((s) => !d.tokens[mode]?.[s])
    assert.deepEqual(missing, [], `${mode} lacks ${missing.join(', ')}`)
  }
})

test('Default is a free-accent theme with the Plex fonts and no effects', () => {
  const d = load('default')
  assert.equal(d.accent, 'free')
  assert.deepEqual(d.fonts, ['IBM Plex Sans', 'IBM Plex Mono'])
  assert.deepEqual(d.effects, [])
})

test('Neon is today\'s look: dark, a fixed accent, Chakra Petch and JetBrains Mono, power motion, no effects', () => {
  assert.ok(folders.includes('neon'), `built-in folders: ${folders.join(', ')}`)
  const n = load('neon')
  assert.deepEqual(n.modes, ['dark'])
  assert.equal(n.accent, 'fixed')
  assert.deepEqual(n.fonts, ['Chakra Petch', 'JetBrains Mono'])
  assert.deepEqual(n.effects, [])
  const t = n.tokens.dark ?? {}
  const ruled = { motion: 'power', led: 'circle', case: 'uppercase', radius: '4px', tracking: '.14em', blur: '0px', glow: '1' }
  for (const [slot, v] of Object.entries(ruled)) assert.equal(t[slot], v, slot)
})

test('Neon fills every slot, so nothing it paints falls back to Default', () => {
  const t = load('neon').tokens.dark ?? {}
  const missing = Object.keys(SLOTS).filter((s) => !t[s])
  assert.deepEqual(missing, [], `neon lacks ${missing.join(', ')}`)
})

test('Neon keeps the colors the code uses today, and its core contrast', () => {
  const t = load('neon').tokens.dark ?? {}
  const known = {
    bg: '#05060d', panel: '#0b0e1c', tx: '#d6dcf0', mu: '#98a2bd', ac: '#5ad7ff', onac: '#021a22',
    ok: '#6fd6a0', warn: '#ffd166', bad: '#ff6b6b', idle: '#8e9bc6', info: '#7f9cff',
    'k-bug': '#ff6b6b', 'k-epic': '#b69cff', 'k-task': '#7fd4ff', 'k-story': '#6fd6a0', 'k-subtask': '#3fe0b4', teammate: '#ff9ad5',
    'role-lead': '#ff9a5a', 'role-agent': '#7f9cff', 'role-advisor': '#b69cff', edge: '#7f9cff', stall: '#ff9a5a',
  }
  for (const [slot, v] of Object.entries(known)) assert.equal(t[slot], v, slot)
  const ratio = (fg: string, on: string) => contrast(parseColor(t[fg]) as Rgba, parseColor(t[on]) as Rgba).toFixed(1)
  assert.equal(ratio('tx', 'bg'), '14.8')
  assert.equal(ratio('mu', 'bg'), '7.9')
  assert.equal(ratio('onac', 'ac'), '10.7')
})

// Every rule outside @keyframes and @property must be scoped to its own theme: a theme.css is global once linked
function selectorsOf(text: string): string[] {
  // Drop comments and the global at-rule bodies, then unwrap @media (its rules are checked like the rest)
  let css = text.replace(/\/\*[\s\S]*?\*\//g, '')
  css = css.replace(/@property[^{]*\{[^}]*\}/g, '').replace(/@keyframes[^{]*\{(?:[^{}]*\{[^{}]*\})*[^{}]*\}/g, '')
  css = css.replace(/@media[^{]*\{/g, '')
  return [...css.matchAll(/([^{}]+)\{[^{}]*\}/g)].flatMap((m) => m[1].split(/,(?![^(]*\))/).map((s) => s.trim())).filter(Boolean)
}

for (const id of folders.filter((f) => existsSync(`${root}${f}/theme.css`))) {
  test(`${id}: theme.css is under the cap and every rule outside @keyframes and @property is scoped to [data-theme="${id}"]`, () => {
    const text = readFileSync(`${root}${id}/theme.css`, 'utf8')
    assert.ok(Buffer.byteLength(text) < LIMITS.cssBytes, 'under the theme.css cap')
    const selectors = selectorsOf(text)
    assert.ok(selectors.length > 0, 'found selectors')
    // its own theme, optionally narrowed by more root attributes (data-fx: motion only while Effects are on)
    const own = new RegExp(`^(?:\\[data-theme="${id}"\\]|:where\\(\\[data-theme="${id}"\\](?:\\[data-[a-z]+="[a-z]+"\\])*\\))`)
    const loose = selectors.filter((s) => !own.test(s))
    assert.deepEqual(loose, [])
  })
}

test('Neon\'s theme.css has its own classes and keyframes, and its motion runs only behind the Effects and reduced-motion gate', () => {
  const text = readFileSync(`${root}neon/theme.css`, 'utf8')
  // every rule runs an animation, or holds the still frame of an element the file animates (the ring's border, the beam's place,
  // the orbits' origin): what stands still on its own is tokens
  const rules = [...text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/@property[^{]*\{[^}]*\}/g, '').replace(/@keyframes[^{]*\{(?:[^{}]*\{[^{}]*\})*[^{}]*\}/g, '').replace(/@media[^{]*\{/g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  assert.ok(rules.length >= 6, 'Neon\'s motion is there (its ring, live pulse, beam and glint, with their still frames)')
  const element = (sel: string) => /(\.[\w-]+)$/.exec(sel.trim())?.[1] ?? sel.trim()
  const moving = new Set(rules.filter((m) => /animation/.test(m[2])).flatMap((m) => m[1].split(/,(?![^(]*\))/).map(element)))
  const still = rules.filter((m) => !/animation/.test(m[2])).flatMap((m) => m[1].split(/,(?![^(]*\))/).map(element))
  assert.deepEqual(still.filter((e) => !moving.has(e)), [], 'a rule that only stands still belongs in tokens')
  for (const k of ['ring', 'beam', 'glint', 'live']) assert.ok(text.includes(`.${k}`), `.${k} is Neon's`)
  // the static look is tokens now (theme.json): the backdrop, the surfaces, the stripe, the brackets, the stage, the mono body
  // read as selectors and declarations, so spacing cannot hide one ('.page {' is '.page{')
  const sels = selectorsOf(text)
  // the tree's dashes, orbits and node beat are the wire tokens', so a tokens-only theme has them too
  for (const cls of ['page', 'page-scan', 'page-head', 'surf', 'stripe', 'brk', 'stage', 'seg', 'cursor', 'wire', 'comet', 'flow', 'flowr', 'orbit', 'orbit2', 'node', 'pk']) {
    const hit = sels.filter((s) => new RegExp(`\\.${cls}(?![\\w-])`).test(s))
    assert.deepEqual(hit, [], `.${cls} is drawn from Neon's tokens, not its CSS`)
  }
  assert.ok(!sels.some((s) => /:hover|:focus-visible/.test(s)), 'hover and focus are tokens')
  assert.ok(!/letter-spacing\s*:|text-transform\s*:/.test(text), 'case and tracking are tokens')
  for (const k of ['spin', 'lead', 'pulse', 'beam', 'glint']) assert.ok(text.includes(`@keyframes ${k}`), `@keyframes ${k}`)
  assert.match(text, /@media \(prefers-reduced-motion: no-preference\)/)
  assert.match(text, /\[data-fx="on"\]/)
})

for (const id of folders) {
  test(`${id}: the folder name is the manifest id, and the theme passes the gate`, () => {
    assert.equal(load(id).id, id)
    const r = checkTheme(read(id), ctx, load('default'))
    assert.ok(r.ok, r.ok ? '' : r.errors.join('\n'))
  })

  test(`${id}: a built-in carries no .source.json (that marks a gallery install)`, () => {
    assert.equal(existsSync(`${root}${id}/.source.json`), false)
  })
}
