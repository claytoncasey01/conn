// A theme's `case` token rules the case of chrome (app.css: nav, button, headings, labels, .hud). An inline
// `text-transform: uppercase` in a component beats that rule, so a lowercase theme (Matrix) could not lower the text. Labels are
// written in capitals in code, where the token lets them be (Default, Neon, CRT: `none`) or lowers them (Matrix: `lowercase`).
// This fails on a literal `text-transform: uppercase` in a component, except the allowlisted ones, each with the number of
// literals and why. A second literal in an allowed file fails too.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'

const root = new URL('../../', import.meta.url).pathname
const LITERAL = /text-transform\s*:\s*uppercase/gi

const WORDMARK = 'the wordmark in the page header: the product name is set in capitals in every theme, so the case token does not touch it'

// file -> [how many literals, why]
const ALLOW: Record<string, [number, string]> = { 'src/lib/Brand.svelte': [1, WORDMARK] }

export function offenders(files: Record<string, string>, allow: Record<string, [number, string]>): string[] {
  const out: string[] = []
  for (const [f, text] of Object.entries(files)) {
    const n = text.match(LITERAL)?.length ?? 0
    const [ok, why] = allow[f] ?? [0, '']
    if (n > ok) out.push(`${f}: ${n} literal${n === 1 ? '' : 's'}, ${ok} allowed${why ? ` (${why})` : ''}`)
  }
  return out
}

const read = (): Record<string, string> => Object.fromEntries([...new Glob('src/**/*.svelte').scanSync(root)].sort().map((f) => [f, readFileSync(root + f, 'utf8')]))

test('no inline text-transform: uppercase in a component outside the allowlist: the theme\'s case token rules labels', () => {
  assert.deepEqual(offenders(read(), ALLOW), [])
})

test('every allowlisted entry is still there, with a reason and its exact count', () => {
  const files = read()
  for (const [f, [n, why]] of Object.entries(ALLOW)) {
    assert.ok(why.length > 20, `${f} has a reason`)
    assert.equal(files[f]?.match(LITERAL)?.length ?? 0, n, `${f} has ${n} literal(s): drop or update the entry`)
  }
})

test('the guard is not vacuous: an extra literal, a new file, spacing and case variants are all named', () => {
  const a = 'src/lib/Brand.svelte'
  assert.deepEqual(offenders({ [a]: 'style="text-transform: uppercase"' }, ALLOW), [])
  assert.equal(offenders({ [a]: 'style="text-transform: uppercase"; style="text-transform:uppercase"' }, ALLOW).length, 1)
  assert.equal(offenders({ 'src/lib/FilterBar.svelte': 'style="text-transform: uppercase"' }, ALLOW).length, 1)
  assert.equal(offenders({ 'src/lib/X.svelte': 'style="TEXT-TRANSFORM :  UPPERCASE !important"' }, ALLOW).length, 1)
  assert.deepEqual(offenders({ 'src/lib/X.svelte': 'style="text-transform: var(--case)"' }, ALLOW), [])
})

test('FilterBar writes its group and chip labels as authored and lets the case token case them (cap)', () => {
  const bar = readFileSync(root + 'src/lib/FilterBar.svelte', 'utf8')
  assert.match(bar, /<span class="hud cap"[^>]*>\{g\.label\}<\/span>/)
  assert.match(bar, /class="tab cap"[\s\S]{0,800}>\{c\.label\}\{c\.count/)
  assert.doesNotMatch(bar, /text-transform/)
})
