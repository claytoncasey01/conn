// Every look the built-in themes can paint: each theme folder, its modes and its variants, as the stylesheet resolves them
// (derived slots included), for the tests that hold a rule to all of them.
import { readdirSync, readFileSync } from 'node:fs'
import { themeTokens } from '../src/lib/theme/css.ts'
import { parseManifest } from '../src/lib/theme/validate.ts'
import type { Mode, ThemeManifest, TokenSet } from '../src/lib/theme/types.ts'

const root = new URL('.', import.meta.url).pathname
const folders = readdirSync(root, { withFileTypes: true })
  .filter((d) => d.isDirectory() && !d.name.startsWith('_'))
  .map((d) => d.name)
  .sort()
const ctx = { source: 'builtin' as const, builtinIds: new Set(folders) }

function load(id: string): ThemeManifest {
  const r = parseManifest(readFileSync(`${root}${id}/theme.json`, 'utf8'), ctx)
  if (!r.ok) throw new Error(`${id}: ${r.errors.join('; ')}`)
  return r.manifest
}

export type Look = { name: string; id: string; mode: Mode; variant: string | null; tokens: Readonly<TokenSet> }

export function looks(): Look[] {
  const fallback = load('default')
  return folders.flatMap((id) => {
    const m = load(id)
    const picks = typeof m.accent === 'object' ? m.accent.variants.map((v) => v.name) : [null]
    return m.modes.flatMap((mode) =>
      picks.map((variant): Look => ({
        name: `${id} ${mode}${variant ? ` ${variant}` : ''}`,
        id,
        mode,
        variant,
        tokens: themeTokens(m, fallback, mode, variant ? { variant } : undefined),
      })),
    )
  })
}
