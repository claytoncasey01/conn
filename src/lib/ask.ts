// What the agent page shows for an agent that has stopped for the person. Pure, so it runs under bun test.
import { agentName, dur } from './format.ts'
import { isTeammateAgent } from './team.ts'
import type { Agent, Ask, AskKind, AskOption, SessionDetail } from './types.ts'

export interface AskOwner {
  /** The session whose terminal holds the question */
  session: SessionDetail
  /** A subagent's page: it has no terminal, so the answer goes on another agent's page */
  viaLead: boolean
  /** With viaLead, that agent on this session's page: 'lead', or the id of the teammate the subagent works under */
  via?: string
}

/** The live session of a teammate agent of `lead`: the one whose uuid is the agent id and that names `lead` as its lead. */
const mateSession = (lead: SessionDetail, agentId: string, sessions: SessionDetail[]) => sessions.find(s => s.sessionId === agentId && s.team?.leadId === lead.id)

/** The nearest teammate up an agent's spawn line (`parentId`), or null when the line goes back to the lead. */
function teammateAbove(session: SessionDetail, agent: Agent): Agent | null {
  const seen = new Set<string>()
  for (let p = agent.parentId; p && !seen.has(p); ) {
    seen.add(p)
    const up = session.agents.find(a => a.id === p)
    if (!up) return null
    if (isTeammateAgent(up)) return up
    p = up.parentId
  }
  return null
}

/**
 * The session whose terminal holds this agent's question. The lead is answered in the session itself. A teammate is
 * answered in its own live session: the agent's id is its transcript's session uuid, which that session carries as
 * `sessionId`, and the session must name this one as its lead. A subagent has no terminal: it is answered through the
 * teammate it works under when that teammate has a live session (only while that session waits), else through the lead
 * (only while the lead waits), and only while it is not done (a subagent blocked on a prompt turns stalled). Anything
 * else has no owner.
 */
export function askOwner(session: SessionDetail, agentId: string, sessions: SessionDetail[]): AskOwner | null {
  if (agentId === 'lead') return { session, viaLead: false }
  const agent = session.agents.find(a => a.id === agentId)
  if (!agent) return null
  if (isTeammateAgent(agent)) {
    const own = mateSession(session, agentId, sessions)
    return own ? { session: own, viaLead: false } : null
  }
  if (agent.status === 'done') return null
  const mate = teammateAbove(session, agent)
  const mates = mate ? mateSession(session, mate.id, sessions) : undefined
  // A teammate's own prompt is in its terminal: a lead that waits on something else is not this agent's question
  if (mate && mates) return mates.status === 'waiting' ? { session: mates, viaLead: true, via: mate.id } : null
  return session.status === 'waiting' ? { session, viaLead: true, via: 'lead' } : null
}

/**
 * The ids of the agents waiting on the person: the lead while its session waits, and a teammate while its own session
 * waits. A subagent is never marked: it may well be running while its lead waits on a prompt of its own, so only its
 * own page says it may be waiting through the lead.
 */
export function waitingAgents(session: SessionDetail, sessions: SessionDetail[]): Set<string> {
  const out = new Set<string>()
  for (const id of ['lead', ...session.agents.map(a => a.id)]) {
    const o = askOwner(session, id, sessions)
    if (o && !o.viaLead && o.session.status === 'waiting') out.add(id)
  }
  return out
}

/** How a subagent's page names the agent whose page has the answer: the lead, or the teammate it works under. */
export const viaName = (session: SessionDetail, via: string): string => {
  const mate = via === 'lead' ? undefined : session.agents.find(a => a.id === via)
  return mate ? `teammate ${agentName(mate)}` : 'the lead'
}

export const waitedFor = (ask: Ask, now: number): string => dur(now - ask.since)

/** The only keys the server will press. */
const KEY = /^[1-9]$/
const MAX_PICKS = 7

export type PanelKind = 'none' | 'lead' | 'nocmux' | 'pending' | 'permission' | 'question' | 'unknown' | 'text'

/**
 * Which layout the panel takes. Only a permission with exactly one allow and one deny gets the two buttons, and only a
 * question of at most 7 options, all plain picks with distinct keys, gets one button each; every key must be one digit 1-9; anything else is `unknown`: the
 * text and the terminal.
 */
