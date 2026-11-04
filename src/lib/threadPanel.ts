// What the replies panel shows for one unresolved thread and what its button may do. Pure: the panel keeps only the box edits and which
// requests are in flight, so every rule is tested here without a browser.
import type { RowThread } from './types.ts'

/** The server refuses a reply over this many characters (after cleaning), so the box does not allow more */
export const REPLY_LIMIT = 2000

export type ThreadButton = 'POST AND RESOLVE' | 'RESOLVE' | 'RESOLVE ONLY (reply posted)'

export interface PanelState {
  text: string
  /** Null when there is nothing to press: the reply and resolve went through and GitHub has not shown the thread resolved yet */
  button: ThreadButton | null
  readOnly: boolean
  /** The server's last error for this thread, shown beside it */
  failed: string | null
}

/** What Casey typed into one thread's box (or the text a click sent), and the proposal text that was in it when the typing began */
export interface Draft {
  base: string
  text: string
}

/**
 * The edit to show: Casey's own wording (or the text a click sent) stands over every later snapshot, a newer round's proposal
 * included, which is offered beside the box instead (`newerProposal`). Undefined when there is none: the box shows the proposal.
 */
export function liveDraft(_t: RowThread, draft: Draft | undefined): string | undefined {
  return draft?.text
}

/**
 * The proposal that arrived after the draft was begun, to offer with USE IT; null when there is no draft, the proposal is the one the
 * typing began on or says what the box already says, or the reply is already on GitHub (the box is read-only then).
 */
export function newerProposal(t: RowThread, draft: Draft | undefined): string | null {
  const p = t.proposal
  if (!draft || !p || p.state === 'replied' || p.state === 'posted') return null
  return p.text !== draft.base && p.text !== draft.text ? p.text : null
}

export function overLimit(text: string): boolean {
  return text.length > REPLY_LIMIT
}

/** `draftText` is the box's local edit (see `liveDraft`), undefined when there is none. */
export function panelState(t: RowThread, draftText: string | undefined): PanelState {
  const p = t.proposal
  const failed = p?.error ?? null
  if (p?.state === 'replied') return { text: p.text, button: 'RESOLVE ONLY (reply posted)', readOnly: true, failed }
  if (p?.state === 'posted') return { text: p.text, button: null, readOnly: true, failed }
  const text = draftText ?? p?.text ?? ''
  return { text, button: text.trim() === '' ? 'RESOLVE' : 'POST AND RESOLVE', readOnly: false, failed }
}

/** Whether the thread's button can be pressed now: it exists, no request for the thread is out, and an editable box is within the limit. */
export function canPress(s: PanelState, busy: boolean): boolean {
  return s.button !== null && !busy && (s.readOnly || !overLimit(s.text))
}

/** The author and where the thread sits, tolerant of an author who is gone and of a thread on the PR as a whole */
export function threadLabel(t: RowThread): string {
  const where = t.path ? (t.line === null ? t.path : `${t.path}:${t.line}`) : 'on the PR'
  return `${t.by || 'unknown author'} · ${where}`
}

/** The row's line for the entries of the session's replies file that the server did not take (`MyPrRow.ignoredReplies`); null for none */
export function ignoredNote(n: number | undefined): string | null {
  if (!n || n <= 0) return null
  return `${n} proposed ${n === 1 ? 'reply was' : 'replies were'} ignored: the session's replies file named a thread that is not an unresolved thread of this PR, or the entry was a repeat, not text, over 2,000 characters, or past the 50 conn takes from one file.`
}

/**
 * Where focus goes once a thread's box is disabled or folded away (POST AND RESOLVE went through, or SKIP): the next thread in `ids`
 * whose box can take it, then the first one before it, never `from` itself; null when there is none (the panel heading takes it then).
 */
export function nextFocus(ids: readonly string[], from: string, usable: (id: string) => boolean): string | null {
  const i = ids.indexOf(from)
  const order = i === -1 ? ids : [...ids.slice(i + 1), ...ids.slice(0, i)]
  return order.find(id => id !== from && usable(id)) ?? null
}
