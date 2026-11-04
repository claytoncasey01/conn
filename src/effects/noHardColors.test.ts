// An effect takes its colors from the theme's tokens, so a theme can change them; a literal here would stay the same
// under every theme. This fails on one in src/effects (the library files, and any CSS or Svelte beside them) and names
// the file, the line and the match. A literal that has to stay is listed in ALLOWED with the reason, and an entry that
// no longer matches anything fails too, so the list cannot grow stale. Tests and the font folder are not scanned.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'

const root = new URL('../../', import.meta.url).pathname
const skip = (f: string) => /\.test\.ts$/.test(f) || f.startsWith('src/effects/fonts/')
// #rgb, #rgba, #rrggbb, #rrggbbaa (and %23), then every color function; any case, so RGB( is caught
const LITERAL = /(?<![\w&])(?:#|%23)(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{3,4})(?![\w-])|\b(?:rgba?|hsla?|oklch|oklab|lab|lch|hwb)\(|\bcolor\(/gi

// 'file: match' to how many times it appears there and the reason it stays (one more of the same is a new literal)
const ALLOWED: Record<string, { times: number; why: string }> = {
  'src/effects/rain.ts: rgb(': {
    times: 1,
    why: 'the bright head of a rain column is the film look itself (near-white with a green cast), not a themed slot; the trails use the theme token',
  },
}

export function literals(files: string[], read: (f: string) => string): string[] {
  const hits: string[] = []
  for (const f of files) {
    if (skip(f)) continue
    read(f).split('\n').forEach((line, i) => {
      for (const m of line.matchAll(LITERAL)) hits.push(`${f}:${i + 1}: ${m[0]}`)
    })
  }
  return hits
}

const key = (hit: string) => hit.replace(/:\d+: /, ': ')

test('no color literal in src/effects but the listed ones, and every listed one is still there', () => {
  const files = [...new Glob('src/effects/**/*.{ts,svelte,css,html}').scanSync(root)].sort()
  assert.ok(files.length > 5, 'the scan found the effect files')
  const hits = literals(files, (f) => readFileSync(root + f, 'utf8'))
  const count = new Map<string, number>()
  for (const h of hits) count.set(key(h), (count.get(key(h)) ?? 0) + 1)
  const unlisted = hits.filter((h) => (count.get(key(h)) ?? 0) > (ALLOWED[key(h)]?.times ?? 0))
  assert.deepEqual(unlisted, [], `color literals in src/effects (take the color from a token, or list it in ALLOWED with a reason):\n${unlisted.join('\n')}`)
  const stale = Object.entries(ALLOWED).filter(([k, a]) => count.get(k) !== a.times).map(([k]) => k)
  assert.deepEqual(stale, [], `ALLOWED entries whose count no longer matches: ${stale.join(', ')}`)
  for (const [k, a] of Object.entries(ALLOWED)) assert.ok(a.why.length > 20, `${k} needs a reason`)
})

test('the scan sees every form, any case, and passes tokens, color-mix, ids and the skipped files', () => {
  const src: Record<string, string> = {
    'src/effects/a.ts':
      "c('#abc'); c('#a1b2c3d4'); c('rgb(1 2 3)'); c('rgba(1,2,3,.5)'); c('RGB(1 2 3)'); c('hsl(1 2% 3%)'); c('hsla(1,2%,3%,.5)'); c('oklch(.5 .1 20)'); c('OKLAB(.5 0 0)'); c('lab(50 1 1)'); c('lch(50 1 1)'); c('hwb(1 2% 3%)'); c('color(display-p3 1 0 0)'); c('%2305060d')",
    'src/effects/lib/ok.ts':
      "tokens.bg || 'black'; `color-mix(in srgb, ${bg} 25%, black)`; color-mix(in oklch, var(--ac) 10%, transparent); el.setAttribute('aria-hidden'); // PR #25, see #app; collab(1); colorize(2)",
    'src/effects/x.test.ts': "'#fff'",
    'src/effects/fonts/matrix-code/x.ts': "'#fff'",
  }
  const hits = literals(Object.keys(src), (f) => src[f])
  assert.deepEqual(hits.map((h) => h.split(': ')[1]), ['#abc', '#a1b2c3d4', 'rgb(', 'rgba(', 'RGB(', 'hsl(', 'hsla(', 'oklch(', 'OKLAB(', 'lab(', 'lch(', 'hwb(', 'color(', '%2305060d'])
})
