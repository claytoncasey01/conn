// The tests run without a browser, so the menu is not mounted here. Its decisions are pure (theme/menu.ts, theme/theme.svelte.ts);
// these checks pin, as text, what the markup must keep: the roles, the consent step, the keys and no hard-coded color.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const menu = readFileSync(new URL('./ThemeMenu.svelte', import.meta.url), 'utf8')
const model = readFileSync(new URL('./theme/menu.ts', import.meta.url), 'utf8')
const nav = readFileSync(new URL('./Nav.svelte', import.meta.url), 'utf8')
const markup = menu.slice(menu.indexOf('</script>'), menu.indexOf('<style>'))
const script = menu.slice(0, menu.indexOf('</script>'))

test('mode and accent are radiogroups of native radios, and a theme with variants gets one for them too', () => {
  assert.equal(markup.match(/role="radiogroup"/g)?.length, 3) // mode, swatches, variants
  assert.match(markup, /class="seg" role="radiogroup" aria-labelledby="\{uid\}-mode"/)
  assert.match(markup, /class="swatches" role="radiogroup" aria-labelledby="\{uid\}-acc"/)
  assert.match(markup, /class="seg" role="radiogroup" aria-labelledby="\{uid\}-var"/)
  assert.ok((markup.match(/<input type="radio"/g)?.length ?? 0) >= 3)
  // each swatch is named for a screen reader
  assert.match(markup, /aria-label=\{s\.name\}/)
})

