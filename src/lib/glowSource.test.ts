// A glow whose colour is code (a helper's return, a prop) is invisible to the token codemod, so its --glow scaling is by hand.
// These checks pin, as text, that the hand-wrapped glows keep it: a theme with --glow: 0 must show no halo.
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { test } from 'node:test'

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8')
const switcher = read('./AgentSwitcher.svelte')
const agent = read('../pages/AgentPage.svelte')

test('the agent dots scale their glow by --glow: the switcher tab and the agent page status dot', () => {
  assert.ok(/box-shadow: 0 0 8px color-mix\(in oklch, \{w \? WAIT : agentDot\([^)]*\)\} calc\(100% \* var\(--glow\)\), transparent\)/.test(switcher), 'AgentSwitcher tab dot')
  assert.ok(/box-shadow: 0 0 10px color-mix\(in oklch, \{statusDot\(agent\)\} calc\(100% \* var\(--glow\)\), transparent\)/.test(agent), 'AgentPage status dot')
})

test('no page or row builds a zero-offset glow straight from a code colour', () => {
  const files = [...readdirSync(new URL('../pages/', import.meta.url)).filter((f) => f.endsWith('.svelte')).map((f) => `../pages/${f}`), './MyPrRow.svelte']
  // `0 0 10px {x}` or `'0 0 8px ' + x` is bare; a `{c ? '…' : '…'}` whose every branch is already --glow-scaled is not
  const scaled = (code: string) => {
    const branches = code.match(/'[^']*'/g)
    return !!branches && branches.every((b) => b.includes('var(--glow)'))
  }
  const bare = files.flatMap((f) =>
    read(f).split('\n').flatMap((l, i) => {
      const hits = [...l.matchAll(/\b0 0 \d+px (?:\{([^{}]*)\}|' \+)/g)].filter((m) => !(m[1] !== undefined && scaled(m[1])))
      return hits.length ? [`${f}:${i + 1}`] : []
    }),
  )
  assert.deepEqual(bare, [])
})
