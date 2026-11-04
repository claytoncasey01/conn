// The `behind` effects layer (Matrix rain) is z-index -1: it shows through only while nothing between it and the
// viewport paints an opaque background. The one solid base is the body's (app.css); every root wrapper must stay
// translucent. This reads the root wrappers as text and fails on one that paints a solid color or an opaque gradient
// stop, naming the file. A root wrapper is any element or rule that is a full-viewport box (height or min-height of
// 100vh/100dvh/100svh) in src/**/*.svelte, plus html, :root and #app in src/app.css (the body is the one allowed).
// A background is see-through when every color in it is `transparent` or a mix with it; a bare token (var(--bg)) is not.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'
import { LOOK_CSS } from './theme/lookCss.ts'

const root = new URL('../../', import.meta.url).pathname

/** Top-level comma split: commas inside parentheses do not separate layers or arguments */
function split(s: string): string[] {
  const out: string[] = []
  let depth = 0
  let cur = ''
  for (const ch of s) {
    if (ch === '(' || ch === '{') depth++
    else if (ch === ')' || ch === '}') depth--
    if (depth === 0 && ch === ',') {
      out.push(cur.trim())
      cur = ''
    } else cur += ch
  }
  out.push(cur.trim())
  return out
}

const SHAPE = /^(to\b|[-\d.]|circle\b|ellipse\b|at\b|from\b|in\b|closest|farthest)/
// translucent by construction: a color-mix with transparent, or the theme's own soft tokens (a mix with transparent)
const seeThrough = (c: string) => c === 'transparent' || (c.startsWith('color-mix(') && /\btransparent\b/.test(c)) || /^var\(--(?:ac-soft|ac-line)\)$/.test(c)

