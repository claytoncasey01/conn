// A theme's `case` and `tracking` reach chrome (.hud, button, nav) and, since text-transform inherits, everything inside it.
// User and model content inside chrome (a session name, an agent label, an option Claude wrote, an activity line, a
// command) must opt out with data-content, which app.css forces back to text-transform:none, or a `case: lowercase` theme
// rewrites it.
//
// Heuristic, text-based like the other source tests. It tokenizes each component's markup (script and style dropped) into
// tags and `{expr}` text interpolations, keeps a stack of open elements, and flags EVERY interpolation whose innermost
// chrome element above it comes before any data-content element (the chrome element itself may carry data-content).
// What may stay cased is listed below by file and exact expression: numbers, counts, times, costs, conn's own words
// (verbs, states, units) and modelName() labels. A new interpolation in chrome therefore fails until it is tagged or listed.
// Known limits: the chrome rule in app.css also covers label, h1-h4, th, legend and summary; this scan does not (no
// content was found in them when it was written). It does not follow a child component (a prop rendered inside another
// component's chrome is checked in that component). Interpolations inside attributes (title, aria-label) are not text and
// are skipped. {@html} is not expected. Expressions are matched as written, so reformatting one means updating its entry.
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { test } from 'node:test'

// Why an interpolation may be cased like chrome
const N = 'a number, count, time, duration or cost'
const W = "conn's own words: a verb, state, unit or glyph chosen in code"
const W_MODEL = "a model choice ('sonnet' / 'opus'), a constant, uppercased in code"
const W_CONST = 'a constant hint or label from this file'
const W_JS = 'a Jira field uppercased in code, shown as a chip like the status chips'
const W_UPPER = "uppercased in code like the other chips; the theme's case token (not an inline text-transform) then keeps it or lowers it"

