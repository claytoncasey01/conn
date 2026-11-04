// The Settings page's pure parts: the repo-pattern grammar (a mirror of the server's parsePattern, which stays the judge),
// the draft list, "N repos matched", the suggestion list and the clone-folder section a save sends. No browser globals.

/** The server refuses a longer list; the page says so before it asks. */
export const MAX_PATTERNS = 25
const MAX_FOLDER = 1024

/** The `repos` section as `settings.json` holds it. Keys of `clones` are lowercase `owner/repo`. */
export interface ReposSettings {
  allow: string[] | null
  clones: Record<string, string>
  cloneRoot: string | null
}

// GitHub name characters. Names are checked as typed, BEFORE they are lowercased: a lookalike (the Kelvin sign lowercases to a
// plain k) must not become a name by case folding. The owner starts with a letter or digit and the repo with a letter, digit, dot
// or underscore (`.github` is a repo), so a name is never a dash-led word a process would read as an option; a repo of only dots
// (`.`, `..`) is no name.
const OWNER = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/
const REPO_PART = /^[A-Za-z0-9_.][A-Za-z0-9_.-]*$/
const dots = (s: string) => /^\.+$/.test(s)
/** The server's REPO_NAME: owner/repo as above */
const REPO = /^(?!\.+\/)(?!.*\/\.+$)[A-Za-z0-9][A-Za-z0-9_.-]*\/[A-Za-z0-9_.][A-Za-z0-9_.-]*$/

export type PatternResult = { ok: true; pattern: string } | { ok: false; reason: string }

/** `owner/repo` or `owner/*`, trimmed and lowercased; `reason` is the first thing wrong with anything else. */
export function parsePattern(raw: string): PatternResult {
  const s = raw.trim()
  if (!s) return { ok: false, reason: 'Type owner/repo or owner/*' }
  if (/\s/.test(s)) return { ok: false, reason: 'A name has no spaces' }
  const parts = s.split('/')
  if (parts.length !== 2 || !parts[0] || !parts[1]) return { ok: false, reason: 'Use owner/repo or owner/*' }
  const [owner, repo] = parts
  if (owner === '*') return { ok: false, reason: 'A star stands for a whole repo name after an owner: owner/*' }
  if (owner.includes('*') || (repo !== '*' && repo.includes('*'))) return { ok: false, reason: 'A star can only be a whole repo name: owner/*' }
  if (!OWNER.test(owner)) return { ok: false, reason: 'The owner starts with a letter or digit and has letters, digits, dots, dashes and underscores only' }
  if (repo !== '*' && (!REPO_PART.test(repo) || dots(repo))) return { ok: false, reason: 'The repo name has letters, digits, dots, dashes and underscores only, and does not start with a dash' }
  return { ok: true, pattern: s.toLowerCase() }
}

/** A repo as GitHub names it: `owner/repo`, no star. */
export const repoName = (s: string): boolean => REPO.test(s)

/** Is `repo` inside the allow list: the same rule as the server's allowed() (case-insensitive, `owner/*` covers that owner only). */
export function covered(repo: string, allow: readonly string[] | null): boolean {
  if (!allow || !repoName(repo)) return false
  const r = repo.toLowerCase()
  const owner = r.slice(0, r.indexOf('/'))
  return allow.some((p) => {
    const q = p.toLowerCase()
    return q === r || q === `${owner}/*`
  })
}

export type AddResult = { ok: true; list: string[]; pattern: string } | { ok: false; reason: string }

/** The list with `raw` added, or why not. Never changes the list it is given. */
export function addPattern(list: readonly string[], raw: string): AddResult {
  const p = parsePattern(raw)
  if (!p.ok) return p
  if (list.includes(p.pattern)) return { ok: false, reason: `${p.pattern} is already in the list` }
  if (list.length >= MAX_PATTERNS) return { ok: false, reason: `At most ${MAX_PATTERNS} patterns: remove one first` }
  return { ok: true, list: [...list, p.pattern], pattern: p.pattern }
}

export const removePattern = (list: readonly string[], p: string): string[] => list.filter((x) => x !== p)

/** Why this list cannot be saved, or null. There is no "allow nothing" state. */
export function saveProblem(list: readonly string[]): string | null {
  if (list.length === 0) return 'Add at least one repository'
  if (list.length > MAX_PATTERNS) return `At most ${MAX_PATTERNS} patterns`
  return null
}

