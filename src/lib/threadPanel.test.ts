import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canPress, ignoredNote, liveDraft, newerProposal, nextFocus, overLimit, panelState, REPLY_LIMIT, threadLabel } from './threadPanel.ts'
import type { RowThread } from './types.ts'

type P = NonNullable<RowThread['proposal']>
const th = (over: Partial<RowThread> = {}): RowThread => ({ id: 'PRRT_kwDOabc123', by: 'sam', path: 'a.ts', line: 12, text: 'Handle the empty case.', proposal: null, ...over })
const prop = (over: Partial<P> = {}): P => ({ text: 'Fixed in the last commit.', state: 'proposed', error: null, ...over })

test('the box starts with the stored proposal, or empty', () => {
  assert.equal(panelState(th({ proposal: prop() }), undefined).text, 'Fixed in the last commit.')
  assert.equal(panelState(th(), undefined).text, '')
})

test('button label follows the box text', () => {
  assert.equal(panelState(th(), undefined).button, 'RESOLVE')
  assert.equal(panelState(th(), '').button, 'RESOLVE')
  assert.equal(panelState(th(), ' \n\t ').button, 'RESOLVE')
  assert.equal(panelState(th(), 'Done.').button, 'POST AND RESOLVE')
  assert.equal(panelState(th({ proposal: prop() }), undefined).button, 'POST AND RESOLVE')
  // Jane empties a proposed reply: the click is then a bare resolve
  assert.equal(panelState(th({ proposal: prop() }), '').button, 'RESOLVE')
  // a proposal with no text (the session found nothing to say) starts as RESOLVE
  assert.equal(panelState(th({ proposal: prop({ text: '' }) }), undefined).button, 'RESOLVE')
})

test('a replied thread is resolve-only with the box read-only', () => {
  const s = panelState(th({ proposal: prop({ state: 'replied', text: 'Posted text.' }) }), 'an edit that must not show')
  assert.deepEqual(s, { text: 'Posted text.', button: 'RESOLVE ONLY (reply posted)', readOnly: true, failed: null })
})

test('a replied thread whose resolve failed shows that error and still offers the resolve alone', () => {
  const s = panelState(th({ proposal: prop({ state: 'replied', text: 'Posted.', error: 'the reply is posted, but the thread was not resolved: boom' }) }), undefined)
  assert.equal(s.button, 'RESOLVE ONLY (reply posted)')
  assert.equal(s.failed, 'the reply is posted, but the thread was not resolved: boom')
})

test('a failed thread shows its error and keeps the text and the normal button', () => {
  const s = panelState(th({ proposal: prop({ state: 'failed', error: 'GitHub said no' }) }), undefined)
  assert.deepEqual(s, { text: 'Fixed in the last commit.', button: 'POST AND RESOLVE', readOnly: false, failed: 'GitHub said no' })
  // an edit made after the failure is kept, and the error stays on show
  const edited = panelState(th({ proposal: prop({ state: 'failed', error: 'GitHub said no' }) }), 'Second try.')
  assert.deepEqual([edited.text, edited.failed, edited.readOnly], ['Second try.', 'GitHub said no', false])
})

test('a posted thread offers no button: it waits for GitHub to show it resolved', () => {
  const s = panelState(th({ proposal: prop({ state: 'posted' }) }), undefined)
  assert.deepEqual(s, { text: 'Fixed in the last commit.', button: null, readOnly: true, failed: null })
  assert.equal(canPress(s, false), false)
})

test('an edit survives a snapshot that does not change the thread', () => {
  const t = th({ proposal: prop() })
  const draft = { base: t.proposal!.text, text: 'My own wording.' }
  // the next snapshot carries an equal thread, a new object
  const next = th({ proposal: prop() })
  assert.equal(panelState(next, liveDraft(next, draft)).text, 'My own wording.')
  // the same for a thread that never had a proposal
  const bare = { base: '', text: 'Typed.' }
  assert.equal(panelState(th(), liveDraft(th(), bare)).text, 'Typed.')
})

test('a newer proposal never replaces what Jane typed: the box keeps it and the newer text is offered beside it', () => {
  const draft = { base: 'Fixed in the last commit.', text: 'My own wording.' }
  const newer = th({ proposal: prop({ text: 'A newer proposal.' }) })
  assert.equal(panelState(newer, liveDraft(newer, draft)).text, 'My own wording.')
  assert.equal(newerProposal(newer, draft), 'A newer proposal.')
  // USE IT drops the draft: the box then holds the newer proposal
  assert.equal(panelState(newer, liveDraft(newer, undefined)).text, 'A newer proposal.')
  // an emptied box stays empty, under the same proposal or a newer one
  assert.equal(liveDraft(th({ proposal: prop() }), { base: 'Fixed in the last commit.', text: '' }), '')
  assert.equal(panelState(newer, liveDraft(newer, { base: 'Fixed in the last commit.', text: '' })).text, '')
  assert.equal(liveDraft(th(), undefined), undefined)
})

