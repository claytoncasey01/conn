// A card, panel, stat tile or row (a `.surf`) gets its fill, edge and depth from the theme, so each look can draw cards its own
// way: Default a plain panel with a hairline, Neon its tinted wash and brackets, Matrix a pane of glass, CRT a double-ruled box.
// The markup hands a surface its state as data (--rim, --wash, --halo) and never paints it inline, where no theme could reach.
// Text-based like the other source tests: it reads every style attribute and <style> rule in src/**/*.svelte.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'
import { LOOK_CSS } from './theme/lookCss.ts'
import { WASH_MAX } from './theme/validate.ts'
import { SLOTS } from './theme/tokens.ts'

const root = new URL('../../', import.meta.url).pathname
const files = [...new Glob('src/**/*.svelte').scanSync(root)].sort()
const read = (f: string) => readFileSync(root + f, 'utf8')
const strip = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '')

/** Opening tags (attributes and all, Svelte expressions included) */
const tags = (text: string) => [...text.matchAll(/<[a-z]+\b((?:[^>{}]|\{(?:[^{}]|\{[^{}]*\})*\})*)>/g)].map((m) => m[1])
const classes = (attrs: string) => /\bclass="([^"]*)"/.exec(attrs)?.[1].split(/\s+/) ?? []
const style = (attrs: string) => /\bstyle="([^"]*)"/.exec(attrs)?.[1] ?? ''
const paints = (decls: string) => [...decls.matchAll(/(?:^|[;{\s])(background|border|box-shadow)\s*:/g)].map((m) => m[1])

/** What is wrong with the surfaces in one svelte file: a surface painted inline, or surface data on an element that is not one */
export function surfaceProblems(f: string, text: string): string[] {
  const out: string[] = []
  for (const attrs of tags(text)) {
    const surf = classes(attrs).includes('surf')
    const st = style(attrs)
    if (surf) for (const p of paints(st)) out.push(`${f}: a surface paints its ${p} inline`)
    if (!surf && /(?:^|[;\s])--(?:wash|rim|halo)\s*:/.test(st)) out.push(`${f}: surface data on an element that is not a .surf`)
  }
  return out
}

/** Neon's tinted card wash (a 160 or 170 degree gradient) painted as a background, inline or in a <style> rule */
export function inlineWashes(f: string, text: string): string[] {
  const css = [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => strip(m[1])).join('\n')
  const attrs = text.replace(/<style[^>]*>[\s\S]*?<\/style>/g, '')
  return [css, attrs].flatMap((s) => [...s.matchAll(/(?:^|[;{\s])background\s*:\s*linear-gradient\(1[67]0deg/g)]).map(() => `${f}: a card wash painted as a background`)
}

test('a surface is never painted inline, and only a surface carries surface data', () => {
  const offenders = files.flatMap((f) => surfaceProblems(f, read(f)))
  const count = files.reduce((n, f) => n + tags(read(f)).filter((a) => classes(a).includes('surf')).length, 0)
  assert.ok(count >= 40, `the surfaces of the pages were found (${count})`)
  assert.deepEqual(offenders, [])
})

test('no card, tile or row paints Neon\'s tinted wash as its background: it hands it over as --wash', () => {
  assert.deepEqual(files.flatMap((f) => inlineWashes(f, read(f))), [])
})

test('the look draws a surface from the look tokens: fill, rim and sides, radius or cut, and the stacked shadows; Neon\'s CSS draws none of it', () => {
  const look = strip(LOOK_CSS)
  const surf = /(?:^|\})\s*\.surf\{([^}]*)\}/.exec(look)?.[1].replace(/\s*\n\s*/g, '') ?? ''
  assert.match(surf, /--rimc:color-mix\(in oklch, var\(--rim, var\(--line\)\) calc\(var\(--card-rim, \.5\) \* 100%\), var\(--line\)\)/)
  // the fill last, under the highlight (an image, or none) that each surface places at --sx and --sy
  assert.match(surf, /background:var\(--look-sheen, none\) calc\(var\(--sx\) - 240px\) calc\(var\(--sy\) - 240px\)\/480px 480px no-repeat, var\(--look-fill, var\(--wash, transparent\)\)/)
  assert.match(surf, /border-color:color-mix\(in oklch, var\(--rimc\) calc\(var\(--look-edge-top, 1\) \* 100%\), transparent\) color-mix\(in oklch, var\(--rimc\) calc\(var\(--look-edge-sides, 1\) \* 100%\), transparent\)/)
  assert.match(surf, /border-radius:calc\(var\(--r, var\(--radius\)\) \* var\(--look-round, 1\)\);clip-path:var\(--look-clip, none\)/)
  assert.match(surf, /box-shadow:var\(--look-edge-light, 0 0 transparent\), var\(--look-frame, [^;]*var\(--card-bloom, 0\)[^;]*var\(--look-gutter, 0\)[^;]*var\(--card-glow, 0\)[^;]*var\(--look-shadow, var\(--halo, 0 0 transparent\)\)/)
  assert.match(look, /(?:^|\})\s*\.stripe\{display:var\(--look-stripe, none\)\}/)
  assert.doesNotMatch(strip(read('themes/neon/theme.css')), /\.surf|\.stripe/)
})

/** The tints a surface's --wash lays over its panel (not the panel's own share), as fractions: N% or calc({a ? x : y} * 100%).
 * A wash bound from a script table (`--wash: {s.bg}`) is read from every `bg:` string in that file's tables. */
export function washTints(text: string): number[] {
  const out: number[] = []
  const tints = (wash: string) => {
    for (const mix of wash.matchAll(/color-mix\(in oklch, var\(--([a-z-]+)\) ([^,]+), transparent\)/g)) {
      if (mix[1] === 'panel' || mix[1] === 'raise') continue
      const pct = /^([\d.]+)%$/.exec(mix[2].trim())
      if (pct) out.push(Number(pct[1]) / 100)
      else for (const n of mix[2].matchAll(/\b(0?\.\d+|1)\b(?=[^%]*\* 100%)/g)) out.push(Number(n[1]))
    }
  }
  for (const m of text.matchAll(/--wash:\s*([^;"]*(?:\{[^}]*\}[^;"]*)*)/g)) tints(m[1])
  if (/--wash:\s*\{\w+\.bg\}/.test(text)) for (const m of text.matchAll(/\bbg:\s*'([^']*)'/g)) tints(m[1])
  return out
}