// Deliberate exceptions: file -> exact expression -> why. Anything else inside .hud, button or nav needs data-content.
const ALLOW: Record<string, Record<string, string>> = {
  'pages/OpenWork.svelte': {
    '{busy ? \'Retrying…\' : \'Retry\'}': W,
    '{sum.open}': N,
    '{sum.runs}': N,
    '{sum.asks}': N,
    '{sum.review}': N,
    '{shown.length}': N,
    '{busy ? \'REFRESHING…\' : \'REFRESH\'}': W,
    '{list.fetchedAt ? `updated ${dur(live.now - list.fetchedAt)} ago` : \'not updated yet\'}': N,
    '{c.count}': N,
    '{t.type.toUpperCase()}': W_JS,
    '{sel.links}': N,
    '{sel.comments ?? \'–\'}': N,
    '{m.hint}': W_CONST,
    '{c.name.toUpperCase()}': 'a Jira status column uppercased in code, a chip like FilterBar\'s',
    '{st.label}': 'run state: RS constants, or the Jira status uppercased in code',
    '{f.label}': 'ALL / MINE / UNASSIGNED',
    '{m.label}': W_CONST,
    '{l.label}': W_CONST,
    '{l.hint}': W_CONST,
  },
  'pages/AdvisorPage.svelte': {
    '{source.archived ? \'history\' : \'sessions\'}': W,
    '{dur(total)}': N,
    '{r.n}': N,
    '{clock(r.c.at)}': N,
    '{r.c.endedAt === null && source.archived ? \'—\' : fmtMs(r.c.endedAt === null ? null : r.c.endedAt - r.c.at)}': N,
    '{cur.n}': N,
    '{ordered.length}': N,
    '{avgMs === null ? \'—\' : fmtMs(avgMs)}': N,
    '{spans.length ? fmtMs(sumMs) : \'—\'}': N,
    '{maxMs === null ? \'—\' : fmtMs(maxMs)}': N,
  },
  'pages/TicketRun.svelte': {
    '{live.connected ? \'Loading\' : \'Connecting…\'}': W,
    '{copied ? \'Copied\' : \'Copy\'}': W,
    '{left}': N,
    '{pending.length}': N,
    '{i + 1}': N,
    '{sending ? \'SENDING ANSWERS…\' : submitted ? \'ANSWERS SENT · WAITING ON THE SERVER\' : ready ? \'▶ SEND ANSWERS AND START THE BUILD\' : `ANSWER ${left} MORE TO CONTINUE`}': W,
    '{run.answered.length}': N,
    '{run.agents.length}': N,
    '{s.name}': 'pipeline stage, a constant (1 RESEARCH, 2 ASK...)',
    '{run.repos.length}': N,
  },
  'pages/Overview.svelte': {
    '{session.agentsRunning}': N,
    '{session.agentsTotal}': N,
    '{session.advisorCalls}': N,
    '{session.advisorCalls === 1 ? \'call\' : \'calls\'}': N,
    '{ctxPct}': N,
    '{toolCalls}': N,
    '{source.archived ? \'ARCHIVED\' : \'LIVE\'}': W,
    '{session.advisorCalls ? `CONSULTED · ${session.advisorCalls} ${session.advisorCalls === 1 ? \'CALL\' : \'CALLS\'}` : \'NOT CONSULTED\'}': W,
    '{a.nested}': N,
    '{a.calls}': N,
    '{a.calls === 1 ? \'CALL\' : \'CALLS\'}': W,
    '{runtimeOf(a)}': N,
    '{hidden.length}': N,
    '{value}': N,
    '{isLead ? \'Lead\' : \'Agent\'}': W,
    '{TEAMMATE_LABEL}': 'the TEAMMATE tag',
    '{labels.get(a.id) ?? \'\'}': 'agent code N-01, made by agentLabels',
    '{lk.label}': 'RUNNING / IDLE / DONE / STALLED / NEEDS INPUT',
  },
  'pages/ReviewsPage.svelte': {
    '{live.connected ? \'Loading\' : \'Connecting…\'}': W,
    '{shown.length}': N,
    '{rows.length}': N,
    '{busy ? \'Refreshing…\' : \'Refresh\'}': W,
  },
  'pages/EntryHeader.svelte': {
    '{asking === \'retry\' ? \'Asking…\' : \'Retry\'}': W,
    '{asking === m ? \'ASKING…\' : m.toUpperCase()}': W_MODEL,
    '{resuming ? \'Opening…\' : \'Resume\'}': W,
    '{copied ? \'Copied ✓\' : \'Copy\'}': W,
  },
  'pages/AgentPage.svelte': {
    '{source.archived ? \'history\' : \'sessions\'}': W,
    '{dur(total)}': N,
    '{r.n}': N,
    '{clock(r.c.at)}': N,
    '{fmtMs(r.c.ms)}': N,
    '{r.c.error ? \'Err ✕\' : r.c.ms === null ? \'Run\' : \'OK\'}': W,
    '{cur.n}': N,
    '{agent.calls}': N,
    '{agent.errors}': N,
    '{avgMs === null ? \'—\' : fmtMs(avgMs)}': N,
    '{running ? \'AWAITING RESULT\' : agentWaits(agent, source.archived) ? \'IDLE · LAST REPLY\' : agent.status === \'done\' ? \'RETURNED TO LEAD\' : \'STALLED\'}': W,
    '{labelOf(agent)}': 'LEAD or the agent code N-01',
  },
  'pages/HistoryPage.svelte': {
    '{backfill.done}': N,
    '{backfill.total}': N,
    '{info ? `${rows.length} OF ${total} SHOWN` : off ? \'UNAVAILABLE\' : \'LOADING\'}': W,
    '{loading ? \'LOADING…\' : `SHOW MORE · ${total - rows.length} LEFT`}': W,
  },
  'pages/MyPrsPage.svelte': {
    '{live.connected ? \'Loading\' : \'Connecting…\'}': W,
    '{shown === \'loading\' ? \'Loading\' : `${rows.length} open`}': W,
    '{busy ? \'Refreshing…\' : \'Refresh\'}': W,
    '{s.label}': 'sort: Opened / Last comment',
  },
  'pages/Sessions.svelte': {
    '{live.connected ? \'Loading\' : \'Connecting…\'}': W,
    '{fleet.sessions}': N,
    '{fleet.agents}': N,
    '{fleet.advisor}': N,
    '{fleet.prs}': N,
    '{shown.length}': N,
    '{sessions.length}': N,
    '{s.agentsTotal ? `${s.agentsRunning} / ${s.agentsTotal}` : \'0\'}': N,
    '{s.advisorCalls}': N,
    '{dur(live.now - s.startedAt)}': N,
  },
  'pages/ReviewRow.svelte': {
    '{row.number}': N,
    '{m.charAt(0).toUpperCase() + m.slice(1)}': W_MODEL,
    '{busy ? \'Starting…\' : again ? \'Re-review\' : \'Review\'}': W,
    '{copied ? \'Copied ✓\' : \'Copy\'}': W,
  },
  'lib/StopSession.svelte': {
    '{compact ? \'Stop\' : \'Stop session\'}': W,
    '{busy ? \'Stop anyway\' : \'Stop\'}': W,
  },
  'lib/ThreadPanel.svelte': {
    '{busy[t.id] ? \'SENDING…\' : s.button}': W,
  },
  'lib/ThemeMenu.svelte': {
    '{effectsOn ? \'On\' : \'Off\'}': W,
  },
  'lib/ReplyBox.svelte': {
    '{step === \'sending\' ? \'Sending…\' : \'Send\'}': W,
  },
  'lib/MyPrRow.svelte': {
    '{row.number}': N,
    '{m.charAt(0).toUpperCase() + m.slice(1)}': W_MODEL,
    '{threadsOpen ? \'Hide\' : \'Show\'}': W,
    '{row.threads.length}': N,
    '{row.threads.length === 1 ? \'thread\' : \'threads\'}': W,
    '{draftCopied ? \'Copied ✓\' : \'Copy\'}': W,
    '{copied ? \'Copied ✓\' : \'Copy\'}': W,
    '{busy ? \'Starting…\' : row.label}': 'Self-review / Re-review, a constant',
  },
  'lib/Nav.svelte': {
    '{waiting}': N,
    '{ready}': N,
    '{failed}': N,
    '{alerts.blocked ? \'Alerts blocked\' : alerts.enabled ? \'🔔 Alerts on\' : \'Alerts off\'}': W,
  },
  'lib/AgentSwitcher.svelte': {
    '{open ? \'▾\' : \'▸\'}': W,
    '{[done && `DONE · ${done}`, stalled && `STALLED · ${stalled}`].filter(Boolean).join(\' / \')}': W,
  },
  'lib/FilterBar.svelte': {
    '{c.count === undefined ? \'\' : ` ${c.count}`}': N,
    '{expanded[g.id] ? \'Show less\' : `Show all ${g.chips.length}`}': W,
    '{g.label}': 'the group name the caller writes (Status, Kind), in sentence case; the cap class gives it the case token',
    '{c.label}': 'the chip name the caller gives (Needs input, an author, a model); the cap class gives it the case token',
  },
  'lib/ConfirmAction.svelte': {
    '{label}': 'the button verb its caller passes (STOP)',
    '{confirmLabel}': 'the confirming verb',
  },
  'pages/ThemesPage.svelte': {
    '{f.label}': 'All / Installed / Community',
    '{counts[f.id]}': N,
    '{cards.length}': N,
    '{counts.all}': N,
    "{checking ? 'Checking…' : 'Check again'}": W,
    "{working[k] ? 'Updating…' : 'Update'}": W,
    "{current ? 'In use' : 'Use'}": W,
    "{working[k] ? (c.action === 'Update' ? 'Updating…' : 'Installing…') : c.action}": W,
  },
}
// modelName() turns a model id into conn's own uppercase label (SONNET 5.5); it is a label, not text anyone wrote.
const ALLOW_EXPR: RegExp[] = [/^\{modelName\([^)]*\)\}$/]