export type AddCheck = { state: 'empty' } | { state: 'ok'; pattern: string } | { state: 'error'; reason: string }

/** What the add box says while the person types: nothing when it is empty. */
export function addCheck(raw: string, list: readonly string[]): AddCheck {
  if (!raw.trim()) return { state: 'empty' }
  const r = addPattern(list, raw)
  return r.ok ? { state: 'ok', pattern: r.pattern } : { state: 'error', reason: r.reason }
}

export const matchedText = (n: number): string => `${n} ${n === 1 ? 'repo' : 'repos'} matched`

/** How many distinct seen repos one pattern covers now (no fetch: the repos the page already holds). */
export function matchedCount(pattern: string, repos: readonly string[]): number {
  return new Set(repos.filter((r) => covered(r, [pattern])).map((r) => r.toLowerCase())).size
}

/** Seen repos the list does not cover yet, once each, in name order (casing as it was seen, first sighting). */
export function suggestions(seen: readonly { repo: string }[], list: readonly string[]): string[] {
  const out = new Map<string, string>()
  for (const { repo } of seen) {
    if (!repoName(repo) || covered(repo, list)) continue
    if (!out.has(repo.toLowerCase())) out.set(repo.toLowerCase(), repo)
  }
  return [...out.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([, v]) => v)
}

export const sameList = (a: readonly string[], b: readonly string[]): boolean => a.length === b.length && a.every((x, i) => x === b[i])

/** Why this folder cannot be saved, or null. Empty is fine: it removes the setting. */
export function folderProblem(raw: string): string | null {
  const s = raw.trim()
  if (!s) return null
  if (/[\r\n\0]/.test(s)) return 'A folder is one line'
  if (s.length > MAX_FOLDER) return 'That path is too long'
  if (!s.startsWith('/')) return 'Use an absolute path, one that starts with /'
  return null
}

/** Saved clone folders, with a row's draft in place of its saved value (trimmed; empty removes it). Rows are the repos the page shows. */
export function clonesFrom(saved: Record<string, string>, rows: readonly string[], drafts: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  const shown = new Set(rows.map((r) => r.toLowerCase()))
  for (const [k, v] of Object.entries(saved)) if (!shown.has(k)) out[k] = v
  for (const r of rows) {
    const v = (drafts[r] ?? '').trim()
    if (v) out[r.toLowerCase()] = v
  }
  return out
}

/** The patterns save: the draft list beside the clones and root as they were saved, so an unsaved clone edit is not written. */
export const savePatterns = (saved: ReposSettings, list: readonly string[]): ReposSettings => ({ allow: [...list], clones: saved.clones, cloneRoot: saved.cloneRoot })

/** The clones save: the patterns as they were saved, so an unsaved list edit is not written. */
export const saveClones = (saved: ReposSettings, rows: readonly string[], drafts: Record<string, string>, root: string): ReposSettings => ({
  allow: saved.allow,
  clones: clonesFrom(saved.clones, rows, drafts),
  cloneRoot: root.trim() || null,
})

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

/** The line under a saved list: what the server stored. */
export const wrotePatterns = (allow: readonly string[]): string => `Saved ${plural(allow.length, 'repository pattern', 'repository patterns')}: ${allow.join(', ')}`

/** The line under saved folders: what the server stored. */
export function wroteClones(clones: Record<string, string>, root: string | null): string {
  const keys = Object.keys(clones)
  const folders = keys.length ? `Saved ${plural(keys.length, 'clone folder', 'clone folders')} (${keys.join(', ')})` : 'Saved: no clone folders set'
  return root ? `${folders} and the clones folder ${root}` : `${folders}, no clones folder`
}

/** Where a found clone came from, in words. */
export const howText = (how: string | null): string => (how === 'settings' ? 'set in Settings' : how === 'root' ? 'in the clones folder' : how === 'projects' ? 'in ~/projects' : '')

/** The row to focus once one is removed: the one now at its place, else the last, else -1 (the add box). `left` rows remain. */
export const nextFocusIndex = (left: number, at: number): number => (left <= 0 ? -1 : Math.min(at, left - 1))

/** The most repos BROWSE asks `gh` for, per owner */
export const BROWSE_REPOS_MAX = 100

