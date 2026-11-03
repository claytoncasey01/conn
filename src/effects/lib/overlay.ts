// What the CRT tube effects (scanlines, vignette, flicker, roll, grain) share: option parsing, one pointer-proof element, and the
// one-instance-per-effect rule. A file in lib/ is never taken for an effect by the registry glob.
import type { EffectOptions } from './types.ts'

// The dark of a scanline or a flicker veil: the theme's own bg pulled most of the way to black, so no color is hard-coded
export function darkOf(tokens: Readonly<Record<string, string>>): string {
  return `color-mix(in srgb, ${tokens.bg || 'black'} 25%, black)`
}

// An absent option takes the default, a wrong type throws, a number outside the range is clamped to it (the caps are the safety limits)
export function numberOption(options: EffectOptions, key: string, fallback: number, lo: number, hi: number, who: string): number {
  const v = options[key]
  if (v === undefined) return fallback
  if (typeof v !== 'number' || !Number.isFinite(v)) throw new Error(`${who}: ${key} must be a number`)
  return Math.min(hi, Math.max(lo, v))
}

// An on/off option: absent takes the default, anything but a boolean throws (a theme cannot pass "yes" for true)
export function booleanOption(options: EffectOptions, key: string, fallback: boolean, who: string): boolean {
  const v = options[key]
  if (v === undefined) return fallback
  if (typeof v !== 'boolean') throw new Error(`${who}: ${key} must be true or false`)
  return v
}

export function createOverlay(root: HTMLElement, css: string): HTMLElement {
  const el = document.createElement('div')
  el.style.cssText = `position:absolute;inset:0;pointer-events:none;${css}`
  el.setAttribute('aria-hidden', 'true')
  root.appendChild(el)
  return el
}

// Two mounts of one effect do not stack: the first to claim holds it until its own release, a second claim gets null.
// A release frees only its own claim, once: a stop called again after a newer mount claimed leaves that mount's hold alone.
export function createSlot(): { claim(): (() => void) | null } {
  let held = false
  return {
    claim() {
      if (held) return null
      held = true
      let live = true
      return () => {
        if (!live) return
        live = false
        held = false
      }
    },
  }
}
