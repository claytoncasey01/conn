// Color parsing and WCAG contrast with no dependencies and no DOM, so the server,
// the loader and `conn theme check` all measure the same way.
export type Rgba = { r: number; g: number; b: number; a: number } // r g b 0-255, a 0-1

// CSS grammar, not JS: whitespace is only these five (JS \s also matches no-break space),
// and a decimal point must be followed by digits.
const WS = String.raw`[ \t\n\r\f]`
export const cssTrim = (s: string) => s.replace(new RegExp(`^${WS}+|${WS}+$`, 'g'), '')
const NUM = String.raw`[+-]?(?:\d+(?:\.\d+)?|\.\d+)`
const num = new RegExp(`^${NUM}$`)
const P = String.raw`[^ \t\n\r\f/,]+`
const COMMA_FORM = new RegExp(`^(${P})${WS}*,${WS}*(${P})${WS}*,${WS}*(${P})(?:${WS}*,${WS}*(${P}))?$`)
const SPACE_FORM = new RegExp(`^(${P})${WS}+(${P})${WS}+(${P})(?:${WS}*/${WS}*(${P}))?$`)
const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n))

// "50%" -> 0.5 of `scale`; a bare number is taken as is. null when it is neither.
function component(s: string, scale: number): number | null {
  if (s.endsWith('%')) {
    const n = s.slice(0, -1)
    return num.test(n) ? (Number(n) / 100) * scale : null
  }
  return num.test(s) ? Number(s) : null
}

function hue(s: string): number | null {
  const m = new RegExp(`^(${NUM})(deg)?$`).exec(s)
  return m ? Number(m[1]) : null
}

function alpha(s: string | undefined): number | null {
  if (s === undefined) return 1
  const n = component(s, 1)
  return n === null ? null : clamp(n, 0, 1)
}

const gamma = (x: number) => (x <= 0.0031308 ? 12.92 * x : 1.055 * x ** (1 / 2.4) - 0.055)

// OKLCH -> OKLab -> linear sRGB by the standard matrices, clamped, no gamut mapping.
function oklch(L: number, C: number, h: number, a: number): Rgba {
  const hr = (h * Math.PI) / 180
  const A = C * Math.cos(hr)
  const B = C * Math.sin(hr)
  const l = (L + 0.3963377774 * A + 0.2158037573 * B) ** 3
  const m = (L - 0.1055613458 * A - 0.0638541728 * B) ** 3
  const s = (L - 0.0894841775 * A - 1.291485548 * B) ** 3
  const lin = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ]
  const [r, g, b] = lin.map((x) => Math.round(gamma(clamp(x, 0, 1)) * 255))
  return { r, g, b, a }
}

function hsl(h: number, s: number, l: number, a: number): Rgba {
  const k = (n: number) => (n + h / 30) % 12
  const q = s * Math.min(l, 1 - l)
  const f = (n: number) => l - q * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)))
  return { r: Math.round(f(0) * 255), g: Math.round(f(8) * 255), b: Math.round(f(4) * 255), a }
}

export function parseColor(v: string): Rgba | null {
  const s = cssTrim(v)
  const hex = /^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(s)
  if (hex) {
    let h = hex[1]
    if (h.length <= 4) h = [...h].map((c) => c + c).join('')
    const byte = (i: number) => parseInt(h.slice(i, i + 2), 16)
    return { r: byte(0), g: byte(2), b: byte(4), a: h.length === 8 ? byte(6) / 255 : 1 }
  }
  const fn = new RegExp(`^(rgba?|hsla?|oklch)\\(${WS}*([^()]*?)${WS}*\\)$`, 'i').exec(s)
  if (!fn) return null
  // comma form is legacy CSS: rgb and hsl only. Space form is exactly three parts and an optional `/ alpha`.
  const legacy = fn[1].toLowerCase() !== 'oklch'
  const m = (legacy && COMMA_FORM.exec(fn[2])) || SPACE_FORM.exec(fn[2])
  if (!m) return null
  const parts = m.slice(1).filter((p) => p !== undefined)
  const a = alpha(parts[3])
  if (a === null) return null
  const kind = fn[1].toLowerCase().slice(0, 3)
  if (kind === 'rgb') {
    const c = parts.slice(0, 3).map((p) => component(p, 255))
    if (c.some((n) => n === null)) return null
    const [r, g, b] = (c as number[]).map((n) => Math.round(clamp(n, 0, 255)))
    return { r, g, b, a }
  }
  if (kind === 'hsl') {
    const h = hue(parts[0])
    const sat = parts[1].endsWith('%') ? component(parts[1], 1) : null
    const lig = parts[2].endsWith('%') ? component(parts[2], 1) : null
    if (h === null || sat === null || lig === null) return null
    return hsl(((h % 360) + 360) % 360, clamp(sat, 0, 1), clamp(lig, 0, 1), a)
  }
  const L = component(parts[0], 1)
  const C = component(parts[1], 0.4)
  const h = hue(parts[2])
  if (L === null || C === null || h === null) return null
  return oklch(clamp(L, 0, 1), Math.max(0, C), h, a)
}

// `fg` laid over `bg`; the result is opaque, as `bg` is taken to be.
export function composite(fg: Rgba, bg: Rgba): Rgba {
  const mix = (f: number, b: number) => Math.round(f * fg.a + b * (1 - fg.a))
  return { r: mix(fg.r, bg.r), g: mix(fg.g, bg.g), b: mix(fg.b, bg.b), a: 1 }
}

function luminance({ r, g, b }: Rgba): number {
  const lin = (c: number) => {
    const x = c / 255
    return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

// WCAG 2.x ratio, 1 to 21; a translucent `fg` is composited over `bg` first.
export function contrast(fg: Rgba, bg: Rgba): number {
  const a = luminance(composite(fg, bg))
  const b = luminance(bg)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}
