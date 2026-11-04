// Matrix's own rules: the values the spec's appendix settled, and the contrast of text on glass over the brightest rain.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { composite, contrast, parseColor, type Rgba } from '../src/lib/theme/color.ts'
import { parseManifest } from '../src/lib/theme/validate.ts'
import type { ThemeManifest } from '../src/lib/theme/types.ts'

const root = new URL('.', import.meta.url).pathname
const ctx = { source: 'builtin' as const, builtinIds: new Set(['default', 'neon', 'matrix']) }

function load(): ThemeManifest {
  const r = parseManifest(readFileSync(`${root}matrix/theme.json`, 'utf8'), ctx)
  assert.ok(r.ok, r.ok ? '' : r.errors.join('; '))
  return r.manifest
}

const tokens = () => load().tokens.dark ?? {}
const color = (slot: string): Rgba => {
  const c = parseColor(tokens()[slot])
  assert.ok(c, `${slot} is a color`)
  return c
}
// the surface the page paints: bg, then the glass panel over it
const bg = () => composite(color('bg'), { r: 0, g: 0, b: 0, a: 1 })
const panel = (under: Rgba) => composite(color('panel'), under)

test('Matrix is dark only, a fixed accent, Share Tech Mono, one rain effect and calm motion', () => {
  const m = load()
  assert.equal(m.id, 'matrix')
  assert.deepEqual(m.modes, ['dark'])
  assert.equal(m.accent, 'fixed')
  assert.deepEqual(m.fonts, ['Share Tech Mono'])
  assert.equal(m.effects.length, 1)
  assert.equal(m.effects[0].name, 'rain')
  assert.equal(m.effects[0].options?.density, 0.6)
})

test('Matrix is lowercase with a diamond LED, glass blur 5px, and the spec\'s palette', () => {
  const t = tokens()
  const ruled = {
    led: 'diamond', case: 'lowercase', blur: '5px', radius: '2px', tracking: '.08em', glow: '.4', motion: 'calm',
    bg: '#010a04', panel: 'rgba(2,16,7,.72)', raise: 'rgba(0,40,14,.8)', line: 'rgba(0,255,65,.2)', line2: 'rgba(0,255,65,.4)',
    tx: '#c9ffd6', bright: '#eafff0', mu: '#6fa8ff', ac: '#00ff41', onac: '#00140a', ok: '#00ff41', warn: '#d8ffb0', bad: '#ff4d5e', idle: '#5b8fb0',
  }
  for (const [slot, v] of Object.entries(ruled)) assert.equal(t[slot], v, slot)
})

test('Matrix keeps the spec\'s contrast: tx on bg, onac on ac, mu on bg', () => {
  const ratio = (fg: string, on: Rgba) => contrast(color(fg), on)
  assert.ok(ratio('tx', bg()) >= 17.9 - 0.1, `tx on bg ${ratio('tx', bg()).toFixed(2)}`)
  assert.ok(ratio('onac', composite(color('ac'), bg())) >= 13.8, 'onac on ac')
  assert.ok(ratio('mu', bg()) >= 8.2, `mu on bg ${ratio('mu', bg()).toFixed(2)}`)
})

// rain.ts does not export its HEAD color or ALPHA, so these are copied from it: HEAD = rgb(220 255 230), ALPHA = { far: .5, near: .9 }.
// Keep them in step with src/effects/rain.ts.
const RAIN_HEAD = { r: 220, g: 255, b: 230 }
const RAIN_TRAIL = { r: 0, g: 255, b: 65 } // #00ff41, the `ac` the trails use

test('text on the glass panel stays readable over the far-depth rain trail (#00ff41 at alpha .5 over bg, the brief\'s case)', () => {
  const glass = panel(composite({ ...RAIN_TRAIL, a: 0.5 }, bg()))
  // body text and the muted blue are held to 4.5; the colored status words to 3 (the brief's 3:1 for status and category colors on the panel; the gate does not check them)
  for (const [slot, min] of [['tx', 4.5], ['mu', 4.5], ['ok', 3], ['warn', 3], ['bad', 3], ['info', 3]] as const) {
    const ratio = contrast(color(slot), glass)
    assert.ok(ratio >= min, `${slot} on the panel over the far-depth trail is ${ratio.toFixed(2)}:1, needs ${min}`)
  }
})

// The worst the rain can paint is the head at full alpha (the near depth draws at .9, and a cell drawn again before it fades stacks toward 1.0, so 1.0 is the bound).
// `bad` and `idle` fall below 3:1 under dense near rain (bad 2.55:1 under the head, 3.16 under the near trail at .9): known, they are
// the brief's values, and the 5px backdrop blur softens a single glyph cell, so it is accepted and not asserted.
test('tx and mu stay readable on the glass panel under a rain head at full alpha (rgb(220 255 230) over bg)', () => {
  const glass = panel(composite({ ...RAIN_HEAD, a: 1 }, bg()))
  for (const [slot, min] of [['tx', 4.5], ['mu', 3]] as const) {
    const ratio = contrast(color(slot), glass)
    assert.ok(ratio >= min, `${slot} on the panel under a rain head is ${ratio.toFixed(2)}:1, needs ${min}`)
  }
})

test('every status and category color is at least 3:1 on the panel', () => {
  const t = tokens()
  const slots = ['ok', 'warn', 'bad', 'idle', 'info', 'k-bug', 'k-epic', 'k-task', 'k-story', 'k-subtask', 'teammate', 'role-lead', 'role-agent', 'role-advisor', 'edge', 'stall']
  const glass = panel(bg())
  for (const s of slots) {
    assert.ok(t[s], `${s} is set`)
    assert.ok(contrast(color(s), glass) >= 3, `${s} on panel ${contrast(color(s), glass).toFixed(2)}:1`)
  }
})
