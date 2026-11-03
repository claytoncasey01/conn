// The boot script is plain text in index.html, so it is run here against fakes, as the browser would at first paint.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import vm from 'node:vm'
import { CSS_HREF, DEFAULT_ID, FONTS_HREF, KEYS } from './storage.ts'
import { cacheEntryOf } from './apply.ts'
import { LOOK_RULES } from './lookCss.ts'
import { resolveTheme } from './resolve.ts'
import type { ThemeManifest } from './types.ts'

const html = readFileSync(new URL('../../../index.html', import.meta.url), 'utf8')
const inline = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)]
const code = inline[0]?.[1] ?? ''

type FakeEl = { tag: string; id: string; textContent: string; rel?: string; href?: string; media?: string; onload?: () => void }
type Fake = { attrs: Record<string, string>; styles: FakeEl[]; links: FakeEl[]; reads: string[]; writes: number }
function run(store: Record<string, string> | 'throws', opts: { dark?: boolean; reduced?: boolean; search?: string; noMatchMedia?: boolean } = {}): Fake {
  const out: Fake = { attrs: {}, styles: [], links: [], reads: [], writes: 0 }
  const localStorage = {
    getItem(k: string) {
      out.reads.push(k)
      if (store === 'throws') throw new Error('denied')
      return Object.hasOwn(store, k) ? store[k] : null
    },
    setItem() { out.writes++ },
    removeItem() { out.writes++ },
  }
  const document = {
    documentElement: { setAttribute: (k: string, v: string) => void (out.attrs[k] = String(v)) },
    createElement: (tag: string): FakeEl => ({ tag, id: '', textContent: '' }),
    head: { appendChild: (el: FakeEl) => void (el.tag === 'style' ? out.styles : out.links).push(el) },
  }
  const matchMedia = (q: string) => ({ matches: q.includes('reduced-motion') ? !!opts.reduced : q.includes('dark') && !!opts.dark })
  const ctx: Record<string, unknown> = { document, localStorage, location: { search: opts.search ?? '' } }
  if (!opts.noMatchMedia) ctx.matchMedia = matchMedia
  vm.runInNewContext(code, ctx)
  return out
}
const css = '[data-theme="matrix"][data-mode="dark"]{--bg:#010a04;}'
const cacheOf = (over: object = {}) =>
  JSON.stringify({ css, attrs: { motion: 'power', led: 'square', glass: 'on' }, only: null, ...over })
const choiceOf = (over: object = {}) => JSON.stringify({ id: 'matrix', mode: 'system', accent: null, variant: null, effects: 'auto', ...over })

test('there is exactly one inline script, plain and short', () => {
  assert.equal(inline.length, 1)
  assert.ok(code.trim().split('\n').length < 40, 'under 40 lines')
  assert.ok(!code.includes('`'), 'no template literals')
  assert.ok(!/\bfetch\b|XMLHttpRequest|innerHTML|setItem|=>|\blet\b|\bconst\b/.test(code), 'ES5, no network, no writes, no innerHTML')
  assert.ok(html.indexOf(inline[0]![0]) < html.indexOf('/src/main.ts'), 'runs before the app script')
})

const SHA = 'a'.repeat(64)
const cssHref = (id = 'matrix', v = SHA) => `/api/themes/${id}/theme.css?v=${v}`
const fontsHref = 'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&display=swap'
const stored = (over: object, id = 'matrix') => ({ [KEYS.choice]: choiceOf({ id }), [KEYS.css(id)]: cacheOf(over) })

test("the script's link patterns are the ones the modules use, so a cache entry the app reads is one the script accepts", () => {
  assert.ok(code.includes(String(CSS_HREF)), String(CSS_HREF))
  assert.ok(code.includes(String(FONTS_HREF)), String(FONTS_HREF))
})

test('a cached stylesheet href is injected once as a stylesheet link, after the token style and the fonts link', () => {
  const r = run(stored({ cssUrl: cssHref(), fontsUrl: fontsHref }))
  const css = r.links.find((l) => l.id === 'conn-theme-css')
  assert.equal(r.links.filter((l) => l.id === 'conn-theme-css').length, 1)
  assert.equal(css?.rel, 'stylesheet')
  assert.equal(css?.href, cssHref())
  assert.deepEqual(r.links.map((l) => l.id), ['conn-theme-fonts', 'conn-theme-css'], 'the app appends them in this order')
  assert.equal(r.styles.length, 1)
})

