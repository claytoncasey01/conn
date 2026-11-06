// What the browser remembers about themes. Everything read here is untrusted text, so every field is
// checked and a bad one falls back to a default; nothing throws. The store is passed in: no global localStorage.
import type { Mode, ModeSetting } from './types.ts'

export const DEFAULT_ID = 'default' // index.html's boot script starts from the same id (a test holds them together)
// The built-in that is always listed and compiled in: what a missing choice falls back to. Not a setting, so it never follows DEFAULT_ID
export const FALLBACK_ID = 'default'
export const KEYS = { choice: 'conn.theme', css: (id: string) => `conn.theme.css.${id}`, cssOk: 'conn.theme.cssOk' }

export type Reader = Pick<Storage, 'getItem'>
export type Store = Pick<Storage, 'getItem' | 'setItem'>

// What the boot script needs to paint the last applied look; `only` is the theme's single mode, null for two.
// `sheet` is the theme's own stylesheet the app had applied, and `cssUrl` the address it was read from (both null: none):
// only for a theme whose CSS the app already served with consent (or a built-in's). The stylesheet is an /api route, which
// needs the secret's header, so the page reads it with a request and applies its text, and the boot script, which makes no
// request, paints the text kept here. `fontsUrl` is a link, only of the allow-listed Google Fonts form fontsHref builds.
export type CacheEntry = {
  css: string
  attrs: { motion: string; led: string; glass: string }
  only: Mode | null
  cssUrl: string | null
  sheet: string | null
  fontsUrl: string | null
}

/** The most characters a theme's own stylesheet may hold to be applied or kept: the server's limit on theme.css (256 KiB) */
export const SHEET_CHARS_MAX = 262144
export type Choice = {
  id: string
  mode: ModeSetting
  accent: { name: string; hue: number; chroma: number } | null
  variant: string | null
  effects: 'auto' | 'on' | 'off'
}

export const ID = /^[a-z0-9][a-z0-9-]{0,39}$/
// The only hrefs the boot script will put in a <link>: a same-origin theme stylesheet path, or the Google Fonts form fontsHref builds
// (the allow-listed family names only; a test builds every ALLOWED_FONTS subset through both).
// index.html carries the same two literals (a test holds them together); the cache is localStorage, so both are checked at every read.
export const CSS_HREF = /^\/api\/themes\/[a-z0-9-]+\/theme\.css\?v=[0-9a-f]{64}$/
export const FONTS_HREF = /^https:\/\/fonts\.googleapis\.com\/css2\?family=(?:Chakra\+Petch|JetBrains\+Mono|IBM\+Plex\+Sans|IBM\+Plex\+Mono|Share\+Tech\+Mono):wght@[0-9;]{1,40}(&family=(?:Chakra\+Petch|JetBrains\+Mono|IBM\+Plex\+Sans|IBM\+Plex\+Mono|Share\+Tech\+Mono):wght@[0-9;]{1,40}){0,7}&display=swap$/
export const MODES = ['light', 'dark', 'system']
const EFFECTS = ['auto', 'on', 'off']
const ATTRS = { motion: ['calm', 'power'], led: ['circle', 'diamond', 'square'], glass: ['on', 'off'] }

const defaults = (): Choice => ({ id: DEFAULT_ID, mode: 'system', accent: null, variant: null, effects: 'auto' })
const obj = (v: unknown): Record<string, unknown> | null => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null)
const num = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

function read(s: Reader | null, key: string): unknown {
  try {
    const t = s?.getItem(key)
    return typeof t === 'string' ? JSON.parse(t) : null
  } catch {
    return null
  }
}

// The accent generator does not range-check and its contrast guarantee only holds in range, so the
// range is enforced here: chroma into [0, .4], hue wrapped into [0, 360).
export function accentOf(v: unknown): Choice['accent'] {
  const a = obj(v)
  if (!a || typeof a.name !== 'string' || a.name.length > 32 || !num(a.hue) || !num(a.chroma)) return null
  return { name: a.name, hue: ((a.hue % 360) + 360) % 360, chroma: Math.min(0.4, Math.max(0, a.chroma)) }
}

