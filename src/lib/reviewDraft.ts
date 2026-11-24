// What the draft panel on a Reviews row decides, as plain functions on the draft the server sent: which findings are ticked,
// whether Approve is open, and where a finding will land. The panel reads these and writes nothing of its own.
import type { ReviewDraft, ReviewFinding } from './types.ts'

export type Verdict = 'approve' | 'comment'

/** Why Approve is disabled: the title on the radio, and the words beside it */
export const APPROVE_BLOCKED = 'A ticked P0 or P1 is a blocker: post it as a comment'

const BLOCKERS = ['P0', 'P1']

/** Every finding's index: the state the panel opens in, and what Select all ticks */
export const allTicked = (d: ReviewDraft): number[] => d.findings.map((_, index) => index)

/** A ticked finding is a P0 or P1. An index that is not a finding of the draft counts for nothing. */
export const approveBlocked = (d: ReviewDraft, ticked: number[]): boolean => ticked.some((index) => BLOCKERS.includes(d.findings[index]?.severity ?? ''))

/** The verdict that would be posted: the choice, except that a blocker ticked makes Approve a comment */
export const verdictAfter = (d: ReviewDraft, ticked: number[], chosen: Verdict): Verdict => (chosen === 'approve' && approveBlocked(d, ticked) ? 'comment' : chosen)

/** Claude's verdict; a comment when the file gave none */
export const startingVerdict = (d: ReviewDraft): Verdict => (d.verdict === 'approve' ? 'approve' : 'comment')

/** Where a finding goes when it is not a comment on a line; '' when it is one */
export function placeNote(f: ReviewFinding): string {
  if (f.inline === true) return ''
  return f.inline === false ? 'outside the diff: goes in the summary' : 'line not checked: goes in the summary'
}

/** The first line with text, which stands for a body until it is opened */
export const firstLine = (body: string): string => body.split('\n').find((line) => line.trim() !== '')?.trim() ?? ''

/** `ticked` with `index` ticked or unticked, in index order */
export function tickedAfter(ticked: number[], index: number, on: boolean): number[] {
  const rest = ticked.filter((t) => t !== index)
  return (on ? [...rest, index] : rest).sort((a, b) => a - b)
}

/**
 * Names the review file a draft was read from, so the panel starts over (everything ticked again) when the file is written
 * again, and keeps the ticks across a snapshot that only moved the head, posted it or listed problems.
 */
export const draftId = (d: ReviewDraft): string => JSON.stringify([d.launchAt, d.commit, d.summary, d.verdict, d.findings, d.dropped])

/**
 * What the row shows for a draft. 'posted' has no controls (a marker keeps no details); 'unreadable' is a file that left
 * no draft, only problems; 'draft' is one to post, with any problems of a newer unreadable write listed beside it.
 */
export function draftKind(d: ReviewDraft): 'posted' | 'unreadable' | 'draft' {
  if (d.postedAt !== null) return 'posted'
  return d.commit === '' && d.findings.length === 0 && d.problems.length > 0 ? 'unreadable' : 'draft'
}