test('the script is at the top of the body, so its links land after the build\'s stylesheet in the head', () => {
  assert.ok(html.indexOf('<body>') < html.indexOf(inline[0]![0]), 'in the body')
  assert.ok(html.indexOf(inline[0]![0]) < html.indexOf('<div id="app">'), 'before the app root')
  assert.ok(html.indexOf('</head>') < html.indexOf(inline[0]![0]))
})

test('a cached fonts href is injected as a stylesheet link that does not block first paint, and applies once it loads', () => {
  const f = run(stored({ fontsUrl: fontsHref })).links[0]
  assert.equal(f.id, 'conn-theme-fonts')
  assert.equal(f.rel, 'stylesheet')
  assert.equal(f.href, fontsHref)
  assert.equal(f.media, 'print')
  f.onload?.call(f)
  assert.equal(f.media, 'all')
})

test('every shipped Google Fonts href the app builds is accepted by the script', () => {
  const all = 'https://fonts.googleapis.com/css2?family=Chakra+Petch:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;700&display=swap'
  assert.equal(run(stored({ fontsUrl: all })).links.length, 1)
})

test('hostile or malformed hrefs inject nothing, and the tokens still paint', () => {
  const hostile = [
    `//evil.com/x.css?v=${SHA}`, `//evil.com/api/themes/matrix/theme.css?v=${SHA}`, `https://evil.com/api/themes/matrix/theme.css?v=${SHA}`,
    `https://example.com${cssHref()}`, `/api/themes/../x/theme.css?v=${SHA}`, `/api/themes/matrix/../x/theme.css?v=${SHA}`,
    cssHref('matrix', 'A'.repeat(64)), cssHref('matrix', 'a'.repeat(63)), cssHref('matrix', 'a'.repeat(65)), cssHref('matrix', 'g'.repeat(64)),
    cssHref('Matrix'), cssHref('mat_rix'), cssHref('mat rix'), `${cssHref()}&x=1`, `${cssHref()}#x`, `${cssHref()}\n`, ` ${cssHref()}`,
    'javascript:alert(1)', 'JAVASCRIPT:alert(1)', `data:text/css,body{}`, `vbscript:x`, `/api/themes/matrix/theme.css`, `/api/themes//theme.css?v=${SHA}`,
    `/api/themes/matrix/theme.css?v=${SHA}`.replace('/api', '/xapi'), cssHref('x'.repeat(5000)), '', 5, null, {}, [cssHref()], true,
  ]
  for (const h of hostile) {
    const r = run(stored({ cssUrl: h }))
    assert.equal(r.links.length, 0, String(h).slice(0, 70))
    assert.equal(r.styles.length, 1, 'tokens still paint')
  }
  const hostileFonts = [
    'https://evil.com/css2?family=Share+Tech+Mono:wght@400&display=swap', 'http://fonts.googleapis.com/css2?family=A:wght@400&display=swap',
    'https://fonts.googleapis.com.evil.com/css2?family=A:wght@400&display=swap', 'https://fonts.googleapis.com@evil.com/css2?family=A:wght@400&display=swap',
    '//fonts.googleapis.com/css2?family=A:wght@400&display=swap', 'https://fonts.googleapis.com/css2?family=A:wght@400&display=swap&x=1',
    'https://fonts.googleapis.com/css2?family=A:wght@400&display=swap#', 'https://fonts.googleapis.com/css2?family=A:wght@400&display=swap\n',
    'https://fonts.googleapis.com/css2?family=<script>:wght@400&display=swap', 'https://fonts.googleapis.com/css2?family=Evil+Font:wght@400&display=swap',
    'https://fonts.googleapis.com/css2?family=Share+Tech+Mono:wght@400&family=Evil+Font:wght@400&display=swap', `https://fonts.googleapis.com/css2?family=${'A'.repeat(5000)}:wght@400&display=swap`,
    'https://fonts.googleapis.com/other?family=A:wght@400&display=swap', 'javascript:1', 'data:text/css,a{}', cssHref(), 5, null, {},
  ]
  for (const h of hostileFonts) assert.equal(run(stored({ fontsUrl: h })).links.length, 0, String(h).slice(0, 70))
})

