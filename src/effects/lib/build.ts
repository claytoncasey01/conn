import type { EffectModule, RegisteredEffect } from './types.ts'

// The registry's filter, apart from the glob so it can be tested: a module is an effect when it exports a
// string `name` and a `mount` function. A helper file beside the effects is just skipped. `layer` is 'above' only
// when the module says exactly that; anything else is behind, the page's original layer.
export function buildRegistry(modules: Record<string, unknown>): Record<string, RegisteredEffect> {
  const kept = Object.values(modules)
    .map((m) => m as Partial<EffectModule> | null)
    .filter((m): m is EffectModule => !!m && typeof m.name === 'string' && typeof m.mount === 'function')
    .map((m): [string, RegisteredEffect] => [m.name, { mount: m.mount, layer: m.layer === 'above' ? 'above' : 'behind' }])
  // fromEntries defines own properties, so a module named __proto__ is a key and not a prototype write
  return Object.fromEntries(kept)
}
