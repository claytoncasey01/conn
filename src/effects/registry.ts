// The closed effect library: every other src/effects/*.ts that exports a `name` and a `mount` is in. A theme can
// only name one of these, so a theme never brings code. Plans add effects as one file each; nothing here changes. A file picks its layer with `export const layer = 'above'`.
import { buildRegistry } from './lib/build.ts'
import type { RegisteredEffect } from './lib/types.ts'

// Vite rewrites this call at build time. Bun (the unit tests) has no import.meta.glob, so there the library is
// empty rather than a crash. The tests are left out so a *.test.ts never reaches the browser bundle.
function modules(): Record<string, unknown> {
  try {
    return import.meta.glob(['./*.ts', '!./registry.ts', '!./browser.ts', '!./*.test.ts'], { eager: true })
  } catch {
    return {}
  }
}

export const registry: Record<string, RegisteredEffect> = buildRegistry(modules())