/** "4 repos", or "first 100 repos" when the list is as long as BROWSE asks for, since there may be more. */
export const ownerCountText = (n: number): string => (n >= BROWSE_REPOS_MAX ? `first ${BROWSE_REPOS_MAX} repos` : `${n} ${n === 1 ? 'repo' : 'repos'}`)

// ---- Services: Slack channels, Jira keys, the cards' drafts, and what Reviews lists ----

/** The server refuses more channels or keys than this (both go into prompts and queries) */
export const MAX_LISTED = 20

// Mirrors of the server's grammars (settings.ts), which stay the judge: a channel is `#` and lowercase letters, digits, dots, dashes and
// underscores; a Jira key is a capital then up to nine capitals or digits. Names are checked as typed BEFORE they are lowercased or
// uppercased: a lookalike (the Kelvin sign lowercases to a plain k) must not become a name by case folding.
const CHANNEL_TYPED = /^#?[A-Za-z0-9._-]{1,80}$/
const KEY_TYPED = /^[A-Za-z][A-Za-z0-9]{1,9}$/

export type NameResult = { ok: true; name: string } | { ok: false; reason: string }

/** `#code-review` or `code-review`, trimmed and lowercased to the stored form `#code-review`. */
export function parseChannel(raw: string): NameResult {
  const s = raw.trim()
  if (!s) return { ok: false, reason: 'Type a channel name, like #code-review' }
  if (/\s/.test(s)) return { ok: false, reason: 'A channel name has no spaces' }
  if (!CHANNEL_TYPED.test(s)) return { ok: false, reason: 'A channel name is # then letters, digits, dots, dashes or underscores' }
  return { ok: true, name: `#${s.replace(/^#/, '').toLowerCase()}` }
}

/** `PROJ` or `proj`, to the stored form `PROJ`. */
export function parseJiraKey(raw: string): NameResult {
  const s = raw.trim()
  if (!s) return { ok: false, reason: 'Type a Jira project key, like PROJ' }
  if (/\s/.test(s)) return { ok: false, reason: 'A project key has no spaces' }
  if (!KEY_TYPED.test(s)) return { ok: false, reason: 'A project key is a letter then 1 to 9 letters or digits, like PROJ' }
  return { ok: true, name: s.toUpperCase() }
}

// The server's branch grammar (settings.ts parseBranch), which stays the judge: letters, digits and . _ - /, a letter or digit first, at most
// 100, and nothing git reads as an option, a range or a bad ref (`..`, `//`, `/.`, `@{`, a trailing `/` or `.`, `.lock` at the end).
const BRANCH = /^[A-Za-z0-9][A-Za-z0-9._/-]*$/

export type BranchResult = { ok: true; branch: string | null } | { ok: false; reason: string }

/** Open work's base branch as typed: empty is none (the repository's default branch), else a branch name, trimmed. */
export function parseBaseBranch(raw: string): BranchResult {
  const s = raw.trim()
  if (!s) return { ok: true, branch: null }
  if (/\s/.test(s)) return { ok: false, reason: 'A branch name has no spaces' }
  if (s.length > 100) return { ok: false, reason: 'A branch name is at most 100 characters' }
  if (!BRANCH.test(s)) return { ok: false, reason: 'A branch name is letters, digits and . _ - /, starting with a letter or digit, like main' }
  if (s.includes('..') || s.includes('//') || s.includes('/.') || s.includes('@{') || s.endsWith('/') || s.endsWith('.') || s.endsWith('.lock')) {
    return { ok: false, reason: 'Not a branch name git takes: no .., //, /., @{, and no / or . or .lock at the end' }
  }
  return { ok: true, branch: s }
}

export type AddNameResult = { ok: true; list: string[]; name: string } | { ok: false; reason: string }

/** The list with `raw` added through `parse`, or why not. Never changes the list it is given. */
export function addName(list: readonly string[], raw: string, parse: (raw: string) => NameResult): AddNameResult {
  const p = parse(raw)
  if (!p.ok) return p
  if (list.includes(p.name)) return { ok: false, reason: `${p.name} is already in the list` }
  if (list.length >= MAX_LISTED) return { ok: false, reason: `At most ${MAX_LISTED}: remove one first` }
  return { ok: true, list: [...list, p.name], name: p.name }
}

export type NameCheck = { state: 'empty' } | { state: 'ok'; name: string } | { state: 'error'; reason: string }

