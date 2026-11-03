// The tests run without a browser, so the Themes page is not mounted here. Its decisions are pure (theme/galleryModel.ts,
// theme/galleryView.ts); these checks pin, as text, what the markup must keep: a sandboxed preview, the lines the gallery owes
// the person, a confirmation before an uninstall, and no hard-coded color.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const page = readFileSync(new URL('../pages/ThemesPage.svelte', import.meta.url), 'utf8')
const menu = readFileSync(new URL('./ThemeMenu.svelte', import.meta.url), 'utf8')
const markup = page.slice(page.indexOf('</script>'), page.indexOf('<style>'))
const iframes = markup.match(/<iframe\b[^>]*>/g) ?? []

test('every preview iframe is sandboxed with nothing granted, carries the csp, a title and a fixed height of at least 250px', () => {
  assert.ok(iframes.length >= 1)
  for (const tag of iframes) {
    assert.match(tag, /sandbox=\{previewAttrs\.sandbox\}/)
    assert.match(tag, /csp=\{previewAttrs\.csp\}/)
    assert.match(tag, /srcdoc=\{previewDoc\(/)
    assert.match(tag, /title="Preview of \{c\.name\}"/)
    // nothing inside a preview takes focus, so the frame is not a stop between card buttons
    assert.match(tag, /tabindex="-1"/)
    assert.ok(Number(/\bheight="(\d+)"/.exec(tag)?.[1]) >= 250, tag)
  }
  assert.match(page, /\.pv \{[^}]*height: 25\d+px/)
  // nothing is ever granted to a frame: no sandbox token and no permission-policy attribute anywhere in the file
  assert.doesNotMatch(page, /allow-/)
  assert.doesNotMatch(page, /\ballow=/)
  assert.doesNotMatch(page, /sandbox="[^"]/)
})

test('a broken install gets its own card before anything is previewed: no preview call and no first-mode read for it', () => {
  const broken = markup.indexOf('c.invalid !== undefined')
  assert.ok(broken >= 0)
  assert.ok(broken < markup.indexOf('previewDoc('), 'the invalid branch comes before the preview')
  assert.match(markup, /<h2 data-content>This theme is broken<\/h2>/)
  assert.match(markup, /\{c\.invalid\}/)
  assert.doesNotMatch(page, /modes\[0\]/)
  assert.equal(markup.match(/previewDoc\(/g)?.length, 1)
})

test('cards are keyed by source and id, never by id alone', () => {
  assert.match(markup, /\{#each cards as c \(keyOf\(c\)\)\}/)
  assert.match(page, /const keyOf = \(c: Card\) => `\$\{c\.source\}:\$\{c\.id\}`/)
  assert.doesNotMatch(markup, /\(c\.id\)/)
})

test('the page is titled, and its card names sit one level under the title', () => {
  assert.match(markup, /<h1>Themes<\/h1>/)
  assert.doesNotMatch(markup, /<h3/)
  assert.equal(markup.match(/<h2 data-content>/g)?.length, 2)
  // the app's own .card and .seg are global (Neon lifts a .card on hover): this page's tiles and filters use other names
  assert.doesNotMatch(page, /class="(card|seg)\b|\.(card|seg)\b/)
})

test('with the gallery off no card offers an install or an uninstall, which the server has no route for', () => {
  assert.match(page, /const canChange = \$derived\(answer\?\.enabled !== false\)/)
  const uses = markup.match(/<ConfirmAction\b[\s\S]*?\/>/g) ?? []
  assert.ok(uses.length >= 2)
  assert.match(markup, /\{#if canChange\}\s*<ConfirmAction/)
  assert.match(markup, /\{#if c\.source === 'gallery' && canChange\}\s*<ConfirmAction/)
})

test('focus is put back where a press removed it: the search box after Show all themes, the card\'s new button after an install or uninstall', () => {
  assert.match(page, /async function refocus\(key: string\)/)
  assert.match(page, /await refocus\(next\)/)
  assert.match(page, /void refocus\(`community:\$\{c\.id\}`\)/)
  assert.match(page, /async function clearSearch\(\)[\s\S]*?await tick\(\)\s*search\?\.focus\(\)/)
  assert.match(markup, /<li class="tile" data-key=\{k\}/)
})

test('the page has a search box and the three filters, All, Installed and Community', () => {
  assert.match(markup, /<input type="search" bind:this=\{search\} bind:value=\{query\}/)
  assert.match(page, /\{ id: 'all', label: 'All' \}/)
  assert.match(page, /\{ id: 'installed', label: 'Installed' \}/)
  assert.match(page, /\{ id: 'community', label: 'Community' \}/)
  assert.match(markup, /aria-pressed=\{filter === f\.id\}/)
  // the search box is named for a screen reader
  assert.match(markup, /<span class="sr">Search themes<\/span>/)
})

test('it names the index repo and when it was checked, and says community themes are colors and settings only', () => {
  assert.match(markup, /Community themes come from <code data-content>\{line\.repo\}<\/code>, \{line\.when\}\./)
  assert.match(markup, /<b>Community themes are colors and settings only\.<\/b>/)
})

test('offline, rate-limited and off show the notice, and an install shows what is in flight', () => {
  assert.match(markup, /\{#if why\}[\s\S]*?\{why\.text\}[\s\S]*?\{why\.reason\}/)
  assert.match(markup, /'Installing…'/)
  assert.match(markup, /aria-disabled=\{working\[k\]\}/)
  // the broken card's Update and the normal card's say the same thing while they work
  assert.equal(markup.match(/'Updating…'/g)?.length, 2)
  assert.doesNotMatch(markup, /working\[k\] \? 'Installing…' : 'Update'/)
  // an error from the API stays on its card
  assert.match(markup, /\{#if failures\[k\]\}<p class="err" role="alert"/)
})

test('an uninstall asks first, through the app\'s own confirm step, and reads the themes again after it', () => {
  const uses = markup.match(/<ConfirmAction\b[\s\S]*?\/>/g) ?? []
  assert.ok(uses.length >= 2, 'a broken card and an installed one')
  for (const u of uses) {
    assert.match(u, /label="Uninstall"/)
    assert.match(u, /confirmText="Uninstall \{c\.(id|name)\}\?"/)
    assert.match(u, /confirmLabel="Uninstall"/)
    assert.match(u, /run=\{\(\) => uninstall\(c\)\}/)
  }
  // no browser dialog, and no direct call that skips the confirm
  assert.doesNotMatch(page, /\bconfirm\(/)
  assert.equal(page.match(/uninstallTheme\(/g)?.length, 1)
  assert.match(page, /const r = await uninstallTheme\(c\.id\)\s*if \(r\.ok\) \{\s*await refreshThemes\(\)/)
  // install reads them again too, so the card turns into Installed
  assert.match(page, /const r = await installTheme\(c\.id\)[\s\S]*?await refreshThemes\(\)/)
})

test('the page paints from tokens only, and every control has the keyboard focus ring and a 44px target', () => {
  assert.doesNotMatch(page, /(?<![\w&])#[0-9a-fA-F]{3,8}(?![\w-])|\b(?:rgba?|hsla?)\(|\boklch\(\s*[.\d]/)
  assert.equal(page.match(/min-height: 44px/g)?.length, 4) // the search box, a filter, the Check again button, and .btn
  // the global ring (app.css) covers button, a and input; nothing here removes it
  assert.doesNotMatch(page, /outline:\s*(none|0)/)
  assert.match(page, /@media \(prefers-reduced-motion: no-preference\)/)
  assert.match(page, /@media \(max-width: 600px\)/)
})

test('the theme menu links to the page, last in its panel, and not from the page itself', () => {
  assert.match(menu, /\{#if router\.route\.page !== 'themes'\}[\s\S]*?<a class="browse" href=\{href\.themes\(\)\}>Browse themes<\/a>/)
  const panel = menu.slice(menu.indexOf('<div class="panel"'), menu.indexOf('<style>'))
  assert.ok(panel.indexOf('class="browse"') > panel.indexOf('setEffects('), 'after the last control')
})

test('a busy or idle button keeps keyboard focus: aria-disabled and a guard, never disabled, and Use and In use are one element', () => {
  assert.doesNotMatch(markup, /(?<![\w-])disabled(=|>)/)
  assert.doesNotMatch(page, /:disabled/)
  assert.match(markup, /<button type="button" class="btn" class:primary=\{!current\} aria-disabled=\{current\} onclick=\{\(\) => !current && use\(c\)\}>\{current \? 'In use' : 'Use'\}<\/button>/)
  assert.match(page, /\.btn\[aria-disabled='true'\] \{[^}]*opacity/)
  // each handler ignores a press while it is busy
  assert.match(page, /async function check\(\) \{\s*if \(checking\) return/)
  assert.match(page, /if \(working\[k\]\) return/)
})

test('long text wraps instead of being clipped or widening a card: the by-line, version, description, flag and meta use overflow-wrap', () => {
  const style = page.slice(page.indexOf('<style>'))
  const rule = (selector: string) => new RegExp(`(?:^|\\n)  ${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} \\{([^}]*)\\}`).exec(style)?.[1] ?? ''
  for (const sel of ['.desc', '.flag', '.reason', 'h2']) assert.match(rule(sel), /overflow-wrap: anywhere/, sel)
  // .ver, .by and .meta share one rule
  assert.match(style, /\.ver,\n  \.by,\n  \.meta \{[^}]*overflow-wrap: anywhere/)
  // a flex child may shrink below its content
  assert.match(rule('.head > *'), /min-width: 0/)
})
