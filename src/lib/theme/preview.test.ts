import assert from 'node:assert/strict'
import { test } from 'node:test'
import defaultTheme from '../../../themes/default/theme.json'
import neon from '../../../themes/neon/theme.json'
import { previewAttrs, previewDoc, safeCss } from './preview.ts'
import { slotProblem } from './validate.ts'
import { LOOK } from './css.ts'
import { SLOTS } from './tokens.ts'
import type { ThemeManifest } from './types.ts'

const DEFAULT = defaultTheme as unknown as ThemeManifest
const NEON = neon as unknown as ThemeManifest
// The themed attributes sit on <html>; the stylesheet repeats the same strings in its selectors, so only the tag proves them.
const htmlTag = (html: string) => /<html[^>]*>/.exec(html)?.[0] ?? ''
const make = (over: Partial<ThemeManifest> = {}): ThemeManifest => ({ ...NEON, id: 'sample', name: 'Sample', ...over })

test('the attrs sandbox the frame with no permissions and carry the CSP', () => {
  assert.equal(previewAttrs.sandbox, '')
  assert.equal(previewAttrs.csp, "default-src 'none'; style-src 'unsafe-inline'")
})

test('the document holds the CSP meta first and nothing that loads or runs', () => {
  for (const m of [DEFAULT, NEON]) {
    for (const mode of m.modes) {
      const html = previewDoc(m, mode, DEFAULT)
      assert.ok(html.includes(`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`))
      assert.ok(html.indexOf('Content-Security-Policy') < html.indexOf('<style'))
      assert.ok(!/<script/i.test(html))
      assert.ok(!/https?:/i.test(html))
      assert.ok(!/@import/i.test(html))
      assert.ok(!/url\(/i.test(html))
      assert.ok(!/<(?:link|img|iframe|object|embed|base)\b/i.test(html))
    }
  }
})

test('the sample UI is painted from the theme own tokens in the asked mode', () => {
  const html = previewDoc(DEFAULT, 'dark', DEFAULT)
  assert.match(htmlTag(html), /\bdata-theme="default"/)
  assert.match(htmlTag(html), /\bdata-mode="dark"/)
  assert.ok(html.includes('--bg:' + (DEFAULT.tokens.dark as Record<string, string>).bg))
  assert.ok(html.includes('class="tab') && html.includes('class="card') && html.includes('class="chip') && html.includes('class="btn primary') && html.includes('class="btn ghost'))
  assert.equal(html.match(/class="dot /g)?.length, 4)
})

test('a mode the theme lacks shows the mode it has', () => {
  const darkOnly = make({ modes: ['dark'], tokens: { dark: { bg: '#010a04' } } })
  const html = previewDoc(darkOnly, 'light', DEFAULT)
  assert.match(htmlTag(html), /\bdata-mode="dark"/)
  assert.ok(html.includes('--bg:#010a04;'))
  const lightOnly = make({ modes: ['light'], tokens: { light: { bg: '#fafafa' } } })
  assert.match(htmlTag(previewDoc(lightOnly, 'dark', DEFAULT)), /\bdata-mode="light"/)
})

test('a free accent shows the theme own ac and a variant theme shows its first variant', () => {
  const free = previewDoc(DEFAULT, 'light', DEFAULT)
  assert.ok(/--ac:oklch\(/.test(free))
  const variants = make({ accent: { variants: [{ name: 'One', tokens: { ac: '#112233' } }, { name: 'Two', tokens: { ac: '#445566' } }] } })
  const html = previewDoc(variants, 'dark', DEFAULT)
  assert.ok(html.includes('--ac:#112233;') && !html.includes('#445566'))
})

// the card shows a theme's tree wires as the Overview draws them: the same parts and head shapes, so the wire tokens show on it
test('the stage draws a wire in every part, a head in every shape, the crawl and a beating node, as the Overview does', () => {
  const doc = previewDoc(NEON, 'dark', DEFAULT)
  for (const part of ['wsh', 'wb', 'wcut', 'wsn']) assert.match(doc, new RegExp(`<path class="${part}" d="M`), part)
  for (const shape of ['arrow', 'plug', 'dot', 'bar']) assert.match(doc, new RegExp(`<path class="h-${shape}" d="M`), shape)
  assert.match(doc, /<path class="flow" d="M/)
  assert.match(doc, /<circle class="node"/)
  assert.match(doc, /<svg class="tree"[^>]*aria-hidden="true"/)
})

test('the led token picks the dot shape', () => {
  const html = previewDoc(make({ tokens: { dark: { led: 'diamond' } } }), 'dark', DEFAULT)
  assert.match(htmlTag(html), /\bdata-led="diamond"/)
  assert.match(htmlTag(previewDoc(make({ tokens: { dark: { led: 'square' } } }), 'dark', DEFAULT)), /\bdata-led="square"/)
  assert.match(htmlTag(previewDoc(make({ tokens: { dark: {} } }), 'dark', { ...DEFAULT, tokens: { dark: {} } })), /\bdata-led="circle"/)
})

test('a mono text falls back to the generic monospace, and the frame clips instead of scrolling', () => {
  const html = previewDoc(DEFAULT, 'dark', DEFAULT)
  assert.ok(html.includes('font-family:var(--font-mono,ui-monospace),monospace;'))
  assert.ok(html.includes('html,body{margin:0;overflow:hidden}'))
})

test('a free-accent theme previews its OWN ac, with ac-soft and ac-line derived from var(--ac), as an install without a pick paints it', () => {
  const own = make({ accent: 'free', tokens: { dark: { ac: '#112233' } } })
  const html = previewDoc(own, 'dark', DEFAULT)
  assert.ok(html.includes('--ac:#112233;'))
  assert.ok(!/--ac:oklch\(/.test(html))
  assert.ok(html.includes('--ac-soft:color-mix(in oklch, var(--ac) 14%, transparent);'))
  assert.ok(html.includes('--ac-line:color-mix(in oklch, var(--ac) 40%, transparent);'))
})

test('a hostile name, author or token value never reaches the document as markup', () => {
  const evil = '</style><script>alert(1)</script>"\''
  const html = previewDoc(make({ name: evil, author: evil, version: evil, description: evil }), 'dark', DEFAULT)
  assert.ok(!/<script/i.test(html))
  assert.ok(html.includes('&quot;&#39;'))
  assert.equal(html.match(/<\/style/gi)?.length, 1)
  assert.ok(html.includes('&lt;/style&gt;&lt;script&gt;'))
  // a token value that failed validation is a placeholder, not an injection
  const bad = previewDoc(make({ tokens: { dark: { bg: '#000;}</style><script>alert(1)</script>' } } }), 'dark', DEFAULT)
  assert.ok(!/<script/i.test(bad))
  assert.equal(bad.match(/<\/style/gi)?.length, 1)
  assert.ok(!bad.includes('alert(1)'))
})

test('a manifest that fails validation yields a placeholder with no styles from it', () => {
  const html = previewDoc(make({ id: 'Bad Id' }), 'dark', DEFAULT)
  assert.ok(html.includes('Content-Security-Policy'))
  assert.ok(html.includes('cannot be previewed'))
  assert.ok(!html.includes('class="card'))
  assert.ok(!/<script/i.test(html))
  const junk = previewDoc(null as unknown as ThemeManifest, 'dark', DEFAULT)
  assert.ok(junk.includes('cannot be previewed'))
})

test('safeCss drops a style close tag, angle brackets and a comment opener whatever their case', () => {
  assert.equal(safeCss('a{b:c}</STYLE ><script>x</script>/* y'), 'a{b:c} scriptx/script y')
  // one pass would leave the second opener of //**/ behind
  assert.ok(!/\/\*/.test(safeCss('//**/')), safeCss('//**/'))
  assert.ok(!/\/\*/.test(safeCss('/ /**/')))
  assert.ok(!/[<>]|\/\*/.test(safeCss('<!-- /**/ </Style>')))
})

test('no value that can leave a style block or open a comment passes the slot gate', () => {
  const refused: [string, string][] = [
    ['bg', '#00<0'],
    ['bg', '#000>'],
    ['bg', 'oklch(.5 .1 200 /*x*/)'],
    ['ac-soft', 'color-mix(in oklch, #fff<b 10%, transparent)'],
    ['lift', '0 0 4px #000 /*x*/'],
    ['lift', '0 0 4px </style>'],
    ['font-ui', "'IBM Plex Sans', <b>, system-ui"],
    ['font-mono', 'ui-monospace /* */'],
    ['case', '<none>'],
    ['radius', '4px/*'],
  ]
  for (const [slot, value] of refused) assert.notEqual(slotProblem(slot, value), null, `${slot}: ${value}`)
})

// Previews stay honest: whatever the look slots set, the preview's sheet draws it
const LOOK_VARS = [...new Set([...Object.values(LOOK).flatMap((t) => Object.values(t).flatMap((row) => Object.keys(row))), '--look-scan'])]
// every number of the look (the slots from font-body down), from the contract, so a new one cannot be left out
const LOOK_NUMBERS = Object.keys(SLOTS).slice(Object.keys(SLOTS).indexOf('font-body')).filter((n) => SLOTS[n].kind === 'number' || SLOTS[n].kind === 'length')

// Neon with a cut corner and a text glow, so the numbers only those use (card-cut, text-glow) are in play too,
// and glass with its light and a wallpaper on, so the glass's numbers (read through the fill, the filter and the light) are too
const EVERY_NUMBER = { ...(NEON.tokens.dark ?? {}), 'card-corner': 'chamfer', 'text-glow': '0.3', 'card-fill': 'glass', 'glass-edge': '0.5', 'card-sheen': '0.5', 'wallpaper-strength': '0.5' }

test('the preview reads every variable the look slots produce, and the numbers they come with', () => {
  const html = previewDoc(make({ tokens: { dark: EVERY_NUMBER } }), 'dark', DEFAULT)
  const unread = [...LOOK_VARS, ...LOOK_NUMBERS.map((n) => `--${n}`)].filter((v) => !html.includes(`var(${v}`))
  assert.deepEqual(unread, [])
})

test('each look slot changes what the preview draws: every variable its two values set differently is read by a rule of the document', () => {
  const slots = [...Object.keys(LOOK), ...LOOK_NUMBERS]
  // the theme's own block declares every variable whether or not a rule reads it, so only the rules after it count
  const rules = (doc: string) => (/<style>([\s\S]*)<\/style>/.exec(doc)?.[1] ?? '').replace(/\[data-theme="[^"]*"\]\[data-mode="[^"]*"\]\{[^}]*\}/g, '')
  const decls = (doc: string) => new Map([...doc.matchAll(/(--[a-z0-9-]+):([^;]*);/g)].map((m) => [m[1], m[2]]))
  for (const slot of slots) {
    const spec = SLOTS[slot]
    const [a, b] = spec.kind === 'enum' ? (spec.values ?? []) : spec.kind === 'length' ? [`${spec.min}px`, `${spec.max}px`] : [String(spec.min), String(spec.max)]
    assert.equal(slotProblem(slot, a), null, `${slot}:${a}`)
    const doc = (v: string) => previewDoc(make({ tokens: { dark: { ...EVERY_NUMBER, [slot]: v } } }), 'dark', DEFAULT)
    const [da, db] = [decls(doc(a)), decls(doc(b))]
    // an enum slot reaches the page through its --look-* row; a number slot is read as itself
    const changed = [...da.keys()].filter((k) => da.get(k) !== db.get(k) && (k.startsWith('--look-') || spec.kind !== 'enum'))
    assert.ok(changed.length > 0, `${slot}: its values set nothing different`)
    const drawn = rules(doc(a))
    // a number can reach a rule through a value a rule reads that carries it (text-glow in the text shadow, card-cut in the clip)
    const carried = (k: string) => [da, db].some((d) => [...d].some(([r, v]) => drawn.includes(`var(${r}`) && v.includes(`var(${k}`)))
    assert.deepEqual(changed.filter((k) => !drawn.includes(`var(${k}`) && !carried(k)), [], `${slot}: set, but no rule of the preview reads it`)
  }
})

test('the header mock draws the Conn mark inside its brand tile, for the token to show or hide', () => {
  const html = previewDoc(DEFAULT, 'dark', DEFAULT)
  assert.match(html, /<span class="brand g-brand-tile"[^>]*><span class="g-brand"><\/span><svg class="g-brand-mark"/)
  assert.ok(!/<script/i.test(html) && !/https?:/i.test(html))
})