/** What an add box says while the person types: nothing when it is empty. */
export function nameCheck(raw: string, list: readonly string[], parse: (raw: string) => NameResult): NameCheck {
  if (!raw.trim()) return { state: 'empty' }
  const r = addName(list, raw, parse)
  return r.ok ? { state: 'ok', name: r.name } : { state: 'error', reason: r.reason }
}

export interface SlackSettings {
  enabled: boolean
  channels: string[]
  dms: boolean
  draftChannel: string | null
}
export interface AtlassianSettings {
  enabled: boolean
  /** `baseBranch`: the branch Open work starts from and opens PRs into; none is the repository's default branch. On the page's draft it is the text as typed. */
  jira: { projects: string[]; buildRepo: string | null; baseBranch?: string | null }
}

/** The slack section a save sends: a draft channel that is no longer one of the channels is sent as none. */
export const slackSection = (s: SlackSettings): SlackSettings => ({
  enabled: s.enabled,
  channels: [...s.channels],
  dms: s.dms,
  draftChannel: s.draftChannel !== null && s.channels.includes(s.draftChannel) ? s.draftChannel : null,
})

/** The atlassian section a save sends: the base branch always goes (none clears it), as its stored form; a draft that is not a branch is sent as none (the page blocks that save). */
export const atlassianSection = (a: AtlassianSettings): AtlassianSettings => {
  const base = parseBaseBranch(a.jira.baseBranch ?? '')
  return { enabled: a.enabled, jira: { projects: [...a.jira.projects], buildRepo: a.jira.buildRepo, baseBranch: base.ok ? base.branch : null } }
}

export const sameSlack = (a: SlackSettings, b: SlackSettings): boolean =>
  a.enabled === b.enabled && a.dms === b.dms && a.draftChannel === b.draftChannel && sameList(a.channels, b.channels)
export const sameAtlassian = (a: AtlassianSettings, b: AtlassianSettings): boolean =>
  a.enabled === b.enabled && a.jira.buildRepo === b.jira.buildRepo && (a.jira.baseBranch ?? null) === (b.jira.baseBranch ?? null) && sameList(a.jira.projects, b.jira.projects)

/** The draft-channel choices: "the first channel" (stored as none) and each channel. With no channels there is nothing to choose and no draft. */
export function draftChannelOptions(channels: readonly string[]): { value: string | null; label: string }[] {
  if (channels.length === 0) return []
  return [{ value: null, label: `First channel (${channels[0]})` }, ...channels.map((c) => ({ value: c, label: c }))]
}

/**
 * The repos Open work can build in: concrete `owner/repo` names only (the server refuses `owner/*`), from the clone rows the page shows,
 * the exact patterns of the allow list and the value already chosen, once each, in name order. Never a repo the list does not cover.
 */
export function buildRepoOptions(allow: readonly string[] | null, rows: readonly { repo: string }[], current: string | null): string[] {
  const out = new Map<string, string>()
  const add = (r: string) => {
    if (repoName(r) && covered(r, allow) && !out.has(r.toLowerCase())) out.set(r.toLowerCase(), r)
  }
  for (const p of allow ?? []) add(p)
  for (const { repo } of rows) add(repo)
  if (current) add(current)
  return [...out.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([, v]) => v)
}

/** The wording of a module's state for its status line, and the tone the page colours it by */
export type ModuleState = 'unchecked' | 'ok' | 'failing' | 'off'
export interface ModuleStatus {
  state: ModuleState
  detail: string
  at: number | null
}
export const STATE_TONES: Record<ModuleState, 'ok' | 'bad' | 'mute'> = { ok: 'ok', failing: 'bad', unchecked: 'mute', off: 'mute' }

