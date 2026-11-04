// The pages are laid out for a desktop and must still fit a 320px phone: no sideways page scroll, no clipped tile, no overlap.
// Every fix here acts only below a breakpoint, or is a min()/clamp() that resolves to today's value on a desktop, so 1280 does
// not change. These checks pin each fix as text; the proof is a 320 screenshot per page.
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'

const page = (name: string) => readFileSync(new URL(`../pages/${name}.svelte`, import.meta.url), 'utf8')

test('the session cards grid can shrink to one column on a phone', () => {
  const src = page('Sessions')
  assert.match(src, /grid-template-columns: repeat\(auto-fill, minmax\(min\(100%, 380px\), 1fr\)\)/)
  assert.doesNotMatch(src, /minmax\(380px/)
})

test('a session card on a phone puts its stats under the tree glyph instead of crushing them beside it', () => {
  const src = page('Sessions')
  assert.match(src, /display: flex; flex-wrap: wrap; gap: 14px 18px; align-items: center">\s*<div aria-hidden="true" style="flex-shrink: 0; width: 96px/)
  assert.match(src, /flex: 1; min-width: 150px; display: grid; grid-template-columns: repeat\(3, minmax\(0, 1fr\)\)/)
})

const pageFiles = () => readdirSync(new URL('../pages/', import.meta.url)).filter(f => f.endsWith('.svelte')).map(f => f.replace(/\.svelte$/, ''))
const appCss = readFileSync(new URL('../app.css', import.meta.url), 'utf8')

test('every page header carries the brand through the one Brand component, whose name is short enough for a tracked theme at 320px', () => {
  const branded = pageFiles().filter(f => page(f).includes('<Brand />'))
  assert.equal(branded.length, 11, `the pages that carry the brand: ${branded}`)
  for (const f of branded) {
    assert.match(page(f), /import Brand from '\.\.\/lib\/Brand\.svelte'/, f)
    assert.doesNotMatch(page(f), /class="hud brand"|Command<span/, `${f} draws its own brand`)
  }
  const brand = readFileSync(new URL('./Brand.svelte', import.meta.url), 'utf8')
  assert.match(brand, /class="hud brand"[^>]*>Conn<\/div>/)
  // four letters at Neon's .14em tracking are under 100px, so the 20px size needs no narrow-screen rule (the old long name needed one)
  assert.doesNotMatch(appCss, /--brand-fs/)
  assert.doesNotMatch(brand, /--brand-fs/)
})

test('the stat tile rows are one class that goes two columns on a phone, with an odd last tile across both', () => {
  const rows = [
    ['Sessions', 'aria-label="Fleet summary"', 4],
    ['Overview', 'aria-label="Session stats"', 5],
    ['OpenWork', 'aria-label="Sprint summary"', 4],
    ['TicketRun', 'aria-label="Pipeline"', 4],
  ] as const
  for (const [f, label, cols] of rows) {
    assert.match(page(f), new RegExp(`<section ${label} class="tiles" style="--cols: ${cols}">`), f)
    assert.doesNotMatch(page(f), new RegExp(`${label}[^>]*repeat\\(${cols}, minmax`), f)
  }
  const css = appCss.replace(/\/\*[\s\S]*?\*\//g, '')
  // the desktop grid is what the inline one was: --cols equal columns, 12px apart
  assert.match(css, /\.tiles\{display:grid;grid-template-columns:repeat\(var\(--cols\),minmax\(0,1fr\)\);gap:12px\}/)
  assert.match(css, /@media \(max-width: ?520px\)\s*\{[^@]*\.tiles\{grid-template-columns:repeat\(2,minmax\(0,1fr\)\)\}[^@]*\.tiles>:last-child:nth-child\(odd\)\{grid-column:1\/-1\}/)
})

test('the Overview agent tree scrolls sideways inside its panel on a phone, its two labels stack, and its aside shrinks to the row', () => {
  const src = page('Overview')
  assert.match(src, /<aside aria-label="Selected \{selKind\}" class="surf brk" style="--c: var\(--edge\); flex: 0 1 340px; min-width: min\(280px, 100%\);/)
  assert.doesNotMatch(src, /min-width: 280px/)
  // one flex row holds both labels, so they wrap into two lines instead of printing over each other
  assert.match(src, /<div class="tree-head" style="position: absolute; left: 20px; right: 20px; top: 14px; display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px">\s*<div class="hud"[^>]*>TREE (?:\/\/|<span class="g-sep"[^>]*><\/span>)[^\n]*<\/div>\s*<div [^>]*>\{session\.agentsTotal\} AGENTS/)
  assert.match(src, /<section aria-label="Agent tree" class="brk stage"[^>]*padding: var\(--stage-pt, 44px\) var\(--stage-px, 28px\) 28px/)
  assert.equal(src.match(/top: var\(--stage-pt, 44px\); bottom: 28px; width: 7px/g)?.length, 2, 'both rails start under the labels')
  assert.match(src, /<div class="tree-scroll">\s*<div class="tree-graph" style="position: relative; width: 100%; max-width: 1180px; aspect-ratio: 900 \/ 680; margin: 0 auto">/)
  const css = appCss.replace(/\/\*[\s\S]*?\*\//g, '')
  // the graph is laid out in percent of a 900 x 680 box with fixed-size nodes: too narrow and they overlap, so it keeps a width
  // a cue that the tree scrolls, shown only where it does
  assert.match(src, /<span class="tree-hint">&nbsp;· SWIPE ◂ ▸<\/span>/)
  assert.match(css, /\.tree-hint\{display:none\}/)
  assert.match(css, /@media \(max-width: ?520px\)\s*\{[^@]*\.tree-hint\{display:inline\}/)
  assert.match(css, /@media \(max-width: ?520px\)\s*\{[^@]*\.stage\{--stage-pt:62px;--stage-px:10px\}[^@]*\.tree-scroll\{overflow-x:auto\}[^@]*\.tree-graph\{min-width:720px\}/)
})

test('the Advisor and Agent stats aside shrinks to the row, so its 2-up tiles stay inside their panel, and the time axis thins out', () => {
  for (const f of ['AdvisorPage', 'AgentPage']) {
    const src = page(f)
    assert.match(src, /<aside aria-label="(Advisor|Agent) stats" style="flex: 0 1 360px; min-width: min\(280px, 100%\);/, f)
    assert.doesNotMatch(src, /min-width: 280px/, f)
    assert.match(src, /<span class="tick" style="position: absolute; top: 3px; left: \{p\}%/, f)
  }
  // the detail grid's 150px column floor is the same bug one level down
  assert.match(page('AdvisorPage'), /grid-template-columns: repeat\(auto-fit, minmax\(min\(150px, 100%\), 1fr\)\)/)
  const css = appCss.replace(/\/\*[\s\S]*?\*\//g, '')
  // five labels such as "5m 05s" and "10m 11s" do not fit a 144px axis: every other one goes, and none wraps
  assert.match(css, /@media \(max-width: ?520px\)\s*\{[^@]*\.tick\{white-space:nowrap\}[^@]*\.tick:nth-child\(even\)\{display:none\}/)
})

test('a consult or tool-call row on a phone drops its index and clock and puts its summary on a second line, so no cell is off-screen', () => {
  for (const f of ['AdvisorPage', 'AgentPage']) {
    const src = page(f)
    assert.match(src, /<button class="row rw" type="button"[^\n]*? style="width: 100%; display: flex; flex-wrap: var\(--rw-wrap, nowrap\); align-items: center; gap: 12px;/, f)
    assert.match(src, /<span class="rw-n" style="width: 38px; flex-shrink: 0;/, f)
    assert.match(src, /<span class="rw-t" style="width: 64px; flex-shrink: 0;/, f)
    assert.match(src, /<span data-content class="rw-s" style="flex: 1; min-width: var\(--rw-sum-min, 0\); overflow: hidden;/, f)
  }
  const css = appCss.replace(/\/\*[\s\S]*?\*\//g, '')
  assert.match(css, /@media \(max-width: ?520px\)\s*\{[^@]*\.rw\{--rw-wrap:wrap;--rw-sum-min:100%\}\.rw-n,\.rw-t\{display:none\}\.rw-s\{order:1\}/)
})

test('the Themes page keeps the 32px side gutter every other page has, on a phone too', () => {
  const src = page('ThemesPage')
  const media = src.slice(src.indexOf('@media (max-width: 600px)'), src.indexOf('@media (prefers-reduced-motion: no-preference)'))
  assert.ok(media.includes('.filters'), 'the phone block is still there')
  // 32px gutters leave 250px: the three filters fit it only with 4px side padding
  assert.match(media, /\.filters button\s*\{[^}]*padding: 0 4px;/)
  assert.doesNotMatch(media, /\.themes\s*\{[^}]*padding/)
  assert.match(src, /\.themes\s*\{[^}]*padding: 24px 32px 48px;/)
  // every page's main shares that gutter
  for (const f of ['Sessions', 'OpenWork', 'ReviewsPage', 'MyPrsPage', 'HistoryPage']) assert.match(page(f), /<main style="[^"]*padding: 24px 32px 40px;/, f)
})

test('a review card on a phone left-aligns its model and REVIEW buttons whether or not they wrap, like its neighbours', () => {
  const src = readFileSync(new URL('../pages/ReviewRow.svelte', import.meta.url), 'utf8')
  // margin-left: auto pushes the group to the right when it fits one line (a 10px offset on one card) and nowhere when it wraps
  assert.match(src, /<span class="rb-actions" style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">/)
  assert.match(src, /\.rb-actions \{ margin-left: auto; \}/)
  assert.match(src, /@media \(max-width: 520px\) \{ \.rb-actions \{ margin-left: 0; \} \}/)
})
