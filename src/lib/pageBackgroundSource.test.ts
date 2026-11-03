// A page wrapper paints no backdrop of its own. The body already paints var(--bg) (app.css), and the `behind` effects layer
// (Matrix rain, CRT glow) sits between the body and the page, so a wrapper's background would hide it; and a backdrop written
// inline is one no theme can take away (every look wore Neon's grid and orbs that way). So each wrapper carries the `page`
// class and no inline `background`: a theme that wants a backdrop picks it with the `backdrop` and `orbs` tokens, which the
// look's `.page` rule (lookCss.ts) paints, and rootBackgroundSource.test.ts holds that rule to the same see-through contract.
//
// Text-based like the other source tests: it finds each page's `min-height: 100vh` wrapper line and reads its attributes.
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { test } from 'node:test'

const PAGES = new URL('../pages/', import.meta.url)

/** What is wrong with a page wrapper's opening tag, or null */
export const wrapperProblem = (line: string): string | null => {
  const cls = /\bclass="([^"]*)"/.exec(line)?.[1].split(/\s+/) ?? []
  if (!cls.includes('page')) return 'it has no `page` class'
  const style = /\bstyle="([^"]*)"/.exec(line)?.[1] ?? ''
  if (/(?:^|[;\s])background(?:-color|-image)?\s*:/.test(style)) return 'it paints a background inline'
  return null
}

test('every page wrapper is a `page` and paints no backdrop inline', () => {
  const files = readdirSync(PAGES).filter(f => f.endsWith('.svelte'))
  const checked: string[] = []
  const offenders: string[] = []
  for (const f of files) {
    const line = readFileSync(new URL(f, PAGES), 'utf8').split('\n').find(l => l.includes('min-height: 100vh'))
    if (line === undefined) continue
    checked.push(f)
    const why = wrapperProblem(line)
    if (why) offenders.push(`${f}: ${why}`)
  }
  assert.ok(checked.length >= 10, `expected the ten page wrappers, found ${checked.length}: ${checked.join(', ')}`)
  assert.deepEqual(offenders, [])
})

test('wrapperProblem names a wrapper without the class, or with an inline backdrop, and passes a bare one', () => {
  assert.equal(wrapperProblem('<div class="adv page" style="position: relative; min-height: 100vh; --orb-1: var(--ac)">'), null)
  assert.equal(wrapperProblem('<div style="position: relative; min-height: 100vh">'), 'it has no `page` class')
  assert.equal(wrapperProblem('<div class="pager" style="min-height: 100vh">'), 'it has no `page` class')
  assert.equal(wrapperProblem('<div class="page" style="min-height: 100vh; background: radial-gradient(a, b) 0 0/44px 44px">'), 'it paints a background inline')
  assert.equal(wrapperProblem('<div class="page" style="min-height: 100vh;background-image: linear-gradient(a, b)">'), 'it paints a background inline')
})
