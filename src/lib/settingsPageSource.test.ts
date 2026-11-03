// The Settings page's promises that no render test checks, pinned as text like the other page source tests: the first-run sentence,
// no GitHub call until BROWSE is pressed, no autosave, nothing from outside rendered as HTML, and the first-launch wiring.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8')
const page = read('../pages/SettingsPage.svelte')
const script = page.slice(page.indexOf('<script'), page.indexOf('</script>'))

test('first run says what it says', () => {
  assert.match(page, /<b>Choose what conn may read\.<\/b> Pick the repositories below\./)
  // no claim the page cannot keep while the readers are not yet gated
  assert.doesNotMatch(page, /Nothing is fetched until you do/)
})

test('BROWSE GITHUB is called from its press handler only, never on load or from an effect', () => {
  assert.equal(script.match(/browseGithub\(/g)?.length, 1)
  assert.match(script, /async function browseNow\(\) \{[\s\S]*?await browseGithub\(\)/)
  const effects = [...script.matchAll(/\$effect\(\(\) => \{[\s\S]*?\n  \}\)/g)].map((m) => m[0])
  assert.ok(effects.length >= 1)
  for (const e of effects) assert.doesNotMatch(e, /browseGithub|putSection|savePatternsNow|saveClonesNow/)
})

test('a save is explicit: putSection runs only from the two save functions, which only buttons call', () => {
  assert.equal(script.match(/putSection\(/g)?.length, 2)
  assert.match(script, /async function savePatternsNow\(\) \{[\s\S]*?const body = savePatterns\([\s\S]*?putSection\('repos', body\)/)
  assert.match(script, /async function saveClonesNow\(\) \{[\s\S]*?putSection\('repos', body\)/)
  assert.equal(page.match(/onclick=\{savePatternsNow\}/g)?.length, 1)
  assert.equal(page.match(/onclick=\{saveClonesNow\}/g)?.length, 1)
  assert.doesNotMatch(page, /on(?:input|change|blur|keyup)=\{[^}]*(?:save|put)/i)
})

test('a failed save keeps the draft; a good one shows what was sent whatever the read-back does', () => {
  assert.match(script, /if \(!r\.ok\) return void \(patError = r\.error\)\n/)
  assert.match(script, /if \(!r\.ok\) return void \(cloneError = r\.error\)\n/)
  // the sent value is applied and shown before the read-back, so a failed or overtaken GET cannot leave an old list as "Saved"
  assert.match(script, /view = withSavedRepos\(view, body\)\n    draftAllow = sent\n    patWrote = wrotePatterns\(sent\)\n    await load\('patterns'\)/)
  assert.match(script, /cloneWrote = wroteClones\(body\.clones, body\.cloneRoot\)\n    await load\('clones'\)/)
})

test('a message about the last save goes when the person edits, and the add box speaks only after blur or Enter', () => {
  assert.match(script, /function edited\(\) \{\n    patError = null/)
  assert.match(script, /function cloneEdited\(\) \{\n    cloneError = null/)
  assert.match(page, /onblur=\{\(\) => \(touched = true\)\}/)
  assert.match(script, /const showErr = \$derived\(touched && check\.state === 'error'\)/)
  // the alert exists only while an error is shown, never as a role toggled on one paragraph
  assert.doesNotMatch(page, /role=\{[^}]*\? 'alert'/)
})

test('focus is not dropped on the body: a removed row or a pressed chip hands it on, and the add box is focused on a pointer-and-hover screen only', () => {
  assert.match(script, /nextFocusIndex\(left\.length, at\)/)
  assert.equal(script.match(/nextFocusIndex\(/g)?.length, 2)
  assert.match(script, /matchMedia\('\(hover: hover\) and \(pointer: fine\)'\)\.matches/)
})

test('nothing from outside is rendered as HTML', () => {
  assert.doesNotMatch(page, /\{@html/)
  assert.doesNotMatch(page, /innerHTML/)
})

test('the first-launch redirect is wired in App, Settings is in the nav, and the theme gallery stays reachable', () => {
  const app = read('../App.svelte')
  assert.match(app, /needsSetup\(router\.route, live\.snap\)/)
  assert.match(app, /location\.replace\(href\.settings\(\)\)/)
  assert.match(app, /<SettingsPage \/>/)
  assert.match(read('./Nav.svelte'), /href=\{href\.settings\(\)\}[^>]*>Settings<\/a>/)
})
