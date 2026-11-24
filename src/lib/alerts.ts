import { plainText } from './ask.ts'
import { agentName } from './format.ts'
import { needsMe, readyCount } from './myPrsBadge.ts'
import { href } from './routes.ts'
import type { Ask, MyPrsView, ReviewRow, ReviewsView, SessionDetail, Snapshot } from './types.ts'

export interface Alert {
  /** A later alert with the same tag replaces an earlier one still on screen. */
  tag: string
  title: string
  body: string
  /** Hash route to open when the alert is clicked */
  href: string
}

/** One session's waiting spell, as far as alerts go; gone when the spell ends. */
export interface Spell {
  /** The id of the last ask seen in the spell ('' before the first); a gap in the ask does not forget it */
  id: string
  /** What the last prompt recorded in the spell says (title and text; '' while there is only screen text), so the same words coming back do not alert again */
  said: string
  /** The spell was already on when the tab opened, with no prompt read yet: its first prompt is recorded, not alerted */
  quiet: boolean
}

/** What the last snapshot looked like, so only changes raise an alert. */
export interface Seen {
  seeded: boolean
  sessions: Map<string, string>
  asks: Map<string, Spell>
  runs: Map<string, string>
  /** Open PR outcome per run (one repository) or per run and repository (several), so a PR going red, green or merged alerts once */
  checks: Map<string, string>
  /** A loaded My PRs view has been seen: the first one only seeds `myprs`, and it need not come with the first snapshot */
  myprsSeeded: boolean
  /** Every My PRs alert tag raised or seeded this page lifetime, so one is raised once and never again */
  myprs: Set<string>
  /** A Reviews view with a finished poll has been seen: the first one only seeds `ready` */
  reviewsSeeded: boolean
  /** `v<key>:<launchAt>` of every review draft seen ready, so one draft alerts once; kept past the row leaving the list, bounded by `MAX_SEEN_READY` */
  ready: Set<string>
  /** `m<repo>#<number>:<launchAt>` of every round summary seen, so one summary alerts once; kept past the row leaving the list, bounded by `MAX_SEEN_ROUNDS` */
  rounds: Set<string>
}

export const newSeen = (): Seen => ({ seeded: false, sessions: new Map(), asks: new Map(), runs: new Map(), checks: new Map(), myprsSeeded: false, myprs: new Set(), reviewsSeeded: false, ready: new Set(), rounds: new Set() })

/**
 * Something that came off a terminal screen or a name, as one line of plain text for a notification: control characters
 * and newlines flattened, invisible and bidi characters escaped, then cut to `max` (after the escaping, and never inside an escape).
 */
export function oneLine(s: string, max: number): string {
  const t = plainText(s.replace(/[\u0000-\u001F\u007F-\u009F]+/g, ' ')).replace(/\s+/g, ' ').trim()
  if (t.length <= max) return t
  // Not in the middle of a surrogate pair or of an escape such as `<U+202E>` or `<U+E0041>` (4 to 6 hex digits, then `>`)
  const cut = t.slice(0, max).replace(/[\uD800-\uDBFF]$/, '').replace(/<(U(\+[0-9A-F]{0,6})?)?$/, '')
  return cut.trimEnd() + '…'
}

const TITLE_MAX = 80
const BODY_MAX = 120
/** A PR title in the body of a failed-draft alert, leaving the rest of the line for the reason */
const FAILED_TITLE_MAX = 50
/** How many review drafts and round summaries are remembered as seen; past it the oldest are forgotten, which can only repeat a very old alert */
export const MAX_SEEN_READY = 500
export const MAX_SEEN_ROUNDS = 500
/** What a round summary says when its first line is empty */
const NO_SUMMARY_LINE = 'summary written'
/** A finding at this severity or worse holds a review back from an approval */
const BLOCKER_SEVERITIES = ['P0', 'P1']
const PREFIX: Record<Ask['kind'], string> = { permission: 'Permission needed: ', question: 'Question: ', text: 'Waiting for your message: ', unknown: 'Needs input: ' }

