// A user's own theme.css can rewrite any part of the page, so it asks once per exact CSS (its sha256).
// Built-in CSS ships in this repo and a gallery theme never has any, so only a user theme ever asks.
import { KEYS } from './storage.ts'
import type { Reader, Store } from './storage.ts'
import type { ThemeEntry } from './types.ts'

export function needsConsent(e: ThemeEntry, ok: Record<string, string>): boolean {
  if (e.source !== 'user' || !e.hasCss) return false
  return !Object.hasOwn(ok, e.id) || ok[e.id] !== e.cssSha256
}

export function readAccepted(s: Reader | null): Record<string, string> {
  try {
    const raw = s?.getItem(KEYS.cssOk)
    const v = typeof raw === 'string' ? JSON.parse(raw) : null
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {}
    return Object.fromEntries(Object.entries(v).filter(([, h]) => typeof h === 'string')) as Record<string, string>
  } catch {
    return {}
  }
}

export function remember(s: Store | null, e: ThemeEntry): void {
  if (!e.hasCss || !e.cssSha256) return
  try {
    s?.setItem(KEYS.cssOk, JSON.stringify({ ...readAccepted(s), [e.id]: e.cssSha256 }))
  } catch {
    // not stored: the next load asks again
  }
}