type Hit = { file: string; expr: string; line: number }
type Tok =
  | { kind: 'open'; name: string; tag: string; at: number; selfClosing: boolean }
  | { kind: 'close'; name: string; at: number }
  | { kind: 'expr'; expr: string; at: number }
  | { kind: 'text'; text: string; at: number }

// Skips a JS string starting at s[i] (one of ' " `); returns the index after it
function skipString(s: string, i: number): number {
  const q = s[i]
  for (let j = i + 1; j < s.length; j++) {
    if (s[j] === '\\') j++
    else if (s[j] === q) return j + 1
  }
  return s.length
}

// From s[i] === '{', the index after the matching '}'
function skipBraces(s: string, i: number): number {
  let depth = 0
  for (let j = i; j < s.length; j++) {
    const c = s[j]
    if (c === "'" || c === '"' || c === '`') j = skipString(s, j) - 1
    else if (c === '{') depth++
    else if (c === '}' && --depth === 0) return j + 1
  }
  return s.length
}

// From s[i] === '<', the index after the tag's closing '>' (braces and quoted attribute values may hold '>')
function skipTag(s: string, i: number): number {
  for (let j = i + 1; j < s.length; j++) {
    const c = s[j]
    if (c === '{') j = skipBraces(s, j) - 1
    else if (c === '"') j = s.indexOf('"', j + 1)
    else if (c === '>') return j + 1
    if (j < 0) return s.length
  }
  return s.length
}