test('each link stands alone: a bad one does not stop the good one', () => {
  assert.deepEqual(run(stored({ cssUrl: '//evil.com/x.css', fontsUrl: fontsHref })).links.map((l) => l.id), ['conn-theme-fonts'])
  assert.deepEqual(run(stored({ cssUrl: cssHref(), fontsUrl: 'javascript:1' })).links.map((l) => l.id), ['conn-theme-css'])
})

test("another theme's stylesheet href is never injected: the path id must be the id being painted", () => {
  assert.equal(run(stored({ cssUrl: cssHref('neon') })).links.length, 0)
  // and ?theme= pins which cache entry is read, so its own id is the one that counts
  const r = run({ [KEYS.choice]: choiceOf({ id: 'default' }), [KEYS.css('matrix')]: cacheOf({ cssUrl: cssHref('matrix') }) }, { search: '?theme=matrix' })
  assert.deepEqual(r.links.map((l) => l.href), [cssHref('matrix')])
})

test('no cache, an old-shape cache entry (no hrefs), or a corrupt one injects no link', () => {
  assert.equal(run({}).links.length, 0)
  assert.equal(run({ [KEYS.choice]: choiceOf() }).links.length, 0)
  assert.equal(run(stored({})).links.length, 0)
  const noFields = JSON.stringify({ css, attrs: { motion: 'power', led: 'square', glass: 'on' }, only: null })
  const r = run({ [KEYS.choice]: choiceOf(), [KEYS.css('matrix')]: noFields })
  assert.deepEqual([r.links.length, r.styles.length], [0, 1])
  // a corrupt entry paints nothing, so it injects no link even with a valid href inside it
  assert.equal(run(stored({ css: 9, cssUrl: cssHref() })).links.length, 0)
  assert.equal(run(stored({ attrs: null, cssUrl: cssHref() })).links.length, 0)
})

test('a theme whose entry caches no link gets none', () => {
  assert.equal(run(stored({ cssUrl: null, fontsUrl: null })).links.length, 0)
})

test('Default is the default for everyone: DEFAULT_ID and the id the boot script paints are both default', () => {
  assert.equal(DEFAULT_ID, 'default')
  assert.ok(code.includes(`id = '${DEFAULT_ID}'`), 'the boot script starts from DEFAULT_ID')
  assert.equal(run({}).attrs['data-theme'], 'default')
})

test('index.html links no fonts of its own: the loader links the active theme\'s, so nothing is fetched for a theme not in use', () => {
  assert.doesNotMatch(html, /fonts\.googleapis|fonts\.gstatic/)
})

test('with nothing stored it sets the default theme, system mode, and the calm attributes', () => {
  const r = run({})
  assert.equal(r.attrs['data-theme'], DEFAULT_ID)
  assert.equal(r.attrs['data-mode'], 'light')
  assert.deepEqual([r.attrs['data-motion'], r.attrs['data-led'], r.attrs['data-glass']], ['calm', 'circle', 'off'])
  assert.equal(r.styles.length, 0)
  assert.equal(run({}, { dark: true }).attrs['data-mode'], 'dark')
})

test('a stored choice with a cache entry applies the theme, mode, attributes and one style with that CSS', () => {
  const r = run({ [KEYS.choice]: choiceOf({ mode: 'light' }), [KEYS.css('matrix')]: cacheOf() })
  assert.equal(r.attrs['data-theme'], 'matrix')
  assert.equal(r.attrs['data-mode'], 'light')
  assert.deepEqual([r.attrs['data-motion'], r.attrs['data-led'], r.attrs['data-glass']], ['power', 'square', 'on'])
  assert.equal(r.styles.length, 1)
  assert.equal(r.styles[0].id, 'conn-theme')
  assert.equal(r.styles[0].textContent, css)
})