/** What a prompt says, to tell the same prompt coming back (under another id or kind) from a new one. */
const words = (a: Ask): string => `${a.title}\n${a.text}`
/** The screen's own text, read as `unknown` with no title: often a passing frame. A dialog downgraded to `unknown` keeps its title. */
const untitled = (a: Ask): boolean => a.kind === 'unknown' && !a.title

/** What the session is asking, for the popup: states it, never offers to answer it (that is done on the page). */
const askBody = (a: Ask): string => {
  // A question's own words are its title (the parser leaves its text empty). A shared terminal or a misnamed wait
  // downgrades a dialog to `unknown` keeping its title and text (a permission's title is `Bash command`, its command the text): both.
  const said = a.kind === 'question' ? a.title || a.text : a.kind === 'unknown' ? [a.title, a.text].filter(Boolean).join(': ') : a.text
  // A body with nothing after the prefix says nothing: fall back to the plain reason
  if (!oneLine(said, BODY_MAX)) return a.kind === 'text' ? 'Waiting for your message' : 'Needs input'
  return PREFIX[a.kind] + oneLine(said, BODY_MAX)
}

/** Where an alert for this session leads, and the agent it names: a teammate's page sits under its lead's session. */
function target(s: SessionDetail, sessions: SessionDetail[]): { title: string; href: string } {
  const own = { title: oneLine(s.name, TITLE_MAX), href: href.agent(s.id, 'lead') }
  if (!s.team?.leadId) return own
  const lead = sessions.find(l => l.id === s.team!.leadId)
  const agent = lead?.agents.find(a => a.id === s.sessionId)
  return lead && agent ? { title: `${own.title} · ${oneLine(agentName(agent), TITLE_MAX)}`, href: href.agent(lead.id, agent.id) } : own
}

/**
 * A PR that needs me: its draft is saved (`Ready to post`) or failed for good (`Draft needs you`, the text is copied by hand). The tag names
 * the head and the state, so a later head alerts again, one head alerts once, and a draft that fails and later saves tells both.
 */
function myPrsAlerts(seen: Seen, view: MyPrsView | undefined): Alert[] {
  // Before the server's first poll succeeds the view has no rows because none were read: it must not seed
  if (!view?.loaded) return []
  const first = !seen.myprsSeeded
  const out: Alert[] = []
  for (const r of view.rows) {
    const d = r.slackDraft
    if (!d) continue
    const tag = `myprs:${r.repo}#${r.number}@${d.sha}:${d.state}`
    // At load a saved draft is seeded whatever the row says (a round at work on its head can hand it back to ready, and that is not
    // news), and so is a failed one whose tries are used up (it can never fail again, and a round at work hides it as well). A failed one
    // still being retried is not: its tag is the same for every try, so seeding it would silence the alert when its last try fails.
    if (first && (d.state === 'saved' || (d.state === 'failed' && d.tries >= view.draftMaxTries))) {
      seen.myprs.add(tag)
      continue
    }
    if (!needsMe(r, view.draftMaxTries)) continue
    if (seen.myprs.has(tag)) continue
    seen.myprs.add(tag)
    if (first) continue
    if (d.state === 'failed') {
      // The title gets a share of the line so a long one cannot push the reason out
      const title = oneLine(r.title, FAILED_TITLE_MAX)
      const why = oneLine(d.error ?? '', BODY_MAX - title.length - 2) || 'the draft failed'
      out.push({ tag, title: 'Draft needs you', body: title ? `${title}: ${why}` : why, href: href.myprs() })
    } else out.push({ tag, title: 'Ready to post', body: oneLine(r.title, BODY_MAX), href: href.myprs() })
  }
  seen.myprsSeeded = true
  return out
}

/** `3 comments`, `1 comment`, `no comments`: the count the person is about to read through. */
const countOf = (n: number, noun: string): string => (n === 0 ? `no ${noun}s` : `${n} ${noun}${n === 1 ? '' : 's'}`)

