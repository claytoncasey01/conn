import assert from 'node:assert/strict'
import { test } from 'node:test'
import { composite, contrast, parseColor } from './color.ts'

const c = (v: string) => {
  const p = parseColor(v)
  assert.ok(p, `${v} should parse`)
  return p
}

test('parseColor reads hex in three, six and eight digits', () => {
  assert.deepEqual(c('#abc'), { r: 170, g: 187, b: 204, a: 1 })
  assert.deepEqual(c('#aabbcc'), { r: 170, g: 187, b: 204, a: 1 })
  const withAlpha = c('#aabbccdd')
  assert.equal(withAlpha.r, 170)
  assert.ok(Math.abs(withAlpha.a - 0xdd / 255) < 1e-9)
})

test('parseColor reads rgb in space and comma forms, with and without alpha', () => {
  assert.deepEqual(c('rgb(1 2 3)'), { r: 1, g: 2, b: 3, a: 1 })
  assert.deepEqual(c('rgba(2,16,7,.72)'), { r: 2, g: 16, b: 7, a: 0.72 })
  assert.deepEqual(c('rgb(139 255 173 / .36)'), { r: 139, g: 255, b: 173, a: 0.36 })
})

test('parseColor reads hsl', () => {
  const g = c('hsl(120 50% 50%)')
  assert.deepEqual([g.r, g.g, g.b], [64, 191, 64])
})

test('parseColor reads oklch, with alpha after a slash', () => {
  const lit = c('oklch(.97 .006 255)')
  assert.ok(lit.r > 235 && lit.g > 235 && lit.b > 235)
  const a = c('oklch(.3 .05 260/.07)')
  assert.equal(a.a, 0.07)
})

test('parseColor refuses names, variables, urls and junk', () => {
  for (const v of ['red', 'var(--x)', 'url(x)', '', '#ab', '#abcde', 'rgb(1 2)', 'oklch(a b c)', 'rgb(1 2 3) x', 'oklch(.5 .1 20; x)']) {
    assert.equal(parseColor(v), null, v)
  }
})

test('parseColor refuses what CSS refuses, so the gate never measures a color the browser would drop', () => {
  const bad = [
    'rgb(1 2 3 4)', // fourth part with no slash
    'rgb(1/2/3)',
    'rgb(1 2 / 3 4)',
    'rgb(1 2 3 / .5 / .5)',
    'oklch(.5, .1, 20)', // no comma form for oklch
    'oklch(.5, .1, 20, .5)',
    'hsl(120 50% 50% 1)',
    'rgb(1, 2, 3, 4, 5)',
    'rgb(1, 2 3)', // comma and space mixed
    'rgb(1. 2 3)', // a decimal point needs digits
    'oklch(.5 .1. 20)',
    'oklch(5. .1 20)',
    'rgb(1\u00a02\u00a03)', // no-break space is not CSS whitespace
    'rgb(1 2 3\u00a0/\u00a0.5)',
    '\u00a0#fff',
    '#fff\u00a0',
  ]
  for (const v of bad) assert.equal(parseColor(v), null, JSON.stringify(v))
})

test('parseColor still takes CSS whitespace and the valid forms', () => {
  assert.ok(parseColor('rgb(\t1\n2  3 )'))
  assert.ok(parseColor('rgb( 1 , 2 , 3 )'))
  assert.ok(parseColor('rgb(1 2 3/.5)'))
  assert.ok(parseColor('oklch(.3 .05 260/.07)'))
  assert.ok(parseColor('hsla(120, 50%, 50%, .5)'))
  assert.ok(parseColor(' #fff '))
})

test('contrast: white on black is 21, black on black is 1', () => {
  assert.ok(Math.abs(contrast(c('#fff'), c('#000')) - 21) < 0.01)
  assert.ok(Math.abs(contrast(c('#000'), c('#000')) - 1) < 0.01)
})

test('contrast matches the numbers the spec settled', () => {
  const near = (fg: string, bg: string, want: number) => {
    const got = contrast(c(fg), c(bg))
    assert.ok(Math.abs(got - want) < 0.1, `${fg} on ${bg}: ${got.toFixed(2)} not near ${want}`)
  }
  near('oklch(.22 .02 260)', 'oklch(.97 .006 255)', 15.9)
  near('#c9ffd6', '#010a04', 17.9)
  near('#d6dcf0', '#05060d', 14.8)
})

test('contrast composites a translucent foreground over the background first', () => {
  const fg = c('rgba(2,16,7,.72)')
  const bg = c('#010a04')
  const flat = composite(fg, bg)
  assert.equal(flat.a, 1)
  assert.ok(Math.abs(contrast(fg, bg) - contrast(flat, bg)) < 1e-9)
  // measured as opaque it would be a different (higher) number
  assert.notEqual(contrast({ ...fg, a: 1 }, bg), contrast(fg, bg))
})

test('composite blends by alpha and keeps an opaque foreground', () => {
  assert.deepEqual(composite({ r: 255, g: 0, b: 0, a: 1 }, { r: 0, g: 0, b: 255, a: 1 }), { r: 255, g: 0, b: 0, a: 1 })
  assert.deepEqual(composite({ r: 255, g: 255, b: 255, a: 0.5 }, { r: 0, g: 0, b: 0, a: 1 }), { r: 128, g: 128, b: 128, a: 1 })
})