export function panelView(s: Pick<SessionDetail, 'status' | 'canReply' | 'ask'>, viaLead: boolean, ask: Ask | null = s.ask ?? null): { view: PanelKind; allow?: AskOption; deny?: AskOption } {
  if (!ask && s.status !== 'waiting') return { view: 'none' }
  if (viaLead) return { view: 'lead' }
  if (!s.canReply) return { view: 'nocmux' }
  if (!ask) return { view: 'pending' }
  const o = ask.options
  // The server presses one digit, 1 to 9, and a list of more than 7 options is answered in the terminal
  if (!o.every(x => KEY.test(x.key))) return { view: 'unknown' }
  if (ask.kind === 'permission') {
    const allow = o.filter(x => x.role === 'allow')
    const deny = o.filter(x => x.role === 'deny')
    return o.length === 2 && allow.length === 1 && deny.length === 1 ? { view: 'permission', allow: allow[0], deny: deny[0] } : { view: 'unknown' }
  }
  if (ask.kind === 'question') {
    const ok = o.length > 0 && o.length <= MAX_PICKS && o.every(x => x.role === 'pick') && new Set(o.map(x => x.key)).size === o.length
    return { view: ok ? 'question' : 'unknown' }
  }
  return { view: ask.kind === 'text' ? 'text' : 'unknown' }
}

/** The request body: the id of the prompt that was shown and the option key, as a string. (Text goes through the reply box, never here.) */
export const answerBody = (askId: string, choice: string) => ({ askId, choice })

const KINDS: AskKind[] = ['question', 'permission', 'text', 'unknown']
const ROLES = ['pick', 'allow', 'deny']

/** An ask from a JSON response body (a 409's `ask`), or null when it is not shaped like one. Not the server's screen parser. */
export function askFromJson(v: unknown): Ask | null {
  const a = v as Partial<Ask> | null
  if (!a || typeof a !== 'object' || !KINDS.includes(a.kind as AskKind)) return null
  if (typeof a.id !== 'string' || typeof a.title !== 'string' || typeof a.text !== 'string' || typeof a.since !== 'number' || !Array.isArray(a.options)) return null
  const okOpt = (o: unknown) => !!o && typeof (o as AskOption).key === 'string' && typeof (o as AskOption).label === 'string' && ROLES.includes((o as AskOption).role)
  if (!a.options.every(okOpt)) return null
  return { kind: a.kind as AskKind, id: a.id, title: a.title, text: a.text, options: a.options.map(o => ({ key: o.key, label: o.label, role: o.role })), since: a.since }
}

/**
 * `unsure`: the key may or may not have been pressed (a 5xx, a request that failed on the way, a 2xx that did not say
 * `{ok: true}`), so the prompt must not be offered again. A 4xx other than 409 is a refusal before anything was pressed.
 */
export type AnswerResult = { ok: true } | { ok: false; conflict: true; ask: Ask | null; error: string } | { ok: false; conflict?: undefined; unsure?: boolean; error: string }

const errorOf = (j: { error?: unknown }, status: number) => (typeof j.error === 'string' && j.error ? j.error : `HTTP ${status}`)
const bodyOf = (body: unknown) => (body && typeof body === 'object' ? body : {}) as { ok?: unknown; error?: unknown; ask?: unknown }

/** 200 with `{ok: true}` is sent; 409 means the prompt changed (the body's `ask` is what the screen shows now); anything else is an error to show. */
export function parseAnswer(status: number, body: unknown): AnswerResult {
  const j = bodyOf(body)
  if (status >= 200 && status < 300) return j.ok === true ? { ok: true } : { ok: false, unsure: true, error: `HTTP ${status} without a confirmation` }
  if (status === 409) return { ok: false, conflict: true, ask: askFromJson(j.ask), error: errorOf(j, 409) }
  return status >= 500 ? { ok: false, unsure: true, error: errorOf(j, status) } : { ok: false, error: errorOf(j, status) }
}

/** A POST that answers `{ok: true}`, such as OPEN TERMINAL: anything else is an error, always as a string. */
export function parseOk(status: number, body: unknown): { ok: true } | { ok: false; error: string } {
  const j = bodyOf(body)
  return status >= 200 && status < 300 && j.ok === true ? { ok: true } : { ok: false, error: errorOf(j, status) }
}

// ---- Showing an ask from the terminal ----

