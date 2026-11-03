// The effect names live in three places: the validator's list (KNOWN_EFFECTS), the registry (one module per effect) and
// the themes that name them. This keeps the three in step: a typo in a theme is refused when it loads, and a name added
// to one list without the others fails here. The registry is built the way Vite builds it, from the files in this folder
// (Bun has no import.meta.glob, so the registry itself is empty under test).
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'
import { KNOWN_EFFECTS } from '../lib/theme/tokens.ts'
import { validateManifest } from '../lib/theme/validate.ts'
import { buildRegistry } from './lib/build.ts'
import { createEffectsHost } from './lib/host.ts'

// Named by a theme and accepted by the validator on purpose, with no module: docs/themes.md reserves the name
const NO_MODULE = ['glow']

const dir = import.meta.dirname
const repo = new URL('../../', import.meta.url).pathname
// the shipped themes and the gallery's seed themes
const THEME_DIRS = ['themes/', 'gallery-seed/']

async function realRegistry() {
  const skip = (f: string) => f === 'registry.ts' || f === 'browser.ts' || f.endsWith('.test.ts')
  const modules: Record<string, unknown> = {}
  for (const f of [...new Glob('*.ts').scanSync(dir)].filter((f) => !skip(f)).sort()) modules[`./${f}`] = await import(`./${f}`)
  return buildRegistry(modules)
}

const names = (xs: Iterable<string>) => [...xs].sort()

test('the registry and KNOWN_EFFECTS name the same effects, apart from the reserved ones with no module', async () => {
  const registry = await realRegistry()
  assert.ok(Object.keys(registry).length >= 4, 'the folder scan found the effects')
  assert.deepEqual(names(Object.keys(registry)), names(KNOWN_EFFECTS.filter((n) => !NO_MODULE.includes(n))))
  // the allowance is not a back door: a reserved name with a module of its own would belong in both
  for (const n of NO_MODULE) assert.ok(KNOWN_EFFECTS.includes(n), `${n} is allowed to lack a module but is not in KNOWN_EFFECTS`)
  for (const n of NO_MODULE) assert.ok(!Object.hasOwn(registry, n), `${n} has a module now: drop it from NO_MODULE`)
})

test('every effect a theme in themes/ or gallery-seed/ names is in KNOWN_EFFECTS and, but for the reserved ones, in the registry', async () => {
  const registry = await realRegistry()
  const named: string[] = []
  const manifests = THEME_DIRS.flatMap((d) =>
    readdirSync(repo + d, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => `${d}${e.name}`)
      .sort(),
  )
  assert.ok(manifests.includes('gallery-seed/ember') && manifests.includes('themes/crt'), 'the scan found the theme folders')
  for (const dir of manifests) {
    const manifest = JSON.parse(readFileSync(`${repo}${dir}/theme.json`, 'utf8')) as { effects?: { name: string }[] }
    for (const e of manifest.effects ?? []) {
      named.push(e.name)
      assert.ok(KNOWN_EFFECTS.includes(e.name), `${dir} names ${e.name}, which is not in KNOWN_EFFECTS`)
      assert.ok(Object.hasOwn(registry, e.name) || NO_MODULE.includes(e.name), `${dir} names ${e.name}, which has no module`)
    }
  }
  // the scan reads real manifests: the shipped themes use these
  for (const n of ['rain', 'scanlines', 'vignette', 'flicker']) assert.ok(named.includes(n), `no theme names ${n}`)
})

const theme = (effects: unknown[]) => ({
  id: 'mine', name: 'Mine', version: '1.0.0', author: 'me', license: 'MIT', description: 'a test theme',
  modes: ['dark'], accent: 'fixed', fonts: [], effects, tokens: { dark: { bg: '#010a04' } },
})

test('a typo in an effect name is refused when the theme loads, naming it and the known effects', () => {
  for (const source of ['user', 'builtin'] as const) {
    const r = validateManifest(theme([{ name: 'scanlinez' }, { name: 'vignette' }]), { source, builtinIds: new Set(['default']) })
    assert.equal(r.ok, false)
    if (r.ok) continue
    assert.equal(r.errors.length, 1)
    assert.match(r.errors[0], /scanlinez/)
    assert.match(r.errors[0], /is not a known effect/)
    for (const n of KNOWN_EFFECTS) assert.ok(r.errors[0].includes(n), `the refusal lists ${n}`)
  }
  // case matters, and so does a name that is no string
  for (const bad of ['Rain', 'rain ', '', 'toString', '__proto__']) {
    assert.equal(validateManifest(theme([{ name: bad }]), { source: 'user', builtinIds: new Set() }).ok, false, JSON.stringify(bad))
  }
  assert.equal(validateManifest(theme([{ name: 7 }]), { source: 'user', builtinIds: new Set() }).ok, false)
})

test('a reserved name validates, and the host mounts nothing for it and does not throw', async () => {
  const registry = await realRegistry()
  for (const n of NO_MODULE) {
    assert.equal(validateManifest(theme([{ name: n }]), { source: 'user', builtinIds: new Set() }).ok, true, n)
    let layers = 0
    const host = createEffectsHost({
      registry,
      layer: () => {
        layers++
        throw new Error('a layer was asked for')
      },
      env: () => ({ reduced: false, hidden: () => false, raf: () => 0, caf: () => {}, now: () => 0 }),
    })
    host.apply([{ name: n }], { bg: '#000' }, true)
    assert.equal(layers, 0, `${n} drew into a layer`)
  }
})