export function readChoice(s: Reader | null): Choice {
  const c = obj(read(s, KEYS.choice))
  const d = defaults()
  if (!c) return d
  return {
    id: typeof c.id === 'string' && ID.test(c.id) ? c.id : d.id,
    mode: typeof c.mode === 'string' && MODES.includes(c.mode) ? (c.mode as ModeSetting) : d.mode,
    accent: accentOf(c.accent),
    variant: typeof c.variant === 'string' && c.variant.length <= 40 ? c.variant : null,
    effects: typeof c.effects === 'string' && EFFECTS.includes(c.effects) ? (c.effects as Choice['effects']) : d.effects,
  }
}

export function writeChoice(s: Store | null, c: Choice): void {
  try {
    s?.setItem(KEYS.choice, JSON.stringify(c))
  } catch {
    // private mode or a full quota: the choice just lasts this load
  }
}

export function readCache(s: Reader | null, id: string): CacheEntry | null {
  const e = obj(read(s, KEYS.css(id)))
  const a = obj(e?.attrs)
  // the stylesheet is the app's own generated text, which never loads anything (the boot script refuses the same)
  if (!e || !a || typeof e.css !== 'string' || /url\(|@import|expression/i.test(e.css)) return null
  const okAttr = (k: keyof typeof ATTRS) => typeof a[k] === 'string' && ATTRS[k].includes(a[k] as string)
  if (!okAttr('motion') || !okAttr('led') || !okAttr('glass')) return null
  if (e.only !== null && e.only !== 'light' && e.only !== 'dark') return null
  // each part stands alone: a bad one is dropped, the tokens still paint. The stylesheet must also be this theme's own, and
  // its text and address come together or not at all.
  const urlOk = typeof e.cssUrl === 'string' && e.cssUrl.startsWith(`/api/themes/${id}/`) && CSS_HREF.test(e.cssUrl)
  const sheetOk = urlOk && typeof e.sheet === 'string' && e.sheet.length <= SHEET_CHARS_MAX
  const cssUrl = sheetOk ? (e.cssUrl as string) : null
  const sheet = sheetOk ? (e.sheet as string) : null
  const fontsUrl = typeof e.fontsUrl === 'string' && FONTS_HREF.test(e.fontsUrl) ? e.fontsUrl : null
  return { css: e.css, attrs: { motion: a.motion as string, led: a.led as string, glass: a.glass as string }, only: e.only, cssUrl, sheet, fontsUrl }
}

export function writeCache(s: Store | null, id: string, e: CacheEntry): void {
  try {
    s?.setItem(KEYS.css(id), JSON.stringify(e))
  } catch {
    // quota: the next load paints from the fallback until the loader applies the theme again
  }
}

// A theme that is gone must not be retried on the next load, so its cached stylesheet is removed.
export function removeCache(s: Pick<Storage, 'removeItem'> | null, id: string): void {
  try {
    s?.removeItem(KEYS.css(id))
  } catch {
    // nothing to do: the entry is only read for an id the list still has
  }
}

// `?theme=<id>&mode=<mode>` pins the look for one load (the shots tool, a link) without being stored. Parsed as the
// boot script does it, with the same id pattern and modes, so the two never disagree about which look a URL means.
export type Pin = { id?: string; mode?: ModeSetting }
export function readPin(search: string): Pin | null {
  const q: Record<string, string> = {}
  try {
    for (const p of search.slice(1).split('&')) {
      const [k, v] = p.split('=')
      if (k === 'theme' || k === 'mode') q[k] = v
    }
  } catch {
    return null
  }
  const pin: Pin = {}
  if (typeof q.theme === 'string' && ID.test(q.theme)) pin.id = q.theme
  if (typeof q.mode === 'string' && MODES.includes(q.mode)) pin.mode = q.mode as ModeSetting
  return pin.id || pin.mode ? pin : null
}

/** The choice as the page wears it: the pin laid over the stored one, key by key. The stored choice itself is never changed. */
export const withPin = (c: Choice, p: Pin | null): Choice => (p ? { ...c, ...p } : c)
