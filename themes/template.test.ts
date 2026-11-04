// The template theme and the authoring guide are held to the code: the template passes the same judgment a user theme does,
// and the guide names every slot, font and effect the token contract has, so a new one cannot be added and forgotten.
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import { ALLOWED_FONTS, KNOWN_EFFECTS, SLOTS } from '../src/lib/theme/tokens.ts'
import { checkTheme, parseManifest } from '../src/lib/theme/validate.ts'
import type { ThemeManifest } from '../src/lib/theme/types.ts'

const root = new URL('..', import.meta.url).pathname
const template = readFileSync(`${root}themes/_template/theme.json`, 'utf8')
const guide = readFileSync(`${root}docs/themes.md`, 'utf8')
const readme = readFileSync(`${root}README.md`, 'utf8')
const usingThemes = readFileSync(`${root}docs/using-themes.md`, 'utf8')

// The docs state exactly the values CONN_THEME_INDEX turns the gallery off for. The server's tests hold its list to the same file.
const THEME_INDEX_OFF: string[] = JSON.parse(readFileSync(`${root}testdata/theme-index-off.json`, 'utf8'))
const ticks = THEME_INDEX_OFF.map(v => `\`${v}\``)

// The built-in themes as the server sees them: every folder whose name is an id, with Default as the contrast fallback
function builtinBase(dir: string): { ids: Set<string>; fallback: ThemeManifest } {
  const ids = new Set(readdirSync(dir, { withFileTypes: true }).filter(e => e.isDirectory() && /^[a-z0-9][a-z0-9-]{0,39}$/.test(e.name)).map(e => e.name))
  const d = parseManifest(readFileSync(`${dir}/default/theme.json`, 'utf8'), { source: 'builtin', builtinIds: ids })
  assert.ok(d.ok, 'the Default theme parses')
  return { ids, fallback: d.manifest }
}
const OFF_IN_USING = `${ticks[0]} (or ${ticks.slice(1, -1).join(', ')} or ${ticks.at(-1)}, in any case)`
const OFF_IN_GUIDE = `${ticks.slice(0, -1).join(', ')} or ${ticks.at(-1)} (any case)`

test('the template passes checkTheme as a user theme, contrast included', () => {
  const { ids, fallback } = builtinBase(`${root}themes`)
  const r = checkTheme(template, { source: 'user', builtinIds: ids }, fallback)
  assert.ok(r.ok, r.ok ? '' : r.errors.join('; '))
  assert.equal(r.manifest.id, 'my-theme')
  assert.deepEqual(r.manifest.modes, ['light', 'dark'])
  assert.equal(r.manifest.accent, 'free')
})

test('the template sets a handful of tokens per mode and leaves the rest to Default', () => {
  const m = JSON.parse(template)
  for (const mode of ['light', 'dark']) {
    const n = Object.keys(m.tokens[mode]).length
    assert.ok(n >= 5 && n < Object.keys(SLOTS).length / 2, `${mode} sets ${n} tokens`)
  }
})

// The text of one `## ` section of the guide, so a name has to be in the right place (a table row or a list item), not anywhere in the file
function section(heading: string): string {
  const start = guide.indexOf(`\n## ${heading}\n`)
  assert.ok(start >= 0, `docs/themes.md has no "${heading}" section`)
  const rest = guide.slice(start + 1)
  const next = rest.indexOf('\n## ', 1)
  return next < 0 ? rest : rest.slice(0, next)
}
const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
const hasRow = (text: string, name: string) => new RegExp(`^\\| \`${escape(name)}\` \\|`, 'm').test(text)
const hasItem = (text: string, name: string) => new RegExp(`^- \`${escape(name)}\`$`, 'm').test(text)

test('the guide has a row in the token table for every slot', () => {
  const tokens = section('Tokens')
  const missing = Object.keys(SLOTS).filter((s) => !hasRow(tokens, s))
  assert.deepEqual(missing, [], `the Tokens table lacks ${missing.join(', ')}`)
})

test('the guide lists every allowed font, and has a row in the effects table for every known effect', () => {
  const fonts = section('Fonts')
  const lacking = ALLOWED_FONTS.filter((f) => !hasItem(fonts, f))
  assert.deepEqual(lacking, [], `the Fonts list lacks ${lacking.join(', ')}`)
  const effects = section('Effects')
  const missing = KNOWN_EFFECTS.filter((e) => !hasRow(effects, e))
  assert.deepEqual(missing, [], `the Effects table lacks ${missing.join(', ')}`)
})

test('the guide states the size limits, where themes live, how to check and how to preview', () => {
  for (const s of ['64 KB', '256 KB', '100 themes', '~/.conn/themes/', 'conn theme check', '?theme=<id>&mode=<mode>']) {
    assert.ok(guide.includes(s), `docs/themes.md lacks ${s}`)
  }
})

test('the guide states the three trust levels', () => {
  for (const s of ['Gallery install', 'Your own folder', 'Built in']) assert.ok(guide.includes(s), `docs/themes.md lacks the trust level ${s}`)
})

test('the guide describes the two effect layers and the built-in themes', () => {
  for (const s of ['behind', 'above', '`layer`', 'Default', 'Neon', 'Matrix', 'CRT', 'Green', 'Amber', 'White']) {
    assert.ok(guide.includes(s), `docs/themes.md lacks ${s}`)
  }
})

test('the README links the guide', () => {
  assert.match(readme, /\]\(docs\/themes\.md\)/)
})

test('the guide has a Gallery section: how a theme is added, how the index is built, who publishes, and the setting', () => {
  const g = section('The gallery')
  for (const s of ['conn-themes', 'pull request', 'conn theme check', 'theme.json', 'theme.css', 'conn theme index', 'index.json', 'sha256', 'maintainer', 'CONN_THEME_INDEX', OFF_IN_GUIDE, 'top-level folder of `--src` must be a theme folder', '.gitattributes', 'Install', 'Update', 'Uninstall', '.source.json']) {
    assert.ok(g.includes(s), `the Gallery section lacks ${s}`)
  }
  assert.ok(!guide.includes('not in this build'), 'the guide still says the gallery is not in this build')
})

test('the guide documents conn theme check --gallery where it checks and where it shares, with the exit codes', () => {
  const checking = section('Checking a theme')
  for (const s of ['conn theme check --gallery', 'theme.json` alone', 'conn theme index', 'exits 0 when everything passed, 1 when something did not, and 2 for bad usage']) {
    assert.ok(checking.includes(s), `the Checking section lacks ${s}`)
  }
  assert.ok(section('The gallery').includes('conn theme check --gallery ./my-theme'), 'the sharing steps do not send a contributor to --gallery')
})

test('docs/using-themes.md says what the gallery reads and writes, and that CONN_THEME_INDEX=off turns it all off, and the README links it', () => {
  assert.match(readme, /\]\(docs\/using-themes\.md\)/)
  for (const s of [OFF_IN_USING, 'https://raw.githubusercontent.com/<themeIndex>/', 'no token', '/main/index.json', '<dataDir>/themes/<id>/theme.json', '.source.json', 'redirect is never followed', 'once a minute', 'removes only a real folder that has a `.source.json`', 'CONN_THEME_INDEX=off', 'sandboxed frames (no scripts, no network)', 'need the `x-conn: 1` header', 'neither `owner/repo` nor one of the off-values below falls back to the default repo', '.tmp-<id>-...-old-...']) {
    assert.ok(usingThemes.includes(s), `docs/using-themes.md lacks ${s}`)
  }
})