// Bidi controls (they reorder what is read), zero-width and other invisible characters, line and paragraph separators,
// tag characters, and C0/C1 controls other than newline and tab
const HIDDEN = /[\u0000-\u0008\u000B-\u001F\u007F-\u009F\u00AD\u061C\u180E\u200B-\u200F\u2028-\u202E\u2060-\u2064\u2066-\u2069\uFEFF\uFFF9-\uFFFB]|[\u{E0000}-\u{E007F}]/gu

/** Text from the screen as it will be drawn: a character that is invisible, or reorders or hides what is read, shows as `<U+202E>`. */
export const plainText = (s: string): string => s.replace(HIDDEN, c => `<U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}>`)

// ---- The panel's state, kept pure so it can be tested ----

/** The answer buttons stay disabled this long after the prompt shown changes. */
export const ARM_MS = 700
/** After a 200, a prompt still on screen this long is offered the terminal. */
export const STUCK_MS = 8000
export const CHANGED = 'the prompt changed — check it again'
export const UNSURE = 'the answer may or may not have arrived: look at the terminal'

export interface PanelState {
  /** The id of the live snapshot's ask, '' for none, as last seen */
  liveId: string
  /** The id of the ask on show (the live one, or an override), '' for none, and when it first showed */
  shownId: string
  shownAt: number
  /** The ask a 409 said the screen has, shown until the next snapshot (`overrideAt` is the `at` of the one it came with) */
  override: Ask | null
  overrideAt: number
  /** The last live ask of this waiting spell, kept through a gap in the read; null until the screen has been read */
  last: Ask | null
  notice: string
  error: string
  opened: boolean
  /** The prompt last answered, and when: `sure` after a 200, not after a 5xx or a lost request (the key may have been pressed) */
  sent: { id: string; at: number; sure: boolean } | null
}

export const newPanel = (now: number): PanelState => ({ liveId: '', shownId: '', shownAt: now, override: null, overrideAt: 0, last: null, notice: '', error: '', opened: false, sent: null })

/** The same object back when nothing in the patch changes anything. */
const patch = (s: PanelState, p: Partial<PanelState>): PanelState => ((Object.keys(p) as (keyof PanelState)[]).every(k => s[k] === p[k]) ? s : { ...s, ...p })

/** The ask on show: an override while there is still a prompt, else the live one. `live` may be undefined on an old row. */
export const shownAsk = (st: PanelState, live: Ask | null | undefined, waiting: boolean): Ask | null => (st.override && (waiting || live) ? st.override : (live ?? null))

/**
 * Bring the state in line with the live snapshot. Call it whenever the live ask, the waiting flag or the snapshot
 * (`snapAt`, its `at`) changes: a 409's override lasts only until the next snapshot, which says what the screen shows now.
 */
export function sync(st: PanelState, live: Ask | null | undefined, waiting: boolean, now: number, snapAt: number): PanelState {
  let s = st
  const liveId = live?.id ?? ''
  if (liveId !== s.liveId) {
    // Live catching up with what a 409 showed keeps the notice; any other new prompt starts clean
    const caughtUp = !!s.override && s.override.id === liveId
    s = patch(s, { liveId, override: null, ...(caughtUp ? {} : { notice: '', error: '', opened: false }) })
  } else if (s.override && snapAt !== s.overrideAt) {
    // A newer snapshot with the same live prompt: the screen went back (or never left), so that prompt is on show again
    s = patch(s, { override: null })
  }
  if (live && s.last?.id !== live.id) s = patch(s, { last: live })
  const ask = shownAsk(s, live, waiting)
  if (!ask && !waiting) s = patch(s, { override: null, last: null, notice: '', error: '', opened: false })
  const id = ask?.id ?? ''
  if (id !== s.shownId) s = patch(s, { shownId: id, shownAt: now })
  // The ask drops after a 200 (and can for a moment otherwise) and the same prompt come back: only a different prompt, or nothing to show, ends the lock
  if (s.sent && ((id && id !== s.sent.id) || (!ask && !waiting))) s = patch(s, { sent: null })
  return s
}

/** The id the answer buttons are armed for, or '' while they are not: the shown prompt, once it has been shown for ARM_MS. */
export const armedId = (st: PanelState, now: number): string => (st.shownId && now - st.shownAt >= ARM_MS ? st.shownId : '')

