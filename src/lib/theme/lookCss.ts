// The look's rules: one stylesheet the app (main.ts) and the gallery preview (preview.ts) both load, so a preview cannot draw a
// theme differently from the pages. Every rule reads the look tokens through the --look-* variables css.ts derives from them
// (and the numbers backdrop-size, backdrop-strength, backdrop-scan, card-rim, card-bloom, card-glow and card-cut), so a theme
// of tokens alone can draw it, and each var() has a fallback, so a sheet cached before the look still draws a whole page
// (not Default's look in every piece: a fill falls back to the card's wash, a shadow to its halo). Fixed text: nothing from a
// theme is written here.
import { GLASS, WALLPAPER, WALLPAPER_FADE } from './tokens.ts'

// A surface's background: the highlight (--look-sheen, an image or none) placed by --sx and --sy, over its fill. Reduce
// transparency paints the same with the fill's solid form.
const SHEEN_BOX = 480
const surfBackground = (fill: string) =>
  `background:var(--look-sheen, none) calc(var(--sx) - ${SHEEN_BOX / 2}px) calc(var(--sy) - ${SHEEN_BOX / 2}px)/${SHEEN_BOX}px ${SHEEN_BOX}px no-repeat, ${fill}`
// a wash surface whose markup gives it no --wash has no fill (as before the highlight), but keeps the highlight
const FILL = 'var(--look-fill, var(--wash, transparent))'
const SOLID_FILL = `var(--look-fill-solid, ${FILL})`
// each mode's share of the glass's light (GLASS), which the look variables css.ts writes read
const modeShares = (Object.keys(GLASS) as (keyof typeof GLASS)[])
  .map((mode) => `[data-mode="${mode}"]{--look-tone:${GLASS[mode].tone};--look-sheen-max:${GLASS[mode].sheen};--look-edge-k:${GLASS[mode].edge};--look-edge-glow:${GLASS[mode].edgeGlow}}`)
  .join('\n')
const wallpaper = WALLPAPER.map(
  (f, i) =>
    `radial-gradient(${f.rx * 100}% ${f.ry * 100}% at ${f.x * 100}% ${f.y * 100}%, color-mix(in oklch, var(--wallpaper-${i + 1}, transparent) calc(var(--wallpaper-strength, 0) * 100%), transparent), transparent ${WALLPAPER_FADE * 100}%)`,
).join(', ')
// the solid form of every surface, the stage, the top bar and the footer, and no wallpaper, for the OS's Reduce transparency or
// Conn.app passing it on (data-transparency): [selector, body]. The top bar and the footer take their fills from the variables
// their markup reads, and the stage's blur and fill likewise, since an inline style outranks this sheet.
const SOLID: [string, string][] = [
  ['.surf', `${surfBackground(SOLID_FILL)};-webkit-backdrop-filter:none;backdrop-filter:none;--look-surf-blur:none;--look-surf-blur-in:none`],
  ['.stage', '--look-surf-blur:none;--look-stage-panel:var(--panel)'],
  ['.page-head', '-webkit-backdrop-filter:none;backdrop-filter:none;--look-head-fill:100%'],
  ['.page-foot', '--look-foot-blur:none;--look-foot-fill:100%'],
  ['.page::before', 'content:none'],
]
const solidRules = (prefix: string) => SOLID.map(([selector, body]) => `${prefix}${selector}{${body}}`).join('')

