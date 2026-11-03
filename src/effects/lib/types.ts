// What a theme's effect is allowed to be: a named module that draws into one of two layers, behind the page or above it.
// A theme only ever supplies a name and options (data), never code.
export type EffectOptions = Record<string, number | string | boolean>

/** Everything an effect touches outside its own root is behind this, so an effect is testable with fakes */
export interface EffectEnv {
  /** prefers-reduced-motion: the host passes it through, the effect decides what to do */
  reduced: boolean
  hidden(): boolean
  raf(cb: (t: number) => void): number
  caf(id: number): void
  now(): number
}

/**
 * Returns what undoes the mount: it must leave nothing in `root` and no listener or timer behind.
 * Validate options before any side effect: a rAF loop or listener started before a throw is out of the host's reach (it only removes the nodes a failed mount added).
 */
export type Mount = (root: HTMLElement, options: EffectOptions, tokens: Readonly<Record<string, string>>, env: EffectEnv) => () => void

/** `behind` is under the page (rain); `above` is over everything, never taking an event (a CRT tube's scanlines) */
export type EffectLayer = 'behind' | 'above'

export interface EffectModule {
  name: string
  mount: Mount
  /** `export const layer`: where the effect draws. Missing, or anything but 'above', is behind. */
  layer?: EffectLayer
}

export type RegisteredEffect = { mount: Mount; layer: EffectLayer }
