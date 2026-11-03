// Which theme, mode and stylesheet a stored choice means against the server's list. Pure.
import { needsConsent } from './consent.ts'
import { themeCss, themeTokens, variantOf } from './css.ts'
import { ID } from './storage.ts'
import type { Choice } from './storage.ts'
import type { Mode, ThemeEntry, ThemeManifest, TokenSet } from './types.ts'

export type Resolved = {
  entry: ThemeEntry
  mode: Mode
  only: Mode | null
  attrs: { motion: 'calm' | 'power'; led: 'circle' | 'diamond' | 'square'; glass: 'on' | 'off' }
  css: string
  /** The merged tokens for `mode` (the values the stylesheet declares), for effects */
  tokens: Readonly<TokenSet>
  fonts: string[]
  cssUrl: string | null
  fellBack: boolean
}

// Ids are not unique in the list (a rejected user folder named `default` is listed beside the built-in),
// so the key is (source, id): only a valid entry counts, and the built-in wins a collision.
export function pick(list: ThemeEntry[], id: string): (ThemeEntry & { manifest: ThemeManifest }) | null {
  if (!ID.test(id)) return null
  const hits = list.filter((e): e is ThemeEntry & { manifest: ThemeManifest } => e.id === id && !e.invalid && !!e.manifest)
  return hits.find((e) => e.source === 'builtin') ?? hits[0] ?? null
}

const oneOf = <T extends string>(v: string | undefined, allowed: readonly T[], dflt: T): T => (allowed.includes(v as T) ? (v as T) : dflt)

export function resolveTheme(args: {
  list: ThemeEntry[]
  choice: Choice
  systemDark: boolean
  accepted: Record<string, string>
  fallback: ThemeManifest
}): Resolved {
  const { list, choice, systemDark, accepted, fallback } = args
  const found = pick(list, choice.id)
  const fellBack = !found
  const entry: ThemeEntry & { manifest: ThemeManifest } = found ?? pick(list, fallback.id) ?? {
    id: fallback.id, source: 'builtin', hasCss: false, cssSha256: null, manifest: fallback,
  }
  const m = entry.manifest
  const only = m.modes.length === 1 ? m.modes[0] : null
  const mode: Mode = only ?? (choice.mode === 'system' ? (systemDark ? 'dark' : 'light') : choice.mode)
  const variant = variantOf(m, choice.variant)
  const tokens: TokenSet = {
    ...(fallback.tokens[mode] ?? fallback.tokens.dark ?? fallback.tokens.light),
    ...m.tokens[mode],
    ...variant?.tokens,
  }
  const blur = Number.parseFloat(tokens.blur ?? '0')
  const pickOf = {
    ...(choice.accent && { accent: { hue: choice.accent.hue, chroma: choice.accent.chroma } }),
    ...(choice.variant && { variant: choice.variant }),
  }
  const css = themeCss(m, fallback, pickOf)
  const served = entry.hasCss && entry.cssSha256 && (entry.source === 'builtin' || (entry.source === 'user' && !needsConsent(entry, accepted)))
  return {
    entry,
    mode,
    only,
    attrs: {
      motion: oneOf(tokens.motion, ['calm', 'power'], 'calm'),
      led: oneOf(tokens.led, ['circle', 'diamond', 'square'], 'circle'),
      glass: blur > 0 ? 'on' : 'off',
    },
    css,
    tokens: themeTokens(m, fallback, mode, pickOf),
    fonts: m.fonts,
    cssUrl: served ? `/api/themes/${entry.id}/theme.css?v=${entry.cssSha256}` : null,
    fellBack,
  }
}
