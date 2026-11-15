// The look's rules (lookCss.ts) are the one place the look is drawn: the app loads them and so does the gallery preview, and
// each piece shows only when a token selects it.
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import defaultTheme from '../../../themes/default/theme.json'
import neon from '../../../themes/neon/theme.json'
import { LOOK } from './css.ts'
import { LOOK_CSS, LOOK_RULES } from './lookCss.ts'
import { previewDoc } from './preview.ts'
import type { ThemeManifest } from './types.ts'

const css = LOOK_RULES
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ sel: m[1].trim().replace(/\s+/g, ' '), body: m[2].replace(/\s+/g, '') }))
const body = (sel: string) => rules.filter((r) => r.sel.split(/,(?![^(]*\))/).map((x) => x.trim()).includes(sel)).map((r) => r.body).join(';')

test('each piece of the look is drawn only when a look token selects it', () => {
  assert.match(body('.brk::after'), /content:var\(--look-marks,none\)/)
  assert.match(body('.surf::before'), /content:var\(--look-cut,none\)/)
  assert.match(body('.stripe'), /display:var\(--look-stripe,none\)/)
  assert.match(body('.glint'), /display:var\(--look-glint,none\)/)
  assert.match(body('.g-brand-mark'), /display:var\(--look-brand-mark,none\)/)
  for (const drawing of ['tube', 'pixel']) assert.match(body(`.bm-${drawing}`), new RegExp(`display:var\\(--look-brand-${drawing},none\\)`), drawing)
  // The solid drawing shows inside the mark unless a look hides it, so a cached look that sets only --look-brand-mark (from
  // before the drawings) still draws Default's mark; the mark itself stays hidden until a look token selects it
  assert.match(body('.bm-solid'), /display:var\(--look-brand-solid,inline\)/)
  assert.match(body('.page-scan'), /display:var\(--look-scan,none\)/)
  for (const sel of ['.stage-ticks', '.stage-floor', '.stage-glow']) assert.match(body(sel), /display:var\(--look-hud,none\)/, sel)
  // the tree: orbits and their comet only under tree-orbits, and what travels a wire only under its wire-flow; the sheath, cutter
  // and sheen only for the wire styles that have them, and a head's other shapes only when it is picked
  for (const sel of ['.orbit', '[data-fx="on"] .orbit-pk']) assert.match(body(sel), /display:var\(--look-orbits,none\)/, sel)
  assert.match(body('[data-fx="on"] .pk'), /display:var\(--look-comet,none\)/)
  assert.match(body('[data-fx="on"] .blk'), /display:var\(--look-blocks,none\)/)
  assert.match(body('[data-fx="on"] .pulse'), /display:var\(--look-pulse,none\)/)
  for (const part of ['wsh', 'wcut', 'wsn']) assert.match(body(`.${part}`), new RegExp(`display:var\\(--look-${part},none\\)`), part)
  for (const head of ['plug', 'dot', 'bar']) assert.match(body(`.h-${head}`), new RegExp(`display:var\\(--look-h-${head},none\\)`), head)
  assert.match(body('[data-fx="on"] .node'), /animation-name:var\(--look-beat,none\)/)
  // a look var that is not set leaves nothing drawn: every multiplier falls back to 0
  assert.match(body('[data-fx="on"] .gly'), /display:var\(--look-glyphs,none\)/)
  for (const set of ['binary', 'hex', 'kana', 'ascii']) assert.match(body(`.gs-${set}`), new RegExp(`display:var\\(--look-gs-${set},none\\)`), set)
  for (const v of ['look-orbs', 'look-grid-h', 'look-grid-v', 'look-dots', 'backdrop-strength', 'backdrop-scan', 'look-stage-dots', 'look-stage-glow', 'look-gutter', 'card-bloom', 'card-glow', 'look-head-rule', 'look-head-float', 'stage-backing']) {
    const uses = [...css.matchAll(new RegExp(`var\\(--${v}(,[^)]*)?\\)`, 'g'))]
    assert.ok(uses.length > 0, `--${v} is read`)
    for (const u of uses) assert.equal(u[1]?.replace(/\s/g, ''), ',0', `--${v} falls back to 0`)
  }
})

test('the rules read every variable the look tokens produce, and the numbers they come with', () => {
  const vars = [...new Set([...Object.values(LOOK).flatMap((t) => Object.values(t).flatMap((row) => Object.keys(row))), '--look-scan'])]
  const numbers = ['--backdrop-size', '--backdrop-strength', '--backdrop-scan', '--card-rim', '--card-bloom', '--card-glow']
  assert.deepEqual([...vars, ...numbers].filter((v) => !css.includes(`var(${v}`)), [])
  // card-cut is read through the clip and the cut art LOOK writes, not here
  assert.ok(Object.values(LOOK['card-corner']).some((row) => Object.values(row).some((v) => v.includes('var(--card-cut)'))))
})

