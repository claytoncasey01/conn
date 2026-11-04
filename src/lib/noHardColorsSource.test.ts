// Every color in the UI comes from a token, so a theme can change it. A literal color left in a page, a helper or the
// base stylesheet would stay the same under every theme; this fails on one and names the file, the line and the match.
// Exempt: src/effects/ (it has its own counted allowlist, src/effects/noHardColors.test.ts), src/lib/theme/ (the generator's own color math) and tests.
// themes/ is not scanned: a theme's files are where literals belong.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'

const root = new URL('../../', import.meta.url).pathname
const exempt = (f: string) => f.startsWith('src/effects/') || f.startsWith('src/lib/theme/') || /\.test\.ts$/.test(f)
// #rgb, #rgba, #rrggbb, #rrggbbaa (and %23, the same in a data URI), then every color function: rgb(, rgba(, hsl(, hsla(,
// oklch(, oklab(, lab(, lch(, hwb( and color(, in any case (RGB( too). `color-mix(` is not one of them: it is a literal only
// through the colors inside it. No call is exempt by what comes before it: a JS helper named color would be caught too.
const LITERAL = /(?<![\w&])(?:#|%23)(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{3,4})(?![\w-])|\b(?:rgba?|hsla?|oklch|oklab|lab|lch|hwb|color)\(/gi

export function hardColors(files: string[], read: (f: string) => string): string[] {
  const hits: string[] = []
  for (const f of files) {
    if (exempt(f)) continue
    read(f).split('\n').forEach((line, i) => {
      for (const m of line.matchAll(LITERAL)) hits.push(`${f}:${i + 1}: ${m[0]}`)
    })
  }
  return hits
}

test('no hard-coded color in src or index.html outside the effects, the theme math and tests', () => {
  const files = [...new Glob('src/**/*.{svelte,ts,css}').scanSync(root), 'index.html'].sort()
  const hits = hardColors(files, (f) => readFileSync(root + f, 'utf8'))
  assert.deepEqual(hits, [], `hard-coded colors:\n${hits.join('\n')}`)
})

test('the scan sees every form, the data-URI one included, and passes ids, issue numbers and the exempt folders', () => {
  const src: Record<string, string> = {
    'src/a.svelte': 'color: #abc; b: #a1b2c3d4; c: rgb(1 2 3); d: rgba(1,2,3,.5); e: hsl(1 2% 3%)',
    'src/more.svelte': 'a: hsla(1,2%,3%,.5); b: oklch(.5 .1 20); c: OKLAB(.5 0 0); d: lab(50 1 1); e: lch(50 1 1); f: hwb(1 2% 3%); g: color(display-p3 1 0 0); h: RGB(1 2 3)',
    'src/js.svelte': 'x = {rgb(1,2,3)} {hsl(1,2%,3%)} {oklch(.5 .1 20)} d3.rgb(1,2,3) $rgb(1) x-rgb(1) {color(on, dim)} .color(a)',
    'index.html': "fill='%2305060d'",
    'src/b.ts': "getElementById('#conn-theme'); // PR #25, see #app; color-mix(in oklch, var(--ac) 10%, transparent); collab(1); getcolor(2); labs(3); colorize(4)",
    'src/effects/rain.ts': "'#00ff41'",
    'src/lib/theme/color.ts': 'rgb(',
    'src/lib/x.test.ts': '#fff',
  }
  const hits = hardColors(Object.keys(src), (f) => src[f])
  assert.deepEqual(hits.map((h) => h.split(': ')[1]), [
    '#abc', '#a1b2c3d4', 'rgb(', 'rgba(', 'hsl(',
    'hsla(', 'oklch(', 'OKLAB(', 'lab(', 'lch(', 'hwb(', 'color(', 'RGB(',
    'rgb(', 'hsl(', 'oklch(', 'rgb(', 'rgb(', 'rgb(', 'color(', 'color(',
    '%2305060d',
  ])
})
