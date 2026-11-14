// app.css is the calm base every theme gets; these checks pin, as text, the rules that make the type and shape tokens act
// and keep a theme's `case` and `tracking` off content (a transcript line, a branch, a draft in the reply box).
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import { LOOK_CSS } from './theme/lookCss.ts'

const raw = readFileSync(new URL('../app.css', import.meta.url), 'utf8')
const css = raw.replace(/\/\*[\s\S]*?\*\//g, '')
// Flat rules only: an at-rule's own `{` never matches, so the rules inside a @media come out one by one.
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ sel: m[1].trim().replace(/\s+/g, ' '), body: m[2].replace(/\s+/g, '') }))
const sels = (r: { sel: string }) => r.sel.split(/,(?![^(]*\))/).map((s) => s.trim())
const find = (sel: string) => rules.filter((r) => sels(r).includes(sel))

test('no body rule sets text-transform or letter-spacing: case and tracking are for chrome, never inherited by content', () => {
  const bad = rules.filter((r) => sels(r).some((s) => /^(html|:root|body)$/.test(s)) && /text-transform|letter-spacing/.test(r.body))
  assert.deepEqual(bad.map((r) => r.sel), [])
})

// the chrome rules are the look's (lookCss.ts): case and tracking reach chrome through the chrome token
const look = LOOK_CSS.replace(/\/\*[\s\S]*?\*\//g, '')
test('case and tracking apply to the chrome whitelist only', () => {
  const chrome = /:is\(nav,h1,h2,h3,h4,label,th,legend,summary,\.hud\)\{([^}]*)\}/.exec(look)?.[1] ?? ''
  assert.ok(chrome.includes('text-transform:var(--look-chrome-case)') && chrome.includes('letter-spacing:var(--look-chrome-tracking)'), chrome)
  const buttons = /:is\(button,button\.hud\)\{([^}]*)\}/.exec(look)?.[1] ?? ''
  assert.ok(buttons.includes('text-transform:var(--look-chrome-case, none)') && buttons.includes('letter-spacing:var(--look-chrome-tracking, normal)'), buttons)
  assert.match(look, /\.cap\.cap\{text-transform:var\(--case\)\}/)
})

