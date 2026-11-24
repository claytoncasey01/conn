// The draft panel's markup, held as text like the other source tests: model and PR text is data, Select all shows a partial
// tick, Approve is disabled with its reason, and a row shows the panel only when it has a draft.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const panel = readFileSync(new URL('./ReviewDraft.svelte', import.meta.url), 'utf8')
const row = readFileSync(new URL('../pages/ReviewRow.svelte', import.meta.url), 'utf8')

test('the summary, the findings and the problems are plain text, never markup', () => {
  assert.doesNotMatch(panel, /\{@html/)
  assert.match(panel, /<div data-content [^>]*>\{draft\.summary\}<\/div>/)
  assert.match(panel, /<li data-content [^>]*>\{problem\}<\/li>/)
})

test('Select all is indeterminate while some findings are ticked', () => {
  assert.match(panel, /<input type="checkbox" checked=\{allOn\} indeterminate=\{someOn\}/)
})

test('the verdict is a labelled group of two radios, and Approve is disabled with its reason while a blocker is ticked', () => {
  assert.match(panel, /<fieldset[^>]*>\s*<legend[^>]*>Verdict<\/legend>/)
  assert.equal(panel.match(/<input type="radio"/g)?.length, 2)
  assert.match(panel, /title=\{blocked \? APPROVE_BLOCKED : undefined\}/)
  assert.match(panel, /value="approve" checked=\{verdict === 'approve'\} disabled=\{blocked \|\| inert\}/)
})

test('the row renders the panel for a draft, keyed by the review file, and greys it once the PR is done', () => {
  assert.match(row, /\{#if row\.draft\}\s*\{#key draftId\(row\.draft\)\}\s*<DraftPanel rowKey=\{row\.key\} draft=\{row\.draft\} greyed=\{draftGreyed\} \/>/)
  assert.match(row, /const draftGreyed = \$derived\(row\.ended \? `This PR is \$\{row\.ended\}` : done \?/)
})
