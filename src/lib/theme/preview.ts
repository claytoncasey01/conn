// A theme as a standalone HTML document for a sandboxed iframe (the gallery's card preview). Pure: no DOM.
// Everything in it comes from a manifest that passed validateManifest and the generated token CSS; every string is escaped.
import { themeCss, themeTokens } from './css.ts'
import { BRAND_MARK } from './brandMark.ts'
import { LOOK_RULES } from './lookCss.ts'
import { HEAD_SHAPES, headsOf } from '../treeHeads.ts'
import type { Mode, ThemeManifest } from './types.ts'
import { validateManifest } from './validate.ts'

const CSP = "default-src 'none'; style-src 'unsafe-inline'"

/** The iframe attributes: `sandbox=""` grants nothing (no scripts, no same-origin); `csp` is enforced on the frame's own document. */
export const previewAttrs = { sandbox: '', csp: CSP }

const ESCAPES: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }
const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ESCAPES[c])

/** Second layer under the validator: nothing in the css can close the style block or open a comment. */
export const safeCss = (css: string) => css.replace(/<\/?style/gi, '').replace(/[<>]/g, '').replace(/\/+\*+/g, '')

const LEDS = ['circle', 'diamond', 'square']

// One wire of the agent tree, from a junction into the lead, drawn as the Overview draws every wire: each part and each head shape,
// so the card shows the theme's wire style, head and crawl (the moving parts need data-fx, which a preview never has: they hold still)
const WIRE_D = 'M12 12 H88'
const WIRE_HEADS = headsOf([[88, 12, 'r']])
const WIRE = `<svg class="tree" aria-hidden="true">` +
  `<path class="wsh" d="${WIRE_D}" fill="none" style="color: var(--role-agent); --wk: 1"></path>` +
  `<path class="wb" d="${WIRE_D}" stroke="var(--role-agent)" stroke-opacity="0.7" fill="none" style="--wk: 1"></path>` +
  `<path class="wcut" d="${WIRE_D}" fill="none" style="--wk: 1"></path>` +
  `<path class="wsn" d="${WIRE_D}" fill="none" style="color: var(--role-agent); --wk: 1"></path>` +
  `<path class="flow" d="${WIRE_D}" stroke="var(--tx)" stroke-opacity="0.8" stroke-width="1.5" stroke-dasharray="3 21" stroke-linecap="round" fill="none"></path>` +
  HEAD_SHAPES.map((k) => `<path class="h-${k}" d="${WIRE_HEADS[k]}" fill="color-mix(in oklab, var(--edge) 75%, var(--bright))"></path>`).join('') +
  `<circle cx="12" cy="12" r="5" fill="var(--panel)" stroke="var(--role-agent)" stroke-width="1.5"></circle><circle class="node" cx="12" cy="12" r="3" fill="var(--role-agent)"></circle></svg>`

// The look is drawn by the app's own rules (lookCss.ts), so the card cannot draw a theme differently from the pages. This sheet only
// lays the sample out: sizes and places, and the plain parts (tabs, rows, dots, chips, buttons) from var(--token). No color here.
const SHEET = `
*{box-sizing:border-box}
html,body{margin:0;overflow:hidden}
body{background:var(--bg);color:var(--tx);font:12px/1.35 var(--font-ui,system-ui);letter-spacing:var(--tracking,0)}
.page{position:relative;min-height:100vh;padding:8px 12px}
.page-scan{position:absolute;inset:0;pointer-events:none}
.page-head{position:relative;display:flex;align-items:center;gap:4px;border-bottom:1px var(--border-style,solid) var(--look-head-ring, var(--line));margin:calc(3px * var(--look-head-float, 0)) 0 4px;padding:0 calc(4px * var(--look-head-float, 0))}
.glint{position:absolute;left:0;right:0;bottom:-1px;height:1px}
.brand{display:inline-flex;align-items:center;justify-content:center;min-width:24px;height:20px;padding:0 3px;margin-right:4px;border-radius:var(--radius,6px);font-size:9px;font-weight:700;letter-spacing:0}
.tab{padding:3px 9px;color:var(--mu);border-bottom:2px solid transparent}
.tab.on{color:var(--bright);border-bottom-color:var(--ac)}
.label{font-size:10px;color:var(--mu);margin:0 0 4px}
.card{padding:2px 10px 7px}
.stripe{position:absolute;left:0;top:0;width:100%;height:2px;background:linear-gradient(90deg,var(--c),transparent)}
.row{display:flex;align-items:center;gap:8px;padding:3px 0;border-bottom:1px var(--border-style,solid) var(--line)}
.dot{width:8px;height:8px;flex:none;border-radius:50%}
[data-led="diamond"] .dot{border-radius:0;transform:rotate(45deg)}
[data-led="square"] .dot{border-radius:0}
.ok{background:var(--ok)}.warn{background:var(--warn)}.bad{background:var(--bad)}.idle{background:var(--idle)}
.grow{flex:1}
.mono{font-family:var(--font-mono,ui-monospace),monospace;color:var(--mu)}
.chip{padding:1px 7px;border-radius:var(--radius,6px);background:var(--ac-soft);border:1px var(--border-style,solid) var(--ac-line);color:var(--bright);font-size:11px}
.line{margin-top:3px;color:var(--mu)}
.g-bullet{color:var(--ac)}
.actions{display:flex;gap:8px;margin-top:5px}
.btn{font:inherit;text-transform:inherit;letter-spacing:inherit;padding:3px 11px;border-radius:var(--radius,6px);border:1px var(--border-style,solid) var(--line2)}
.btn.primary{background:var(--ac);color:var(--onac);border-color:var(--ac);box-shadow:0 0 calc(var(--glow,0)*14px) var(--ac-line)}
.btn.ghost{background:transparent;color:var(--tx)}
.stage{position:relative;height:24px;margin-top:6px;border:1px var(--border-style,solid) var(--line);border-radius:var(--radius,6px);overflow:hidden}
.stage-ticks{position:absolute;left:0;top:4px;bottom:4px;width:5px}
.stage-glow{position:absolute;left:30%;top:-50%;width:40%;height:200%;pointer-events:none}
.orbit{position:absolute;left:50%;top:50%;width:46px;height:46px;margin:-23px 0 0 -23px;border-radius:50%;border:1px dashed color-mix(in oklch, var(--role-lead) 40%, transparent)}
.tree{position:absolute;left:0;top:0;width:100%;height:100%;overflow:visible}
.lead{position:absolute;left:50%;top:50%;transform:translate(-50%,-50%);padding:1px 8px;border-radius:var(--radius,6px);border:1px var(--border-style,solid) var(--role-lead);color:var(--bright);font-size:10px}
`

