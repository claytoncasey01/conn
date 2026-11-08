// The theme the page wears. The server's list is the one source of themes; Default is compiled in, so the first
// paint and any fetch failure still have a look. DOM and storage work is in apply.ts and storage.ts.
import { api } from '../api.ts'
import defaultTheme from '../../../themes/default/theme.json'
import { browserEffectsHost } from '../../effects/browser.ts'
import type { EffectsHost } from '../../effects/lib/host.ts'
import { reduced } from '../fx.ts'
import { amend, applyResolved, cacheEntryOf, domFor, installFallback, reconcile, select } from './apply.ts'
import type { Dom } from './apply.ts'
import { readAccepted, remember } from './consent.ts'
import { fallbackCss } from './css.ts'
import { pick, resolveTheme } from './resolve.ts'
import type { Resolved } from './resolve.ts'
import { FALLBACK_ID, readCache, readChoice, readPin, removeCache, SHEET_CHARS_MAX, withPin, writeCache, writeChoice } from './storage.ts'
import type { Choice, Pin } from './storage.ts'
import type { ModeSetting, ThemeEntry, ThemeManifest } from './types.ts'

const DEFAULT = defaultTheme as unknown as ThemeManifest

const store = (): Storage | null => {
  try {
    return localStorage
  } catch {
    return null
  }
}

let dark: MediaQueryList | null = null
// A URL pin (?theme=, ?mode=) overrides the stored choice for this load only: it is never written, never reconciled.
// An explicit choice by the person (change below) drops it and persists as usual.
let pin: Pin | null = null
const effective = (c: Choice): Choice => withPin(c, pin)
const resolveNow = (list: ThemeEntry[], choice: Choice): Resolved =>
  resolveTheme({ list, choice: effective(choice), systemDark: dark?.matches ?? false, accepted: readAccepted(store()), fallback: DEFAULT })

const first = readChoice(store())

export const theme = $state({
  list: [] as ThemeEntry[],
  choice: first,
  /** Before `ready` and for a non-default choice this is only Default's look: the boot script has painted the real one */
  resolved: resolveNow([], first),
  /** The list has been read once */
  ready: false,
  /** Why the last list read failed; what is applied stays as it was */
  error: null as string | null,
  /** The URL pin that is on screen instead of `choice` (read-only for the menu, which says so); null once a choice drops it */
  pin: null as Pin | null,
})

// `pin` above is what the resolver reads (it is needed before this object exists); the menu reads the copy in `theme`
function setPin(p: Pin | null) {
  pin = p
  theme.pin = p
}

let dom: Dom | null = null
let effects: EffectsHost | null = null
let started = false

// The themes' own stylesheets read so far, by address (each holds its version, so a changed stylesheet is a new address).
// At most SHEETS_MAX are kept: the one on screen and a few the person tried; the rest are read again when picked.
const SHEETS_MAX = 8
const sheets = new Map<string, string>()
const reading = new Set<string>()

function keepSheet(href: string, text: string) {
  sheets.delete(href)
  sheets.set(href, text)
  for (const old of sheets.keys()) {
    if (sheets.size <= SHEETS_MAX) break
    sheets.delete(old)
  }
}

// The stylesheet at href if it has been read; otherwise null now, and a read that applies it when it arrives (if it is still
// the one wanted). Read through api() because it is an /api route, which needs the secret.
function sheetFor(href: string | null): string | null {
  if (href === null) return null
  const known = sheets.get(href)
  if (known !== undefined) return known
  void readSheet(href)
  return null
}

async function readSheet(href: string) {
  if (reading.has(href)) return
  reading.add(href)
  try {
    const res = await api(href, { cache: 'no-store' })
    if (!res.ok) return
    const text = await res.text()
    if (text.length > SHEET_CHARS_MAX) return
    keepSheet(href, text)
    if (theme.resolved.cssUrl === href) reapply()
  } catch {
    // the theme paints without its own rules; the next apply reads it again
  } finally {
    reading.delete(href)
  }
}

// The effects follow the look on screen (the pin included) and the choice's own switch. Auto means on unless the OS asks for less motion.
// The same decision marks the root data-fx, which every theme's CSS motion keys off, so the two never disagree.
function syncEffects(r: Resolved) {
  const e = effective(theme.choice).effects
  const on = e === 'on' || (e === 'auto' && !reduced())
  dom?.setAttr('data-fx', on ? 'on' : 'off')
  effects?.apply(r.entry.manifest?.effects ?? [], r.tokens, on)
}