const VOID = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr'])

// The markup as tags, interpolations and text, script, style and comments blanked (line numbers stay true)
function tokens(src: string): { s: string; toks: Tok[] } {
  const blank = (m: string) => m.replace(/[^\n]/g, ' ')
  const s = src.replace(/<script[\s\S]*?<\/script>/g, blank).replace(/<style[\s\S]*?<\/style>/g, blank).replace(/<!--[\s\S]*?-->/g, blank)
  const toks: Tok[] = []
  let i = 0
  let text = ''
  let textAt = 0
  const flush = () => {
    if (text.trim()) toks.push({ kind: 'text', text, at: textAt })
    text = ''
  }
  while (i < s.length) {
    const c = s[i]
    if (c === '<' && /[a-zA-Z/]/.test(s[i + 1] ?? '')) {
      flush()
      const end = skipTag(s, i)
      const tag = s.slice(i, end)
      if (tag[1] === '/') toks.push({ kind: 'close', name: /^<\/\s*([\w:.-]+)/.exec(tag)?.[1] ?? '', at: i })
      else {
        const name = /^<([\w:.-]+)/.exec(tag)?.[1] ?? ''
        toks.push({ kind: 'open', name, tag, at: i, selfClosing: tag.endsWith('/>') || VOID.has(name.toLowerCase()) })
      }
      i = end
    } else if (c === '{') {
      flush()
      const end = skipBraces(s, i)
      toks.push({ kind: 'expr', expr: s.slice(i, end), at: i })
      i = end
    } else {
      if (!text) textAt = i
      text += c
      i++
    }
  }
  flush()
  return { s, toks }
}