test('content is never cased: pre, code, input and textarea (and the rest) are forced to text-transform:none', () => {
  const r = rules.find((x) => /^:is\(/.test(x.sel) && ['pre', 'code', 'input', 'textarea'].every((t) => new RegExp(`[(,]${t}[,)]`).test(x.sel)))
  assert.ok(r, 'a rule names pre, code, input and textarea')
  assert.ok(r.body.includes('text-transform:none'), r.body)
  for (const t of ['kbd', 'samp', 'select', '[data-content]']) assert.ok(r.sel.includes(t), `${t} is content too`)
})

test('the content rule beats the chrome rule: equal weight (one class or attribute each), and it comes later', () => {
  // the look's sheet is put first in the head (main.ts) and app.css comes after it
  assert.ok(look.includes(':is(nav,h1,h2,h3,h4,label,th,legend,summary,.hud)'), 'the chrome rule is in the look')
  assert.ok(css.includes(':is(pre,code,kbd,samp,input,textarea,select,[data-content])'), 'the content rule is in app.css')
  // no argument of either list is heavier than one class or attribute (no ids, no compound selectors)
  for (const list of ['nav,h1,h2,h3,h4,label,th,legend,summary,.hud', 'pre,code,kbd,samp,input,textarea,select,[data-content]'])
    for (const arg of list.split(',')) assert.match(arg, /^(?:[a-z][a-z0-9]*|\.[\w-]+|\[[\w-]+\])$/, arg)
})

test('the type and shape tokens act: the page font, inherited control fonts, the page background', () => {
  const body = find('body').map((r) => r.body).join(';')
  assert.ok(body.includes('font-family:var(--font-ui)'), body)
  assert.ok(body.includes('background:var(--bg)'), body)
  const ctl = rules.find((r) => ['button', 'input', 'select'].every((t) => sels(r).includes(t)))
  assert.ok(ctl?.body.includes('font:inherit'), 'button, input, select inherit the font')
})

test('color-scheme follows the resolved mode, so native controls (date inputs, scrollbars) match it', () => {
  assert.ok(find(':root[data-mode="light"]').some((r) => r.body.includes('color-scheme:light')))
  assert.ok(find(':root[data-mode="dark"]').some((r) => r.body.includes('color-scheme:dark')))
})

test('glass is a blur behind panels only when the theme turns it on', () => {
  assert.ok(find('[data-glass="on"] [style*="var(--panel)"]:not(.page-head):not(.surf)').some((r) => r.body.includes('backdrop-filter:blur(var(--blur))')))
})

test('the status dot (.led) takes the theme\'s led shape', () => {
  assert.ok(find('.led').some((r) => r.body.includes('border-radius:50%')))
  assert.ok(find('html[data-led="diamond"] .led').some((r) => r.body.includes('border-radius:0') && r.body.includes('rotate(45deg)')))
  assert.ok(find('html[data-led="square"] .led').some((r) => r.body.includes('border-radius:0')))
})

test('Neon\'s motion is not in the base: no ring, beam, glint run, lift, pulse, comet, orbit or flow here, and the look is not here at all', () => {
  // a :not(.surf) only keeps a base rule off the surfaces; it draws nothing on them
  assert.doesNotMatch(css, /(?<!:not\()\.surf|\.page\{|\.stripe|\.brk::after|--look-/, 'the look is lookCss.ts, which the app and the preview share')
  assert.doesNotMatch(css, /@keyframes (spin|lead|pulse|flow|rot|node|beam|glint)\b|@property/)
  assert.doesNotMatch(css, /translate[XY]\(-?\d/, 'no hover lift')
  for (const sel of ['.ring', '.cursor', '.seg', '.segon', '.segon-o']) {
    for (const r of find(sel)) assert.doesNotMatch(r.body, /gradient\([^)]*#|conic-gradient/, `${sel} is plain and on tokens`)
  }
  assert.match(look, /\.beam\{display:none\}/, 'the beam is hidden unless Neon runs it')
  assert.match(look, /\.pk\{display:var\(--look-comet, none\)/, 'packets travel only when wire-flow is packets')
})

// the glass rule (0,2,0) outweighs .page-head and .surf (0,1,0), so it must leave both to their own tokens: the header's frost,
// and card-fill's blur (Matrix's glass cards were drawn at the 5px of `blur`, not card-fill's 10px, and unlike the preview)
test('glass frosts panels but not the top bar or a surface, so the header and card-fill tokens decide their own blur', () => {
  const glass = rules.filter((r) => r.sel.startsWith('[data-glass="on"]') && r.body.includes('blur(var(--blur))'))
  assert.equal(glass.length, 1)
  assert.match(glass[0].sel, /:not\(\.page-head\)/)
  assert.match(glass[0].sel, /:not\(\.surf\)/)
})

// Reduce transparency takes the frost off those panels, from the OS (the same weight, later in the cascade) or from Conn.app
test('glass frosts no panel under Reduce transparency, from either source', () => {
  const frost = '[data-glass="on"] [style*="var(--panel)"]:not(.page-head):not(.surf)'
  assert.ok(css.includes(`@media (prefers-reduced-transparency: reduce){${frost}{backdrop-filter:none}}`), 'the OS setting')
  assert.ok(css.includes(`[data-transparency="reduce"]${frost}{backdrop-filter:none}`), 'Conn.app passing it on')
  assert.ok(css.indexOf(`${frost}{backdrop-filter:none}`) > css.indexOf(`${frost}{backdrop-filter:blur(var(--blur))}`), 'after the frost')
})

// the base's animations run only behind the Effects gate (motionGateSource.test.ts); reduced motion also drops its transitions
test('reduced motion stops what the base animates', () => {
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*\.card,\.row,\.opt,\.go\{transition:none\}/)
  assert.match(css, /@media \(prefers-reduced-motion: no-preference\)\{\[data-fx="on"\] \.blink\{animation:blink/)
})

const svelteFiles = () => {
  const dir = (d: string) => readdirSync(new URL(d, import.meta.url)).filter((f) => f.endsWith('.svelte')).map((f) => d + f)
  return [...dir('../pages/'), ...dir('./')]
}

test('pages and components name fonts by token, so a theme\'s fonts reach them', () => {
  const hits = svelteFiles().filter((f) => /font-family:\s*'/.test(readFileSync(new URL(f, import.meta.url), 'utf8')))
  assert.deepEqual(hits, [])
})

test('a status dot carries .led and no inline round radius, so the theme\'s led shape reaches it', () => {
  const bare = svelteFiles().flatMap((f) =>
    [...readFileSync(new URL(f, import.meta.url), 'utf8').matchAll(/<span\b[^>]*>/g)]
      .filter((m) => /width: (7|8|9|10)px; height: \1px/.test(m[0]) && /border-radius: 50%/.test(m[0]))
      .map(() => f),
  )
  assert.deepEqual(bare, [])
  const tagged = svelteFiles().reduce((n, f) => n + (readFileSync(new URL(f, import.meta.url), 'utf8').match(/class=(?:"led[ "]|\{\['led')/g)?.length ?? 0), 0)
  assert.ok(tagged >= 40, `${tagged} dots carry .led`)
})