function document(title: string, body: string, css: string, attrs = ''): string {
  return `<!doctype html><html lang="en"${attrs}><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${CSP}"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${esc(title)}</title><style>${safeCss(css)}</style></head><body>${body}</body></html>`
}

// Plain system colors, so the placeholder is readable in either scheme without a theme.
const PLACEHOLDER_CSS = 'html{color-scheme:light dark}body{font:13px/1.4 system-ui;padding:12px;background:Canvas;color:CanvasText}'

/** The preview document for `manifest` in `mode` (a one-mode theme shows its own mode). A manifest that fails validation gets a placeholder.
 * `reduce` draws it as Reduce transparency does: the preview runs no script, so Conn.app's mark on the page (data-transparency)
 * is passed on here; a browser's own setting reaches the preview through the media query. */
export function previewDoc(manifest: ThemeManifest, mode: Mode, fallback: ThemeManifest, reduce = false): string {
  const checked = validateManifest(manifest, { source: 'builtin', builtinIds: new Set() })
  if (!checked.ok) return document('Preview', '<p>This theme cannot be previewed.</p>', PLACEHOLDER_CSS)
  const m = checked.manifest
  const shown: Mode = m.modes.includes(mode) ? mode : m.modes[0]
  // No pick: a free-accent theme shows its own ac, which is what an install paints until a swatch is chosen, and a variant theme shows its first variant
  const css = themeCss(m, fallback) + LOOK_RULES + SHEET
  const led = themeTokens(m, fallback, shown).led ?? 'circle'
  const card = '--c: var(--ac); --r: calc(var(--radius) * 1.5); --rim: color-mix(in oklch, var(--ac) 34%, transparent); --wash: linear-gradient(170deg, color-mix(in oklch, var(--ac) 10%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%); --halo: 0 0 22px color-mix(in oklch, var(--ac) calc(16% * var(--glow, 0)), transparent)'
  const hide = 'aria-hidden="true"'
  const body = `<div class="page"><div class="page-scan" ${hide}></div>
<div class="page-head"><span class="brand g-brand-tile" ${hide}><span class="g-brand"></span>${BRAND_MARK}</span><span class="tab cap on">Sessions</span><span class="tab cap">Reviews</span><span class="tab cap">History</span><span class="glint"></span></div>
<div class="label cap"><span class="g-go-back" ${hide}></span> Sessions <span class="g-sep" ${hide}></span> 3 live</div>
<div class="card surf brk" style="${card}"><span class="stripe"></span><div class="row"><span class="dot ok"></span><span class="grow">Build passing</span><span class="mono">12s</span></div>
<div class="row"><span class="dot warn"></span><span class="grow">Waiting on input</span><span class="chip cap">Ready</span></div>
<div class="row"><span class="dot bad"></span><span class="grow">Needs attention</span><span class="mono">3m</span></div>
<div class="row"><span class="dot idle"></span><span class="grow">Idle</span><span class="mono">1h</span></div>
<div class="line"><span class="g-bullet" ${hide}></span>Tests look fine <span class="g-go" ${hide}></span></div>
<div class="actions"><span class="btn primary cap">Open <span class="g-fwd" ${hide}></span></span><span class="btn ghost cap"><span class="g-back" ${hide}></span> Back</span></div></div>
<div class="stage"><span class="stage-ticks"></span><span class="stage-glow"></span><span class="orbit"></span>${WIRE}<span class="lead cap">Lead</span></div></div>`
  const attrs = ` data-theme="${esc(m.id)}" data-mode="${shown}" data-led="${LEDS.includes(led) ? led : 'circle'}"${reduce ? ' data-transparency="reduce"' : ''}`
  return document(m.name, body, css, attrs)
}
