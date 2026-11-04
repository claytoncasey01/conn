// The Themes gallery requests. Not post() in live.svelte.ts: a refused install is an HTTP error with its reason in the body (as myPrsApi.ts does).
import type { IndexEntry } from './galleryTypes.ts'
import type { GalleryAnswer } from './galleryTypes.ts'
import { parseEntryAction } from '../entryAction.ts'

const message = (e: unknown) => (e instanceof Error ? e.message : 'request failed')
const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

/** An answer that is only a reason: the page shows it where it would list themes. */
const failed = (error: string): GalleryAnswer => ({ enabled: true, repo: null, themes: [], error })

/** The index and whether the gallery is on. Never throws: a failure is an answer with an `error` and no themes. */
export async function loadGallery(): Promise<GalleryAnswer> {
  try {
    const r = await fetch('/api/gallery')
    const body: unknown = await r.json().catch(() => null)
    if (!r.ok) return failed(isObject(body) && typeof body.error === 'string' && body.error ? body.error : `HTTP ${r.status}`)
    if (!isObject(body) || typeof body.enabled !== 'boolean' || !Array.isArray(body.themes)) return failed('the gallery answered with something unexpected')
    // only an entry that is an object with an id and a manifest of that same id is kept: the page reads these without a check
    const themes = (body.themes as unknown[]).filter((t): t is IndexEntry => isObject(t) && typeof t.id === 'string' && typeof t.commit === 'string' && typeof t.version === 'string' && isObject(t.manifest) && t.manifest.id === t.id)
    const dropped = body.themes.length - themes.length
    return { ...(body as unknown as GalleryAnswer), themes, ...(dropped > 0 && { dropped: (typeof body.dropped === 'number' ? body.dropped : 0) + dropped }) }
  } catch (e) {
    return failed(message(e))
  }
}

async function send(path: string, id: string): Promise<{ ok: boolean; error?: string }> {
  try {
    const r = await fetch(path, {
      method: 'POST',
      headers: { 'x-conn': '1', 'content-type': 'application/json' },
      body: JSON.stringify({ id }),
    })
    const { ok, error } = parseEntryAction(r.status, await r.json().catch(() => null))
    return { ok, ...(error && { error }) }
  } catch (e) {
    return { ok: false, error: message(e) }
  }
}

/** INSTALL or UPDATE: fetch the theme at the index's pinned commit. Never throws. */
export const installTheme = (id: string) => send('/api/gallery/install', id)

/** UNINSTALL a gallery theme. Never throws. */
export const uninstallTheme = (id: string) => send('/api/gallery/uninstall', id)
