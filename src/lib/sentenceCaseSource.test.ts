// Labels are written in sentence case in the markup and carry the class `cap`, which takes the theme's case token: Default
// shows "Agents running", Neon, CRT and Blueprint "AGENTS RUNNING", Matrix "agents running". This holds the markup to it.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'

const root = new URL('../../', import.meta.url).pathname
const files = [...new Glob('src/**/*.svelte').scanSync(root)].sort()
const ACRONYMS = new Set(['PR', 'PRs', 'CI', 'ID', 'OK', 'URL', 'API', 'CSS', 'DM', 'MCP', 'EOF', 'QA', 'AI', 'UTC'])

/** The literal words in capitals inside a `cap` element's own text (expressions and nested tags left out), acronyms aside */
export function shouting(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/<([a-z][a-z0-9]*)\b[^>]*\bclass="[^"]*\bcap\b[^"]*"[^>]*>([^<]*)<\//g)) {
    const own = m[2].replace(/\{(?:[^{}]|\{[^{}]*\})*\}/g, (e) => e.replace(/[^'`]+(?=['`])|['`][^'`]*['`]/g, (q) => (q.startsWith("'") || q.startsWith('`') ? q : ' ')))
    for (const w of own.match(/\b[A-Z][A-Z]+\b/g) ?? []) if (!ACRONYMS.has(w)) out.push(w)
  }
  return out
}

test('a label with the cap class is written in sentence case, so the case token alone decides how it reads', () => {
  const caps = files.reduce((n, f) => n + (readFileSync(root + f, 'utf8').match(/\bclass="[^"]*\bcap\b/g)?.length ?? 0), 0)
  assert.ok(caps >= 150, `the labels were found (${caps})`)
  assert.deepEqual(files.flatMap((f) => shouting(readFileSync(root + f, 'utf8')).map((w) => `${f}: ${w}`)), [])
})

test('shouting finds capitals in a cap label and its quoted alternatives, and passes acronyms and sentence case', () => {
  assert.deepEqual(shouting('<div class="cap" style="x">AGENTS running</div>'), ['AGENTS'])
  assert.deepEqual(shouting(`<span class="hud cap">{on ? 'LIVE' : 'Idle'}</span>`), ['LIVE'])
  assert.deepEqual(shouting('<a class="tabc cap" href={h}>My PRs</a><div>SESSIONS</div>'), [])
})

/** Text in the markup with a word in capitals outside any cap label: the labels not yet written in sentence case */
export function unconverted(f: string, text: string): string[] {
  const body = text.replace(/<script[\s\S]*?<\/script>|<style[\s\S]*?<\/style>/g, '').replace(/<title>[^<]*<\/title>/g, '')
  const out: string[] = []
  for (const m of body.matchAll(/<([a-z][a-z0-9]*)\b([^>]*)>((?:[^<>{}]|\{(?:[^{}]|\{[^{}]*\})*\})*)</g)) {
    if (/\bclass="[^"]*\bcap\b/.test(m[2]) || /\bdata-content\b/.test(m[2])) continue
    const lit = m[3].replace(/\{(?:[^{}]|\{[^{}]*\})*\}/g, '')
    if ((lit.match(/\b[A-Z][A-Z]+\b/g) ?? []).some((w) => !ACRONYMS.has(w) && w !== 'EST')) out.push(`${f}: ${m[3].trim().slice(0, 60)}`)
  }
  return out
}

// A ratchet: the labels still in capitals (they mix a label with data or units, or come uppercased from code) may only go down.
// Converting one: write it in sentence case, give its literal its own cap span, and lower the number.
const LEFT = 34
test(`at most ${LEFT} labels are still written in capitals outside a cap label, and the number only goes down`, () => {
  const left = files.flatMap((f) => unconverted(f, readFileSync(root + f, 'utf8')))
  assert.ok(left.length <= LEFT, `${left.length} labels in capitals (at most ${LEFT}):\n${left.join('\n')}`)
  assert.deepEqual(unconverted('x', '<div style="a">AGENTS RUNNING</div><div class="cap">Agents</div><span data-content>NAME</span>'), ['x: AGENTS RUNNING'])
})
