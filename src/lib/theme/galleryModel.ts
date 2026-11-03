// The gallery's cards: the index and what is installed, merged into one list. Pure.
import type { IndexEntry } from './galleryTypes.ts'
import type { Mode, ThemeEntry, ThemeManifest } from './types.ts'

export type CardFilter = 'all' | 'installed' | 'community'

export type Card = {
  id: string
  name: string
  author: string
  version: string
  modes: Mode[]
  /** The theme's own accent color, or '' when it has none of its own (a free accent follows the picker) */
  accent: string
  source: 'builtin' | 'gallery' | 'user' | 'community'
  action: 'Use' | 'Install' | 'Update' | 'Installed'
  /** The index entry (what Install and Update fetch), or null for a theme the index no longer lists */
  entry: IndexEntry | null
  /** Why an installed gallery theme cannot be used, when it is broken */
  invalid?: string
}

// What the preview paints as ac: the first variant's over the mode's own (dark first, as a card shows dark). A free accent
// previews its own ac too (an install paints it until a swatch is picked), and a theme with none follows Default, so ''.
function accentOf(m: ThemeManifest): string {
  const mode = m.modes.includes('dark') ? 'dark' : 'light'
  const variant = typeof m.accent === 'object' ? m.accent.variants[0]?.tokens.ac : undefined
  return variant ?? m.tokens[mode]?.ac ?? ''
}

const DOTTED = /^\d+(\.\d+)*$/

// Dotted numbers compare by part ("1.10" is newer than "1.9", "1.0.0" equals "1.0"); null when either is not one.
function compare(a: string, b: string): number | null {
  if (!DOTTED.test(a) || !DOTTED.test(b)) return null
  const x = a.split('.').map(Number)
  const y = b.split('.').map(Number)
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const d = (x[i] ?? 0) - (y[i] ?? 0)
    if (d !== 0) return d
  }
  return 0
}

// Update only when the index is strictly newer by number, so a downgrade or a prerelease is never offered as one. Versions
// that are not both numbers (v2, 1.0.0-beta) have no order: equal strings are Installed, two non-numeric ones that differ
// are Update (the index is the authority), and a number against a non-number is left as Installed.
function wantsUpdate(index: string, installed: string): boolean {
  const c = compare(index, installed)
  if (c !== null) return c > 0
  return index !== installed && !DOTTED.test(index) && !DOTTED.test(installed)
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

// An index entry from the browser's side of the wire is trusted for nothing: the server checks it, but a card must not throw
// on one that got through some other way. It needs an id and a manifest holding what a card and its search read.
function usable(e: unknown): e is IndexEntry {
  // the commit and version are read off the entry itself (the pinned commit line, the update check)
  if (!isObject(e) || typeof e.id !== 'string' || typeof e.commit !== 'string' || typeof e.version !== 'string' || !isObject(e.manifest)) return false
  const m = e.manifest
  return typeof m.name === 'string' && typeof m.author === 'string' && typeof m.version === 'string' && Array.isArray(m.modes) && m.modes.length > 0 && isObject(m.tokens)
}

const card = (id: string, m: ThemeManifest, source: Card['source'], action: Card['action'], entry: IndexEntry | null): Card => ({
  id, name: m.name, author: m.author, version: m.version, modes: m.modes, accent: accentOf(m), source, action, entry,
})

/** Installed themes in the order given, then index themes that are not installed, by name. An installed theme owns its id (an invalid one too). */
export function cardsModel(index: IndexEntry[], installed: ThemeEntry[], filter: CardFilter, query: string): Card[] {
  // a repeated index id is one card: the first wins
  const byId = new Map<string, IndexEntry>()
  for (const e of index as unknown[]) if (usable(e) && !byId.has(e.id)) byId.set(e.id, e)
  const cards: Card[] = []
  const have = new Set<string>()
  for (const t of installed) {
    have.add(t.id)
    const entry = byId.get(t.id) ?? null
    if (!t.manifest) {
      // a broken gallery install keeps a card so it can be uninstalled (or updated over); a broken user or built-in one only holds its id
      if (t.source === 'gallery') {
        cards.push({ id: t.id, name: t.id, author: '', version: '', modes: [], accent: '', source: 'gallery', action: entry ? 'Update' : 'Installed', entry, invalid: t.invalid })
      }
      continue
    }
    if (t.source === 'gallery') cards.push(card(t.manifest.id, t.manifest, 'gallery', entry && wantsUpdate(entry.version, t.manifest.version) ? 'Update' : 'Installed', entry))
    else cards.push(card(t.manifest.id, t.manifest, t.source, 'Use', null))
  }
  const rest = [...byId.values()].filter((e) => !have.has(e.id)).sort((a, b) => a.name.localeCompare(b.name))
  // keyed by the entry's own id: that is what Install sends, and what the page keys on
  for (const e of rest) cards.push(card(e.id, e.manifest, 'community', 'Install', e))
  const q = query.trim().toLowerCase()
  const seen = new Set<string>()
  return cards.filter((c) => {
    // the page keys a card by source and id, so a repeat would throw there
    const key = `${c.source}:${c.id}`
    if (seen.has(key)) return false
    seen.add(key)
    if (filter === 'installed' && c.source === 'community') return false
    if (filter === 'community' && c.source !== 'community' && c.source !== 'gallery') return false
    return !q || c.name.toLowerCase().includes(q) || c.author.toLowerCase().includes(q)
  })
}
