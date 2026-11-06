// The Settings requests: typed wrappers over /api/settings. Not post() in live.svelte.ts: the page needs the server's own words
// when a save is refused (a 400 names the entry), so every wrapper answers a result and never throws.
import { api } from './api.ts'
import { parseOk } from './ask.ts'
import { parseBaseBranch, repoName, REVIEW_MOVES, SESSION_LAYOUTS } from './settingsModel.ts'
import type { AtlassianSettings, ModuleState, ModuleStatus, ReposSettings, ReviewMove, SessionLayout, SlackSettings } from './settingsModel.ts'

/** `settings.json` as the server holds it. */
export interface Settings {
  version: 1
  repos: ReposSettings
  github: { enabled: boolean }
  slack: SlackSettings
  atlassian: AtlassianSettings
  reviews: { requested: boolean; allOpen: boolean }
}
export type ModuleName = 'github' | 'slack' | 'atlassian'
export const MODULE_NAMES: readonly ModuleName[] = ['github', 'slack', 'atlassian']
export type SettingsSection = 'repos' | 'github' | 'slack' | 'atlassian' | 'reviews'

export type SeenFrom = 'session' | 'myprs' | 'history'
export interface SeenRepo {
  repo: string
  from: SeenFrom
}
/** A repo conn has seen and where its clone is: `path` null when none was found, `how` says which rule found it. */
export interface CloneRow {
  repo: string
  path: string | null
  how: string | null
}
export interface SettingsView {
  settings: Settings
  /** True while no repository list has been saved */
  setup: boolean
  seen: SeenRepo[]
  clones: CloneRow[]
  /** Saved pattern -> how many seen repos it covers (the server's count; the page counts a draft itself) */
  matched: Record<string, number>
  /** What the server ignored when it read a hand-edited settings.json, one sentence each (empty when nothing was) */
  dropped: string[]
  /** Where the first save kept the unreadable or newer-version settings.json it replaced (settings.json.bak), or null when it kept none */
  backedUp: string | null
  /** What conn knows of each module: read from memory by the server, never probed by this request */
  modules: Record<ModuleName, ModuleStatus>
}
export interface Owner {
  owner: string
  repos: string[]
}

/** The view as it is once `repos` has been saved as sent: the page shows this at once, whether or not the read-back that follows works. */
export function withSavedRepos(view: SettingsView, repos: ReposSettings): SettingsView {
  return { ...view, settings: { ...view.settings, repos }, setup: view.setup && !(repos.allow && repos.allow.length > 0) }
}

/** The view as it is once a services section has been saved as sent. */
export function withSavedSection<K extends 'github' | 'slack' | 'atlassian' | 'reviews'>(view: SettingsView, section: K, value: Settings[K]): SettingsView {
  return { ...view, settings: { ...view.settings, [section]: value } }
}

/** The view with one module's status replaced (a CHECK answered, or a save changed its switch). */
export const withStatus = (view: SettingsView, name: ModuleName, status: ModuleStatus): SettingsView => ({ ...view, modules: { ...view.modules, [name]: status } })

export type Result<T> = ({ ok: true } & T) | { ok: false; error: string }

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const msg = (e: unknown) => (e instanceof Error ? e.message : 'request failed')
const DROPPED_MAX = 25
const FROM: readonly unknown[] = ['session', 'myprs', 'history']

const bool = (v: unknown, fallback: boolean): boolean => (typeof v === 'boolean' ? v : fallback)
const strings = (v: unknown, ok: (s: string) => boolean = () => true): string[] => (Array.isArray(v) ? v.filter((x): x is string => isStr(x) && ok(x)) : [])
const STATES: readonly unknown[] = ['unchecked', 'ok', 'failing', 'off']

