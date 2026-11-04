// Where the agent tree's wires meet a card, in each of the shapes the wire-head token picks from. Pure: the tree draws every shape
// at every tip and the look shows one (lookCss.ts), so a theme changes the heads without a layout change.

/** A wire's end: where it touches the card, and which way the wire runs into it (right, left or down) */
export type Tip = readonly [x: number, y: number, dir: 'r' | 'l' | 'd']
export const HEAD_SHAPES = ['arrow', 'plug', 'dot', 'bar'] as const
export type Heads = Record<(typeof HEAD_SHAPES)[number], string>

// along the wire (negative is back from the tip) and across it, to page coordinates
const frame = ([x, y, d]: Tip) => (along: number, across: number): string =>
  d === 'r' ? `${x + along} ${y + across}` : d === 'l' ? `${x - along} ${y + across}` : `${x + across} ${y + along}`
const poly = (at: (a: number, c: number) => string, pts: [number, number][]) => 'M' + pts.map(([a, c]) => at(a, c)).join(' L') + ' Z'

// the triangle the tree has always drawn, vertex for vertex
function arrow([x, y, d]: Tip): string {
  if (d === 'r') return `M${x - 10} ${y - 6} L${x} ${y} L${x - 10} ${y + 6} Z`
  if (d === 'l') return `M${x + 10} ${y - 6} L${x} ${y} L${x + 10} ${y + 6} Z`
  return `M${x - 6} ${y - 10} L${x + 6} ${y - 10} L${x} ${y} Z`
}
// a connector: a block on the wire and two pins into the card
const plug = (t: Tip) => {
  const at = frame(t)
  return [poly(at, [[-10, -5], [-3, -5], [-3, 5], [-10, 5]]), poly(at, [[-3, -3.5], [0, -3.5], [0, -1.5], [-3, -1.5]]), poly(at, [[-3, 1.5], [0, 1.5], [0, 3.5], [-3, 3.5]])].join(' ')
}
const R = 3.5
const dot = (t: Tip) => {
  const [x, y, d] = t
  const cx = d === 'r' ? x - R : d === 'l' ? x + R : x
  const cy = d === 'd' ? y - R : y
  return `M${cx - R} ${cy} a${R} ${R} 0 1 0 ${2 * R} 0 a${R} ${R} 0 1 0 ${-2 * R} 0`
}
// a bar across the wire where it stops
const bar = (t: Tip) => poly(frame(t), [[-3, -6], [0, -6], [0, 6], [-3, 6]])

export function headsOf(tips: readonly Tip[]): Heads {
  const join = (shape: (t: Tip) => string) => tips.map(shape).join(' ')
  return { arrow: join(arrow), plug: join(plug), dot: join(dot), bar: join(bar) }
}