test('the app and the preview load the same rules: main.ts injects LOOK_CSS first in the head, and a preview carries LOOK_RULES whole', () => {
  const main = readFileSync(new URL('../../main.ts', import.meta.url), 'utf8')
  assert.match(main, /look\.textContent = LOOK_CSS\s*\n\s*document\.head\.prepend\(look\)/)
  for (const t of [defaultTheme, neon]) assert.ok(previewDoc(t as unknown as ThemeManifest, 'dark', defaultTheme as unknown as ThemeManifest).includes(LOOK_RULES))
  assert.equal(LOOK_RULES, LOOK_CSS.replace(/\/\*[\s\S]*?\*\//g, ''))
  assert.ok(!/\/\*/.test(LOOK_RULES), 'no comment opener survives for the preview to mangle')
})

test('a cut card keeps a visible focus ring: the cuts move it inside the card, where the clip cannot take it, and redraw the rim', () => {
  assert.match(body('.surf:focus-visible'), /outline-offset:var\(--look-focus,3px\)/)
  for (const cut of ['chamfer', 'notch'] as const) {
    const row = LOOK['card-corner'][cut]
    assert.match(row['--look-clip'], /^polygon\(/, cut)
    assert.ok(Number.parseFloat(row['--look-focus']) < 0, `${cut} puts the ring inside`)
    assert.equal(row['--look-cut'], '""', `${cut} draws the rim along the cut`)
  }
  for (const flat of ['round', 'square'] as const) assert.equal(LOOK['card-corner'][flat]['--look-clip'], 'none')
})

// a selected card's outline sits inside a cut, against the card's own rim: there it is 2px in the accent, so it reads at a glance
test('a selected surface is outlined by the look: 1px as before on a plain corner, 2px in the accent inside a cut', () => {
  assert.match(body('.surf[aria-pressed="true"]:not(:focus-visible)'), /outline:var\(--look-sel,1pxsolidcolor-mix\(inoklch,var\(--tx\)55%,transparent\)\)/)
  for (const flat of ['round', 'square'] as const) assert.equal(LOOK['card-corner'][flat]['--look-sel'], '1px solid color-mix(in oklch, var(--tx) 55%, transparent)', flat)
  for (const cut of ['chamfer', 'notch'] as const) assert.equal(LOOK['card-corner'][cut]['--look-sel'], '2px solid color-mix(in oklch, var(--ac) 70%, transparent)', cut)
  const ow = readFileSync(new URL('../../pages/Overview.svelte', import.meta.url), 'utf8')
  assert.doesNotMatch(ow, /class="surf[^"]*"[^>]*style:outline/, 'a surface takes its selection outline from the look')
})

// the agent cards' ports sit half outside the card's edge, where a cut's clip would take half of each away
test('a card\'s ports stand out from its edge on a plain corner and tuck inside it under a cut, and forced colors drop the cut', () => {
  assert.match(body('.port-t'), /top:calc\(2px-7px\*var\(--look-out,1\)\)/)
  assert.match(body('.port-b'), /bottom:calc\(2px-7px\*var\(--look-out,1\)\)/)
  for (const cut of ['chamfer', 'notch'] as const) assert.equal(LOOK['card-corner'][cut]['--look-out'], '0', cut)
  for (const flat of ['round', 'square'] as const) assert.equal(LOOK['card-corner'][flat]['--look-out'], '1', flat)
  const ow = readFileSync(new URL('../../pages/Overview.svelte', import.meta.url), 'utf8')
  assert.doesNotMatch(ow, /(?:top|bottom): -5px; width: 8px/, 'the ports take their place from the look')
  assert.equal((ow.match(/class="port-[tb]"/g) ?? []).length, 4)
  // forced colors paint no box-shadow and no rim art, so a cut would leave the border open at every corner
  assert.match(css, /@media \(forced-colors:active\)\{\.surf\{clip-path:none\}/)
})

// Reduce transparency comes from the OS (the media query) or from Conn.app, which marks the root itself since WebKit does not
test('under Reduce transparency every surface, the stage, the top bar and the footer is solid, nothing frosts and the wallpaper is not drawn, from either source', () => {
  const reduce = '[data-transparency="reduce"]'
  assert.match(body(`${reduce} .surf`), /var\(--look-fill-solid,var\(--look-fill,var\(--wash,transparent\)\)\)/)
  assert.match(body(`${reduce} .surf`), /backdrop-filter:none;--look-surf-blur:none;--look-surf-blur-in:none/)
  // the stage, the top bar and the footer are drawn by their markup's inline style, which reads these
  assert.equal(body(`${reduce} .stage`), '--look-surf-blur:none;--look-stage-panel:var(--panel)')
  assert.match(body(`${reduce} .page-head`), /backdrop-filter:none;--look-head-fill:100%/)
  assert.equal(body(`${reduce} .page-foot`), '--look-foot-blur:none;--look-foot-fill:100%')
  assert.equal(body(`${reduce} .page::before`), 'content:none')
  // the OS's rules weigh what the attribute's do (:root), so both outweigh the nested-surface rule they follow
  const media = css.match(/@media \(prefers-reduced-transparency: reduce\)\{(.*)\}$/m)?.[1].replace(/\s+/g, '') ?? ''
  for (const sel of ['.surf', '.stage', '.page-head', '.page-foot', '.page::before']) assert.ok(media.includes(`:root${sel}{${body(`${reduce} ${sel}`)}}`), sel)
  assert.ok(css.indexOf('@media (prefers-reduced-transparency') > css.indexOf(':is(.surf,.stage) .surf{'))
})

// the markup that draws the stage, the top bar and the footer reads the variables Reduce transparency sets
test('the stage, every top bar and the footer take their blur and fill from variables Reduce transparency can set', () => {
  const pages = new URL('../../pages/', import.meta.url)
  const heads = readdirSync(pages).filter((f) => f.endsWith('.svelte')).flatMap((f) => readFileSync(new URL(f, pages), 'utf8').match(/<[a-z]+ class="page-head[^>]*>/g) ?? [])
  assert.ok(heads.length >= 10, 'every page has a top bar')
  for (const head of heads) assert.match(head, /background: color-mix\(in oklch, var\(--panel\) var\(--look-head-fill, 72%\), transparent\)/)
  const stage = readFileSync(new URL('Overview.svelte', pages), 'utf8').match(/<[a-z]+ [^>]*class="brk stage"[^>]*>/)?.[0] ?? ''
  assert.match(stage, /backdrop-filter: var\(--look-surf-blur, blur\(6px\)\)/)
  const foot = readFileSync(new URL('../StatusFooter.svelte', import.meta.url), 'utf8').match(/<footer[^>]*>/)?.[0] ?? ''
  assert.match(foot, /class="page-foot"/)
  assert.match(foot, /var\(--panel\) var\(--look-foot-fill, 90%\)/)
  assert.match(foot, /[^-]backdrop-filter: var\(--look-foot-blur, blur\(14px\)\)/)
})

// a surface inside another or on the stage filters again only while the theme's glass is untuned (Matrix keeps its blur)
test('a nested surface keeps the glass of before and drops it once a theme tunes its glass', () => {
  assert.match(body(':is(.surf,.stage) .surf'), /[^-]backdrop-filter:var\(--look-surf-blur-in,var\(--look-glass,none\)\)/)
  assert.equal(LOOK['card-fill'].glass['--look-glass'].startsWith('blur('), true)
})


// header-shape: the float's shape is the stylesheet's, since the markup's inline style sets only the bar's padding, fill and
// bottom edge; every piece is a multiple of --look-head-float, so a bar (and a theme from before the slot) is today's bar
test('a float top bar is inset, rounded to its tabs, ringed and lifted, and a bar keeps none of it', () => {
  const head = body('.page-head')
  assert.match(head, /margin:calc\(12px\*var\(--look-head-float,0\)\)calc\(16px\*var\(--look-head-float,0\)\)calc\(8px\*var\(--look-head-float,0\)\)/)
  assert.match(head, /border-radius:calc\(\(var\(--radius,0px\)\+14px\)\*var\(--look-head-float,0\)\)/)
  assert.match(head, /border-width:calc\(1px\*var\(--look-head-float,0\)\)/)
  assert.match(head, /box-shadow:inset0calc\(-2px\*var\(--look-head-rule,0\)\)0var\(--edge\),var\(--look-head-lift,00transparent\)/)
  assert.match(body('.glint'), /margin-inline:calc\(\(var\(--radius,0px\)\+14px\)\*var\(--look-head-float,0\)\)/)
  assert.deepEqual(LOOK['header-shape'], {
    bar: { '--look-head-float': '0', '--look-head-lift': 'initial', '--look-head-ring': 'initial' },
    float: { '--look-head-float': '1', '--look-head-lift': 'var(--lift)', '--look-head-ring': 'color-mix(in oklch, var(--edge) 16%, transparent)' },
  })
})