/** What a finished review holds, for the popup: `approve, 3 comments` or `comment, 1 blocker, 3 comments`. */
function readyBody(row: ReviewRow): string {
  const draft = row.draft!
  const blockers = draft.findings.filter(f => BLOCKER_SEVERITIES.includes(f.severity)).length
  const parts = [draft.verdict, blockers > 0 ? countOf(blockers, 'blocker') : '', countOf(draft.findings.length, 'comment')]
  return parts.filter(Boolean).join(', ')
}

/** Remember a tag, forgetting the oldest ones past `max` (a Set keeps its insertion order). Returns whether the tag was new. */
function remember(set: Set<string>, tag: string, max: number): boolean {
  if (set.has(tag)) return false
  set.add(tag)
  for (const oldest of set) {
    if (set.size <= max) break
    set.delete(oldest)
  }
  return true
}

/**
 * Reviews rows that entered REVIEW READY. The tag names the row and its draft's launch, so one draft alerts once, a newer launch alerts
 * again, and a row that drops off the list for a while (a Slack scan failing hides the rows only it found) and returns stays quiet.
 * The tags are kept, not derived from the rows now listed, and bounded by `MAX_SEEN_READY`.
 */
function reviewReadyAlerts(seen: Seen, view: ReviewsView | undefined): Alert[] {
  // Before the first poll the view has no rows because none were read: it must not seed
  if (!view || view.lastPollAt === null) return []
  const first = !seen.reviewsSeeded
  const out: Alert[] = []
  for (const r of view.rows) {
    if (!r.draft || r.state !== 'ready') continue
    const tag = `v${r.key}:${r.draft.launchAt}`
    const isNew = remember(seen.ready, tag, MAX_SEEN_READY)
    if (!isNew || first) continue
    out.push({ tag, title: oneLine(`${r.repo}#${r.number} review ready`, TITLE_MAX), body: readyBody(r), href: href.reviews(r.key) })
  }
  seen.reviewsSeeded = true
  return out
}

/** The first line of a round's summary with something on it, as one line for the popup. */
function summaryLine(text: string): string {
  const line = text.split('\n').find(l => oneLine(l, BODY_MAX) !== '') ?? ''
  return oneLine(line, BODY_MAX) || NO_SUMMARY_LINE
}

/**
 * My PRs rows whose self-review round has written its summary. `roundSummary` is the newest round that has one, so a round starting
 * changes nothing and its summary arriving does. Call before `myPrsAlerts`, which marks the view as seeded.
 */
function roundAlerts(seen: Seen, view: MyPrsView | undefined): Alert[] {
  if (!view?.loaded) return []
  const first = !seen.myprsSeeded
  const out: Alert[] = []
  for (const r of view.rows) {
    if (!r.roundSummary) continue
    const tag = `m${r.repo}#${r.number}:${r.roundSummary.launchAt}`
    const isNew = remember(seen.rounds, tag, MAX_SEEN_ROUNDS)
    if (!isNew || first) continue
    const title = oneLine(`${r.repo}#${r.number} self-review round ${r.roundSummary.round} done`, TITLE_MAX)
    out.push({ tag, title, body: summaryLine(r.roundSummary.text), href: href.myprs(`${r.repo}#${r.number}`) })
  }
  return out
}

/**
 * Compare a snapshot with the last one. The first snapshot only seeds the state: opening the tab with
 * five idle sessions must not raise five alerts, nor a session already waiting when its screen is read.
 */
