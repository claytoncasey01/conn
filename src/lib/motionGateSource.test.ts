// The Effects switch reaches every theme's motion: a keyframe animation runs only on a root marked data-fx="on" (Effects on, or
// auto without the OS asking for less motion) and only while the OS does not ask for less motion, so an explicit "on" never
// overrides a reduced-motion user for the CSS either. What travels the agent tree's wires is hidden, not paused, outside that gate:
// a dot frozen halfway down a wire reads as a glitch. The sources are every theme.css, the look (lookCss.ts) and app.css.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'
import { LOOK_CSS } from './theme/lookCss.ts'

const root = new URL('../../', import.meta.url).pathname
const GATE_MEDIA = '(prefers-reduced-motion: no-preference)'
const GATE_ATTR = '[data-fx="on"]'
const PACKETS = ['.pk', '.blk', '.pulse', '.orbit-pk', '.gly']

type Rule = { media: string[]; selectors: string[]; body: string }

/** Every style rule with the @media conditions it sits in (comments dropped, @keyframes skipped) */
export function rulesOf(css: string): Rule[] {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const out: Rule[] = []
  const stack: (string | null)[] = []
  let i = 0
  let head = ''
  while (i < text.length) {
    const ch = text[i]
    if (ch === '{') {
      const h = head.trim()
      head = ''
      if (h.startsWith('@keyframes') || h.startsWith('@property') || h.startsWith('@font-face')) {
        // skip the whole block
        let depth = 1
        i++
        while (i < text.length && depth > 0) {
          if (text[i] === '{') depth++
          else if (text[i] === '}') depth--
          i++
        }
        continue
      }
      if (h.startsWith('@')) {
        stack.push(h)
        i++
        continue
      }
      const end = text.indexOf('}', i)
      out.push({ media: stack.filter((m): m is string => m !== null), selectors: h.split(/,(?![^(]*\))/).map((x) => x.trim()), body: text.slice(i + 1, end) })
      i = end + 1
      continue
    }
    if (ch === '}') {
      stack.pop()
      head = ''
      i++
      continue
    }
    head += ch
    i++
  }
  return out
}

const animates = (body: string) => /(?:^|;)\s*animation(?:-name)?\s*:\s*(?!none\s*(?:;|$))/.test(body)
const gated = (r: Rule) => r.media.some((m) => m.replace(/\s+/g, ' ').includes(GATE_MEDIA)) && r.selectors.every((s) => s.includes(GATE_ATTR))

/** Rules that run a keyframe animation outside the gate, named by source and selector */
export function ungatedMotion(name: string, css: string): string[] {
  return rulesOf(css).filter((r) => animates(r.body) && !gated(r)).map((r) => `${name}: ${r.selectors.join(', ')}`)
}

const sources = (): [string, string][] => [
  ['lookCss.ts', LOOK_CSS],
  ['src/app.css', readFileSync(root + 'src/app.css', 'utf8')],
  ...[...new Glob('themes/*/theme.css').scanSync(root)].sort().map((f): [string, string] => [f, readFileSync(root + f, 'utf8')]),
]

test('no keyframe animation in a theme.css, the look or app.css runs outside data-fx="on" and the no-preference media query', () => {
  const all = sources()
  assert.ok(all.length >= 4, 'the sources were found')
  const moving = all.flatMap(([n, css]) => rulesOf(css).filter((r) => animates(r.body)).map((r) => `${n}: ${r.selectors.join(', ')}`))
  assert.ok(moving.length >= 10, `the animations were found (${moving.length})`)
  assert.deepEqual(all.flatMap(([n, css]) => ungatedMotion(n, css)), [])
})

test('what travels a wire shows only inside the gate: outside it every packet, block, pulse and orbit comet is hidden', () => {
  const rules = rulesOf(LOOK_CSS)
  for (const sel of PACKETS) {
    const outside = rules.filter((r) => !gated(r) && r.selectors.includes(sel) && /(?:^|;)\s*display\s*:/.test(r.body))
    assert.ok(outside.length > 0, `${sel} has a display outside the gate`)
    for (const r of outside) assert.match(r.body, /(?:^|;)\s*display\s*:\s*none\s*(?:;|$)/, `${sel} is hidden outside the gate`)
    const inside = rules.filter((r) => gated(r) && r.selectors.some((s) => s.endsWith(sel)) && /display\s*:\s*var\(/.test(r.body))
    assert.ok(inside.length > 0, `${sel} is shown by its token inside the gate`)
  }
})

test('the checks see an ungated animation, one gated by only the marker or only the media query, and pass the gated form', () => {
  assert.deepEqual(ungatedMotion('x', '.a{animation:spin 1s infinite}'), ['x: .a'])
  assert.deepEqual(ungatedMotion('x', '[data-fx="on"] .a{animation:spin 1s infinite}'), ['x: [data-fx="on"] .a'])
  assert.deepEqual(ungatedMotion('x', '@media (prefers-reduced-motion: no-preference){.a{animation-name:spin}}'), ['x: .a'])
  assert.deepEqual(ungatedMotion('x', '@media (prefers-reduced-motion: no-preference){[data-fx="on"] .a, .b{animation:spin 1s}}'), ['x: [data-fx="on"] .a, .b'])
  assert.deepEqual(ungatedMotion('x', '@media (prefers-reduced-motion: no-preference){[data-fx="on"] .a{animation:spin 1s}}.b{animation:none}@keyframes spin{to{transform:rotate(1turn)}}'), [])
})
