// The Services block's promises that no render test checks, pinned as text like the other page source tests: CHECK only from a press,
// a save only from a button and only its own section, no autosave, the off rule, nothing from outside rendered as HTML.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8')
const settingsPage = read('../pages/SettingsPage.svelte')
const page = read('../pages/SettingsServices.svelte')
const list = read('../pages/SettingsNameList.svelte')
const script = page.slice(page.indexOf('<script'), page.indexOf('</script>'))
const effects = [...script.matchAll(/\$effect\(\(\) => \{[\s\S]*?\n  \}\)/g)].map((m) => m[0])

test('CHECK is called from its press handler only, never on load, from an effect or on a timer', () => {
  assert.equal(script.match(/checkModule\(/g)?.length, 1)
  assert.match(script, /async function check\(name: ModuleName\) \{[\s\S]*?await checkModule\(name\)/)
  assert.equal(page.match(/onclick=\{\(\) => check\(name\)\}/g)?.length, 1)
  assert.ok(effects.length >= 1)
  for (const e of effects) assert.doesNotMatch(e, /checkModule|check\(|putSection|save/)
  // the one interval is a clock for "3 min ago"
  assert.equal(script.match(/setInterval\(/g)?.length, 1)
  assert.match(script, /setInterval\(\(\) => \(now = Date\.now\(\)\), 30_000\)/)
})

test('a save is explicit: putSection runs from the five save functions, one section each, which only buttons call', () => {
  assert.equal(script.match(/putSection\(/g)?.length, 5)
  for (const [fn, section] of [['saveGithub', 'github'], ['saveSlack', 'slack'], ['saveAtlassian', 'atlassian'], ['saveReviews', 'reviews'], ['saveSessions', 'sessions']]) {
    assert.match(script, new RegExp(`async function ${fn}\\(\\) \\{[\\s\\S]*?putSection\\('${section}', body\\)`))
  }
  for (const fn of ['saveGithub', 'saveSlack', 'saveAtlassian', 'saveReviews', 'saveSessions']) assert.equal(page.match(new RegExp(`\\b${fn}\\b`, 'g'))?.length, 2, `${fn}: declared, then passed once as an onsave argument`)
  // the one button every card saves through: it presses nothing that is not changed or is already saving
  assert.equal(page.match(/onclick=\{\(\) => dirty && !saving\[section\] && onsave\(\)\}/g)?.length, 1)
  assert.doesNotMatch(page, /on(?:input|change|blur|keyup)=\{[^}]*(?:save|put)/i)
  assert.doesNotMatch(list, /putSection|settingsApi/)
})

test('a failed save keeps the draft and shows the server’s words; a good one shows what was sent', () => {
  for (const s of ['github', 'slack', 'atlassian', 'reviews', 'sessions']) assert.match(script, new RegExp(`if \\(!r\\.ok\\) return void \\(saveError\\.${s} = r\\.error\\)`))
  assert.match(script, /wrote\.slack = wroteSlack\(body\)/)
  assert.match(script, /wrote\.atlassian = wroteAtlassian\(body\)/)
})

test('a module that is off keeps its settings in view, greyed and not editable, and says conn makes no calls', () => {
  assert.match(page, /const OFF_TEXT = 'Off: conn makes no calls'/)
  assert.match(page, /<fieldset class="mset" disabled=\{!slack\.enabled\}>/)
  assert.match(page, /<fieldset class="mset" disabled=\{!atl\.enabled\}>/)
  assert.match(page, /\.mod\.off \.mset,/)
  // CHECK needs the saved switch on: the server refuses a probe of a module that is off
  assert.match(page, /aria-disabled=\{!savedOn\[name\] \|\| checking\[name\]\}/)
})

test('the page makes no GitHub or connector call by itself: the only requests are the saves and CHECK', () => {
  assert.doesNotMatch(script, /fetch\(/)
  assert.doesNotMatch(page, /browseGithub/)
})

test('nothing from outside is rendered as HTML', () => {
  for (const src of [page, list]) {
    assert.doesNotMatch(src, /\{@html/)
    assert.doesNotMatch(src, /innerHTML/)
  }
})

test('the Services block is rendered from one place, so ending setup moves it and never rebuilds it (a draft in it survives)', () => {
  assert.equal(settingsPage.match(/<SettingsServices\b/g)?.length, 1)
  assert.doesNotMatch(settingsPage, /\{@render services\(\)\}|\{#snippet services/)
  assert.match(settingsPage, /<div class="svc" class:later=\{!setup\}>\s*<SettingsServices/)
  assert.match(settingsPage, /\.svc\.later \{\s*order: 1;/)
  assert.match(settingsPage, /\.clones \{\s*order: 2;/)
})

test('the old review records card comes after Local clones: the page orders its cards, so it carries an order past theirs', () => {
  assert.match(read('../pages/ReviewLegacy.svelte'), /\.card \{\s*order: 3;/)
  assert.ok(settingsPage.indexOf('<ReviewLegacy') > settingsPage.indexOf('class="surf card clones"'))
})

test('a button that is not pressable keeps the keyboard focus: CHECK, Save and Add are aria-disabled, not disabled', () => {
  assert.doesNotMatch(page, /<button[^>]*\sdisabled=\{/)
  assert.doesNotMatch(list, /<button type="submit"[^>]*\sdisabled=\{/)
  assert.match(list, /<button type="submit" class="btn small cap" aria-disabled=/)
})

test('what is on or off now is what is saved: the off note and the Lists sentence read the saved switches, the two review switches are the live part', () => {
  for (const m of ['github', 'slack', 'atlassian']) assert.match(page, new RegExp(`\\{#if !savedOn\\.${m}\\}<p class="off-note">`))
  assert.match(page, /const sources = \$derived\(\{ github: view\.settings\.github\.enabled, slackOn: view\.settings\.slack\.enabled, channels: view\.settings\.slack\.channels, dms: view\.settings\.slack\.dms, requested, allOpen \}\)/)
})

test('a save does not overwrite what was typed while it was in flight: the draft is reset only when it is still what was sent', () => {
  assert.match(script, /if \(sameSlack\(slack, sent\)\) slack = copySlack\(body\)/)
  assert.match(script, /if \(sameAtlassian\(atl, sent\)\) atl = copyAtl\(body\)/)
  assert.doesNotMatch(script, /\n    slack = copySlack\(body\)/)
  assert.doesNotMatch(script, /\n    atl = copyAtl\(body\)/)
})

test('first run puts the keyboard at Step 1 (the Services heading), once, and only where there is a keyboard to type with', () => {
  const pageScript = settingsPage.slice(settingsPage.indexOf('<script'), settingsPage.indexOf('</script>'))
  assert.match(pageScript, /matchMedia\('\(hover: hover\) and \(pointer: fine\)'\)\.matches[\s\S]*?document\.getElementById\('services-h'\)\?\.focus\(\)/)
  assert.match(page, /<h2 id="services-h" class="hud cap" tabindex="-1">/)
})

test('the Atlassian card has a Base branch box: labelled, a draft until its card is saved, and Save waits while the name is not a branch', () => {
  assert.match(page, /<label class="lab" for="jira-base">Base branch for every repository without its own<\/label>/)
  assert.match(page, /id="jira-base"[^>]*oninput=\{\(e\) => \{ atl\.jira\.baseBranch = e\.currentTarget\.value; edited\('atlassian'\) \}\}/)
  assert.match(page, /\{@render saveRow\('atlassian', atlDirty && !anyBaseProblem, saveAtlassian\)\}/)
  assert.match(page, /const anyBaseProblem = \$derived\(baseProblem !== null \|\| Object\.keys\(repoProblems\)\.length > 0\)/)
  assert.match(page, /Empty: the repository's default branch\./)
})

test('the Atlassian card: the build repository is an optional default with None as a real choice, each allowed repository has its own optional base branch box, and the review move and layout are two selects, all drafts until Save', () => {
  assert.match(page, /<label class="lab" for="jira-build">Default repository \(optional\)<\/label>/)
  assert.match(page, /<option value="">None<\/option>/)
  assert.match(page, /\{#each baseRepos as repo, index \(repo\.toLowerCase\(\)\)\}/)
  assert.match(page, /Base branch for \{plainText\(repo\)\} \(optional\)/)
  assert.match(page, /oninput=\{\(e\) => \{ atl\.jira\.baseBranches\[key\] = e\.currentTarget\.value; edited\('atlassian'\) \}\}/)
  assert.match(page, /<label class="lab" for="jira-review">Move the ticket to review<\/label>/)
  assert.match(page, /<label class="lab" for="jira-layout">Sessions across repositories<\/label>/)
  assert.match(page, /\{#each REVIEW_MOVES as m \(m\.value\)\}/)
  assert.match(page, /\{#each SESSION_LAYOUTS as l \(l\.value\)\}/)
  // each select has its one-line help, and a choice only edits the draft
  assert.match(page, /id="jira-review-hint" class="hint"/)
  assert.match(page, /id="jira-layout-hint" class="hint"/)
  assert.match(script, /const setReviewMove = \(value: string\) => \{[\s\S]*?edited\('atlassian'\)\s*\}/)
  assert.match(script, /const setSessionLayout = \(value: string\) => \{[\s\S]*?edited\('atlassian'\)\s*\}/)
})

test('the sessions card offers each choice with its one line of help, and no field that names a program', () => {
  assert.match(page, /\{#each WHERE_CHOICES as choice \(choice\.value\)\}/)
  assert.match(page, /<b>\{choice\.label\}<\/b>/)
  assert.match(page, /<span class="hint">\{choice\.help\}<\/span>/)
  assert.doesNotMatch(page, /terminalCommand|terminalText|sessions-terminal/)
  assert.doesNotMatch(page, /\{@html/)
})