/** The sections the page edits, as the server sends them, each field of the right type or its default. */
function readSlack(v: unknown): SlackSettings {
  const o = isObj(v) ? v : {}
  const channels = strings(o.channels)
  return { enabled: bool(o.enabled, false), channels, dms: bool(o.dms, false), draftChannel: isStr(o.draftChannel) && channels.includes(o.draftChannel) ? o.draftChannel : null }
}
/** The per-repository base branches: each repository a name and each branch one, anything else left out. */
function readBranches(v: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  if (!isObj(v)) return out
  for (const [repo, text] of Object.entries(v)) {
    const parsed = isStr(text) ? parseBaseBranch(text) : null
    if (repoName(repo) && parsed?.ok && parsed.branch) out[repo.toLowerCase()] = parsed.branch
  }
  return out
}
function readAtlassian(v: unknown): AtlassianSettings {
  const o = isObj(v) ? v : {}
  const jira = isObj(o.jira) ? o.jira : {}
  const base = isStr(jira.baseBranch) ? parseBaseBranch(jira.baseBranch) : null
  const reviewMove = REVIEW_MOVES.find((m) => m.value === jira.reviewMove)?.value ?? ('all' satisfies ReviewMove)
  const sessionLayout = SESSION_LAYOUTS.find((l) => l.value === jira.sessionLayout)?.value ?? ('shared' satisfies SessionLayout)
  return {
    enabled: bool(o.enabled, false),
    jira: {
      projects: strings(jira.projects),
      buildRepo: isStr(jira.buildRepo) && repoName(jira.buildRepo) ? jira.buildRepo : null,
      baseBranch: base?.ok ? base.branch : null,
      baseBranches: readBranches(jira.baseBranches),
      reviewMove,
      sessionLayout,
    },
  }
}
function readStatus(v: unknown): ModuleStatus {
  if (!isObj(v) || !STATES.includes(v.state)) return { state: 'unchecked', detail: 'Not checked yet', at: null }
  return { state: v.state as ModuleState, detail: isStr(v.detail) ? v.detail : '', at: typeof v.at === 'number' && Number.isFinite(v.at) ? v.at : null }
}
/** A server that sends no `modules` (an older one) is every module not checked. */
function readModules(v: unknown): Record<ModuleName, ModuleStatus> {
  const o = isObj(v) ? v : {}
  return { github: readStatus(o.github), slack: readStatus(o.slack), atlassian: readStatus(o.atlassian) }
}

function readRepos(v: unknown): ReposSettings | null {
  if (!isObj(v)) return null
  const clones: Record<string, string> = {}
  if (isObj(v.clones)) for (const [k, p] of Object.entries(v.clones)) if (isStr(p)) clones[k] = p
  return { allow: Array.isArray(v.allow) ? v.allow.filter(isStr) : null, clones, cloneRoot: isStr(v.cloneRoot) ? v.cloneRoot : null }
}

/** The GET body as the page uses it: entries it cannot read are dropped, a body with no settings is null. */
export function parseView(body: unknown): SettingsView | null {
  if (!isObj(body) || !isObj(body.settings)) return null
  const repos = readRepos(body.settings.repos)
  if (!repos) return null
  const matched: Record<string, number> = {}
  if (isObj(body.matched)) for (const [k, n] of Object.entries(body.matched)) if (typeof n === 'number' && Number.isFinite(n)) matched[k] = n
  return {
    settings: {
      version: 1,
      repos,
      github: { enabled: bool(isObj(body.settings.github) ? body.settings.github.enabled : undefined, true) },
      slack: readSlack(body.settings.slack),
      atlassian: readAtlassian(body.settings.atlassian),
      reviews: {
        requested: bool(isObj(body.settings.reviews) ? body.settings.reviews.requested : undefined, true),
        allOpen: bool(isObj(body.settings.reviews) ? body.settings.reviews.allOpen : undefined, false),
      },
    },
    setup: body.setup === true,
    seen: (Array.isArray(body.seen) ? body.seen : []).filter((s): s is SeenRepo => isObj(s) && isStr(s.repo) && FROM.includes(s.from)).map((s) => ({ repo: s.repo, from: s.from })),
    clones: (Array.isArray(body.clones) ? body.clones : [])
      .filter((c): c is CloneRow => isObj(c) && isStr(c.repo) && (c.path === null || isStr(c.path)) && (c.how === null || isStr(c.how)))
      .map((c) => ({ repo: c.repo, path: c.path, how: c.how })),
    matched,
    backedUp: isStr(body.backedUp) && body.backedUp ? body.backedUp : null,
    dropped: (Array.isArray(body.dropped) ? body.dropped : []).filter((d): d is string => isStr(d) && d.length > 0).slice(0, DROPPED_MAX),
    modules: readModules(body.modules),
  }
}