const lineOf = (s: string, at: number) => s.slice(0, at).split('\n').length
const isBlock = (expr: string) => /^\{\s*[#:/]/.test(expr)

export function uncased(file: string, src: string): Hit[] {
  const { s, toks } = tokens(src)
  const stack: { name: string; chrome: boolean; content: boolean }[] = []
  const hits: Hit[] = []
  for (const t of toks) {
    if (t.kind === 'open') {
      const cls = /\sclass=(?:"([^"]*)"|\{([^}]*)\})/.exec(t.tag)
      const hud = !!cls && /(^|[\s'"])hud([\s'"]|$)/.test(cls[1] ?? cls[2] ?? '')
      if (!t.selfClosing) stack.push({ name: t.name, chrome: hud || t.name === 'button' || t.name === 'nav', content: /\sdata-content(?=[\s=/>])/.test(t.tag) })
    } else if (t.kind === 'close') {
      const at = stack.map((e) => e.name).lastIndexOf(t.name)
      if (at >= 0) stack.length = at
    } else if (t.kind === 'expr' && !isBlock(t.expr) && !/^\{\s*@/.test(t.expr)) {
      for (let k = stack.length - 1; k >= 0; k--) {
        if (stack[k].content) break
        if (stack[k].chrome) {
          hits.push({ file, expr: t.expr.replace(/\s+/g, ' '), line: lineOf(s, t.at) })
          break
        }
      }
    }
  }
  return hits
}

// A bare <span data-content> wrapper inside a flex or grid parent (or one with a gap) becomes a flex item of its own, so it
// must hold the whole text run: text or an interpolation right beside it would be split into separate items, with the gap
// between them. Svelte blocks ({#if}, {:else}, {/if}) separate runs. Limit: only an inline style is read (display: flex,
// inline-flex, grid, or a gap); a parent made flex by a stylesheet class is not seen.
export function splitRuns(file: string, src: string): Hit[] {
  const { s, toks } = tokens(src)
  type El = { name: string; flex: boolean; kids: ('text' | 'wrap' | 'el' | 'sep')[]; at: number }
  const stack: El[] = []
  const hits: Hit[] = []
  const kid = (k: El['kids'][number]) => stack.at(-1)?.kids.push(k)
  for (const t of toks) {
    if (t.kind === 'open') {
      const wrap = /^<span\s+data-content\s*>$/.test(t.tag)
      kid(wrap ? 'wrap' : 'el')
      const style = /\sstyle="([^"]*)"/.exec(t.tag)?.[1] ?? ''
      if (!t.selfClosing) stack.push({ name: t.name, flex: /display:\s*(inline-)?(flex|grid)|(^|[;\s])gap:/.test(style), kids: [], at: t.at })
    } else if (t.kind === 'close') {
      const at = stack.map((e) => e.name).lastIndexOf(t.name)
      if (at < 0) continue
      const el = stack[at]
      stack.length = at
      if (!el.flex) continue
      el.kids.forEach((k, i) => {
        if (k === 'wrap' && (el.kids[i - 1] === 'text' || el.kids[i + 1] === 'text')) hits.push({ file, expr: `<${el.name}> splits a text run around <span data-content>`, line: lineOf(s, el.at) })
      })
    } else if (t.kind === 'expr') kid(isBlock(t.expr) ? 'sep' : /^\{\s*@/.test(t.expr) ? 'el' : 'text')
    else kid('text')
  }
  return hits
}

const svelteFiles = () => {
  const dir = (d: string) => readdirSync(new URL(d, import.meta.url)).filter((f) => f.endsWith('.svelte')).map((f) => d + f)
  return [...dir('../pages/'), ...dir('./'), '../App.svelte']
}
const each = (fn: (file: string, src: string) => Hit[]) =>
  svelteFiles().flatMap((f) => fn(f.replace(/^\.\.\//, '').replace(/^\.\//, 'lib/'), readFileSync(new URL(f, import.meta.url), 'utf8')))

test('content inside .hud, button or nav carries data-content, so a cased theme never rewrites it', () => {
  const left = each(uncased)
    .filter((h) => !Object.hasOwn(ALLOW[h.file] ?? {}, h.expr) && !ALLOW_EXPR.some((r) => r.test(h.expr)))
    .map((h) => `${h.file}:${h.line} ${h.expr}`)
  assert.deepEqual(left, [], `content in chrome without data-content:\n${left.join('\n')}`)
})

test('every allow-list entry is still in use, so the list stays as narrow as the code', () => {
  const seen = new Set(each(uncased).map((h) => `${h.file} ${h.expr}`))
  const stale = Object.entries(ALLOW).flatMap(([f, m]) => Object.keys(m).filter((e) => !seen.has(`${f} ${e}`)).map((e) => `${f} ${e}`))
  assert.deepEqual(stale, [])
})

test('a data-content wrapper never splits a text run inside a flex or grid parent (its gap would open up)', () => {
  const split = each(splitRuns).map((h) => `${h.file}:${h.line} ${h.expr}`)
  assert.deepEqual(split, [], split.join('\n'))
})

test('the scan: every interpolation in chrome is flagged, data-content on the element or a wrapping span clears it', () => {
  const src = [
    '<script>let row = { name: "x" }</script>',
    '<span class="hud">{row.name}</span>',
    '<span class="hud" data-content>{row.name}</span>',
    '<button onclick={() => go(a > b)}>STOP <span data-content>{row.name}</span></button>',
    '<button>{count} items</button>',
    '<nav><a href="#">{plainText(t)}</a></nav>',
    '<div data-content><span class="hud">{label}</span></div>',
    '{#if row.name}<p>{row.name}</p>{/if}',
    '<button title={row.name}>GO</button>',
  ].join('\n')
  assert.deepEqual(uncased('x.svelte', src).map((h) => `${h.line} ${h.expr}`), ['2 {row.name}', '5 {count}', '6 {plainText(t)}', '7 {label}'])
})

test('the run check: a wrapper beside text in a flex parent is flagged; a wrapper holding the whole run, or in a block parent, is not', () => {
  const src = [
    '<a style="display: flex; gap: 8px"><span class="led"></span>◂ <span data-content>{n}</span></a>',
    '<a style="display: flex; gap: 8px"><span class="led"></span><span data-content>◂ {n}</span></a>',
    '<button style="display: flex; gap: 12px">{#if w}STARTING <span data-content>{k}</span>…{:else}<span>▶</span><span data-content>START {k}</span>{/if}</button>',
    '<div class="hud" style="font-size: 12px">CALL // <span data-content>{n}</span></div>',
  ].join('\n')
  assert.deepEqual(splitRuns('x.svelte', src).map((h) => h.line), [1, 3])
})