test('no card wash is stronger than the tint the contrast check measures (WASH_MAX)', () => {
  const tints = files.flatMap((f) => washTints(read(f)).map((t) => [f, t] as const))
  assert.ok(tints.length >= 30, `the washes were read (${tints.length})`)
  assert.deepEqual(tints.filter(([, t]) => t > WASH_MAX), [])
  assert.deepEqual(washTints('style="--wash: linear-gradient(170deg, color-mix(in oklch, var(--warn) 30%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)"'), [0.3])
  assert.deepEqual(washTints("style=\"--wash: linear-gradient(170deg, color-mix(in oklch, var(--tx) calc({on ? 0.2 : 0.05} * 100%), transparent), var(--panel))\""), [0.2, 0.05])
  // a wash bound from a script table ({s.bg}) is read from that table's bg strings
  assert.deepEqual(washTints("<script>const S = { ask: { bg: 'linear-gradient(160deg, color-mix(in oklch, var(--warn) 45%, transparent), var(--panel))' } }</script><div class=\"surf\" style=\"--wash: {s.bg}\">"), [0.45])
  assert.ok(washTints(read('src/pages/TicketRun.svelte')).length >= 10, 'Ticket Run\'s stage and card washes are read')
})

/** Cards a style test cannot see in a literal attribute: a fill and rim both bound from data ({s.bg}, {s.border}) on an element
 * that is not a .surf, and a card written as a script string (a rim, the card radius and a fill) instead of surface data */