/** "just now", "3 min ago", "2 h ago", "4 d ago": how long since a status was recorded. */
export function agoText(at: number | null, now: number): string {
  if (at === null || !Number.isFinite(at)) return ''
  const s = Math.max(0, Math.round((now - at) / 1000))
  if (s < 45) return 'just now'
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min ago`
  const h = Math.round(m / 60)
  if (h < 48) return `${h} h ago`
  return `${Math.round(h / 24)} d ago`
}

/** The state as a word, for what colour alone would carry: ok and failing get one (off and unchecked already say so in their own text). */
export const stateWord = (state: ModuleState): string => (state === 'ok' ? 'Working' : state === 'failing' ? 'Failing' : '')

/** A status as one line: its state in words, what it says, and when it was recorded. */
export function statusLine(s: ModuleStatus, now: number): string {
  const word = stateWord(s.state)
  const text = word ? `${word}: ${s.detail}` : s.detail
  return s.at === null ? text : `${text} · ${agoText(s.at, now)}`
}

export interface SourcesInput {
  /** The GitHub module is on */
  github: boolean
  /** The Slack module is on */
  slackOn: boolean
  channels: readonly string[]
  dms: boolean
  /** `reviews.requested`: PRs that ask for your review */
  requested: boolean
  /** `reviews.allOpen`: every open non-draft PR you did not write */
  allOpen: boolean
}

/** Which Slack reads the combination makes: the channel scan needs Slack on and a channel, the DM scan needs Slack on and its switch. */
export const slackReads = (i: SourcesInput): { channel: boolean; dm: boolean } => ({ channel: i.slackOn && i.channels.length > 0, dm: i.slackOn && i.dms })

const joinWords = (xs: readonly string[], last: 'and' | 'or'): string => (xs.length <= 1 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} ${last} ${xs[xs.length - 1]}`)

/**
 * The one sentence under the Reviews switches: what Reviews lists for this combination, in plain words. GitHub off lists nothing (the
 * screen is gone, and a PR Slack finds is fetched from GitHub); with no source on it says so rather than going quiet. A PR that is only
 * ready for review (not asking for you) is listed only when a message backs it up, unless every open PR is on; with no Slack read on, it says Reviews makes no Slack call.
 */
export function listsSentence(i: SourcesInput): string {
  if (!i.github) return 'Lists: nothing. GitHub is off, so there is no Reviews screen and conn reads no PR.'
  const slack = slackReads(i)
  const where: string[] = []
  if (slack.channel) where.push(`posted in ${joinWords(i.channels, 'or')}`)
  if (slack.dm) where.push('sent to you in a DM')
  if (!i.requested && !i.allOpen && where.length === 0) return 'Lists: nothing. Turn on a source below.'
  if (i.allOpen) {
    const head = 'Lists: every open, non-draft PR in your allowed repositories that you did not write.'
    if (where.length === 0) return head
    return `${head} PRs ${where.join(', or ')} show their Slack links; the rest are tagged "not in a channel".`
  }
  const parts: string[] = []
  if (i.requested) parts.push('PRs that request your review')
  if (slack.channel) parts.push(`PRs posted in ${joinWords(i.channels, 'or')}`)
  if (slack.dm) parts.push('PRs sent to you in a DM')
  const body = `Lists: ${joinWords(parts, 'and')}.`
  if (where.length > 0) return `${body} A PR that is only ready for review, with no message about it, is left out.`
  return `${body} Reviews makes no Slack call.`
}

// ---- What a save says it wrote (from what was sent: the server stored exactly that) ----

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`
const onOff = (b: boolean) => (b ? 'on' : 'off')

export const wroteGithub = (enabled: boolean): string => `Saved: GitHub is ${onOff(enabled)}`

export function wroteSlack(s: SlackSettings): string {
  const channels = s.channels.length ? `${count(s.channels.length, 'channel', 'channels')} (${s.channels.join(', ')})` : 'no channels'
  const draft = s.draftChannel ? `, drafts in ${s.draftChannel}` : s.channels.length ? `, drafts in ${s.channels[0]}` : ', no drafts'
  return `Saved: Slack is ${onOff(s.enabled)}, ${channels}, DM scan ${onOff(s.dms)}${draft}`
}

export function wroteAtlassian(a: AtlassianSettings): string {
  const keys = a.jira.projects.length ? `${count(a.jira.projects.length, 'project', 'projects')} (${a.jira.projects.join(', ')})` : 'no projects'
  return `Saved: Atlassian is ${onOff(a.enabled)}, ${keys}, ${a.jira.buildRepo ? `builds in ${a.jira.buildRepo}` : 'no build repo'}${a.jira.baseBranch ? ` from ${a.jira.baseBranch}` : ''}`
}

export const wroteReviews = (r: { requested: boolean; allOpen: boolean }): string =>
  `Saved: "Requests me" is ${onOff(r.requested)}, "Every open non-draft PR" is ${onOff(r.allOpen)}`
