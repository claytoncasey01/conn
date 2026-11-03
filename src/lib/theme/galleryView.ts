// What the Themes page says about a card and about the index, as plain values. Pure: the page only lays them out.
import type { GalleryAnswer } from './galleryTypes.ts'
import { ago } from '../format.ts'
import type { Card } from './galleryModel.ts'
import type { Mode, ThemeEntry, ThemeManifest } from './types.ts'

/** The manifest a card previews: the installed one when it is installed (the card's own source decides between same-id entries), else the index's. Null for a broken install. */
export function manifestOf(c: Card, installed: ThemeEntry[]): ThemeManifest | null {
  if (c.invalid !== undefined) return null
  const source = c.source === 'community' ? null : c.source
  const own = source && installed.find((e) => e.id === c.id && e.source === source && e.manifest)
  return own?.manifest ?? c.entry?.manifest ?? null
}

/** The mode a card previews: the one on screen when the theme has it, else its first */
export const previewMode = (m: ThemeManifest, on: Mode): Mode => (m.modes.includes(on) ? on : m.modes[0])

export const modesNote = (modes: Mode[]): string => (modes.length > 1 ? 'Light and dark' : modes[0] === 'light' ? 'Light only' : 'Dark only')

export const accentNote = (m: ThemeManifest): string =>
  m.accent === 'free' ? 'Any accent' : m.accent === 'fixed' ? 'Fixed accent' : `Variants: ${m.accent.variants.map((v) => v.name).join(', ')}`

const SOURCES: Record<Card['source'], string> = { builtin: 'Built in', gallery: 'Installed from the index', user: 'Your own theme', community: 'Community theme' }
export const sourceNote = (s: Card['source']): string => SOURCES[s]

/** A community card is never the theme on screen; the others are when id and source both match the resolved entry */
export const isCurrent = (c: Card, on: { id: string; source: string }): boolean => c.source !== 'community' && c.id === on.id && c.source === on.source

/** Why the index is not listing anything, or null. For an error `reason` is the server's own words. */
export function notice(a: GalleryAnswer | null): { kind: 'off' | 'error'; text: string; reason?: string } | null {
  if (!a) return null
  if (!a.enabled) return { kind: 'off', text: 'The gallery is turned off (CONN_THEME_INDEX=off).', reason: 'Installed themes still work. Installing and uninstalling are unavailable.' }
  // the server words a missing index.json as "(404 not found)": the host answered, so the repo is there but has nothing published yet
  if (a.error && a.repo && /\(404 not found\)$/.test(a.error)) return { kind: 'error', text: `${a.repo} has no theme index yet. Installed themes still work.`, reason: a.error }
  if (a.error) return { kind: 'error', text: 'The theme index cannot be reached. Installed themes still work.', reason: a.error }
  return null
}

/** The index repo and when it was read; null when there is no repo to name (off, or a request that never reached the server) */
export function indexLine(a: GalleryAnswer | null, now: number): { repo: string; when: string } | null {
  if (!a || !a.enabled || !a.repo) return null
  return { repo: a.repo, when: a.checkedAt && !a.error ? `checked ${ago(a.checkedAt, now)}` : 'not read just now' }
}
