// Every look the built-in themes can paint: each theme folder, its modes and its variants, as the stylesheet resolves them
// (derived slots included), for the tests that hold a rule to all of them.
import { readdirSync, readFileSync } from 'node:fs'
import { themeTokens } from '../src/lib/theme/css.ts'
import { parseManifest } from '../src/lib/theme/validate.ts'
import type { Rgba } from '../src/lib/theme/color.ts'
import type { Mode, ThemeManifest, TokenSet } from '../src/lib/theme/types.ts'

const root = new URL('.', import.meta.url).pathname
const folders = readdirSync(root, { withFileTypes: true })
  .filter((d) => d.isDirectory() && !d.name.startsWith('_'))
  .map((d) => d.name)
  .sort()
const ctx = { source: 'builtin' as const, builtinIds: new Set(folders) }

function load(id: string): ThemeManifest {
  const r = parseManifest(readFileSync(`${root}${id}/theme.json`, 'utf8'), ctx)
  if (!r.ok) throw new Error(`${id}: ${r.errors.join('; ')}`)
  return r.manifest
}

export type Look = { name: string; id: string; mode: Mode; variant: string | null; tokens: Readonly<TokenSet> }

export function looks(): Look[] {
  const fallback = load('default')
  return folders.flatMap((id) => {
    const m = load(id)
    const picks = typeof m.accent === 'object' ? m.accent.variants.map((v) => v.name) : [null]
    return m.modes.flatMap((mode) =>
      picks.map((variant): Look => ({
        name: `${id} ${mode}${variant ? ` ${variant}` : ''}`,
        id,
        mode,
        variant,
        tokens: themeTokens(m, fallback, mode, variant ? { variant } : undefined),
      })),
    )
  })
}

// color-mix(in oklab, a pct%, b), as the components mix a token into another for text
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
export const mixOklab = (a: Rgba, pct: number, b: Rgba): Rgba => {
  const [x, y] = [toLab(a), toLab(b)]
  return fromLab([0, 1, 2].map((i) => (x[i] * pct) / 100 + (y[i] * (100 - pct)) / 100) as [number, number, number])
}
