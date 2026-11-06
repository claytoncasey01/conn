// The terminal's colours come from the theme's tokens. Every built-in look, and token sets a user theme could hold, must
// give a terminal that can be read: the text, each of the 16 colours a program writes in, the cursor and a selection.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { looks } from '../../themes/looks.ts'
import { contrast, parseColor, type Rgba } from './theme/color.ts'
import { ANSI_INK_DARK, ANSI_INK_LIGHT, CURSOR_CONTRAST_MIN, TEXT_CONTRAST_MIN, terminalFont, terminalTheme, type XtermTheme } from './terminalTheme.ts'

const HEX = /^#[0-9a-f]{6}$/

function rgb(value: string | undefined, what: string): Rgba {
  assert.ok(value !== undefined, `${what} is set`)
  assert.match(value, HEX, `${what} is an opaque #rrggbb`)
  const parsed = parseColor(value)
  assert.ok(parsed, `${what} parses`)
  return parsed
}

const ratio = (theme: XtermTheme, fg: keyof XtermTheme, bg: keyof XtermTheme) =>
  contrast(rgb(theme[fg] as string, String(fg)), rgb(theme[bg] as string, String(bg)))

/** Whether the look's background is darker than its text. */
const BLACK: Rgba = { r: 0, g: 0, b: 0, a: 1 }
const isDark = (theme: XtermTheme) => contrast(rgb(theme.background, 'background'), BLACK) < contrast(rgb(theme.foreground, 'foreground'), BLACK)

const all = looks()

test('there are looks to check', () => {
  assert.ok(all.length >= 6, `looks: ${all.map(l => l.name).join(', ')}`)
})

for (const look of all) {
  test(`${look.name}: the text is at least 4.5:1 on the background`, () => {
    const theme = terminalTheme(look.tokens)
    assert.ok(ratio(theme, 'foreground', 'background') >= TEXT_CONTRAST_MIN, ratio(theme, 'foreground', 'background').toFixed(2))
  })

  test(`${look.name}: every colour a program writes text in is at least 4.5:1 on the background`, () => {
    const theme = terminalTheme(look.tokens)
    const ink = isDark(theme) ? ANSI_INK_DARK : ANSI_INK_LIGHT
    for (const name of ink) {
      const r = ratio(theme, name, 'background')
      assert.ok(r >= TEXT_CONTRAST_MIN, `${name} ${theme[name]} on ${theme.background}: ${r.toFixed(2)}`)
    }
  })

  test(`${look.name}: the cursor shows against the background and the character under it against the cursor`, () => {
    const theme = terminalTheme(look.tokens)
    assert.ok(ratio(theme, 'cursor', 'background') >= CURSOR_CONTRAST_MIN, ratio(theme, 'cursor', 'background').toFixed(2))
    assert.ok(ratio(theme, 'cursorAccent', 'cursor') >= CURSOR_CONTRAST_MIN, ratio(theme, 'cursorAccent', 'cursor').toFixed(2))
  })

  test(`${look.name}: selected text can be read and the selection can be seen`, () => {
    const theme = terminalTheme(look.tokens)
    assert.ok(ratio(theme, 'selectionForeground', 'selectionBackground') >= TEXT_CONTRAST_MIN, ratio(theme, 'selectionForeground', 'selectionBackground').toFixed(2))
    assert.ok(ratio(theme, 'selectionBackground', 'background') >= 1.2, `selection against the background ${ratio(theme, 'selectionBackground', 'background').toFixed(2)}`)
    assert.ok(ratio(theme, 'selectionForeground', 'selectionInactiveBackground') >= TEXT_CONTRAST_MIN)
  })

  test(`${look.name}: all 16 colours, and every other colour, are opaque #rrggbb`, () => {
    const theme = terminalTheme(look.tokens)
    const names = Object.keys(theme) as (keyof XtermTheme)[]
    assert.equal(names.filter(n => /^(bright)?(black|red|green|yellow|blue|magenta|cyan|white)$/i.test(n)).length, 16)
    for (const name of names) rgb(theme[name] as string, name)
  })
}

test('the colours follow the tokens: text, background, the status colours and the cursor', () => {
  const look = all.find(l => l.name === 'neon dark')
  assert.ok(look)
  const theme = terminalTheme(look.tokens)
  assert.equal(theme.background, '#05060d')
  assert.equal(theme.foreground, '#d6dcf0')
  assert.equal(theme.red, '#ff6b6b')
  assert.equal(theme.green, '#6fd6a0')
  assert.equal(theme.yellow, '#ffd166')
  assert.equal(theme.blue, '#7f9cff')
  assert.equal(theme.cursor, '#b9ccff')
})

const hostile: [string, Record<string, string>][] = [
  ['text the colour of the background', { bg: '#202020', tx: '#202020', bright: '#202020', mu: '#202020', bad: '#202020', ok: '#202020', warn: '#202020', info: '#202020', 'role-advisor': '#202020', 'k-subtask': '#202020', focus: '#202020', ac: '#202020' }],
  ['mid grey everywhere', { bg: '#777777', tx: '#787878', bright: '#808080', mu: '#767676', bad: '#7a7070', ok: '#707a70', warn: '#7a7a70', info: '#70707a', 'role-advisor': '#7a707a', 'k-subtask': '#707a7a', focus: '#777777', ac: '#777777' }],
  ['a bright accent over dim text', { bg: '#000000', tx: '#777777', ac: '#ffffff', mu: '#777777' }],
  ['a translucent background', { bg: 'rgba(255,255,255,.1)', tx: '#eeeeee' }],
  ['no tokens at all', {}],
  ['tokens that are not colours', { bg: 'url(x)', tx: 'var(--bg)', focus: 'color-mix(in oklch, red, blue)' }],
]

for (const [name, tokens] of hostile) {
  test(`a theme with ${name} still gives a readable terminal`, () => {
    const theme = terminalTheme(tokens)
    assert.ok(ratio(theme, 'foreground', 'background') >= TEXT_CONTRAST_MIN)
    const ink = isDark(theme) ? ANSI_INK_DARK : ANSI_INK_LIGHT
    for (const n of ink) assert.ok(ratio(theme, n, 'background') >= TEXT_CONTRAST_MIN, `${n}: ${ratio(theme, n, 'background').toFixed(2)}`)
    assert.ok(ratio(theme, 'cursor', 'background') >= CURSOR_CONTRAST_MIN)
    assert.ok(ratio(theme, 'selectionForeground', 'selectionBackground') >= TEXT_CONTRAST_MIN)
  })
}

test('terminalTheme is pure: the same tokens give the same theme, and the tokens are not changed', () => {
  const look = all[0]
  const before = JSON.stringify(look.tokens)
  assert.deepEqual(terminalTheme(look.tokens), terminalTheme(look.tokens))
  assert.equal(JSON.stringify(look.tokens), before)
})

test('the font is the theme\'s monospace family, ending in a family a canvas knows', () => {
  for (const look of all) {
    const font = terminalFont(look.tokens)
    assert.ok(font.startsWith(look.tokens['font-mono']), `${look.name}: ${font}`)
    assert.match(font, /, monospace$/)
  }
  assert.equal(terminalFont({}), 'monospace')
})
