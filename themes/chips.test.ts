// The filter chips that would show nothing (count 0) are dimmed, never disabled, and an inactive control may be quiet, but not
// unreadable: their text keeps 3:1 in every look, also over the rain behind the page (its near-white glyph head under the backing).
// The backing exists only while an effect is mounted behind the page, so a theme without one is drawn exactly as it was.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { composite, contrast, parseColor, type Rgba } from '../src/lib/theme/color.ts'
import { CHIP_BACKING, CHIP_DIM } from '../src/lib/chipStyle.ts'
import { looks, mixOklab, type Look } from './looks.ts'

const BLACK: Rgba = { r: 0, g: 0, b: 0, a: 1 }
const color = (l: Look, slot: string): Rgba => {
  const c = parseColor(l.tokens[slot] ?? '')
  assert.ok(c, `${l.name}: ${slot} is a color`)
  return c
}
const bgOf = (l: Look) => composite(color(l, 'bg'), BLACK)

// The brightest thing the rain draws is its column head, a fixed colour that is not a token: read from the effect itself
const rainSrc = readFileSync(new URL('../src/effects/rain.ts', import.meta.url), 'utf8')
const HEAD = parseColor(rainSrc.match(/const HEAD = '([^']+)'/)?.[1] ?? '')
assert.ok(HEAD, 'rain.ts names its head colour')

// What a chip's text is read against: the plain page (no backing: no effect is mounted), or the backing over a rain head, opaque
const surfaces = (l: Look): Rgba[] => [bgOf(l), composite({ ...color(l, 'bg'), a: CHIP_BACKING }, { ...HEAD, a: 1 })]

// the text of an off chip is mu; of a selected chip, role-lead 30% into bright (FilterBar's textColor)
const TEXTS: Record<string, (l: Look) => Rgba> = {
  off: (l) => color(l, 'mu'),
  selected: (l) => mixOklab(color(l, 'role-lead'), 30, color(l, 'bright')),
}

/** The lowest ratio of a dimmed chip's text at `alpha`, on the backing, over each thing that can be behind it */
export function dimmedFloor(l: Look, text: (l: Look) => Rgba, alpha: number): number {
  return Math.min(...surfaces(l).map((surface) => contrast({ ...text(l), a: alpha }, surface)))
}

test('a dimmed chip\'s text is at least 3:1 in every look, over the page and over a rain glyph head', () => {
  for (const l of looks()) {
    for (const [name, text] of Object.entries(TEXTS)) {
      const r = dimmedFloor(l, text, CHIP_DIM)
      assert.ok(r >= 3, `${l.name}: ${name} chip dimmed ${r.toFixed(2)}:1`)
    }
  }
})

test('a dimmed chip is still visibly dimmer than a live one, which is at least 4.5:1', () => {
  for (const l of looks()) {
    const live = contrast(color(l, 'mu'), bgOf(l))
    assert.ok(live >= 4.5, `${l.name}: live chip ${live.toFixed(2)}:1`)
    const dim = dimmedFloor(l, TEXTS.off, CHIP_DIM)
    assert.ok(dim < live * 0.85, `${l.name}: dimmed ${dim.toFixed(2)}:1 is not dimmer than live ${live.toFixed(2)}:1`)
  }
})

test('the check is not vacuous: the old dimming (45%) fails it in every look', () => {
  for (const l of looks()) assert.ok(dimmedFloor(l, TEXTS.off, 0.45) < 3, `${l.name}: 45% would pass`)
})

test('FilterBar takes the dimming and the backing from chipStyle.ts, and the backing applies only while an effect is behind the page', () => {
  const bar = readFileSync(new URL('../src/lib/FilterBar.svelte', import.meta.url), 'utf8')
  assert.match(bar, /import \{ CHIP_BACKING, CHIP_DIM \} from '\.\/chipStyle\.ts'/)
  assert.doesNotMatch(bar, /dim \? 0\.45/)
  // the colour is a variable on the bar; every chip-like button (chips, SHOW ALL, CLEAR) paints --chip-fill, which is transparent
  // until the rule below sets it, so a theme without a canvas behind the page is drawn as it was (an unset fill must not become the UA grey)
  assert.match(bar, /class="chipbar" role="toolbar"[^>]*style="--chip-back: \{BACKING\};/)
  assert.equal(bar.match(/background-color: var\(--chip-fill, transparent\)/g)?.length, 3)
  assert.doesNotMatch(bar, /background-color: \{BACKING\}|background: transparent/)
  // the effects layer behind the page holds a canvas only while one is mounted (the host drops it when effects are off or reduced)
  const layer = readFileSync(new URL('../src/effects/browser.ts', import.meta.url), 'utf8').match(/export const LAYER_ID = '([^']+)'/)?.[1]
  assert.ok(layer, 'browser.ts names the layer')
  assert.match(bar, new RegExp(`:global\\(html:has\\(#${layer} canvas\\)\\) \\.chipbar \\{\\s*--chip-fill: var\\(--chip-back\\);`))
})

test('FilterBar keeps each chip\'s name as authored and speaks the group once: the group label is for the eye only', () => {
  const bar = readFileSync(new URL('../src/lib/FilterBar.svelte', import.meta.url), 'utf8')
  assert.match(bar, /aria-label=\{`\$\{c\.label\}\$\{c\.count === undefined \? '' : ` \$\{c\.count\}`\}`\}/)
  assert.match(bar, /<span class="hud cap" aria-hidden="true"/)
})
