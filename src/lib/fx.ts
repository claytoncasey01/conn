import { cubicOut } from 'svelte/easing'

/** The one reading of prefers-reduced-motion: the transitions here and the theme effects both go by it */
export const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
const clamp = (n: number) => Math.min(1, Math.max(0, n))

/**
 * Moving to another session, agent or page is instant: for this long after a navigation every transition here is skipped, the
 * outgoing page's too. What still animates is a live change on a page that stays (an agent appearing, a row arriving).
 */
export const NAV_QUIET_MS = 1000
let navAt = -Infinity
/** The router calls this before it changes the route: a listener of its own here would run after the page had already rendered. */
export const noteNavigation = (at: number = Date.now()) => { navAt = at }
const still = () => reduced() || Date.now() - navAt < NAV_QUIET_MS

/**
 * The theme's `motion` token, as <html data-motion>: `power` is Neon's line, flash and flicker; anything else
 * (Default's `calm`, a missing or unknown value) is a plain fade, the side with less motion. Read per transition, so a
 * theme switch applies to the next one.
 */
const powered = () => typeof document !== 'undefined' && document.documentElement?.dataset?.motion === 'power'
const fade = (duration: number, delay = 0) => ({ delay, duration, css: (t: number) => `opacity: ${t};` })

/**
 * Boot-up and power-down for a session (power motion): a thin line of light draws across, then the panel opens
 * from it (and the same in reverse on the way out) under a brightness flash and a few scan flickers.
 */
function power(opts: { duration: number; delay?: number; pad?: number }) {
  if (still()) return { duration: 0 }
  const { duration, delay = 0, pad = 0 } = opts
  return {
    delay,
    duration,
    css: (t: number) => {
      const w = cubicOut(clamp(t / 0.42))
      const h = cubicOut(clamp((t - 0.38) / 0.62))
      const side = (1 - w) * 50
      const vert = (1 - h) * 49.5
      const flicker = t > 0.38 && t < 0.8 ? (Math.sin(t * 90) > 0.35 ? 0.78 : 1) : 1
      const glow = (1 - t) * 1.6
      return (
        `clip-path: inset(${vert}% ${side}% ${vert}% ${side}% round calc(var(--radius) * 1.5));` +
        `opacity: ${clamp(t * 6) * flicker};` +
        `filter: brightness(${1 + glow}) saturate(${1 + glow * 0.6}) drop-shadow(0 0 ${(1 - h) * 14 + 2}px color-mix(in oklch, var(--ac) calc(${0.55 * (1 - t) + 0.05} * 100% * var(--glow)), transparent));` +
        `transform: scale(${0.97 + 0.03 * t}) translateZ(0);`
      )
    },
  }
}

export const bootIn = (_: Element, o: { index?: number; skip?: boolean } = {}) => {
  if (o.skip || still()) return { duration: 0 }
  return powered() ? power({ duration: 650, delay: Math.min((o.index ?? 0) * 70, 350) }) : fade(240, Math.min((o.index ?? 0) * 40, 200))
}
export const bootOut = (_: Element) => (still() ? { duration: 0 } : powered() ? power({ duration: 480 }) : fade(180))

/** A row in a list: a bright wipe sweeps in from the left; on the way out it sweeps back. */
function sweep(duration: number) {
  if (still()) return { duration: 0 }
  return {
    duration,
    css: (t: number) => {
      const e = cubicOut(t)
      return (
        `clip-path: inset(0 ${(1 - e) * 100}% 0 0);` +
        `opacity: ${clamp(t * 4)};` +
        `filter: brightness(${1 + (1 - t) * 1.2}) drop-shadow(0 0 ${(1 - t) * 8}px color-mix(in oklch, var(--ac) calc(60% * var(--glow)), transparent));` +
        `transform: translateX(${(1 - e) * -10}px);`
      )
    },
  }
}
export const rowIn = (_: Element, o: { skip?: boolean } = {}) => (o.skip || still() ? { duration: 0 } : powered() ? sweep(380) : fade(200))
export const rowOut = (_: Element) => (still() ? { duration: 0 } : powered() ? sweep(260) : fade(160))

/** Wiring and other thin graphics: they light up and fade, no clipping. */
function glow(duration: number) {
  if (still()) return { duration: 0 }
  return {
    duration,
    css: (t: number) => `opacity: ${t}; filter: brightness(${1 + (1 - t) * 1.6}) drop-shadow(0 0 ${(1 - t) * 10}px color-mix(in oklch, var(--ac) calc(70% * var(--glow)), transparent));`,
  }
}
export const glowIn = (_: Element, o: { skip?: boolean } = {}) => (o.skip || still() ? { duration: 0 } : powered() ? glow(600) : fade(240))
export const glowOut = (_: Element) => (still() ? { duration: 0 } : powered() ? glow(420) : fade(180))