// Until the list arrives, a stored non-default choice is already on screen from the cache (or is just Default's
// tokens through the fallback sheet), so applying Default now would only flash the wrong look and cache it.
function reapply() {
  const r = resolveNow(theme.list, theme.choice)
  theme.resolved = r
  if (!dom || (!theme.ready && effective(theme.choice).id !== FALLBACK_ID)) return
  const sheet = sheetFor(r.cssUrl)
  applyResolved(dom, r, sheet)
  syncEffects(r)
  // the id is the one in data-theme, so a stylesheet is never cached under another theme's name
  writeCache(store(), r.entry.id, cacheEntryOf(r, sheet))
}

function change(next: Choice | null) {
  if (!next) return
  setPin(null)
  theme.choice = next
  writeChoice(store(), next)
  reapply()
}

const isEntry = (e: unknown): e is ThemeEntry => {
  const o = e as Partial<ThemeEntry> | null
  return !!o && typeof o === 'object' && typeof o.id === 'string' && typeof o.source === 'string' && (typeof o.invalid === 'string' || (!!o.manifest && typeof o.manifest === 'object'))
}

let inflight: Promise<void> | null = null

export function refreshThemes(): Promise<void> {
  inflight ??= load().finally(() => (inflight = null))
  return inflight
}

async function load() {
  let list: ThemeEntry[]
  try {
    const res = await api('/api/themes', { cache: 'no-store' })
    if (!res.ok) throw new Error(`the server answered ${res.status}`)
    const body: unknown = await res.json()
    if (!Array.isArray(body)) throw new Error('the list was not a list')
    list = body.filter(isEntry)
    // Default is always a built-in, so a list without one is a read that failed upstream (the server answers [] when listing throws)
    if (!list.some((e) => e.source === 'builtin' && !e.invalid)) throw new Error('the list had no built-in theme')
  } catch (e) {
    theme.error = `Could not load themes: ${e instanceof Error ? e.message : 'unknown error'}`
    return
  }
  theme.error = null
  theme.list = list
  theme.ready = true
  // a pinned load is a look, not a choice: an unlisted pinned id shows Default and nothing stored changes
  const out = pin ? { choice: theme.choice, dropCache: null } : reconcile(list, theme.choice)
  if (out.dropCache) removeCache(store(), out.dropCache)
  if (out.choice !== theme.choice) {
    theme.choice = out.choice
    writeChoice(store(), out.choice)
  }
  reapply()
}

/** `deps.effects` lets a test stand in for the real layer */
export function initTheme(deps: { effects?: EffectsHost } = {}): void {
  if (started) return
  started = true
  effects = deps.effects ?? browserEffectsHost()
  try {
    setPin(readPin(location.search))
  } catch {
    setPin(null)
  }
  // the stylesheet the boot script painted from the cache is the one the app applies first, not read again
  const cached = readCache(store(), effective(theme.choice).id)
  if (cached?.cssUrl && cached.sheet !== null) keepSheet(cached.cssUrl, cached.sheet)
  dom = domFor(document as unknown as Parameters<typeof domFor>[0])
  installFallback(document as unknown as Parameters<typeof installFallback>[0], fallbackCss(DEFAULT))
  try {
    dark = matchMedia('(prefers-color-scheme: dark)')
    dark.addEventListener('change', reapply)
  } catch {
    // no matchMedia: system mode reads as light
  }
  try {
    // auto effects follow the OS setting live
    matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', reapply)
  } catch {
    // no matchMedia: motion is not reduced
  }
  window.addEventListener('focus', () => void refreshThemes())
  reapply()
  void refreshThemes()
}

/** The CSS hashes accepted in this browser, by theme id; read fresh, since another tab can accept one */
export const acceptedCss = (): Record<string, string> => readAccepted(store())

/** A user theme with CSS asks first: this says whether `id` is waiting for acceptCss */
export function needsCssConsent(id: string): boolean {
  return select(theme.list, theme.choice, id, readAccepted(store())).kind === 'consent'
}

export function selectTheme(id: string): void {
  const s = select(theme.list, theme.choice, id, readAccepted(store()))
  if (s.kind === 'apply') change(s.choice)
}

export function acceptCss(id: string): void {
  const e = pick(theme.list, id)
  if (!e || e.source !== 'user') return
  remember(store(), e)
  selectTheme(id)
}

export const setMode = (m: ModeSetting): void => change(amend(theme.choice, { mode: m }))
export const setAccent = (a: { name: string; hue: number; chroma: number }): void => change(amend(theme.choice, { accent: a }))
export const setVariant = (name: string): void => change(amend(theme.choice, { variant: name }))
export const setEffects = (v: 'auto' | 'on' | 'off'): void => change(amend(theme.choice, { effects: v }))