export const LOOK_CSS = `
/* chrome takes the case and tracking tokens (chrome styled), or keeps what the markup gives it (authored: the var() is invalid,
   so the property inherits; a button goes back to its own normal and none). A label written in sentence case (.cap) always
   takes the case token. app.css's content rule comes after this sheet, so content inside chrome is never cased. */
:is(nav,h1,h2,h3,h4,label,th,legend,summary,.hud){text-transform:var(--look-chrome-case);letter-spacing:var(--look-chrome-tracking)}
:is(button,button.hud){text-transform:var(--look-chrome-case, none);letter-spacing:var(--look-chrome-tracking, normal)}
.cap.cap{text-transform:var(--case)}
body{text-shadow:var(--look-text-shadow, none)}

/* focus, the text cursor, the hover lift and the meters */
button:focus-visible,a:focus-visible,input:focus-visible{outline:2px solid var(--focus, var(--ac));outline-offset:3px}
.cursor{display:inline-block;width:var(--look-cursor-w, 7px);height:var(--look-cursor-h, 13px);background:var(--focus, var(--ac));margin-left:var(--look-cursor-ml, 4px);vertical-align:var(--look-cursor-va, -2px);box-shadow:var(--look-cursor-glow, none)}
.card{transition:transform .18s, border-color .18s}
.card:hover{transform:translateY(calc(-3px * var(--look-lift, 0)))}
.row{transition:border-color .16s, background .16s, transform .16s}
.row:hover{border-color:color-mix(in oklch, var(--ac) 50%, transparent);transform:translateX(calc(2px * var(--look-lift, 0)))}
.go{transition:transform .16s, box-shadow .16s}
.go:hover:not(:disabled){box-shadow:var(--look-go-glow, 0 0 0 3px var(--ac-soft));transform:translateY(calc(-2px * var(--look-lift, 0)))}
.seg{background:repeating-linear-gradient(90deg,var(--meter-track, var(--line)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px);border-radius:var(--look-meter-r, 2px)}
.segon{background:repeating-linear-gradient(90deg,var(--meter-lit, var(--ac)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px);border-radius:var(--look-meter-r, 2px)}
.segon-o{background:repeating-linear-gradient(90deg,var(--meter-lead, var(--role-lead)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px);border-radius:var(--look-meter-r, 2px)}
/* the wires of the agent tree glow with the glow token (Neon's beam is keyframed: its CSS) */
.wire{filter:var(--look-wire, none)}
.beam{display:none}
/* each wire is a base line (.wb), an under-sheath (.wsh), a cutter (.wcut) and a sheen (.wsn), each head in every shape: the
   style shows its parts at wire-width, scaled by the wire's own weight (--wk, 1 for the trunk, .75 for the thinner links) */
.wb{display:var(--look-wb, inline);stroke-width:calc(var(--wire-width, 2px) * var(--wk, 1));stroke-dasharray:var(--look-wb-dash, none)}
.wsh{display:var(--look-wsh, none);stroke:color-mix(in oklch, currentColor 38%, var(--panel));stroke-width:calc(var(--wire-width, 2px) * var(--wk, 1) * var(--look-wsh-k, 0))}
.wcut{display:var(--look-wcut, none);stroke:var(--panel);stroke-width:calc(var(--wire-width, 2px) * var(--wk, 1) * 1.3)}
.wsn{display:var(--look-wsn, none);stroke:color-mix(in oklch, currentColor 40%, var(--bright));stroke-opacity:.55;stroke-width:calc(var(--wire-width, 2px) * var(--wk, 1) * .4);transform:translate(calc(var(--wire-width, 2px) * -.5), calc(var(--wire-width, 2px) * -.5))}
.h-arrow{display:var(--look-h-arrow, inline)}
.h-plug{display:var(--look-h-plug, none)}
.h-dot{display:var(--look-h-dot, none)}
.h-bar{display:var(--look-h-bar, none)}
/* what moves on the tree: the crawling dashes, and what travels a wire while an agent works (packets, square blocks or a lit
   pulse), each one a code-owned animation timed by wire-speed; a comet group glows with the glow token */
.flow,.flowr{display:var(--look-crawl, inline)}
.comet{filter:var(--look-comet-glow, none)}
.pk,.blk,.orbit-pk,.glyph{offset-rotate:0deg}
/* what travels a wire takes its wire's role color, or one signal color for all (wire-signal) */
.pk,.blk,.glyph{fill:var(--look-signal, currentColor)}
.pulse{stroke:var(--look-signal, currentColor)}
/* a glyphs flow: a short stream of fixed characters (wire-glyphs picks the set) running the wire, corners and all */
.glyph{font:700 11px var(--font-mono), monospace;text-anchor:middle;dominant-baseline:central}
.gs-binary{display:var(--look-gs-binary, none)}
.gs-hex{display:var(--look-gs-hex, none)}
.gs-kana{display:var(--look-gs-kana, none)}
.gs-ascii{display:var(--look-gs-ascii, none)}
.pulse{stroke-dasharray:12 88}
.node{transform-box:fill-box;transform-origin:center}
/* motion runs only while Effects are on (data-fx, from the app and the boot script) and the OS does not ask for less; outside
   that, what travels a wire is hidden (a dot frozen halfway down a wire reads as a glitch) and the rest holds its still frame */
.pk,.blk,.pulse,.orbit-pk,.gly{display:none}
@media (prefers-reduced-motion: no-preference){
[data-fx="on"] .cursor{animation:blink 1s steps(2) infinite}
[data-fx="on"] .segon{animation:segmove 1.8s ease-in-out infinite}
[data-fx="on"] .segon-o{animation:segmove 2.2s ease-in-out infinite}
[data-fx="on"] .pk{display:var(--look-comet, none)}
[data-fx="on"] .blk{display:var(--look-blocks, none)}
[data-fx="on"] .pulse{display:var(--look-pulse, none)}
[data-fx="on"] .orbit-pk{display:var(--look-orbits, none)}
[data-fx="on"] .gly{display:var(--look-glyphs, none)}
[data-fx="on"] .pk,[data-fx="on"] .blk,[data-fx="on"] .orbit-pk,[data-fx="on"] .glyph{animation:pk calc(var(--pk-dur, 2.4s) / var(--wire-speed, 1)) linear calc(var(--pk-at, 0s) / var(--wire-speed, 1)) infinite}
[data-fx="on"] .pulse{animation:pulse-run calc(var(--pk-dur, 2.4s) / var(--wire-speed, 1)) linear calc(var(--pk-at, 0s) / var(--wire-speed, 1)) infinite}
[data-fx="on"] .node{animation:node calc(2.4s / var(--wire-speed, 1)) ease-in-out infinite;animation-name:var(--look-beat, none)}
[data-fx="on"] .flow{animation:flow calc(1.2s / var(--wire-speed, 1)) linear infinite}
[data-fx="on"] .flowr{animation:flow calc(1.2s / var(--wire-speed, 1)) linear infinite reverse}
[data-fx="on"] .orbit{animation:rot calc(40s / var(--wire-speed, 1)) linear infinite}
[data-fx="on"] .orbit2{animation:rot calc(60s / var(--wire-speed, 1)) linear infinite reverse}
}
@keyframes node{0%,100%{transform:scale(1);opacity:.9}50%{transform:scale(1.5);opacity:.4}}
@keyframes flow{to{stroke-dashoffset:-24}}
@keyframes rot{to{transform:rotate(360deg)}}
@keyframes pk{from{offset-distance:0%}to{offset-distance:100%}}
@keyframes pulse-run{from{stroke-dashoffset:100}to{stroke-dashoffset:0}}

/* the page: the body paints var(--bg); the wrapper (.page) only lays see-through layers over it, so the effects layer shows.
   Three orbs, tinted per page by --orb-N (a hue) at --orb-N-a, scaled by the orbs token; then the pattern: a grid's two sets of
   lines, or dots, at most 9% (30% for dots) of edge at full strength */
.page{font-family:var(--look-body, var(--font-ui));background:
 radial-gradient(900px 520px at 80% -6%, color-mix(in oklch, var(--look-orb-tint, var(--orb-1, var(--edge))) calc(var(--orb-1-a, 20%) * var(--look-orbs, 0)), transparent), transparent 62%),
 radial-gradient(760px 520px at 4% 22%, color-mix(in oklch, var(--look-orb-tint, var(--orb-2, var(--role-advisor))) calc(var(--orb-2-a, 15%) * var(--look-orbs, 0)), transparent), transparent 62%),
 radial-gradient(760px 420px at 48% 34%, color-mix(in oklch, var(--look-orb-tint, var(--orb-3, var(--role-lead))) calc(var(--orb-3-a, 9%) * var(--look-orbs, 0)), transparent), transparent 66%),
 linear-gradient(color-mix(in oklch, var(--edge) calc(9% * var(--backdrop-strength, 0) * var(--look-grid-h, 0)), transparent) 1px, transparent 1px) 0 0/var(--backdrop-size, 24px) var(--backdrop-size, 24px),
 linear-gradient(90deg, color-mix(in oklch, var(--edge) calc(9% * var(--backdrop-strength, 0) * var(--look-grid-v, 0)), transparent) 1px, transparent 1px) 0 0/var(--backdrop-size, 24px) var(--backdrop-size, 24px),
 radial-gradient(color-mix(in oklch, var(--edge) calc(30% * var(--backdrop-strength, 0) * var(--look-dots, 0)), transparent) 1px, transparent 1.5px) 0 0/var(--backdrop-size, 24px) var(--backdrop-size, 24px)}
/* the wallpaper: up to five soft fields of color fixed to the window, under everything on the page (WALLPAPER places them) */
.page::before{content:var(--look-wall, none);position:fixed;inset:0;z-index:-1;pointer-events:none;background:${wallpaper}}
.page-scan{display:var(--look-scan, none);background:repeating-linear-gradient(0deg, color-mix(in oklch, var(--bright) calc(2.2% * var(--backdrop-scan, 0)), transparent) 0 1px, transparent 1px 3px)}
/* the top bar: a frost of what is behind it, a rule along its bottom edge, or the glint, a lit line there (Neon sets it running) */
.page-head{-webkit-backdrop-filter:var(--look-head-blur, none);backdrop-filter:var(--look-head-blur, none);box-shadow:inset 0 calc(-2px * var(--look-head-rule, 0)) 0 var(--edge), var(--look-head-lift, 0 0 transparent)}
/* its shape (header-shape): a float sits inset from the window's edges, its ends rounded to match the tabs inside it (their radius
   and the bar's 14px padding, so two rows of tabs make a round-cornered panel rather than a pill), ringed on every side as its
   markup draws the bottom edge (an inline border, which outranks this), and lifted; the glint keeps clear of the round ends. A
   gap under it keeps a strip that spans the window (the disconnected notice) off its edge. A bar, or a theme from before the
   slot, has each of these at 0 */
.page-head{margin:calc(12px * var(--look-head-float, 0)) calc(16px * var(--look-head-float, 0)) calc(8px * var(--look-head-float, 0));border-radius:calc((var(--radius, 0px) + 14px) * var(--look-head-float, 0));border-style:var(--border-style);border-color:color-mix(in oklch, var(--edge) 16%, transparent);border-width:calc(1px * var(--look-head-float, 0))}
.glint{margin-inline:calc((var(--radius, 0px) + 14px) * var(--look-head-float, 0))}
.glint{display:var(--look-glint, none);background:linear-gradient(90deg,transparent,var(--edge),var(--role-lead),var(--role-advisor),transparent);background-size:220% 100%}
/* the Overview stage: a dot grid and glows behind the lead and advisor over the panel; hud adds the ruler ticks on its edges, the
   perspective floor, the glow behind the lead and the rings around it */
.stage{background:radial-gradient(color-mix(in oklch, var(--edge) calc(22% * var(--look-stage-dots, 0)), transparent) 1px, transparent 1.3px) 0 0/24px 24px, radial-gradient(520px 300px at 62% 24%, color-mix(in oklch, var(--role-lead) calc(10% * var(--look-stage-glow, 0)), transparent), transparent 70%), radial-gradient(460px 360px at 12% 60%, color-mix(in oklch, var(--role-advisor) calc(10% * var(--look-stage-glow, 0)), transparent), transparent 70%), color-mix(in oklch, var(--look-stage-panel, color-mix(in oklch, var(--panel) 80%, transparent)), var(--bg) calc(var(--stage-backing, 0) * 100%))}
.stage-ticks{display:var(--look-hud, none);background:repeating-linear-gradient(0deg, color-mix(in oklch, var(--edge) 40%, transparent) 0 1px, transparent 1px 12px)}
.stage-floor{display:var(--look-hud, none)}
.stage-glow{display:var(--look-hud, none);background:radial-gradient(closest-side, color-mix(in oklch, var(--role-lead) 24%, transparent), transparent)}
.orbit,.orbit2{display:var(--look-orbits, none);transform-box:fill-box;transform-origin:center}

/* a surface (.surf: a panel, a card, a stat tile, a row). The markup hands it its state as data: --rim, the edge's color (a waiting
   session's is warn); --wash, a fill tinted by it; --halo, a glow in it; --c, its own color; --r, its radius. The data is reset
   here, so a surface inside another never takes its parent's (inline data on the element itself still wins). card-fill picks
   the panel, the wash or glass; card-rim how much of the rim the border takes and card-frame which sides show and what lines
   sit inside; card-corner the radius or a cut; the shadows stack the frame, the bloom, the gutter, the panel glow and then
   card-shadow (lift, or the halo), and last --lit, a glow a page's hover adds (it sets --lit, never box-shadow). */
.surf{--wash:initial;--rim:initial;--halo:initial;--r:initial;--lit:initial;--mx:initial;--my:initial;--sx:64px;--sy:0px;--rimc:color-mix(in oklch, var(--rim, var(--line)) calc(var(--card-rim, .5) * 100%), var(--line));position:relative;
 ${surfBackground(FILL)};-webkit-backdrop-filter:var(--look-glass, none);backdrop-filter:var(--look-glass, none);
 border:1px var(--border-style) transparent;border-color:color-mix(in oklch, var(--rimc) calc(var(--look-edge-top, 1) * 100%), transparent) color-mix(in oklch, var(--rimc) calc(var(--look-edge-sides, 1) * 100%), transparent);
 border-radius:calc(var(--r, var(--radius)) * var(--look-round, 1));clip-path:var(--look-clip, none);
 box-shadow:var(--look-edge-light, 0 0 transparent), var(--look-frame, 0 0 transparent), inset 0 0 28px color-mix(in oklch, var(--tx) calc(var(--card-bloom, 0) * 20%), transparent), inset calc(2px * var(--look-gutter, 0)) 0 0 var(--c, color-mix(in oklch, var(--ac) 55%, transparent)), 0 0 calc(22px * var(--card-glow, 0)) color-mix(in oklch, var(--c, var(--edge)) calc(var(--card-glow, 0) * 30%), transparent), var(--look-shadow, var(--halo, 0 0 transparent)), var(--lit, 0 0 transparent)}
/* a surface inside another (or on the agent tree's stage) does not filter again once a theme tunes its glass (--look-surf-blur-in
   is none; its own markup's blur reads --look-surf-blur), and keeps the glass of before otherwise; the highlight rests at the top
   left, and follows the pointer (sheen.ts sets --mx and --my on the surface under it) only while motion may run */
:is(.surf,.stage) .surf{-webkit-backdrop-filter:var(--look-surf-blur-in, var(--look-glass, none));backdrop-filter:var(--look-surf-blur-in, var(--look-glass, none));--look-surf-blur:var(--look-surf-blur-in, blur(6px))}
@media (prefers-reduced-motion: no-preference){[data-fx="on"] .surf{--sx:var(--mx, 64px);--sy:var(--my, 0px)}}
/* the glass's light, each mode's share of it */
${modeShares}
/* Reduce transparency: every surface, the stage, the top bar and the footer solid, nothing frosted and no wallpaper, from the OS
   (with :root, so it weighs what the attribute form does) or from Conn.app (which reads the Mac's setting
   itself, since WebKit does not pass it to the page) */
@media (prefers-reduced-transparency: reduce){${solidRules(':root ')}}
${solidRules('[data-transparency="reduce"] ')}
/* a cut corner clips the border and an outer focus ring: the rim is redrawn along each cut, and the ring moves inside
   (a surface that draws its own outline, like the Overview's selection, offsets it by --look-focus too) */
.surf::before{content:var(--look-cut, none);position:absolute;inset:0;pointer-events:none;color:var(--rimc);background:var(--look-cut-art, none)}
.surf:focus-visible{outline-offset:var(--look-focus, 3px)}
/* a selected surface (the Overview's agent cards and advisor rail): inside a cut the outline lies against the card's own rim,
   so it is 2px in the accent there (the focus ring, at full strength, replaces it while the card has focus); the surface
   places it with outline-offset from --look-focus */
.surf[aria-pressed="true"]:not(:focus-visible){outline:var(--look-sel, 1px solid color-mix(in oklch, var(--tx) 55%, transparent))}
/* what stands out from a card's edge (the agent cards' ports) tucks inside it under a cut, where the clip would take it */
.port-t{top:calc(2px - 7px * var(--look-out, 1))}
.port-b{bottom:calc(2px - 7px * var(--look-out, 1))}
/* forced colors paint no box-shadow and no rim art, so a cut would leave the border open at every corner: no cut there */
@media (forced-colors:active){.surf{clip-path:none}.surf::before{content:none}}
/* the stripe along the top edge of a card or tile */
.stripe{display:var(--look-stripe, none)}
/* marks on the main panels (.brk), in the panel's own color: corner brackets or edge ticks. The anchor stays for absolute children */
.brk{position:relative}
.brk::after{content:var(--look-marks, none);position:absolute;inset:-1px;pointer-events:none;color:var(--c, var(--edge));background:var(--look-marks-art, none)}

/* the glyphs of the chrome: the arrows on links and buttons, the bullet before a status line, the separator in section labels and
   the brand mark (a text glyph, or the Conn mark as an inline SVG in one of its drawings). Decorative: the spans are aria-hidden, and the words around them carry the meaning */
.g-fwd::before{content:var(--look-fwd, "›")}
.g-back::before{content:var(--look-back, "‹")}
.g-go::before{content:var(--look-go, "›")}
.g-go-back::before{content:var(--look-go-back, "‹")}
.g-bullet::before{content:var(--look-bullet, "•");margin-right:var(--look-bullet-gap, .45em)}
.g-sep::before{content:var(--look-sep, "·")}
.g-brand::before{content:var(--look-brand, "▟█▙");font-size:var(--look-brand-size, 1em)}
.g-brand-tile{background:var(--look-brand-tile, var(--ac));color:var(--look-brand-ink, var(--onac));outline:var(--look-brand-ring, none);outline-offset:-1px}
.g-brand-mark{display:var(--look-brand-mark, none);width:1.85em;height:1.85em;flex:none;filter:var(--look-brand-bloom, none);-webkit-mask-image:var(--look-brand-lines, none);mask-image:var(--look-brand-lines, none)}
/* the solid drawing is the one shown when no --look-brand-solid is set: a cached look from before the drawings set only --look-brand-mark */
.bm-solid{display:var(--look-brand-solid, inline)}
.bm-tube{display:var(--look-brand-tube, none)}
.bm-pixel{display:var(--look-brand-pixel, none);shape-rendering:crispEdges}
.bm-core{stroke:color-mix(in oklab, currentColor 35%, white)}
`

/** The same rules without their comments, for a place that strips comment openers (the preview's safeCss) */
export const LOOK_RULES = LOOK_CSS.replace(/\/\*[\s\S]*?\*\//g, '')