/** The arguments of a layer that is a gradient, up to its own closing paren (a position or size after it may hold var()s), or null */
function gradientArgs(layer: string): string | null {
  const head = /^(?:repeating-)?(?:linear|radial|conic)-gradient\(/.exec(layer)
  if (!head) return null
  let depth = 1
  for (let i = head[0].length; i < layer.length; i++) {
    if (layer[i] === '(') depth++
    else if (layer[i] === ')' && --depth === 0) return layer.slice(head[0].length, i)
  }
  return null
}

/** Why a background value (one `background:` list) hides what is behind it, or null when it does not */
export function opaqueReason(value: string): string | null {
  // a Svelte expression picks between strings: check each one it can produce
  if (value.includes('{')) {
    const branches = [...value.matchAll(/'([^']*)'|"([^"]*)"|`([^`]*)`/g)].map((m) => m[1] ?? m[2] ?? m[3])
    for (const b of branches) {
      const why = opaqueReason(b)
      if (why) return why
    }
    return null
  }
  const layers = split(value.trim())
  for (const layer of layers) {
    const g = gradientArgs(layer)
    if (g !== null) {
      // every stop of a gradient paints; a first argument that is a direction or a shape is not one
      const stops = split(g).filter((a) => !SHAPE.test(a))
      for (const stop of stops) {
        const color = stop.replace(/\s+-?[\d.]+(?:%|px|em)?(?:\s+-?[\d.]+(?:%|px|em)?)?$/, '').trim()
        if (!seeThrough(color)) return `a gradient stop of ${color}`
      }
    } else if (/^(?:none|transparent|inherit|initial|unset|url\(.*|image-set\(.*)$/.test(layer)) continue
    else if (!seeThrough(layer.replace(/\s+!important$/, '')) && !/\s(?:0|\d+%?)\s+(?:0|\d+%?)\//.test(layer)) return `a solid ${layer}`
  }
  return null
}

/** The `background` and `background-color` values declared in a block of declarations */
const backgrounds = (decls: string) => [...decls.matchAll(/(?:^|[;\s"'])background(?:-color)?\s*:\s*([^;]*?)\s*(?:;|$)/g)].map((m) => m[1])
const FULL = /(?:^|[;\s])(?:min-)?height\s*:\s*100(?:d|s|l)?vh\b/

/** [where, declarations] of every style attribute and every `<style>` rule in a svelte file */
function blocks(f: string, text: string): [string, string][] {
  const out: [string, string][] = []
  const marked = text.replace(/<style[^>]*>([\s\S]*?)<\/style>/g, (_, css: string) => {
    for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) out.push([`${f} ${m[1].trim().replace(/\s+/g, ' ')}`, m[2]])
    return ''
  })
  for (const m of marked.matchAll(/style="([^"]*)"/g)) out.push([`${f}:${marked.slice(0, m.index).split('\n').length}`, m[1]])
  return out
}

test('no full-viewport root wrapper in src/**/*.svelte paints an opaque background over the effects layer', () => {
  const files = [...new Glob('src/**/*.svelte').scanSync(root)].sort()
  const wrappers: string[] = []
  const offenders: string[] = []
  for (const f of files) {
    for (const [where, decls] of blocks(f, readFileSync(root + f, 'utf8'))) {
      if (!FULL.test(decls)) continue
      wrappers.push(where)
      for (const bg of backgrounds(decls)) {
        const why = opaqueReason(bg)
        if (why) offenders.push(`${where}: ${why}`)
      }
    }
  }
  assert.ok(wrappers.length >= 10, `expected the page wrappers (ten pages), found ${wrappers.length}: ${wrappers.join(', ')}`)
  assert.deepEqual(offenders, [], `these root wrappers hide the effects layer:\n${offenders.join('\n')}`)
})

test('in app.css only the body paints the base: html, :root and #app have no opaque background', () => {
  const css = readFileSync(root + 'src/app.css', 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ sels: split(m[1].trim()), decls: m[2] }))
  const rootRules = rules.filter((r) => r.sels.some((s) => /^(?:html|:root|#app)(?:\[[^\]]*\])*$/.test(s.trim())))
  assert.ok(rootRules.length >= 2, 'the root rules were found')
  const offenders = rootRules.flatMap((r) => backgrounds(r.decls).flatMap((bg) => opaqueReason(bg) ?? []).map((why) => `${r.sels.join(', ')}: ${why}`))
  assert.deepEqual(offenders, [])
  // the base is on the body, and a body without one would leave the page bare under a theme
  const body = rules.filter((r) => r.sels.includes('body')).flatMap((r) => backgrounds(r.decls))
  assert.deepEqual(body, ['var(--bg)'])
})

/** `selectors: why` for every rule in a stylesheet that paints the page wrapper (`.page`, not `.page-head`) opaquely */
export function opaquePageRules(css: string): string[] {
  const rules = [...css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  return rules
    .filter((m) => split(m[1].trim()).some((s) => /\.page(?![\w-])/.test(s)))
    .flatMap((m) => backgrounds(m[2]).flatMap((bg) => opaqueReason(bg) ?? []).map((why) => `${m[1].trim()}: ${why}`))
}

test('the page backdrop a stylesheet paints (.page in the look, app.css or a built-in theme.css) is see-through too', () => {
  const sheets = ['src/app.css', ...[...new Glob('themes/*/theme.css').scanSync(root)].sort()]
  assert.deepEqual(opaquePageRules(LOOK_CSS), [], 'the look (lookCss.ts)')
  const offenders = sheets.flatMap((f) => opaquePageRules(readFileSync(root + f, 'utf8')).map((o) => `${f} ${o}`))
  assert.ok(/\.page\{[^}]*background:/.test(LOOK_CSS), 'the look paints the backdrop')
  assert.deepEqual(offenders, [])
})

test('opaquePageRules reads .page rules only, through :where() scopes, and names an opaque one', () => {
  // a page's orbs are its own hues (--orb-N) mixed with transparent, so they can never be opaque
  const neon = '.page{background:radial-gradient(9px 5px at 8% 6%, color-mix(in oklch, var(--orb-1, var(--edge)) calc(var(--orb-1-a, 20%) * var(--look-orbs, 0)), transparent), transparent 62%)}'
  assert.deepEqual(opaquePageRules(neon), [])
  assert.deepEqual(opaquePageRules(':where([data-theme="x"]) .page{background:linear-gradient(color-mix(in oklch, red 5%, transparent) 1px, transparent 1px), var(--bg)}'), [':where([data-theme="x"]) .page: a solid var(--bg)'])
  assert.deepEqual(opaquePageRules('.page-head{background:var(--panel)} .pager{background:black}'), [])
})

test('opaqueReason: solid colors and opaque gradient stops are caught; gradients of transparent mixes pass', () => {
  const ok = [
    'radial-gradient(900px 520px at 80% -6%, color-mix(in oklch, var(--ac) 16%, transparent), transparent 62%), linear-gradient(color-mix(in oklch, var(--edge) 4.5%, transparent) 1px, transparent 1px) 0 0/44px 44px',
    'transparent',
    'none',
    'var(--ac-soft)',
    "{on ? 'color-mix(in oklch, var(--ac) 10%, transparent)' : 'transparent'}",
    'linear-gradient(to bottom, transparent, color-mix(in srgb, var(--bg) 40%, transparent))',
  ]
  for (const v of ok) assert.equal(opaqueReason(v), null, v)
  // a size after the gradient may hold var()s with fallbacks; only the stops inside are judged
  assert.equal(opaqueReason('linear-gradient(color-mix(in oklch, var(--edge) 9%, transparent) 1px, transparent 1px) 0 0/var(--s, 24px) var(--s, 24px)'), null)
  assert.notEqual(opaqueReason('linear-gradient(var(--edge) 1px, transparent 1px) 0 0/var(--s, 24px) var(--s, 24px)'), null)

  const bad = [
    'var(--bg)',
    'radial-gradient(900px 520px at 80% -6%, color-mix(in oklch, var(--ac) 16%, transparent), transparent 62%), var(--bg)',
    'linear-gradient(var(--bg), var(--panel))',
    'linear-gradient(to bottom, transparent, var(--bg) 90%)',
    'color-mix(in oklch, var(--ac) 8%, var(--panel))',
    "{on ? 'var(--panel)' : 'transparent'}",
    'var(--bg) !important',
    'black',
  ]
  for (const v of bad) assert.notEqual(opaqueReason(v), null, v)
})

test('the scan finds a wrapper in a style attribute and in a <style> rule, and ignores boxes that are not full-viewport', () => {
  const svelte = `<div style="min-height: 100vh; background: var(--bg)"></div>
<div style="min-height: 40vh; background: var(--bg)"></div>
<style>.page { height: 100dvh; background-color: var(--panel); } .card { background: var(--panel); }</style>`
  const found = blocks('x.svelte', svelte).filter(([, d]) => FULL.test(d))
  assert.deepEqual(found.map(([w]) => w), ['x.svelte .page', 'x.svelte:1'])
  assert.deepEqual(found.flatMap(([, d]) => backgrounds(d)), ['var(--panel)', 'var(--bg)'])
})
