// The advisor a session Conn starts runs: Claude Code's own setting, or one model family. The server takes only these (internal/advisor,
// whose tests read the same testdata/advisor-choices.json), turns a choice into claude's arguments itself, and fills in Settings' choice when
// a start sends none. No browser globals.

export type AdvisorChoice = 'claude-code' | 'fable' | 'opus' | 'sonnet'

/** A fresh install's choice: Claude Code's own, so nothing changes until someone picks one. */
export const DEFAULT_ADVISOR: AdvisorChoice = 'claude-code'

/** The choices in the order a page lists them, each with its label. A family's alias runs its newest model, so no version is named. */
export const ADVISOR_CHOICES: readonly { value: AdvisorChoice; label: string }[] = [
  { value: 'claude-code', label: 'Claude Code’s setting' },
  { value: 'fable', label: 'Fable' },
  { value: 'opus', label: 'Opus' },
  { value: 'sonnet', label: 'Sonnet' },
]

const VALUES: readonly unknown[] = ADVISOR_CHOICES.map((c) => c.value)

/** v when it is a choice, else the default. */
export const advisorOf = (v: unknown): AdvisorChoice => (VALUES.includes(v) ? (v as AdvisorChoice) : DEFAULT_ADVISOR)

/** A choice's label; the default's for anything else. */
export const advisorLabel = (v: unknown): string => ADVISOR_CHOICES.find((c) => c.value === advisorOf(v))!.label

/** What a start picks for its one session: a choice, or '' for Settings' choice, which the server fills in (nothing is sent). */
export type AdvisorPick = AdvisorChoice | ''

/** What a start's picker offers: Settings' choice first, then each choice. */
export const ADVISOR_PICKS: readonly { value: AdvisorPick; label: string }[] = [{ value: '', label: 'As in Settings' }, ...ADVISOR_CHOICES]

/** body with the advisor a start picked; unchanged for Settings' choice, so an older server is sent nothing it does not read. */
export function withAdvisor<T extends object>(body: T, pick: AdvisorPick): T | (T & { advisor: AdvisorChoice }) {
  return pick === '' ? body : { ...body, advisor: pick }
}
