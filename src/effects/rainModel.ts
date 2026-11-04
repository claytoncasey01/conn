// The pure part of the rain effect: where each column of glyphs is and how it falls. No canvas, no clock, no DOM.

// Half-width katakana (mirrored in the font file) and the digits, the whole set the font carries
export const GLYPHS: string[] = [
  ...Array.from({ length: 0xff9d - 0xff66 + 1 }, (_, i) => String.fromCharCode(0xff66 + i)),
  ...'0123456789',
]

export type Column = {
  x: number
  /** px of the head from the top of the canvas; negative while the column is still above it */
  y: number
  /** px per second at speed 1 */
  speed: number
  /** 0 is far (slower), 1 is near (faster) */
  depth: 0 | 1
  glyphs: string[]
}

// One column per this many px of width at density 1
const COLUMN_PX = 14
const POOL = 32
const SPEED = { 0: [60, 110], 1: [130, 220] } as const

const pick = (rng: () => number) => GLYPHS[Math.floor(rng() * GLYPHS.length) % GLYPHS.length]
const fresh = (rng: () => number) => Array.from({ length: POOL }, () => pick(rng))

export function createColumns(width: number, height: number, density: number, rng: () => number): Column[] {
  const n = Math.round(Math.max(0, Math.min(1, density)) * width / COLUMN_PX)
  return Array.from({ length: n }, (_, i) => {
    const depth = rng() < 0.5 ? 0 : 1
    const [lo, hi] = SPEED[depth]
    return { x: ((i + 0.5) * width) / n, y: rng() * height, speed: lo + rng() * (hi - lo), depth, glyphs: fresh(rng) }
  })
}

export function stepColumns(cols: Column[], dtMs: number, height: number, speed: number, rng: () => number): void {
  for (const c of cols) {
    c.y += (c.speed * speed * dtMs) / 1000
    // past the bottom: back to somewhere above the top, with new glyphs
    if (c.y > height) {
      c.y = -rng() * height * 0.5
      c.glyphs = fresh(rng)
    }
  }
}
