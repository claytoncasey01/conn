// The browser side of the effects host: the real layer, clock and frame source. Not unit tested (it is the DOM);
// everything with logic in it is in lib/ and takes these as arguments.
import { reduced } from '../lib/fx.ts'
import { createEffectsHost } from './lib/host.ts'
import type { EffectsHost } from './lib/host.ts'
import type { EffectEnv, EffectLayer } from './lib/types.ts'
import { registry } from './registry.ts'

export const LAYER_ID = 'conn-effects'
export const ABOVE_LAYER_ID = 'conn-effects-above'

// Behind the page and never over a control. z-index -1 only sits behind the page while nothing between the
// layer and the canvas paints an opaque background over it: the page must leave its background to the body.
const BEHIND_STYLE = 'position:fixed;inset:0;pointer-events:none;z-index:-1'
// Over the page and still never over a control: pointer-events:none lets every click and key through. 9000 clears
// the highest z-index in the app (6, the sticky footer and the pages' status bars).
const ABOVE_STYLE = 'position:fixed;inset:0;pointer-events:none;z-index:9000'

export function effectsLayer(which: EffectLayer): HTMLElement {
  const id = which === 'above' ? ABOVE_LAYER_ID : LAYER_ID
  const found = document.getElementById(id)
  if (found) return found
  const el = document.createElement('div')
  el.id = id
  el.setAttribute('aria-hidden', 'true')
  el.style.cssText = which === 'above' ? ABOVE_STYLE : BEHIND_STYLE
  document.body.appendChild(el)
  return el
}

const env = (): EffectEnv => ({
  reduced: reduced(),
  hidden: () => document.hidden,
  raf: (cb) => requestAnimationFrame(cb),
  caf: (id) => cancelAnimationFrame(id),
  now: () => performance.now(),
})

export const browserEffectsHost = (): EffectsHost => createEffectsHost({ registry, layer: effectsLayer, env })