export function hiddenCards(f: string, text: string): string[] {
  const out: string[] = []
  for (const attrs of tags(text)) {
    const st = style(attrs)
    const card = /border-radius:\s*calc\(var\(--radius\) \* 1\.5\)/.test(st)
    // a .row is a selectable list row: its fill and rim show which one is selected, and its hover moves its border (Rows, in the report)
    if (!classes(attrs).includes('surf') && !classes(attrs).includes('row') && card && /(?:^|[;\s])background:\s*\{/.test(st) && /(?:^|[;\s])border:\s*1px var\(--border-style\) \{/.test(st)) out.push(`${f}: a card's fill and rim bound from data on an element that is not a .surf`)
  }
  for (const m of text.matchAll(/'([^'\n]*)'/g)) {
    const v = m[1]
    if (/(?:^|[;\s])border:\s*1px var\(--border-style\)/.test(v) && /border-radius:\s*calc\(var\(--radius\) \* 1\.5\)/.test(v) && /(?:^|[;\s])background:/.test(v)) out.push(`${f}: a card written as a string: ${v.slice(0, 50)}…`)
  }
  return out
}

test('a card (the card radius) whose fill and rim come from data or from a script string is a surface too', () => {
  assert.deepEqual(files.flatMap((f) => hiddenCards(f, read(f))), [])
  assert.deepEqual(hiddenCards('x', '<div style="overflow: hidden; background: {s.bg}; border: 1px var(--border-style) {s.border}; border-radius: calc(var(--radius) * 1.5)">'), ["x: a card's fill and rim bound from data on an element that is not a .surf"])
  assert.deepEqual(hiddenCards('x', '<div class="surf" style="--wash: {s.bg}; --rim: {s.border}">'), [])
  assert.deepEqual(hiddenCards('x', '<span style="background: {on}; border: 1px var(--border-style) {line}; border-radius: var(--radius)">'), [], 'a chip is not a card')
  assert.equal(hiddenCards('x', "const card = 'padding: 4px; border: 1px var(--border-style) red; border-radius: calc(var(--radius) * 1.5); background: var(--panel)'").length, 1)
  assert.deepEqual(hiddenCards('x', "const pill = 'border: 1px var(--border-style) red; border-radius: calc(var(--radius) * 0.75); background: none'"), [])
})

/** The padding of each surface, as [top, right, bottom, left] px, from its style (a literal, or a script string it names) */
export function surfacePaddings(text: string): number[][] {
  const consts = new Map([...text.matchAll(/const (\w+) = '([^']*)'/g)].map((m) => [m[1], m[2]]))
  const out: number[][] = []
  for (const attrs of tags(text)) {
    if (!classes(attrs).includes('surf')) continue
    const named = /\bstyle=\{(\w+)\}/.exec(attrs)?.[1]
    const st = (named ? consts.get(named) ?? '' : style(attrs)).replace(/\{(\w+)\}/g, (_, n: string) => consts.get(n) ?? '')
    const pad = [...st.matchAll(/(?:^|[;\s])padding:\s*([^;]+)/g)].at(-1)?.[1].trim().split(/\s+/).map((v) => Number.parseFloat(v))
    if (!pad || pad.some(Number.isNaN)) continue
    const [t, r = t, b = t, l = r] = pad
    out.push([t, r, b, l])
  }
  return out
}

// A chamfer or notch clips the card's corner, content and all: a cut of c removes everything where x + y < c from that corner,
// so every surface's padding has to keep its first and last letters clear of the deepest cut the contract allows.
test('no card-cut the contract allows reaches a surface\'s text: each corner\'s padding (x + y) clears the deepest cut by 6px', () => {
  const max = SLOTS['card-cut'].max ?? 0
  const pads = [...files, 'src/lib/AskPanel.svelte'].flatMap((f) => surfacePaddings(read(f)).map((p) => [f, p] as const))
  assert.ok(pads.length >= 45, `the surfaces' paddings were read (${pads.length})`)
  const tight = pads.filter(([, [t, r, b, l]]) => Math.min(t + l, t + r, b + l, b + r) < max + 6).map(([f, p]) => `${f}: ${p.join(' ')}`)
  assert.deepEqual(tight, [], `a ${max}px cut would clip the text of these`)
  // AskPanel pads in its scoped style, which this does not read: 18px 22px clears it too
  assert.ok(18 + 22 >= max + 6)
  assert.deepEqual(surfacePaddings('<div class="surf" style="--r: 4px; padding: 12px 14px">'), [[12, 14, 12, 14]])
  assert.deepEqual(surfacePaddings("<script>const tile = 'padding: 2px 3px 4px 5px'</script><div class=\"surf\" style=\"{tile}; --wash: red\">"), [[2, 3, 4, 5]])
})

// A cut corner clips everything outside its polygon, an outline at a positive offset included: a surface that draws its own
// outline (the Overview's selection) or sets its own offset (which beats the look's focus rule) offsets it by the look's
// focus offset, which a cut turns negative, so the ring is drawn inside the card.
test('a surface that places its own outline places it by the look\'s focus offset, so a cut corner never clips it away', () => {
  const own = files.flatMap((f) => tags(read(f)).filter((a) => classes(a).includes('surf') && (/\bstyle:outline\b/.test(a) || /outline-offset\s*:/.test(style(a)))).map((a) => [f, a] as const))
  assert.ok(own.length >= 2, `the selectable surfaces were found (${own.length})`)
  const fixed = own.filter(([, a]) => !/outline-offset\s*:\s*calc\(var\(--look-focus, 3px\) \+ 1px\)/.test(style(a))).map(([f, a]) => `${f}: ${/outline-offset\s*:[^;"]*/.exec(style(a))?.[0] ?? 'no outline-offset'}`)
  assert.deepEqual(fixed, [])
})

/** A page's :hover rules for the classes its surfaces carry that replace the look's shadow stack or lift without hover-lift */
export function surfaceHovers(f: string, text: string): string[] {
  const names = new Set(tags(text).filter((a) => classes(a).includes('surf')).flatMap((a) => classes(a)).filter((c) => /^[a-z][\w-]*$/.test(c) && c !== 'surf'))
  const css = [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => strip(m[1])).join('\n')
  const out: string[] = []
  for (const r of css.matchAll(/([^{}]*):hover\s*\{([^}]*)\}/g)) {
    const name = /\.([\w-]+)\s*$/.exec(r[1])?.[1]
    if (!name || !names.has(name)) continue
    if (/(?:^|[;\s])box-shadow\s*:/.test(r[2])) out.push(`${f}: .${name}:hover replaces the look's shadows`)
    if (/translate/.test(r[2]) && !/var\(--look-lift/.test(r[2])) out.push(`${f}: .${name}:hover lifts without hover-lift`)
  }
  return out
}

// A hover on a surface adds to the look, never replaces it: the frame, gutter, bloom and lift are box-shadow layers, so a
// hover that sets box-shadow takes them away (CRT's double rule, Matrix's gutter); a lift follows the hover-lift token
test('a surface\'s hover adds to the look\'s shadows and lifts only under hover-lift', () => {
  assert.deepEqual(files.flatMap((f) => surfaceHovers(f, read(f))), [])
  assert.deepEqual(surfaceHovers('x', '<div class="surf sess"></div><style>.sess:hover { transform: translateY(-3px); box-shadow: 0 0 9px red }</style>'), ['x: .sess:hover replaces the look\'s shadows', 'x: .sess:hover lifts without hover-lift'])
  assert.deepEqual(surfaceHovers('x', '<div class="surf sess"></div><style>.sess:hover { --lit: 1; transform: translateY(calc(-3px * var(--look-lift, 0))) }</style>'), [])
})

test('the checks name a painted surface, stray data and an inline wash, and pass the converted forms', () => {
  assert.deepEqual(surfaceProblems('x', '<div class="surf brk" style="--c: red; --wash: var(--panel); --rim: red">'), [])
  assert.deepEqual(surfaceProblems('x', '<div class="surf" style="padding: 4px; background: var(--panel); border: 1px solid red">'), ['x: a surface paints its background inline', 'x: a surface paints its border inline'])
  assert.deepEqual(surfaceProblems('x', '<a class="surf" href={h} style="--rim: {on ? \'a\' : \'b\'}; box-shadow: 0 0 4px red">'), ['x: a surface paints its box-shadow inline'])
  assert.deepEqual(surfaceProblems('x', '<div class="tile" style="--wash: red">'), ['x: surface data on an element that is not a .surf'])
  assert.deepEqual(surfaceProblems('x', '<div style="border-top: 1px solid red; background-color: red">'), [])
  assert.deepEqual(inlineWashes('x', '<div style="--wash: linear-gradient(170deg, a, b)"></div>'), [])
  assert.deepEqual(inlineWashes('x', '<div style="padding: 0; background: linear-gradient(160deg, a, b)"></div>'), ['x: a card wash painted as a background'])
  assert.deepEqual(inlineWashes('x', '<style>.panel { background: linear-gradient(170deg, a, b); }</style>'), ['x: a card wash painted as a background'])
})
