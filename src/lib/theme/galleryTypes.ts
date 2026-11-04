// The shapes the Themes gallery API answers with (GET /api/gallery). The server is the source of these; the UI only reads them.
import type { ThemeManifest } from './types.ts'

/** One theme in the gallery index */
export interface IndexEntry {
  id: string
  name: string
  author: string
  version: string
  description: string
  /** The commit the theme is pinned at (40 hex) and the sha256 of its theme.json at that commit */
  commit: string
  sha256: string
  files?: string[]
  manifest: ThemeManifest
}

/** What GET /api/gallery answers */
export type GalleryAnswer = { enabled: boolean; repo: string | null; checkedAt?: number; themes: IndexEntry[]; dropped?: number; error?: string }