/** GET /api/settings: the settings, whether conn is in setup, the repos it has seen, where their clones are. Makes no GitHub call. */
export async function getSettings(): Promise<Result<{ view: SettingsView }>> {
  try {
    const r = await api('/api/settings')
    const body = await r.json().catch(() => null)
    if (r.status === 404) return { ok: false, error: 'This server has no settings yet. Restart it from the current version.' }
    if (!r.ok) return { ok: false, error: isObj(body) && isStr(body.error) && body.error ? body.error : `HTTP ${r.status}` }
    const view = parseView(body)
    return view ? { ok: true, view } : { ok: false, error: 'The settings answer could not be read.' }
  } catch (e) {
    return { ok: false, error: msg(e) }
  }
}

/** PUT one section, whole. A refusal is the server's message (it names the bad entry); the page keeps its draft. */
export async function putSection(section: SettingsSection, body: unknown): Promise<{ ok: true } | { ok: false; error: string }> {
  try {
    const r = await api(`/api/settings/${section}`, {
      method: 'PUT',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    return parseOk(r.status, await r.json().catch(() => null))
  } catch (e) {
    return { ok: false, error: msg(e) }
  }
}

/** BROWSE GITHUB: the repos and owners the signed-in `gh` can reach. Only the button calls this. */
/** `failed` names owners GitHub could not list while the rest could. */
export async function browseGithub(): Promise<Result<{ owners: Owner[]; failed: string[] }>> {
  try {
    const r = await api('/api/settings/browse', { method: 'POST', headers: { 'x-conn': '1' } })
    const body = await r.json().catch(() => null)
    if (!r.ok) return { ok: false, error: isObj(body) && isStr(body.error) && body.error ? body.error : `HTTP ${r.status}` }
    if (!isObj(body) || !Array.isArray(body.owners)) return { ok: false, error: 'The GitHub list could not be read.' }
    const owners: Owner[] = []
    for (const o of body.owners) {
      if (!isObj(o) || !isStr(o.owner) || !repoName(`${o.owner}/x`) || !Array.isArray(o.repos)) continue
      owners.push({ owner: o.owner, repos: o.repos.filter((x): x is string => isStr(x) && repoName(x)) })
    }
    const failed = (Array.isArray(body.failed) ? body.failed : []).filter((o): o is string => isStr(o) && repoName(`${o}/x`))
    return { ok: true, owners, failed }
  } catch (e) {
    return { ok: false, error: msg(e) }
  }
}

/** CHECK: one probe of one module, run by this press and no other way. The server answers the module's new status; a refusal (the module is off) is its words. */
export async function checkModule(name: ModuleName): Promise<Result<{ status: ModuleStatus }>> {
  try {
    const r = await api(`/api/settings/check/${name}`, { method: 'POST', headers: { 'x-conn': '1' } })
    const body = await r.json().catch(() => null)
    if (!r.ok) return { ok: false, error: isObj(body) && isStr(body.error) && body.error ? body.error : `HTTP ${r.status}` }
    if (!isObj(body) || !isObj(body.status) || !STATES.includes(body.status.state)) return { ok: false, error: 'The check answer could not be read.' }
    return { ok: true, status: readStatus(body.status) }
  } catch (e) {
    return { ok: false, error: msg(e) }
  }
}