test('the accent control is shown for a free accent, variants for a variant theme, and nothing for a fixed accent', () => {
  assert.match(markup, /\{#if model\.accent === 'free'\}[\s\S]*?\{:else if model\.accent === 'variants'\}[\s\S]*?\{\/if\}/)
  assert.match(markup, /\{#each SWATCHES as s \(s\.name\)\}/)
  // keyed by position: a repeated name (validated away, but a stale list can carry one) must not throw each_key_duplicate
  assert.match(markup, /\{#each model\.variants as v, i \(i\)\}/)
  assert.doesNotMatch(markup, /\{#each model\.variants as v \(v\)\}/)
})

test('the mode control is disabled for a one-mode theme, and a hint says why', () => {
  assert.match(markup, /disabled=\{!model\.modeEnabled\}/)
  assert.match(markup, /\{#if !model\.modeEnabled\}<p class="hint"[^>]*>This theme comes in one mode only\.<\/p>\{\/if\}/)
})

test('Effects is a real button with aria-pressed that sets an explicit on or off', () => {
  assert.match(markup, /<button type="button" class="fx" aria-pressed=\{effectsOn\}/)
  assert.match(markup, /onclick=\{\(\) => setEffects\(effectsOn \? 'off' : 'on'\)\}/)
  assert.doesNotMatch(markup, /role="switch"/)
  // the name stays "Effects": the state is aria-pressed's, so the On/Off text is hidden from the name
  assert.match(markup, /aria-labelledby="\{uid\}-fx" aria-describedby/)
  assert.match(markup, /<span aria-hidden="true">\{effectsOn \? 'On' : 'Off'\}<\/span>/)
})

test('the theme list is keyed by source and id, never by id alone, and the pure model emits that key', () => {
  assert.match(markup, /\{#each model\.themes as t \(t\.key\)\}/)
  assert.doesNotMatch(markup, /\(t\.id\)|\(theme\.id\)|\(e\.id\)/)
  assert.match(model, /key: unique\(`\$\{e\.source\}:\$\{e\.id\}`\)/)
  // an option carries the key, so a same-id invalid row is never confused with the built-in
  assert.match(markup, /<option value=\{t\.key\} selected=\{t\.active\} disabled=\{t\.disabled\}>/)
})

test('a user theme with custom CSS asks first, with the spec\'s wording, Cancel and Use anyway', () => {
  assert.match(markup, /This theme includes custom CSS\. CSS can change how any part of conn looks, including what a button says\. Use it only if you wrote it or have read it\./)
  assert.match(markup, /role="alertdialog"/)
  assert.match(markup, />Cancel<\/button>/)
  assert.match(markup, />Use anyway<\/button>/)
  const choose = script.slice(script.indexOf('function choose('), script.indexOf('function accept('))
  // the consent check comes before the one selectTheme call, and a pending row returns without applying
  assert.ok(choose.indexOf('needsCssConsent(row.id)') > 0 && choose.indexOf('needsCssConsent(row.id)') < choose.indexOf('selectTheme('))
  assert.match(choose, /pending = \{[^}]*\}[\s\S]*?showCurrent\(\)[\s\S]*?return/)
  // counted over the whole file, so an inline handler in the markup would add one
  assert.equal(menu.match(/selectTheme\(/g)?.length, 1)
  // only the button accepts, and Cancel puts the select back on the theme that is applied
  assert.equal(menu.match(/acceptCss\(/g)?.length, 1)
  assert.match(markup, /onclick=\{accept\}>Use anyway/)
  assert.match(markup, /onclick=\{leavePrompt\}>Cancel/)
})

test('the panel is a popover in the top layer, so no stacking context on the page can paint over it, and it is placed inside the viewport', () => {
  assert.match(markup, /<div class="panel" id="\{uid\}-panel" popover="manual" bind:this=\{panel\}>/)
  assert.match(script, /panel\.showPopover\(\)/)
  assert.match(script, /panel\.hidePopover\(\)/)
  assert.match(script, /innerWidth - 16/)
  assert.match(markup, /onresize=\{place\} onscroll=\{place\}/)
})

test('the panel rests on an opaque base: bg underneath, the theme\'s panel colour layered over it', () => {
  // --panel is translucent in CRT and Matrix, so on its own the page (and the rain) prints through the popover
  const rule = menu.match(/\.panel \{[^}]*\}/)?.[0] ?? ''
  assert.match(rule, /background-color: var\(--bg\);/)
  assert.match(rule, /background-image: linear-gradient\(var\(--panel\), var\(--panel\)\);/)
  assert.doesNotMatch(rule, /background: var\(--panel\)/)
})

test('an unlisted stored choice shows as a disabled placeholder option, so the select never shows another theme', () => {
  assert.match(markup, /\{#if !current\}<option value="" selected disabled>\{shown\.id\}\{theme\.ready \? ' \(not installed\)' : ''\}<\/option>\{\/if\}/)
  assert.match(script, /picker\.selectedIndex = i < 0 \? 0 : i/)
})

test('after Cancel or Use anyway the select follows the store and keeps focus, and the sync runs whenever the model changes', () => {
  // the select is unbound and a touched option ignores `selected`, so the DOM is set by hand, from an effect on the model too
  assert.match(script, /\$effect\(showCurrent\)/)
  assert.match(script, /function showCurrent\(\) \{[\s\S]*?model\.themes\.findIndex\(\(t\) => t\.active\)[\s\S]*?picker\.selectedIndex/)
  const leave = script.slice(script.indexOf('async function leavePrompt('), script.indexOf('async function accept('))
  assert.match(leave, /pending = null\s+showCurrent\(\)\s+await tick\(\)\s+picker\?\.focus\(\)/)
  const accept = script.slice(script.indexOf('async function accept('), script.indexOf('function keydown('))
  assert.match(accept, /await leavePrompt\(\)/)
  // Cancel is leavePrompt too; the consent focus waits for the block to render
  assert.match(markup, /onclick=\{leavePrompt\}>Cancel/)
  assert.match(script, /await tick\(\)\s+cancel\?\.focus\(\)/)
  assert.doesNotMatch(script, /Promise\.resolve\(\)\.then/)
})

test('Use anyway accepts only the CSS that was asked about: the hash is kept with the prompt and checked, else a notice shows as plain text', () => {
  assert.match(script, /pending = \{ id: row\.id, label: row\.label, sha: row\.cssSha256 \}/)
  const accept = script.slice(script.indexOf('async function accept('), script.indexOf('function keydown('))
  assert.match(accept, /const r = canAccept\(p, theme\.list\)\s+if \(r === 'ok'\) acceptCss\(p\.id\)\s+else note = NOTES\[r\]/)
  assert.match(script, /changed: 'This theme changed while you were deciding\. Pick it again to review it\.'/)
  assert.match(script, /gone: 'This theme is no longer available\.'/)
  assert.match(markup, /\{#if note\}<p class="hint warn" role="status">\{note\}<\/p>\{\/if\}/)
  assert.doesNotMatch(menu, /\{@html/)
})

test('the select and the trigger name the look on screen: the stored choice with the link\'s pin over it, and the menu says so', () => {
  assert.match(script, /const shown = \$derived\(withPin\(theme\.choice, theme\.pin\)\)/)
  assert.match(script, /menuModel\(list, shown\.id\)/)
  assert.match(script, /const shownMode = \$derived\(model\.modeEnabled \? shown\.mode : theme\.resolved\.mode\)/)
  assert.match(markup, /\{current\?\.label \?\? shown\.id\}/)
  assert.match(markup, /<option value="" selected disabled>\{shown\.id\}/)
  // the saved choice is what a change goes back to: the note names it, in plain text, tied to the select
  assert.match(script, /pinNote\(theme\.pin, \{ theme: current\?\.label \?\? shown\.id, mode: shown\.mode \}, \{ id: theme\.choice\.id, theme: savedName, mode: theme\.choice\.mode \}\)/)
  assert.match(markup, /\{#if pinText\}<p class="hint" id="\{uid\}-pin">\{pinText\}<\/p>\{\/if\}/)
  assert.match(markup, /aria-describedby=\{pinText \? `\$\{uid\}-pin` : undefined\}/)
  // the pin comes from the store; the menu never parses the URL itself
  assert.doesNotMatch(menu, /readPin|location\b|URLSearchParams|\.search\b/)
  // nothing is labelled from the stored id alone any more
  assert.doesNotMatch(menu, /menuModel\(list, theme\.choice\.id\)\s*\)?\s*\n\s*const current/)
})

test('the three small leftovers of the menu review: long names wrap, a checked segment shows in forced colors, and the Effects line follows the OS setting live', () => {
  // a long theme name in the prompt or a hint must wrap inside the panel, not push it wider
  assert.match(menu, /\.panel \{[^}]*overflow-wrap: anywhere;/)
  // forced colors drop every background, so the checked segment is told apart by an outline (a system color, no literal)
  assert.match(menu, /@media \(forced-colors: active\) \{\s+\.opt:has\(input:checked\) \{\s+outline: 2px solid CanvasText;/)
  // reduced() is read once per derivation; the OS setting can change with the panel open, so a listener feeds a state
  assert.match(script, /let motionOff = \$state\(reduced\(\)\)/)
  assert.match(script, /matchMedia\('\(prefers-reduced-motion: reduce\)'\)[\s\S]*?addEventListener\('change', on\)[\s\S]*?removeEventListener\('change', on\)/)
  assert.match(script, /const quiet = \$derived\(theme\.choice\.effects === 'auto' && motionOff\)/)
  assert.match(script, /const effectsOn = \$derived\(theme\.choice\.effects === 'on' \|\| \(theme\.choice\.effects === 'auto' && !motionOff\)\)/)
})

test('keyboard: Escape closes and returns focus, leaving the panel closes it, focus is visible, targets are 44 px', () => {
  assert.match(script, /e\.key === 'Escape'/)
  assert.match(script, /trigger\?\.focus\(\)/)
  assert.match(markup, /aria-expanded=\{open\} aria-controls|aria-expanded=\{open\}\s+aria-controls/)
  assert.match(script, /relatedTarget/)
  assert.match(menu, /:focus-visible[\s\S]{0,200}outline: 2px solid var\(--ac\)/)
  assert.match(menu, /min-height: 44px;\s*min-width: 44px/)
})

test('no hard-coded color in the menu or its model: only var(--...) slots and a swatch computed from SWATCHES', () => {
  for (const [name, src] of [['ThemeMenu.svelte', menu], ['menu.ts', model]]) {
    assert.doesNotMatch(src, /#[0-9a-fA-F]{3,8}\b/, `${name} has a hex color`)
    assert.doesNotMatch(src, /\brgba?\(|\bhsla?\(/i, `${name} has an rgb or hsl color`)
  }
  assert.match(markup, /style="background: \{swatch\(s\)\}"/)
  assert.match(script, /accentTokens\(theme\.resolved\.mode, s\.hue, s\.chroma\)\.ac/)
})

test('Nav only gains the menu: one import and one element after the alerts button, and the menu is the last child', () => {
  assert.equal(nav.match(/<ThemeMenu\b/g)?.length, 1)
  assert.match(nav, /import ThemeMenu from '\.\/ThemeMenu\.svelte'/)
  assert.match(nav, /\{\/if\}\n  <ThemeMenu \/>\n<\/nav>/)
})

test('an active theme whose CSS changed shows a hint, as text, and its button opens the same consent prompt a pick does', () => {
  assert.match(script, /const review = \$derived\(needsReview\(model, list, acceptedCss\(\)\)\)/)
  const block = markup.slice(markup.indexOf('{#if review && !pending}'), markup.indexOf('{#if pending}'))
  assert.ok(block.length > 0, 'the hint sits between the select and the prompt, hidden while the prompt is open')
  assert.match(block, /<p class="hint warn" role="status">This theme's CSS changed\. Review it to use it again\.<\/p>/)
  // the button goes through choose(), so the hash pinning and canAccept guard are the pick's own
  assert.match(block, /<button type="button" class="btn" onclick=\{\(\) => review && choose\(review\.key\)\}>Review CSS<\/button>/)
  // one place sets a prompt, one applies, one accepts: counted over the whole file
  assert.equal(menu.match(/pending = \{/g)?.length, 1)
  assert.equal(menu.match(/selectTheme\(/g)?.length, 1)
  assert.equal(menu.match(/acceptCss\(/g)?.length, 1)
})

test('the variant radios check the shown variant, not the raw stored one', () => {
  assert.match(script, /const shownVar = \$derived\(shownVariant\(model\.variants, theme\.choice\.variant\)\)/)
  assert.match(markup, /checked=\{shownVar === v\}/)
  assert.doesNotMatch(markup, /theme\.choice\.variant === v/)
})