export function diffAlerts(seen: Seen, snap: Snapshot): Alert[] {
  const out: Alert[] = []
  const first = !seen.seeded
  const nextSessions = new Map<string, string>()
  const nextAsks = new Map<string, Spell>()
  for (const s of snap.sessions) {
    nextSessions.set(s.id, s.status)
    // `ask` can be missing on an old row
    const ask = s.ask ?? null
    const was = seen.sessions.get(s.id)
    if (s.status === 'waiting') {
      const prev = was === 'waiting' ? seen.asks.get(s.id) : undefined
      const alert = (body: string) => {
        const { title, href: to } = target(s, snap.sessions)
        out.push({ tag: `s${s.id}`, title: `${title} needs input`, body, href: to })
      }
      if (first || !prev) {
        // A new spell: the status flip alerts (not on the first snapshot), with the ask when it is already read
        if (!first) alert(ask ? askBody(ask) : oneLine(s.waitingFor ?? 'waiting on you', BODY_MAX))
        const prompt = !!ask && !untitled(ask)
        nextAsks.set(s.id, { id: ask?.id ?? '', said: prompt ? words(ask) : '', quiet: first && !prompt })
        continue
      }
      const spell = { ...prev }
      if (ask && ask.id !== spell.id) {
        // The screen is read after the status flips: the question, or a new one, replaces the first alert on screen. Not:
        // screen text (untitled `unknown`), (a titled `unknown` is a real dialog downgraded by the server, so it alerts when its words are new);
        // the same words back under another id (a shared terminal's downgrade coming and going); the first prompt of a
        // spell older than the tab.
        const noise = ask.kind === 'unknown' && !ask.title
        if (!noise) {
          if (!spell.quiet && words(ask) !== spell.said) alert(askBody(ask))
          spell.said = words(ask)
          spell.quiet = false
        }
        spell.id = ask.id
      }
      nextAsks.set(s.id, spell)
      continue
    }
    if (first) continue
    if (s.status === 'idle' && (was === 'busy' || was === 'waiting'))
      out.push({ tag: `s${s.id}`, title: `${oneLine(s.name, TITLE_MAX)} finished`, body: s.activity || 'idle', href: `#/s/${s.id}` })
  }
  const nextRuns = new Map<string, string>()
  const nextChecks = new Map<string, string>()
  for (const r of snap.runs) {
    nextRuns.set(r.key, r.state)
    const was = seen.runs.get(r.key)
    if (!first && was !== r.state) {
      if (r.state === 'asking') out.push({ tag: `r${r.key}`, title: `${r.key} has questions`, body: `${r.questions.filter(q => q.answer === null).length} for you before the build starts`, href: `#/work/${r.key}` })
      else if (r.state === 'failed') out.push({ tag: `r${r.key}`, title: `${r.key} failed`, body: r.error ?? 'the run stopped', href: `#/work/${r.key}` })
    }
    // Each leg's PR is followed on its own. One leg keeps the key and words it always had; with several, the key and title
    // name the repository too (a key has no space, so "KEY repo" never meets a one-leg key), so two legs never share an alert.
    for (const leg of r.repos) {
      const pr = leg.pr?.status
      if (!pr) continue
      const id = r.repos.length > 1 ? `${r.key} ${leg.repo.toLowerCase()}` : r.key
      const outcome = pr.state === 'merged' || pr.state === 'closed' ? pr.state : pr.checks.state === 'failing' || pr.checks.state === 'passing' ? pr.checks.state : ''
      nextChecks.set(id, outcome)
      if (!first && outcome && seen.checks.get(id) !== outcome) {
        const title = { merged: `${id} PR merged`, closed: `${id} PR closed`, failing: `${id} checks failed`, passing: `${id} checks passed` }[outcome]!
        out.push({ tag: `p${id}`, title, body: outcome === 'failing' ? pr.checks.failed.join(', ') || `PR #${leg.pr!.number}` : `PR #${leg.pr!.number}`, href: `#/work/${r.key}` })
      }
    }
  }
  seen.seeded = true
  seen.sessions = nextSessions
  seen.asks = nextAsks
  seen.runs = nextRuns
  seen.checks = nextChecks
  out.push(...reviewReadyAlerts(seen, snap.reviews))
  out.push(...roundAlerts(seen, snap.myPrs))
  out.push(...myPrsAlerts(seen, snap.myPrs))
  return out
}

/** Things holding up the person: sessions stopped on a prompt, runs with open questions, and My PRs rows that need them (the tab badge's count). */
export const attention = (snap: Snapshot): number =>
  snap.sessions.filter(s => s.status === 'waiting').length + snap.runs.filter(r => r.state === 'asking').length + readyCount(snap.myPrs)
