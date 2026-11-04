// What the header menu shows for the server's list and the stored choice's id. Pure.
import { needsConsent } from './consent.ts'
import { pick } from './resolve.ts'
import type { Pin } from './storage.ts'
import type { ModeSetting, ThemeEntry, ThemeSource } from './types.ts'

export type MenuTheme = {
  /** `source:id`. Ids repeat (a rejected user folder named like a built-in is listed beside it), so only this is a safe key */
  key: string
  id: string
  source: ThemeSource
  label: string
  disabled: boolean
  /** Why an invalid theme cannot be picked */
  hint: string | null
  customCss: boolean
  /** The CSS hash the row was listed with; what a consent prompt is about */
  cssSha256: string | null
  /** The row the resolver would pick for the active id (the built-in, when ids collide) */
  active: boolean
}

export type MenuModel = {
  themes: MenuTheme[]
  modeEnabled: boolean
  accent: 'free' | 'variants' | 'none'
  variants: string[]
}

export function menuModel(list: ThemeEntry[], activeId: string): MenuModel {
  const on = pick(list, activeId)
  // The list is the server's: a repeated source:id (two long folder names clipped alike) gets a suffix, so the keyed each never throws
  const seen = new Map<string, number>()
  const unique = (k: string) => {
    const n = seen.get(k) ?? 0
    seen.set(k, n + 1)
    return n ? `${k}#${n}` : k
  }
  const themes = list.map((e): MenuTheme => ({
    key: unique(`${e.source}:${e.id}`),
    id: e.id,
    source: e.source,
    label: e.manifest?.name ?? e.id,
    disabled: e.invalid !== undefined,
    hint: e.invalid ?? null,
    customCss: e.source === 'user' && e.hasCss,
    cssSha256: e.cssSha256,
    active: e === on,
  }))
  const a = on?.manifest.accent
  // first of a name kept, as the resolver's find does; two radios of one value would both read as checked
  const variants = typeof a === 'object' ? [...new Set(a.variants.map((v) => v.name))] : []
  return {
    themes,
    modeEnabled: !on || on.manifest.modes.length > 1,
    accent: a === 'free' ? 'free' : variants.length ? 'variants' : 'none',
    variants,
  }
}

// The radio to check: the stored variant when the theme lists it, else the first (what the resolver paints for an unset or unknown one)
export const shownVariant = (variants: string[], stored: string | null): string | null =>
  stored !== null && variants.includes(stored) ? stored : (variants[0] ?? null)

/**
 * The active row, when it is a user theme whose CSS is not the hash accepted for it (edited since): the page has dropped that
 * CSS, and picking the same row again fires no change, so the menu offers the prompt itself. Null when nothing needs review.
 */
export function needsReview(model: MenuModel, list: ThemeEntry[], accepted: Record<string, string>): MenuTheme | null {
  const row = model.themes.find((t) => t.active)
  if (!row?.customCss) return null
  const e = pick(list, row.id)
  return e && needsConsent(e, accepted) ? row : null
}

/** What a consent prompt is about: the theme and the exact CSS the person was shown */
export type Pending = { id: string; label: string; sha: string | null }
export type Accept = 'ok' | 'changed' | 'gone'

/**
 * "Use anyway" accepts only the CSS that was on screen when the prompt opened. The list is re-read on every window focus, so by
 * then the theme may be gone, or its CSS (or its source) may be another one: nothing is remembered for that.
 */
export function canAccept(p: Pending, list: ThemeEntry[]): Accept {
  const e = pick(list, p.id)
  if (!e) return 'gone'
  return e.source === 'user' && e.hasCss && e.cssSha256 !== null && e.cssSha256 === p.sha ? 'ok' : 'changed'
}

/**
 * What the menu says when the link (?theme=, ?mode=) is why the page looks as it does: what the link shows, and what a change
 * here does (any change drops the pin and goes back to the saved choice). Null when there is no pin, or it only repeats the saved choice.
 */
export function pinNote(pin: Pin | null, shown: { theme: string; mode: ModeSetting }, saved: { id: string; theme: string; mode: ModeSetting }): string | null {
  const id = pin?.id !== undefined && pin.id !== saved.id
  const mode = pin?.mode !== undefined && pin.mode !== saved.mode
  const tail = 'Changing a setting here goes back to your saved'
  if (id && mode) return `The link you opened shows ${shown.theme} in ${shown.mode} mode. ${tail} choice, ${saved.theme} in ${saved.mode} mode.`
  if (id) return `The link you opened shows ${shown.theme}. ${tail} theme, ${saved.theme}.`
  if (mode) return `The link you opened shows ${shown.mode} mode. ${tail} mode, ${saved.mode}.`
  return null
}
