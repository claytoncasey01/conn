// The token contract: every slot a theme may fill, and what a value for it may be.
// Pure data, shared by the server, the loader and `theme:check`.
export type SlotSpec = {
  kind: 'color' | 'length' | 'number' | 'enum' | 'shadow' | 'font'
  min?: number
  max?: number
  unit?: string
  pxOnly?: boolean // a length that bounds a clip: a rem or em would grow with the reader's font past its max
  values?: string[] // enum choices; for a font slot, the generic families it allows besides `end`
  end?: string // a font slot's required last family
}

const color: SlotSpec = { kind: 'color' }
const colors = (...names: string[]): [string, SlotSpec][] => names.map((n) => [n, color])

// A plain object with only own data: lookups go through Object.hasOwn, never `in`.
export const SLOTS: Record<string, SlotSpec> = Object.freeze(
  Object.fromEntries([
    ...colors('bg', 'panel', 'raise', 'sunk', 'hover', 'line', 'line2', 'tx', 'bright', 'mu', 'onac', 'ac', 'ac-soft', 'ac-line'),
    ...colors('ok', 'warn', 'bad', 'idle', 'info'),
    ...colors('k-bug', 'k-epic', 'k-task', 'k-story', 'k-subtask', 'teammate'),
    ...colors('role-lead', 'role-agent', 'role-advisor'),
    // added from Neon's inventory: edge is the chrome hue (borders, focus, hover wash, HUD lines; mostly at an alpha),
    // stall a stalled agent. Default derives them: edge = its mu, stall = warn and bad mixed half and half.
    ...colors('edge', 'stall'),
    // the keyboard focus ring and the text cursor; the meters' track, their lit stretch and the lead's. Each follows a token when
    // a theme leaves it out (ac; line, ac and role-lead)
    ...colors('focus', 'meter-track', 'meter-lit', 'meter-lead'),
    ['font-ui', { kind: 'font', values: ['system-ui', 'ui-sans-serif', 'sans-serif'], end: 'system-ui' }],
    ['font-mono', { kind: 'font', values: ['ui-monospace', 'monospace'], end: 'ui-monospace' }],
    ['case', { kind: 'enum', values: ['none', 'lowercase', 'uppercase'] }],
    ['tracking', { kind: 'length', unit: 'em', min: -0.05, max: 0.3 }],
    ['radius', { kind: 'length', unit: 'px', min: 0, max: 24 }],
    ['led', { kind: 'enum', values: ['circle', 'diamond', 'square'] }],
    ['border-style', { kind: 'enum', values: ['solid', 'dashed', 'dotted'] }],
    ['lift', { kind: 'shadow' }],
    ['glow', { kind: 'number', min: 0, max: 1 }],
    ['blur', { kind: 'length', unit: 'px', min: 0, max: 16 }],
    ['motion', { kind: 'enum', values: ['calm', 'power'] }],
    // the look: what the page is drawn on, how a card is shaped and marked, and the glyphs of the chrome. Keywords and bounded
    // numbers only; css.ts turns each into code-owned values (LOOK), so no text from a theme ever becomes CSS of its own.
    ['font-body', { kind: 'enum', values: ['ui', 'mono'] }],
    ['chrome', { kind: 'enum', values: ['styled', 'authored'] }],
    ['text-glow', { kind: 'number', min: 0, max: 1 }],
    ['cursor', { kind: 'enum', values: ['bar', 'block'] }],
    ['hover-lift', { kind: 'enum', values: ['off', 'on'] }],
    ['meter', { kind: 'enum', values: ['smooth', 'segmented'] }],
    ['backdrop', { kind: 'enum', values: ['none', 'grid', 'lines', 'dots'] }],
    ['backdrop-size', { kind: 'length', unit: 'px', min: 12, max: 96 }],
    ['backdrop-strength', { kind: 'number', min: 0, max: 1 }],
    ['orbs', { kind: 'enum', values: ['none', 'soft', 'bright'] }],
    ['orbs-tint', { kind: 'enum', values: ['roles', 'accent', 'edge'] }],
    ['backdrop-scan', { kind: 'number', min: 0, max: 1 }],
    ['header', { kind: 'enum', values: ['flat', 'frost', 'rule', 'glint'] }],
    ['stage', { kind: 'enum', values: ['plain', 'dots', 'hud'] }],
    // a dense bg under the tree's stage, so an effect behind the page (the rain) does not print through its wires
    ['stage-backing', { kind: 'number', min: 0, max: 1 }],
    ['card-fill', { kind: 'enum', values: ['plain', 'wash', 'glass'] }],
    ['card-corner', { kind: 'enum', values: ['round', 'square', 'chamfer', 'notch'] }],
    ['card-cut', { kind: 'length', unit: 'px', pxOnly: true, min: 4, max: 20 }],
    ['card-frame', { kind: 'enum', values: ['none', 'hairline', 'bold', 'double', 'rule'] }],
    ['card-marks', { kind: 'enum', values: ['none', 'brackets', 'ticks', 'gutter'] }],
    ['card-stripe', { kind: 'enum', values: ['off', 'on'] }],
    ['card-rim', { kind: 'number', min: 0, max: 1 }],
    ['card-bloom', { kind: 'number', min: 0, max: 1 }],
    ['card-glow', { kind: 'number', min: 0, max: 1 }],
    ['card-shadow', { kind: 'enum', values: ['none', 'lift', 'halo'] }],
    ['glyph-arrow', { kind: 'enum', values: ['triangle', 'chevron', 'arrow', 'none'] }],
    ['glyph-bullet', { kind: 'enum', values: ['triangle', 'dot', 'dash', 'prompt', 'none'] }],
    ['glyph-sep', { kind: 'enum', values: ['slash', 'dot', 'bar', 'dash'] }],
    ['glyph-brand', { kind: 'enum', values: ['conn', 'blocks', 'prompt', 'diamond', 'none'] }],
    // the Overview agent tree: how its wires are drawn and end, what crawls and travels along them, and what moves round the lead
    ['wire-style', { kind: 'enum', values: ['line', 'cable', 'rail', 'dash'] }],
    ['wire-width', { kind: 'length', unit: 'px', pxOnly: true, min: 1, max: 6 }],
    ['wire-head', { kind: 'enum', values: ['arrow', 'plug', 'dot', 'bar', 'none'] }],
    ['wire-crawl', { kind: 'enum', values: ['off', 'on'] }],
    ['wire-flow', { kind: 'enum', values: ['packets', 'blocks', 'pulse', 'glyphs', 'none'] }],
    // the characters a glyphs flow carries (fixed sets in code, never text from a theme) and the color of whatever travels
    ['wire-glyphs', { kind: 'enum', values: ['binary', 'hex', 'kana', 'ascii'] }],
    ['wire-signal', { kind: 'enum', values: ['roles', 'lead', 'bright', 'tx'] }],
    ['wire-speed', { kind: 'number', min: 0.25, max: 4 }],
    ['tree-orbits', { kind: 'enum', values: ['off', 'on'] }],
    ['node-beat', { kind: 'enum', values: ['off', 'on'] }],
  ]),
)