test('a newer proposal is offered only when there is something new to use', () => {
  const draft = { base: 'Fixed in the last commit.', text: 'My own wording.' }
  // no draft: the box shows the proposal itself
  assert.equal(newerProposal(th({ proposal: prop({ text: 'A newer proposal.' }) }), undefined), null)
  // the proposal the typing began on
  assert.equal(newerProposal(th({ proposal: prop() }), draft), null)
  // a newer proposal that says what the box already says
  assert.equal(newerProposal(th({ proposal: prop({ text: 'My own wording.' }) }), draft), null)
  // a reply already on GitHub is read-only: nothing to swap in
  for (const state of ['replied', 'posted'] as const) assert.equal(newerProposal(th({ proposal: prop({ text: 'A newer proposal.', state }) }), draft), null)
  // a proposal that went away (the thread has none now) offers nothing
  assert.equal(newerProposal(th(), draft), null)
})

test('a failed click keeps its text when a newer round proposes another: the pressed text is a draft like a typed one', () => {
  // the click sent the stored proposal unedited, and it failed; the panel keeps what it sent as the box's draft
  const sentText = { base: 'Fixed in the last commit.', text: 'Fixed in the last commit.' }
  const replaced = th({ proposal: prop({ text: 'A newer proposal.' }) })
  assert.equal(panelState(replaced, liveDraft(replaced, sentText)).text, 'Fixed in the last commit.')
  assert.equal(newerProposal(replaced, sentText), 'A newer proposal.')
})


test('the box is limited to 2000 characters: over it the button is disabled, at it not', () => {
  assert.equal(REPLY_LIMIT, 2000)
  assert.equal(overLimit('x'.repeat(2000)), false)
  assert.equal(overLimit('x'.repeat(2001)), true)
  const at = panelState(th(), 'x'.repeat(2000))
  const over = panelState(th(), 'x'.repeat(2001))
  assert.equal(canPress(at, false), true)
  assert.equal(canPress(over, false), false)
  // the label is still the honest one
  assert.equal(over.button, 'POST AND RESOLVE')
})

test('a click is not possible while its request is in flight', () => {
  assert.equal(canPress(panelState(th(), 'Done.'), true), false)
  assert.equal(canPress(panelState(th(), 'Done.'), false), true)
  assert.equal(canPress(panelState(th(), ''), false), true)
  assert.equal(canPress(panelState(th({ proposal: prop({ state: 'replied' }) }), undefined), false), true)
})

test('threadLabel is the author and path:line, and copes with every missing part', () => {
  assert.equal(threadLabel(th()), 'sam · a.ts:12')
  assert.equal(threadLabel(th({ line: null })), 'sam · a.ts')
  assert.equal(threadLabel(th({ path: null, line: null })), 'sam · on the PR')
  assert.equal(threadLabel(th({ path: null, line: 4 })), 'sam · on the PR')
  assert.equal(threadLabel(th({ by: '' })), 'unknown author · a.ts:12')
})

test('ignoredNote says how many of the session\'s proposed replies were ignored, in one sentence, and nothing for none', () => {
  assert.equal(ignoredNote(0), null)
  assert.equal(ignoredNote(undefined), null)
  assert.equal(ignoredNote(1), "1 proposed reply was ignored: the session's replies file named a thread that is not an unresolved thread of this PR, or the entry was a repeat, not text, over 2,000 characters, or past the 50 conn takes from one file.")
  assert.match(ignoredNote(3) ?? '', /^3 proposed replies were ignored: /)
})

test('nextFocus is the next thread whose box can take focus, then one before it, else none', () => {
  const ids = ['a', 'b', 'c', 'd']
  const all = () => true
  assert.equal(nextFocus(ids, 'b', all), 'c')
  assert.equal(nextFocus(ids, 'd', all), 'a')
  // a skipped, held or posted thread is passed over
  assert.equal(nextFocus(ids, 'a', id => id !== 'b' && id !== 'c'), 'd')
  assert.equal(nextFocus(ids, 'c', id => id === 'a'), 'a')
  // the thread itself never: its box was just disabled or folded away
  assert.equal(nextFocus(ids, 'b', id => id === 'b'), null)
  assert.equal(nextFocus(['a'], 'a', all), null)
  // a thread no longer listed starts from the top
  assert.equal(nextFocus(ids, 'gone', id => id !== 'a'), 'b')
})
