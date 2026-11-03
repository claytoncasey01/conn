// Mounts a theme's effects into up to two fixed layers, one behind the page and one above it, each effect into the
// layer its module names. A theme gives names and options only (data it does not control the shape of), so an
// unknown name is skipped, an option that is not a primitive is dropped, and one effect failing never keeps another,
// or the page, from working.
import type { EffectSpec } from '../../lib/theme/types.ts'
import type { EffectEnv, EffectLayer, EffectOptions, RegisteredEffect } from './types.ts'

export type EffectsHost = {
  apply(effects: EffectSpec[], tokens: Readonly<Record<string, string>>, enabled: boolean): void
  dispose(): void
}

const LAYERS: readonly EffectLayer[] = ['behind', 'above']

export function createEffectsHost(deps: {
  registry: Record<string, RegisteredEffect>
  layer: (which: EffectLayer) => HTMLElement
  env: () => EffectEnv
}): EffectsHost {
  // one root and one stop list per layer: a layer exists only while an effect is wanted in it
  const roots: Record<EffectLayer, HTMLElement | null> = { behind: null, above: null }
  const stops: Record<EffectLayer, (() => void)[]> = { behind: [], above: [] }
  // what is mounted now: apply with the same list, tokens and reduced-motion state is a no-op, not a remount
  let mounted = ''

  const options = (o: unknown): EffectOptions =>
    Object.fromEntries(
      Object.entries(o && typeof o === 'object' ? o : {}).filter(([, v]) => typeof v === 'string' || typeof v === 'boolean' || (typeof v === 'number' && Number.isFinite(v))),
    ) as EffectOptions

  // what a failed mount added: it never handed back a stop, so the host removes the nodes itself
  function removeAddedSince(layer: HTMLElement, before: number) {
    for (let n = layer.children?.length ?? 0; n > before; n--) layer.lastElementChild?.remove()
  }

  function unmountLayer(which: EffectLayer) {
    const was = stops[which]
    stops[which] = []
    for (const stop of was.reverse()) {
      try {
        stop()
      } catch {
        // a stuck effect must not keep the rest running
      }
    }
    // belt for an effect that left a node behind or threw halfway through its own mount
    roots[which]?.replaceChildren?.()
  }

  function unmount() {
    for (const which of LAYERS) unmountLayer(which)
    mounted = ''
  }

  function dropLayer(which: EffectLayer) {
    unmountLayer(which)
    roots[which]?.remove()
    roots[which] = null
  }

  function dropAll() {
    for (const which of LAYERS) dropLayer(which)
    mounted = ''
  }

  const layerOf = (name: string): EffectLayer => (deps.registry[name].layer === 'above' ? 'above' : 'behind')

  return {
    apply(effects, tokens, enabled) {
      const env = deps.env()
      const wanted = enabled ? effects.filter((e) => !!e && typeof e.name === 'string' && Object.hasOwn(deps.registry, e.name)) : []
      if (!wanted.length) return dropAll()
      const needed = LAYERS.filter((which) => wanted.some((e) => layerOf(e.name) === which))
      const key = JSON.stringify([env.reduced, wanted.map((e) => [e.name, options(e.options)]), tokens])
      if (key === mounted && needed.every((which) => roots[which])) return
      unmount()
      // a layer nothing is wanted in any more goes away
      for (const which of LAYERS) if (!needed.includes(which)) dropLayer(which)
      // a layer that cannot be made loses its effects only; the other layer still runs and the next apply tries again
      let complete = true
      for (const which of needed) {
        try {
          roots[which] ??= deps.layer(which)
        } catch {
          complete = false
        }
      }
      const frozen = Object.freeze({ ...tokens })
      for (const e of wanted) {
        const which = layerOf(e.name)
        const layer = roots[which]
        if (!layer) continue
        const before = layer.children?.length ?? 0
        let stop: unknown
        try {
          stop = deps.registry[e.name].mount(layer, options(e.options), frozen, env)
        } catch {
          // a bad option or a bug in one effect skips that effect only
        }
        if (typeof stop === 'function') stops[which].push(stop as () => void)
        // no stop means nothing can remove its nodes later, and `mounted` would keep them until the next change
        else removeAddedSince(layer, before)
      }
      if (complete) mounted = key
    },
    dispose: dropAll,
  }
}
