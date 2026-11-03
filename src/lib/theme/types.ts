export type Mode = 'light' | 'dark'
export type ModeSetting = Mode | 'system'
export type TokenSet = Record<string, string>
export type ThemeSource = 'builtin' | 'user' | 'gallery'

export interface EffectSpec {
  name: string
  options?: Record<string, number | string | boolean>
}

export interface ThemeManifest {
  id: string
  name: string
  version: string
  author: string
  license: string
  description: string
  modes: Mode[]
  accent: 'free' | 'fixed' | { variants: { name: string; tokens: TokenSet }[] }
  fonts: string[]
  tokens: Partial<Record<Mode, TokenSet>>
  effects: EffectSpec[]
}

export type ThemeEntry = { id: string; source: ThemeSource; hasCss: boolean; cssSha256: string | null } & (
  | { manifest: ThemeManifest; invalid?: undefined }
  | { manifest?: undefined; invalid: string }
)
