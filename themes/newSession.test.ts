// New session's form on the Sessions page is drawn only from theme tokens, mixed the way its neighbours mix them. Each pair
// of text and what it is drawn on is held to 4.5:1 in every look the built-in themes paint (each theme, mode and variant).
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
/** The form's card: the panel over the page (.surf), the floor every label and message is read on */
const card = (l: Look) => composite(color(l, 'panel'), page(l))
const tint = (l: Look, slot: string, alpha: number) => composite({ ...color(l, slot), a: alpha }, page(l))

/** Each text in the form, the surface under it, and how the markup paints it (held to the source below) */
const PAIRS: { name: string; text: (l: Look) => Rgba; on: (l: Look) => Rgba; source: RegExp }[] = [
  {
    name: 'New session',
    text: (l) => mixOklab(color(l, 'role-lead'), 65, color(l, 'bright')),
    on: (l) => tint(l, 'role-lead', 0.1),
    source: /background: color-mix\(in oklch, var\(--role-lead\) 10%, transparent\); color: color-mix\(in oklab, var\(--role-lead\) 65%, var\(--bright\)\)">New session/,
  },
  { name: 'Start', text: (l) => color(l, 'onac'), on: (l) => color(l, 'ac'), source: /background: var\(--ac\); color: var\(--onac\)/ },
  { name: 'a field', text: (l) => color(l, 'tx'), on: (l) => composite(color(l, 'sunk'), card(l)), source: /color: var\(--tx\); background: var\(--sunk\)/ },
  { name: 'a label', text: (l) => color(l, 'mu'), on: card, source: /color: var\(--mu\)">Repository/ },
  { name: 'Cancel', text: (l) => color(l, 'tx'), on: card, source: /const plain = `\$\{btn\}[^`]*color: var\(--tx\);`/ },
  {
    name: 'the error',
    text: (l) => mixOklab(color(l, 'bad'), 55, color(l, 'bright')),
    on: card,
    source: /role="alert"[^>]*color: color-mix\(in oklab, var\(--bad\) 55%, var\(--bright\)\)/,
  },
  {
    name: 'the dry-run note',
    text: (l) => mixOklab(color(l, 'warn'), 55, color(l, 'bright')),
    on: card,
    source: /color: color-mix\(in oklab, var\(--warn\) 55%, var\(--bright\)\)">Dry run/,
  },
  { name: 'a command', text: (l) => color(l, 'tx'), on: (l) => composite(color(l, 'sunk'), card(l)), source: /<code data-content style="[^"]*background: var\(--sunk\)[^"]*color: var\(--tx\)/ },
]

test('every text in the form is at least 4.5:1 on what it is drawn on, in every look', () => {
  const all = looks()
  assert.ok(all.length >= 6, `the looks were found (${all.length})`)
  const low = all.flatMap((l) =>
    PAIRS.map((p) => ({ at: `${l.name}: ${p.name}`, ratio: contrast(p.text(l), p.on(l)) })).filter((r) => r.ratio < MIN),
  )
  assert.deepEqual(low.map((r) => `${r.at} ${r.ratio.toFixed(2)}:1`), [])
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
  assert.ok(contrast(color(light, 'warn'), card(light)) < MIN)
})