test('a cached one-mode theme wins over the stored mode', () => {
  const r = run({ [KEYS.choice]: choiceOf({ mode: 'light' }), [KEYS.css('matrix')]: cacheOf({ only: 'dark' }) })
  assert.equal(r.attrs['data-mode'], 'dark')
  assert.equal(run({ [KEYS.choice]: choiceOf({ mode: 'light' }), [KEYS.css('matrix')]: cacheOf({ only: 'dark' }) }, { search: '?mode=light' }).attrs['data-mode'], 'dark')
})

test('Default with a picked accent applies its cached CSS, not a fallback', () => {
  const rose = '[data-theme="default"][data-mode="light"]{--ac:oklch(.5 .16 355);}'
  const r = run({
    [KEYS.choice]: choiceOf({ id: 'default', accent: { name: 'Rose', hue: 355, chroma: 0.16 } }),
    [KEYS.css('default')]: cacheOf({ css: rose, attrs: { motion: 'calm', led: 'circle', glass: 'off' } }),
  })
  assert.equal(r.styles[0].textContent, rose)
})

test('system mode with a dark OS sets dark', () => {
  const r = run({ [KEYS.choice]: choiceOf(), [KEYS.css('matrix')]: cacheOf() }, { dark: true })
  assert.equal(r.attrs['data-mode'], 'dark')
})

test('corrupt or wrong-shaped cache falls back to the defaults for the stored id with no style', () => {
  const stored = { [KEYS.choice]: choiceOf() }
  for (const bad of ['{nope', 'null', '5', cacheOf({ css: 9 }), cacheOf({ only: 'sepia' }), cacheOf({ attrs: { motion: 'wild', led: 'circle', glass: 'on' } }), cacheOf({ attrs: null })]) {
    const r = run({ ...stored, [KEYS.css('matrix')]: bad })
    assert.equal(r.styles.length, 0, bad)
    assert.equal(r.attrs['data-theme'], 'matrix')
    assert.deepEqual([r.attrs['data-motion'], r.attrs['data-led'], r.attrs['data-glass']], ['calm', 'circle', 'off'], bad)
  }
})

test('corrupt choice JSON, wrong values and a throwing localStorage set the defaults and never throw', () => {
  for (const bad of ['{nope', 'null', '[]', '7', choiceOf({ id: '../x', mode: 'loud' }), choiceOf({ id: 5 }), choiceOf({ mode: ['dark'] })]) {
    const r = run({ [KEYS.choice]: bad })
    assert.equal(r.attrs['data-mode'] === 'light' || r.attrs['data-mode'] === 'dark', true, bad)
    if (bad.includes('"id":5') || bad.includes('..')) assert.equal(r.attrs['data-theme'], DEFAULT_ID, bad)
  }
  const t = run('throws')
  assert.equal(t.attrs['data-theme'], DEFAULT_ID)
  assert.equal(t.attrs['data-mode'], 'light')
  assert.equal(run('throws', { dark: true }).attrs['data-mode'], 'dark')
})

// the effects switch reaches all theme motion through data-fx, so first paint must mark it too, or a page with Effects off would
// run Neon's ring and the tree's packets until the app loads. The choice is read back untrusted: only on, off or auto count.
test('first paint marks data-fx from the cached effects choice and the OS: on wins, off wins, auto follows reduced motion', () => {
  const fx = (effects: unknown, reduced = false) => run({ 'conn.theme': choiceOf({ effects }), 'conn.theme.css.matrix': cacheOf() }, { reduced }).attrs['data-fx']
  assert.equal(fx('auto'), 'on')
  assert.equal(fx('auto', true), 'off')
  assert.equal(fx('on', true), 'on')
  assert.equal(fx('off'), 'off')
  // anything else is auto, as storage.ts reads it
  for (const bad of ['OFF', 'yes', 1, null, ['off'], { off: true }]) {
    assert.equal(fx(bad), 'on', JSON.stringify(bad))
    assert.equal(fx(bad, true), 'off', JSON.stringify(bad))
  }
  assert.equal(run({}).attrs['data-fx'], 'on', 'no choice: auto')
  assert.equal(run('throws').attrs['data-fx'], 'on', 'storage that throws: auto')
  assert.equal(run({}, { noMatchMedia: true }).attrs['data-fx'], 'on', 'no matchMedia: motion is not reduced')
})

test('a missing matchMedia does not throw', () => {
  assert.equal(run({}, { noMatchMedia: true }).attrs['data-mode'], 'light')
})