/** Locked on the prompt shown since it was answered, and while the session still waits with no prompt read (the answer is on its way). */
export const isLocked = (st: PanelState, shownId: string): boolean => !!st.sent && (st.sent.id === shownId || !shownId)
/** Offer the terminal: right away when it is not sure the answer arrived, else once the prompt has stayed STUCK_MS. */
export const isStuck = (st: PanelState, now: number): boolean => !!st.sent && (!st.sent.sure || now - st.sent.at >= STUCK_MS)

/**
 * The prompt id a click may answer now, or '': the prompt on show, armed for it, with no request out and no answer
 * locked on it. The panel checks it again when the click lands, so a prompt that changed under the pointer sends nothing.
 */
export const pressable = (st: PanelState, shown: Ask | null, sending: boolean, now: number): string => {
  const id = armedId(st, now)
  return id && shown?.id === id && !sending && !isLocked(st, id) ? id : ''
}

/**
 * What a waiting panel with no prompt to show says: nothing while an answer is locked (its status says it), the reply
 * box kept through a gap in the read after a text prompt (a typed draft stays), else whether the screen was ever read.
 */
export function pendingBody(st: PanelState, locked: boolean): 'none' | 'reply' | 'unread' | 'reread' {
  if (locked) return 'none'
  if (st.last?.kind === 'text') return 'reply'
  return st.last ? 'reread' : 'unread'
}

/** The panel's status line, in a live region that is always there so a screen reader says each change once. */
export function statusLine(st: PanelState, sending: boolean, locked: boolean): string {
  if (sending) return 'SENDING…'
  const parts: string[] = []
  if (locked && st.sent) parts.push(st.sent.sure ? 'SENT ✓ · waiting for the screen to move on' : UNSURE)
  if (st.opened) parts.push('cmux workspace selected')
  return parts.join(' · ')
}

/** A new answer is going out: the old messages go. */
export const begin = (st: PanelState): PanelState => patch(st, { notice: '', error: '' })

/**
 * The result of answering prompt `clicked` while `shown` was on show, with snapshot `snapAt` live. A 409 is never
 * retried: it shows the server's own error when it has no ask or is about the same prompt, and otherwise says the prompt
 * changed and, if the clicked prompt is still the one on show, switches to the ask the server saw until the next snapshot
 * (a late answer cannot replace a prompt the page has already moved on to). A result that may have pressed the key locks
 * the prompt as a 200 does, and says it is not sure.
 */
export function onResult(st: PanelState, clicked: string, shown: Ask | null, r: AnswerResult, now: number, snapAt: number): PanelState {
  if (r.ok) return patch(st, { sent: { id: clicked, at: now, sure: true }, notice: '', error: '' })
  if (!r.conflict) return patch(st, { error: r.error, notice: '', ...(r.unsure ? { sent: { id: clicked, at: now, sure: false } } : {}) })
  // No ask (no cmux, a send in flight, not in the foreground...) or the same prompt: the server's own reason
  if (!r.ask || r.ask.id === clicked) return patch(st, { error: r.error, notice: '' })
  return patch(st, { notice: CHANGED, error: '', ...(shown?.id === clicked ? { override: r.ask, overrideAt: snapAt } : {}) })
}

// ---- A long permission command must be read to its end before ALLOW ONCE ----

export interface BoxMetrics { scrollTop: number; scrollHeight: number; clientHeight: number; lineHeight: number }

/** A rounding error in the scroll position is not a hidden line. */
const SLACK_PX = 2

/** How many lines of the box lie below what is in view: 0 at its end, or when it all fits. */
export function linesBelow(m: BoxMetrics): number {
  const px = m.scrollHeight - m.clientHeight - m.scrollTop
  return px <= SLACK_PX ? 0 : Math.ceil(px / Math.max(1, m.lineHeight))
}

/**
 * After measuring the command box of prompt `id`: the lines still below, and `readTo`, the id of the prompt whose box
 * has been seen to its end (it fitted, or was scrolled down). Once seen it stays read, also after scrolling back up.
 */
export function readGate(readTo: string, id: string, m: BoxMetrics): { more: number; readTo: string } {
  const more = linesBelow(m)
  return { more, readTo: more === 0 ? id : readTo }
}

/** ALLOW ONCE waits until the command of prompt `id` has been seen to its end; one with no command has nothing to read. */
export const mustScrollFirst = (readTo: string, ask: Pick<Ask, 'id' | 'text'>): boolean => !!ask.text && readTo !== ask.id
