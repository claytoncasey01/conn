// The pure and DOM-facing halves of the loader: what a choice means against the list, and how a resolved
// theme reaches the page. The DOM is passed in (Dom, Doc), so none of this needs a browser to test.
import { needsConsent } from './consent.ts'
import { fontsHref } from './fonts.ts'
import { pick } from './resolve.ts'
import type { Resolved } from './resolve.ts'
import { accentOf, FALLBACK_ID } from './storage.ts'
import type { CacheEntry, Choice } from './storage.ts'
import type { ThemeEntry } from './types.ts'

export type Dom = {
  setAttr(name: string, v: string): void
  style(css: string | null): void
  link(id: string, href: string | null): void
}

export const IDS = { style: 'conn-theme', fallback: 'conn-theme-fallback', fonts: 'conn-theme-fonts', css: 'conn-theme-css' }

export function applyResolved(dom: Dom, r: Resolved): void {
  dom.setAttr('data-theme', r.entry.id)
  dom.setAttr('data-mode', r.mode)
  dom.setAttr('data-motion', r.attrs.motion)
  dom.setAttr('data-led', r.attrs.led)
  dom.setAttr('data-glass', r.attrs.glass)
  dom.style(r.css)
  dom.link(IDS.fonts, fontsHref(r.fonts))
  dom.link(IDS.css, r.cssUrl)
}

// What the boot script paints before the app loads: the generated token stylesheet and the two links the app applied.
// The boot script runs before the consent check, so consent is settled here: r.cssUrl is null for a user theme whose CSS
// is not accepted (that exact hash) and for any gallery theme, so such a link is never cached and never injected.
export const cacheEntryOf = (r: Resolved): CacheEntry => ({
  css: r.css,
  attrs: r.attrs,
  only: r.only,
  cssUrl: r.cssUrl,
  fontsUrl: fontsHref(r.fonts),
})

/**
 * A theme that was deleted, edited invalid or renamed falls back to Default (FALLBACK_ID), and its cached
 * stylesheet is dropped so the next load does not retry it.
 */
export function reconcile(list: ThemeEntry[], choice: Choice): { choice: Choice; dropCache: string | null } {
  if (choice.id === FALLBACK_ID || pick(list, choice.id)) return { choice, dropCache: null }
  return { choice: { ...choice, id: FALLBACK_ID, variant: null }, dropCache: choice.id }
}

export type Select = { kind: 'apply'; choice: Choice } | { kind: 'consent' } | { kind: 'none' }

// Same picking as the resolver, so an invalid user folder never shadows a valid built-in of its id.
export function select(list: ThemeEntry[], choice: Choice, id: string, accepted: Record<string, string>): Select {
  const e = pick(list, id)
  if (!e) return { kind: 'none' }
  if (needsConsent(e, accepted)) return { kind: 'consent' }
  return { kind: 'apply', choice: { ...choice, id: e.id, variant: e.id === choice.id ? choice.variant : null } }
}

const MODES = ['light', 'dark', 'system']
const EFFECTS = ['auto', 'on', 'off']

// The menu is a caller like any other: a bad value is refused here, and an accent is clamped as readChoice does.
export function amend(choice: Choice, patch: Partial<Pick<Choice, 'mode' | 'accent' | 'variant' | 'effects'>>): Choice | null {
  const next = { ...choice }
  if (patch.mode !== undefined) {
    if (!MODES.includes(patch.mode)) return null
    next.mode = patch.mode
  }
  if (patch.effects !== undefined) {
    if (!EFFECTS.includes(patch.effects)) return null
    next.effects = patch.effects
  }
  if (patch.variant !== undefined) {
    if (patch.variant !== null && (typeof patch.variant !== 'string' || patch.variant.length > 40)) return null
    next.variant = patch.variant
  }
  if (patch.accent !== undefined) {
    const a = patch.accent === null ? null : accentOf(patch.accent)
    if (patch.accent !== null && !a) return null
    next.accent = a
  }
  return next
}

// getAttribute: a real link's `href` reads back resolved, so the written value is compared through the attribute
export type El = {
  id: string
  textContent: string | null
  rel?: string
  href?: string
  onload?: (() => void) | null
  getAttribute?(name: string): string | null
  remove(): void
}
export type Doc = {
  documentElement: { setAttribute(name: string, v: string): void }
  head: { lastElementChild?: El | null; appendChild(e: El): unknown; insertBefore(e: El, ref: El | null): unknown }
  getElementById(id: string): El | null
  createElement(tag: string): El
}

export function domFor(doc: Doc): Dom {
  const hrefs = new Map<string, string>()
  // Neon's rules beat the calm base at equal specificity by coming later, so the theme's stylesheet must be the last one. The boot
  // script's link is, in the built page; where the app's CSS is added after it (dev), an equal link goes last and takes over the id
  // once it has loaded, so the early one serves until then. Dropped if the href changed or the link went away in the meantime.
  const lastly = (cur: El, href: string) => {
    const next = doc.createElement('link')
    next.rel = 'stylesheet'
    next.href = href
    next.onload = () => {
      if (doc.getElementById(IDS.css) === cur && hrefs.get(IDS.css) === href) {
        cur.remove()
        next.id = IDS.css
      } else next.remove()
    }
    doc.head.appendChild(next)
  }
  return {
    setAttr: (name, v) => doc.documentElement.setAttribute(name, v),
    style(css) {
      const cur = doc.getElementById(IDS.style)
      if (css === null) return cur?.remove()
      const el = cur ?? doc.createElement('style')
      if (!cur) {
        el.id = IDS.style
        doc.head.appendChild(el)
      }
      if (el.textContent !== css) el.textContent = css
    },
    link(id, href) {
      const cur = doc.getElementById(id)
      if (href === null) {
        hrefs.delete(id)
        return cur?.remove()
      }
      // the boot script may have made this link already: the same href is adopted, not written again (a write reloads the sheet)
      if (cur && (hrefs.get(id) === href || cur.getAttribute?.('href') === href)) {
        const adopted = hrefs.get(id) !== href
        hrefs.set(id, href)
        if (adopted && id === IDS.css && doc.head.lastElementChild && doc.head.lastElementChild !== cur) lastly(cur, href)
        return
      }
      const el = cur ?? doc.createElement('link')
      if (!cur) {
        el.id = id
        el.rel = 'stylesheet'
        doc.head.appendChild(el)
      }
      el.href = href
      hrefs.set(id, href)
    },
  }
}

// Default's own selectors have the same specificity as a cached or applied Default theme's, so the fallback
// must come first in the document or it would beat the theme (a picked accent turning back to Teal).
export function installFallback(doc: Doc, css: string): void {
  if (doc.getElementById(IDS.fallback)) return
  const el = doc.createElement('style')
  el.id = IDS.fallback
  el.textContent = css
  doc.head.insertBefore(el, doc.getElementById(IDS.style))
}