// Code-owned, so not run through the slot grammar (which has no var()): a theme that sets ac but not its soft and
// line forms still gets them, following whatever ac is; the same for edge (its mu) and stall (its warn and bad).
// themeCss writes them above the fallback layer, and the contrast gate measures them the same way, so a theme never gets
// Default's literal for a slot it derives from its own.
export const FROM_VAR: Readonly<Record<string, string>> = Object.freeze({
  'ac-soft': 'color-mix(in oklch, var(--ac) 14%, transparent)',
  'ac-line': 'color-mix(in oklch, var(--ac) 40%, transparent)',
  edge: 'var(--mu)',
  stall: 'color-mix(in oklch, var(--warn) 50%, var(--bad))',
  // a theme that leaves its panels' glow unset glows them as much as its dots and text, as before panels had a slot
  'card-glow': 'var(--glow)',
  focus: 'var(--ac)',
  'meter-track': 'var(--line)',
  'meter-lit': 'var(--ac)',
  'meter-lead': 'var(--role-lead)',
})

export const ALLOWED_FONTS: readonly string[] = ['Chakra Petch', 'JetBrains Mono', 'IBM Plex Sans', 'IBM Plex Mono', 'Share Tech Mono']

export const KNOWN_EFFECTS = ['rain', 'scanlines', 'vignette', 'flicker', 'glow', 'roll', 'grain']

export const LIMITS = { manifestBytes: 65536, cssBytes: 262144, userThemes: 100 }
