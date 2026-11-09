// New session's form on the Sessions page is drawn only from theme tokens, mixed the way its neighbours mix them. Each pair
// of text and what it is drawn on is held to 4.5:1 in every look the built-in themes paint (each theme, mode and variant),
// over each surface the look's card-fill can put under it (see cards() for what is and is not modelled).
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { composite, contrast, parseColor, type Rgba } from '../src/lib/theme/color.ts'
import { SLOTS } from '../src/lib/theme/tokens.ts'
import { looks, mixOklab, type Look } from './looks.ts'

const form = readFileSync(new URL('../src/lib/NewSession.svelte', import.meta.url), 'utf8')
const BLACK: Rgba = { r: 0, g: 0, b: 0, a: 1 }
const MIN = 4.5

const color = (l: Look, slot: string): Rgba => {
  const c = parseColor(l.tokens[slot] ?? '')
  assert.ok(c, `${l.name}: ${slot} is a color`)
  return c
}
const page = (l: Look) => composite(color(l, 'bg'), BLACK)
const faded = (c: Rgba, share: number): Rgba => ({ ...c, a: c.a * share })
// The far rain trail, Matrix's brief case (themes/matrix.test.ts): its `ac` at alpha .5 over the page. Copied from rain.ts's ALPHA.
const RAIN_FAR = 0.5

/**
 * What the form's card (.surf, with the --wash it sets) can be under its text, by the look's card-fill (src/lib/theme/css.ts):
 * plain is the panel over the page; wash paints the card's own --wash, a gradient from edge at 14% to the panel at 80%, so both
 * ends are modelled; glass is the panel at 55%, over the page and over the far rain trail. Not modelled: a rain head at full
 * alpha under glass (Matrix accepts that for its own colours, see matrix.test.ts), the backdrop blur, and CRT's scanlines and
 * text glow, which draw over the text rather than under it.
 */
const cards = (l: Look): Rgba[] => {
  const panel = color(l, 'panel')
  switch (l.tokens['card-fill']) {
    case 'wash':
      return [composite(faded(color(l, 'edge'), 0.14), page(l)), composite(faded(panel, 0.8), page(l))]
    case 'glass':
      return [
        composite(faded(panel, 0.55), page(l)),
        composite(faded(panel, 0.55), composite({ ...color(l, 'ac'), a: RAIN_FAR }, page(l))),
      ]
    default:
      return [composite(panel, page(l))]
  }
}
const tint = (l: Look, slot: string, alpha: number) => composite({ ...color(l, slot), a: alpha }, page(l))

/** Each text in the form, the surface under it, and how the markup paints it (held to the source below) */
const PAIRS: { name: string; text: (l: Look) => Rgba; on: (l: Look) => Rgba[]; source: RegExp }[] = [
  {
    name: 'New session',
    text: (l) => mixOklab(color(l, 'role-lead'), 65, color(l, 'bright')),
    on: (l) => [tint(l, 'role-lead', 0.1)],
    source: /background: color-mix\(in oklch, var\(--role-lead\) 10%, transparent\); color: color-mix\(in oklab, var\(--role-lead\) 65%, var\(--bright\)\)">New session/,
  },
  { name: 'Start', text: (l) => color(l, 'onac'), on: (l) => [color(l, 'ac')], source: /background: var\(--ac\); color: var\(--onac\)/ },
  { name: 'a field', text: (l) => color(l, 'tx'), on: (l) => cards(l).map((under) => composite(color(l, 'sunk'), under)), source: /color: var\(--tx\); background: var\(--sunk\)/ },
  {
    name: 'a label',
    text: (l) => mixOklab(color(l, 'mu'), 50, color(l, 'tx')),
    on: cards,
    source: /color: color-mix\(in oklab, var\(--mu\) 50%, var\(--tx\)\)">Repository/,
  },
  { name: 'Cancel', text: (l) => color(l, 'tx'), on: cards, source: /const plain = `\$\{btn\}[^`]*color: var\(--tx\);`/ },
  {
    name: 'the error',
    text: (l) => mixOklab(color(l, 'bad'), 55, color(l, 'bright')),
    on: cards,
    source: /role="alert"[^>]*color: color-mix\(in oklab, var\(--bad\) 55%, var\(--bright\)\)/,
  },
  {
    name: 'the dry-run note',
    text: (l) => mixOklab(color(l, 'warn'), 55, color(l, 'bright')),
    on: cards,
    source: /color: color-mix\(in oklab, var\(--warn\) 55%, var\(--bright\)\)">Dry run/,
  },
  { name: 'a command', text: (l) => color(l, 'tx'), on: (l) => cards(l).map((under) => composite(color(l, 'sunk'), under)), source: /<code data-content style="[^"]*background: var\(--sunk\)[^"]*color: var\(--tx\)/ },
]

test('every text in the form is at least 4.5:1 on what it is drawn on, in every look', () => {
  const all = looks()
  assert.ok(all.length >= 6, `the looks were found (${all.length})`)
  const low = all.flatMap((l) =>
    PAIRS.map((p) => ({ at: `${l.name}: ${p.name}`, ratio: Math.min(...p.on(l).map((under) => contrast(p.text(l), under))) })).filter(
      (r) => r.ratio < MIN,
    ),
  )
  assert.deepEqual(low.map((r) => `${r.at} ${r.ratio.toFixed(2)}:1`), [])
})

test('every card-fill a built-in look uses is one the test models', () => {
  for (const l of looks()) assert.ok(['plain', 'wash', 'glass'].includes(l.tokens['card-fill'] ?? 'plain'), `${l.name}: ${l.tokens['card-fill']}`)
})

test('the form sets the --wash a wash look paints, as the Sessions page tiles do', () => {
  assert.match(form, /class="surf"\s+style="[^"]*--wash: linear-gradient\(160deg, color-mix\(in oklch, var\(--edge\) 14%, transparent\), color-mix\(in oklch, var\(--panel\) 80%, transparent\) 60%\)/)
})

test('each pair above is how the form really paints it', () => {
  for (const p of PAIRS) assert.match(form, p.source, p.name)
})

test('the form names only theme tokens, and no colour of its own', () => {
  const used = [...new Set([...form.matchAll(/var\(--([a-z0-9-]+)/g)].map((m) => m[1]))]
  assert.deepEqual(used.filter((name) => !Object.hasOwn(SLOTS, name)), [])
  assert.doesNotMatch(form, /#[0-9a-f]{3,8}\b|\b(rgba?|hsla?|oklch|oklab|lab|lch|hwb)\(/i)
})

test('its corners and borders follow the theme: every radius is the token, every border its style', () => {
  for (const r of form.match(/border-radius: [^;"]+/g) ?? []) assert.match(r, /var\(--radius\)/, r)
  for (const b of form.match(/border: [^;"]+/g) ?? []) assert.match(b, /^border: 1px var\(--border-style\) /, b)
})

test('the check is not vacuous: the first draft (onac on role-lead for Start, plain warn for the dry note) fails it in default light', () => {
  const light = looks().find((l) => l.name === 'default light')
  assert.ok(light, 'default light is a look')
  assert.ok(contrast(color(light, 'onac'), color(light, 'role-lead')) < MIN)
  assert.ok(contrast(color(light, 'warn'), cards(light)[0]) < MIN)
})