test('?theme and ?mode win for this load and write nothing', () => {
  const r = run(
    { [KEYS.choice]: choiceOf({ id: 'default', mode: 'light' }), [KEYS.css('matrix')]: cacheOf() },
    { search: '?theme=matrix&mode=dark' },
  )
  assert.equal(r.attrs['data-theme'], 'matrix')
  assert.equal(r.attrs['data-mode'], 'dark')
  assert.equal(r.styles[0].textContent, css)
  assert.equal(r.writes, 0)
})

test('a ?theme or ?mode value that is not valid is ignored', () => {
  const stored = { [KEYS.choice]: choiceOf({ id: 'default', mode: 'light' }) }
  for (const search of ['?theme=Mat rix', '?theme=a_b', '?theme=../x', '?theme=%3Cscript%3E', '?theme=', '?theme=-' + 'a'.repeat(60)])
    assert.equal(run(stored, { search }).attrs['data-theme'], 'default', search)
  assert.equal(run(stored, { search: '?mode=sepia' }).attrs['data-mode'], 'light')
})

test("the script's storage keys are the ones the modules use", () => {
  const r = run({ [KEYS.choice]: choiceOf() })
  assert.deepEqual(r.reads, [KEYS.choice, KEYS.css('matrix')])
})

// The look rides in the cached stylesheet: a cold start paints a theme's backdrop, cards and glyphs before the app runs, with
// no attribute to set, because the --look-* variables are declarations in the same CSS text the script injects.
test('a cold start paints the cached look: the real cache entry for Matrix carries its look variables to first paint', () => {
  const read = (id: string) => JSON.parse(readFileSync(new URL(`../../../themes/${id}/theme.json`, import.meta.url), 'utf8')) as ThemeManifest
  const def = read('default')
  const matrix = read('matrix')
  const r = resolveTheme({ list: [{ id: 'matrix', source: 'builtin', hasCss: false, cssSha256: null, manifest: matrix }], choice: JSON.parse(choiceOf()), systemDark: true, accepted: {}, fallback: def })
  const entry = cacheEntryOf(r)
  const painted = run({ [KEYS.choice]: choiceOf(), [KEYS.css('matrix')]: JSON.stringify(entry) }).styles.map((s) => s.textContent).join('')
  for (const decl of ['--look-gutter:1;', '--look-head-blur:blur(14px);', '--look-fill:color-mix(in oklch, var(--panel) 55%, transparent);', '--look-fwd:"→";', '--look-bullet:"❯";', '--look-body:var(--font-mono), monospace;'])
    assert.ok(painted.includes(decl), decl)
})

test('a stylesheet cached before the look existed still paints a whole page: every look variable the rules read has a fallback', () => {
  const reads = [...LOOK_RULES.matchAll(/var\((--look-[a-z0-9-]+)(,?)/g)]
  assert.ok(reads.length > 40)
  // the chrome rule reads its two without one on purpose: invalid, the property inherits, as authored chrome means
  assert.deepEqual(reads.filter((m) => m[2] !== ',').map((m) => m[1]).sort(), ['--look-chrome-case', '--look-chrome-tracking'], 'read without a fallback')
})

test('a hostile cache entry is not painted: a stylesheet that would load something, or attrs outside their lists, paint no theme style', () => {
  for (const css of ['[data-theme="matrix"]{--look-marks:url(https://x/y.svg)}', '@import "https://x/y.css";', 'a{width:expression(alert(1))}']) {
    const r = run({ [KEYS.choice]: choiceOf(), [KEYS.css('matrix')]: cacheOf({ css }) })
    assert.equal(r.styles.length, 0, css)
    assert.equal(r.attrs['data-glass'], 'off', 'and the attributes fall back too')
  }
  assert.equal(run({ [KEYS.choice]: choiceOf(), [KEYS.css('matrix')]: cacheOf({ attrs: { motion: 'power', led: 'hexagon', glass: 'on' } }) }).styles.length, 0)
  assert.equal(run({ [KEYS.choice]: choiceOf(), [KEYS.css('matrix')]: cacheOf() }).styles.length, 1, 'the same entry with a clean stylesheet paints')
})
