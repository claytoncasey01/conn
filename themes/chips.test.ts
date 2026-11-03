// The filter chips that would show nothing (count 0) are dimmed, never disabled, and an inactive control may be quiet, but not
// unreadable: their text keeps 3:1 in every look, also over the rain behind the page (its near-white glyph head under the backing).
// The backing exists only while an effect is mounted behind the page, so a theme without one is drawn exactly as it was.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { composite, contrast, parseColor, type Rgba } from '../src/lib/theme/color.ts'
import { CHIP_BACKING, CHIP_DIM } from '../src/lib/chipStyle.ts'
import { looks, type Look } from './looks.ts'

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

// color-mix(in oklab, a pct%, b): the one mix FilterBar uses to build a selected chip's text
const toLin = (c: number) => ((c / 255) <= 0.04045 ? c / 255 / 12.92 : (((c / 255) + 0.055) / 1.055) ** 2.4)
const fromLin = (x: number) => Math.round(255 * Math.min(1, Math.max(0, x <= 0.0031308 ? 12.92 * x : 1.055 * x ** (1 / 2.4) - 0.055)))
const toLab = ({ r, g, b }: Rgba): [number, number, number] => {
  const [R, G, B] = [toLin(r), toLin(g), toLin(b)]
  const l = Math.cbrt(0.4122214708 * R + 0.5363325363 * G + 0.0514459929 * B)
  const m = Math.cbrt(0.2119034982 * R + 0.6806995451 * G + 0.1073969566 * B)
  const s = Math.cbrt(0.0883024619 * R + 0.2817188376 * G + 0.6299787005 * B)
  return [0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s, 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s, 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s]
}
const fromLab = ([L, a, b]: [number, number, number]): Rgba => {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  return {
    r: fromLin(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    g: fromLin(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    b: fromLin(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
    a: 1,
  }
}
const mixOklab = (a: Rgba, pct: number, b: Rgba): Rgba => {
  const [x, y] = [toLab(a), toLab(b)]
  return fromLab([0, 1, 2].map((i) => (x[i] * pct) / 100 + (y[i] * (100 - pct)) / 100) as [number, number, number])
}

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
