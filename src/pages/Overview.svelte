<script lang="ts">
  import { live } from '../lib/live.svelte.ts'
  import { bootIn, bootOut, glowIn, glowOut, rowIn, rowOut } from '../lib/fx.ts'
  import { RETURN_MS, agentLabels, agentName, agentWaits, ago, dur, modelName, tokens, usd, visibleAgents } from '../lib/format.ts'
  import { TEAMMATE_COLORS, TEAMMATE_LABEL, isTeammateAgent } from '../lib/team.ts'
  import { href } from '../lib/router.svelte.ts'
  import { waitingAgents } from '../lib/ask.ts'
  import { HEAD_SHAPES, headsOf, type Heads, type Tip } from '../lib/treeHeads.ts'
  import Brand from '../lib/Brand.svelte'
  import StatusFooter from '../lib/StatusFooter.svelte'
  import ArchiveNotice from '../lib/ArchiveNotice.svelte'
  import ArchivedMark from '../lib/ArchivedMark.svelte'
  import EntryHeader from './EntryHeader.svelte'
  import type { SessionSource } from '../lib/source.svelte.ts'
  import type { Agent } from '../lib/types.ts'

  let { source }: { source: SessionSource } = $props()
  const born = Date.now()

  const session = $derived(source.session())
  // Agents waiting on the person (an archive has none): drawn first, whatever their age
  const waiting = $derived(session && !source.archived ? waitingAgents(session, live.snap?.sessions ?? []) : new Set<string>())
  const agents = $derived(session ? visibleAgents(session.agents, source.now(), source.archived, waiting) : [])
  const labels = $derived(session ? agentLabels(session.agents) : new Map<string, string>())

  // ---- slot math: the visible agents, folded to at most four card slots ----
  const hasMore = $derived(agents.length > 4)
  const real = $derived(hasMore ? agents.slice(0, 3) : agents)
  const hidden = $derived(hasMore ? agents.slice(3) : [])
  const slots = $derived(real.length + (hasMore ? 1 : 0))
  // one char per slot: is any agent behind it running. A primitive, so geometry only recomputes when it flips.
  const runKey = $derived(
    real.map(a => (a.status === 'running' ? '1' : '0')).join('') + (hasMore ? (hidden.some(a => a.status === 'running') ? '1' : '0') : '')
  )

  // An agent that actually came back a moment ago. Only then does the return path flow. An archive has no moment ago.
  const isBack = (a: Agent) => !source.archived && a.status === 'done' && source.now() - (a.endedAt ?? a.lastAt) < RETURN_MS
  const retKey = $derived(
    real.map(a => (isBack(a) ? '1' : '0')).join('') + (hasMore ? (hidden.some(isBack) ? '1' : '0') : '')
  )
  // A derived boolean: it re-evaluates each second but only notifies when it flips, so nothing is rebuilt.
  const returning = $derived(agents.some(isBack))

  type Comet = { id: string; path: string; color: string; dur: number; r: number; op: number; begin: string }
  type Junction = { key: string; x: number; y: number; color: string }

  const f = (x: number) => x.toFixed(2)

  // the heads of the link between the lead and the advisor: it never moves
  const LINK_HEADS = headsOf([[437, 172, 'r'], [201, 172, 'l']])
  // a packet's trail: radius, opacity and how far behind the head it runs (s)
  const TRAIL: [number, number, number][] = [[4, 1, 0], [2.8, 0.55, 0.14], [1.9, 0.3, 0.28]]
  // a glyphs flow's characters: fixed sets, never text from a theme (wire-glyphs picks one); the first leads the stream
  const GLYPH_SETS: [string, string[]][] = [['binary', ['1', '0', '1', '1']], ['hex', ['F', '3', 'A', '7']], ['kana', ['ｱ', 'ﾑ', 'ｶ', 'ﾀ']], ['ascii', ['#', '%', '@', '&']]]
  // the lead and the advisor trading a message, right after a consult: one packet each way on the link between them
  const LIT = [
    { key: 'to-advisor', path: 'M435 172 H200', at: -1.5, color: 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' },
    { key: 'to-lead', path: 'M200 172 H435', at: -0.3, color: 'color-mix(in oklab, var(--role-advisor) 35%, var(--bright))' },
  ]

  function geometry(slotCount: number, running: string, back: string) {
    const w = slotCount >= 4 ? 138 : 190
    const step = slotCount >= 4 ? 158 : 210
    const cx: number[] = []
    for (let i = 0; i < slotCount; i++) cx.push(Math.round(555 + (i - (slotCount - 1) / 2) * step))
    const c0 = cx[0]
    const cL = cx[slotCount - 1]
    const has = slotCount > 0

    const wires = {
      trunk: has ? 'M555 214 V260' : '',
      bus: slotCount > 1 ? 'M' + c0 + ' 260 H' + cL : '',
      branches: cx.map(x => 'M' + x + ' 260 V296').join(' '),
      heads: headsOf(cx.map((x): Tip => [x, 297, 'd'])),
      stems: has ? cx.map(x => 'M' + x + ' 510 V550').join(' ') + (slotCount > 1 ? ' M' + c0 + ' 550 H' + cL : '') + ' M555 550 V580' : '',
      retHead: headsOf(has ? [[555, 581, 'd']] : []),
      loop: has ? 'M675 615 H885 V172 H675' : '',
      loopHead: headsOf(has ? [[675, 172, 'l']] : []),
      advA: has ? 'M200 403 H' + (c0 - w / 2) : '',
      advAHeads: headsOf(has ? [[c0 - w / 2 + 2, 403, 'r'], [201, 403, 'l']] : []),
      advR: has ? 'M200 615 H435' : '',
      advRHeads: headsOf(has ? [[437, 615, 'r'], [201, 615, 'l']] : []),
      flowDown: ''
    }
    wires.flowDown = [wires.trunk, wires.bus, wires.branches].filter(Boolean).join(' ')

    const seen = new Set<string>()
    const junctions: Junction[] = []
    const addJ = (x: number, y: number, color: string) => {
      const key = x + ',' + y
      if (!seen.has(key)) {
        seen.add(key)
        junctions.push({ key, x, y, color })
      }
    }
    if (has) {
      addJ(555, 260, 'var(--role-lead)')
      addJ(555, 550, 'var(--tx)')
    }
    cx.forEach(x => {
      addJ(x, 260, 'var(--role-agent)')
      addJ(x, 550, 'var(--tx)')
    })

    const comets: Comet[] = []
    const trail = TRAIL
    const addComet = (slot: number, kind: string, path: string, color: string, d: number, b: number) =>
      trail.forEach(([r, op, off]) => comets.push({ id: `${slot}-${kind}-${r}`, path, color, dur: d, r, op, begin: f(b + off) }))
    cx.forEach((x, i) => {
      if (running[i] === '1') {
        const down = 'M555 214 V260 H' + x + ' V296'
        const dd = 2.2 + 0.2 * i
        addComet(i, 'd1', down, i % 2 ? 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' : 'var(--role-lead)', dd, -0.4 * i)
        addComet(i, 'd2', down, i % 2 ? 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' : 'var(--role-lead)', dd, -0.4 * i - dd / 2)
      }
      if (back[i] === '1') {
        const up = 'M' + x + ' 510 V550 H555 V580'
        addComet(i, 'up', up, i % 2 ? 'var(--tx)' : 'color-mix(in oklab, var(--role-agent) 55%, var(--bright))', 1.6 + 0.15 * i, -0.3 - 0.35 * i)
      }
    })
    const loopComets = has ? trail.map(([r, op, off]) => ({ id: `loop-${r}`, r, op, begin: f(-1 + off), path: wires.loop })) : []

    const pos = cx.map(x => ({ left: f((x - w / 2) / 9), width: f(w / 9) }))
    return { wires, junctions, comets, loopComets, pos }
  }

  const geo = $derived(geometry(slots, runKey, retKey))
  // With no sub-agents the lead and advisor sit alone, so the pair moves to the middle of the stage (y 172 -> 340).
  const hasTree = $derived(slots > 0)
  const dy = $derived(hasTree ? 0 : 168)
  const dyPct = $derived(dy / 6.8)

  // ---- selection: the lead, the advisor, or an agent id ----
  const ADVISOR = ':advisor'
  let sel = $state<string | null>(null)
  const selected = $derived(sel && sel !== 'lead' && sel !== ADVISOR ? (real.find(a => a.id === sel) ?? null) : null)
  const selKind = $derived<'lead' | 'advisor' | 'agent'>(
    sel === 'lead' ? 'lead' : sel === ADVISOR ? 'advisor' : selected ? 'agent' : real.some(a => a.status === 'running') ? 'agent' : 'lead'
  )
  // what the aside shows when it is an agent-shaped record: an agent card, or the lead
  const shown = $derived<Agent | null>(
    !session || selKind === 'advisor' ? null : selKind === 'lead' ? session.lead : (selected ?? real.find(a => a.status === 'running') ?? null)
  )
  const selId = $derived(selKind === 'lead' ? 'lead' : selKind === 'advisor' ? ADVISOR : (shown?.id ?? null))

  // a live session's teammate between messages waits for the next one: idle, like the lead, not done
  const look = (a: Agent) =>
    a.status === 'running'
      ? { dot: 'var(--info)', border: 'color-mix(in oklch, var(--info) 50%, transparent)', label: 'RUNNING' }
      : agentWaits(a, source.archived)
        ? { dot: 'var(--idle)', border: 'color-mix(in oklch, var(--idle) 35%, transparent)', label: 'IDLE' }
        : a.status === 'done'
          ? { dot: 'var(--ok)', border: 'color-mix(in oklch, var(--ok) 35%, transparent)', label: 'DONE ✓' }
          : { dot: 'color-mix(in oklab, var(--warn) 80%, var(--panel))', border: 'color-mix(in oklch, color-mix(in oklab, var(--warn) 80%, var(--panel)) 34%, transparent)', label: 'STALLED' }

  // the same yellow as the session card's NEEDS INPUT
  const waitLook = { dot: 'var(--warn)', border: 'color-mix(in oklch, var(--warn) 50%, transparent)', label: 'NEEDS INPUT' }
  const lookOf = (a: Agent) => (waiting.has(a.id) ? waitLook : look(a))

  const runtimeOf = (a: { startedAt: number; endedAt: number | null }) => dur((a.endedAt ?? source.now()) - a.startedAt)

  // the lead is busy, waiting on the person or idle, never stalled or done
  const leadLook = $derived(
    session?.status === 'busy'
      ? { dot: 'var(--info)', border: '', label: 'RUNNING' }
      : session?.status === 'waiting'
        ? { dot: 'var(--warn)', border: 'color-mix(in oklch, var(--warn) 50%, transparent)', label: 'NEEDS INPUT' }
        : { dot: 'var(--idle)', border: '', label: 'IDLE' }
  )
  const shownLook = $derived(selKind === 'lead' ? leadLook : shown ? lookOf(shown) : leadLook)

  const mix = $derived.by(() => {
    if (!shown) return []
    const rows = Object.entries(shown.tools).sort((a, b) => b[1] - a[1]).slice(0, 6)
    const top = rows.length ? rows[0][1] : 1
    return rows.map(([name, n]) => ({ name, n, pct: Math.round((n / top) * 100) }))
  })

  // ---- hidden (overflow) agents ----
  const hiddenRunning = $derived(hidden.filter(a => a.status === 'running').length)
  const hiddenDone = $derived(hidden.filter(a => a.status === 'done').length)
  const hiddenWaiting = $derived(hidden.filter(a => waiting.has(a.id)).length)

  // ---- stats ----
  const toolCalls = $derived(session ? session.leadCalls + session.agents.reduce((n, a) => n + a.calls, 0) : 0)
  const ctxPct = $derived(session && session.ctxWindow ? Math.min(100, Math.round((session.ctxTokens / session.ctxWindow) * 100)) : 0)
  const runtime = $derived(session ? dur(source.now() - session.startedAt) : '')

  // ---- advisor ----
  const consults = $derived(session ? [...session.consults].sort((a, b) => b.at - a.at) : [])
  const advisorLines = $derived(session ? session.log.filter(l => l.who === 'advisor').sort((a, b) => b.at - a.at) : [])
  const lastAdvisorAt = $derived(consults.length ? consults[0].at : advisorLines.length ? advisorLines[0].at : 0)
  // ask/answer comets: a consult is still open, or one finished in the last 8s. Never on an archive, whose clock is stopped.
  const lit = $derived(!source.archived && !!session && session.consults.some(c => c.endedAt === null || source.now() - c.endedAt < 8000))
  const unanswered = $derived(consults.some(c => c.endedAt === null))
  // Nothing is still answering a consult on an archive: it is UNANSWERED and does not blink
  const answering = $derived(!source.archived && unanswered)
  const finished = $derived(consults.flatMap(c => (c.endedAt === null ? [] : [c.endedAt - c.at])))
  const totalMs = $derived(finished.reduce((n, d) => n + d, 0))
  const avgMs = $derived(finished.length ? totalMs / finished.length : 0)

  // ---- return box ----
  const retLine = $derived.by(() => {
    const back = agents.filter(a => a.status === 'done').length
    const waiting = agents.filter(a => a.status !== 'done').map(a => agentName(a))
    return `${back} OF ${agents.length} BACK` + (waiting.length ? ` · AWAITING ${waiting.slice(0, 2).join(', ')}${waiting.length > 2 ? '…' : ''}` : '')
  })

  // ---- session log ----
  const clock = (at: number) => new Date(at).toLocaleTimeString('en-GB')
  const whoColor = { lead: 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))', advisor: 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))', agent: 'color-mix(in oklab, var(--role-agent) 55%, var(--bright))' } as const
  const log = $derived.by(() => {
    if (!session) return []
    const seen = new Map<string, number>()
    return [...session.log]
      .sort((a, b) => b.at - a.at)
      .slice(0, 7)
      .map((l, i) => {
        const base = `${l.at}|${l.who}|${l.label}|${l.text}`
        const n = seen.get(base) ?? 0
        seen.set(base, n + 1)
        return { key: `${base}#${n}`, t: clock(l.at), who: l.who === 'agent' ? l.label : l.who, color: whoColor[l.who], text: l.text, o: Math.max(0.4, 1 - i * 0.1).toFixed(2) }
      })
  })

  const hostLabel = typeof location === 'undefined' ? '' : location.host
</script>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <!-- top bar -->
  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) 72%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    {#if source.archived}
    <nav aria-label="Breadcrumb" style="display: flex; flex-wrap: wrap; gap: 8px; flex: 1 1 360px; min-width: 0; max-width: 100%">
      <a class="tab" href={source.href.back()} style="display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--ac) 55%, transparent); background: color-mix(in oklch, var(--ac) 10%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); font-size: 12px; letter-spacing: var(--tracking); text-decoration: none; box-shadow: 0 0 18px color-mix(in oklch, var(--ac) calc(18% * var(--glow)), transparent)"><span class="cap"><span class="g-back" aria-hidden="true"></span> History</span></a>
      {#if session}
        <span class="brk" aria-current="page" style="--c: var(--role-advisor); min-width: 0; max-width: 100%; display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 50%, transparent); background: color-mix(in oklch, var(--role-advisor) 10%, transparent); color: color-mix(in oklab, var(--role-advisor) 30%, var(--bright)); font-size: 13px"><span class="led" style="flex-shrink: 0; width: 8px; height: 8px; background: var(--role-advisor)"></span><span data-content title={session.name} style="min-width: 0; max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{session.name}</span></span>
      {/if}
    </nav>
    <ArchivedMark endedAt={source.now()} />
    {:else}
    <nav aria-label="Running sessions" style="display: flex; flex-wrap: wrap; gap: 8px; flex: 1 1 360px">
      {#each live.snap?.sessions ?? [] as t (t.id)}
        {#if t.id === source.id}
          <a class="tab brk" in:bootIn|global={{ skip: Date.now() - born < 800 }} out:bootOut|global href={href.overview(t.id)} aria-current="page" style="--c: var(--role-lead); display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright)); font-size: 13px; text-decoration: none; box-shadow: 0 0 20px color-mix(in oklch, var(--role-lead) calc(18% * var(--glow)), transparent)"><span class="led blink" style="flex-shrink: 0; width: 8px; height: 8px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></span><span data-content style="max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{t.name}</span></a>
        {:else}
          <a class="tab" in:bootIn|global={{ skip: Date.now() - born < 800 }} out:bootOut|global href={href.overview(t.id)} style="display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: var(--mu); font-size: 13px; text-decoration: none"><span class="led" style="flex-shrink: 0; width: 8px; height: 8px; background: {t.status === 'busy' ? 'var(--ok)' : t.status === 'waiting' ? 'var(--warn)' : 'color-mix(in oklab, var(--mu) 65%, var(--bg))'}; box-shadow: {t.status === 'busy' ? '0 0 8px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)' : t.status === 'waiting' ? '0 0 8px color-mix(in oklch, var(--warn) calc(100% * var(--glow)), transparent)' : 'none'}"></span><span data-content style="max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{t.name}</span></a>
        {/if}
      {/each}
      <a class="tab" href={href.sessions()} style="display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px dashed color-mix(in oklch, var(--edge) 28%, transparent); background: transparent; color: var(--mu); font-size: 13px; text-decoration: none"><span>all sessions <span class="g-fwd" aria-hidden="true"></span></span></a>
    </nav>
    <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class={['led', source.connected() ? 'blink' : '']} style="width: 8px; height: 8px; background: {source.connected() ? 'var(--ok)' : 'var(--bad)'}; box-shadow: 0 0 10px color-mix(in oklch, {source.connected() ? 'var(--ok)' : 'var(--bad)'} calc(100% * var(--glow)), transparent)"></span>{source.connected() ? 'LINK ESTABLISHED' : 'LINK LOST'}{hostLabel ? ' · ' + hostLabel : ''}</div>
    {/if}
  </header>

  {#if !source.connected() && source.state() !== 'loading'}
    <div role="status" style="position: relative; padding: 8px 32px; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 35%, transparent); font-size: 11px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--bad) 55%, var(--bright))">SERVER DISCONNECTED <span class="g-sep" aria-hidden="true"></span> showing the last snapshot, retrying</div>
  {/if}

  {#if source.archived && source.state() !== 'ready'}
    <main style="flex: 1; display: flex; align-items: center; justify-content: center; padding: 40px 32px">
      <ArchiveNotice {source} />
    </main>
  {:else if source.state() === 'loading'}
    <main style="flex: 1; display: flex; align-items: center; justify-content: center; padding: 40px 32px">
      <div class="brk" style="--c: color-mix(in oklch, var(--edge) 60%, transparent); padding: 28px 40px; border: 1px dashed color-mix(in oklch, var(--edge) 30%, transparent); border-radius: calc(var(--radius) * 1.5); text-align: center; font-size: 12px; line-height: 1.6; letter-spacing: var(--tracking); color: var(--mu)">
        <div class="hud" style="font-size: 18px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--edge) 45%, var(--bright))">CONNECTING<span class="blink">…</span></div>
        <div style="margin-top: 6px">Waiting for the first snapshot from the local server.</div>
      </div>
    </main>
  {:else if !session}
    <main style="flex: 1; display: flex; align-items: center; justify-content: center; padding: 40px 32px">
      <div class="brk" style="--c: color-mix(in oklch, var(--role-lead) 60%, transparent); padding: 28px 40px; border: 1px dashed color-mix(in oklch, var(--role-lead) 35%, transparent); border-radius: calc(var(--radius) * 1.5); text-align: center; font-size: 12px; line-height: 1.6; letter-spacing: calc(var(--tracking) * 0.7143); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))">
        <div class="hud cap" style="font-size: 18px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">Session not found</div>
        <div style="margin-top: 6px">No live session has the id {source.id}. It may have ended.</div>
        <a class="tab" href={source.href.back()} style="display: inline-flex; align-items: center; justify-content: center; margin-top: 16px; padding: 8px 18px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 50%, transparent); background: color-mix(in oklch, var(--edge) 12%, transparent); color: var(--tx); font-size: 12px; letter-spacing: var(--tracking); text-decoration: none"><span class="cap">All sessions <span class="g-fwd" aria-hidden="true"></span></span></a>
      </div>
    </main>
  {:else}
    <main style="flex: 1; display: flex; flex-wrap: wrap; justify-content: center; gap: 20px; padding: 24px 32px 40px; align-items: flex-start">

      <div style="flex: 1 1 760px; max-width: 1240px; min-width: 0; display: flex; flex-direction: column; gap: 16px">

        {#if source.archived}
          <EntryHeader {source} />
        {/if}

        <!-- stats -->
        <section aria-label="Session stats" class="tiles" style="--cols: 5">
          <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--role-lead) 13%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--role-lead) 28%, transparent); --r: calc(var(--radius) * 1.5); padding: 12px 14px">
            <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))" class="cap">Lead</div>
            <div class="hud" style="font-weight: 700; font-size: 23px; margin-top: 4px; color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--role-lead) calc(60% * var(--glow)), transparent)">{modelName(session.model)}</div>
            <div style="font-size: 11px; color: color-mix(in oklab, var(--role-lead) 30%, var(--mu)); margin-top: 2px">{session.leadCalls} lead calls</div>
          </div>
          <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--role-agent) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--role-agent) 28%, transparent); --r: calc(var(--radius) * 1.5); padding: 12px 14px">
            <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--role-agent); box-shadow: 0 0 10px color-mix(in oklch, var(--role-agent) calc(100% * var(--glow)), transparent)"></div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-agent) 40%, var(--mu))" class="cap">Agents</div>
            <div class="hud" style="font-weight: 700; font-size: 23px; margin-top: 4px; color: color-mix(in oklab, var(--role-agent) 55%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--role-agent) calc(60% * var(--glow)), transparent)">{session.agentsRunning}/{session.agentsTotal}</div>
            <div style="font-size: 11px; color: var(--mu); margin-top: 2px">{session.agentsTotal ? `${session.agentsRunning} running · ${session.agentsTotal - session.agentsRunning} finished` : 'none spawned'}</div>
          </div>
          <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--role-advisor) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--role-advisor) 28%, transparent); --r: calc(var(--radius) * 1.5); padding: 12px 14px">
            <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)"></div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Advisor</div>
            <div class="hud" style="font-weight: 700; font-size: 23px; margin-top: 4px; color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--role-advisor) calc(60% * var(--glow)), transparent)">{session.advisorCalls} {session.advisorCalls === 1 ? 'call' : 'calls'}</div>
            <div style="font-size: 11px; color: color-mix(in oklab, var(--role-advisor) 10%, var(--mu)); margin-top: 2px">{lastAdvisorAt ? `last ${ago(lastAdvisorAt, source.now())}` : session.advisorCalls ? modelName(session.advisorModel) : 'not consulted'}</div>
          </div>
          <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--tx) 9%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--tx) 20%, transparent); --r: calc(var(--radius) * 1.5); padding: 12px 14px">
            <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--tx); box-shadow: 0 0 10px color-mix(in oklch, var(--tx) calc(100% * var(--glow)), transparent)"></div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Context</div>
            <div class="hud" style="font-weight: 700; font-size: 23px; margin-top: 4px; text-shadow: 0 0 16px color-mix(in oklch, var(--tx) calc(45% * var(--glow)), transparent)">{ctxPct}%</div>
            <div class="seg" style="height: 5px; margin-top: 9px; position: relative"><div style="position: absolute; left: 0; top: 0; bottom: 0; width: {ctxPct}%; overflow: hidden"><div style="width: 1000px; height: 100%; background: repeating-linear-gradient(90deg,var(--tx) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div></div></div>
          </div>
          <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--ok) 11%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--ok) 24%, transparent); --r: calc(var(--radius) * 1.5); padding: 12px 14px">
            <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--ok) 55%, var(--mu))" class="cap">Tool calls</div>
            <div class="hud" style="font-weight: 700; font-size: 23px; margin-top: 4px; color: color-mix(in oklab, var(--ok) 60%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--ok) calc(50% * var(--glow)), transparent)">{toolCalls}</div>
            <div style="font-size: 11px; color: color-mix(in oklab, var(--ok) 30%, var(--mu)); margin-top: 2px">{session.errors} {session.errors === 1 ? 'error' : 'errors'} · up {runtime}</div>
          </div>
        </section>

        <!-- the stage -->
        <section aria-label="Agent tree" class="brk stage" style="--c: color-mix(in oklch, var(--edge) 75%, transparent); position: relative; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 20%, transparent); border-radius: var(--radius); padding: var(--stage-pt, 44px) var(--stage-px, 28px) 28px; overflow: hidden; backdrop-filter: var(--look-surf-blur, blur(6px)); box-shadow: inset 0 1px 0 color-mix(in oklch, var(--bright) 6%, transparent), 0 30px 80px color-mix(in oklch, var(--sunk) 45%, transparent)">
          <div class="stage-ticks" aria-hidden="true" style="position: absolute; left: 0; top: var(--stage-pt, 44px); bottom: 28px; width: 7px"></div>
          <div class="stage-ticks" aria-hidden="true" style="position: absolute; right: 0; top: var(--stage-pt, 44px); bottom: 28px; width: 7px"></div>
          <div class="beam" aria-hidden="true"></div>
          <div class="tree-head" style="position: absolute; left: 20px; right: 20px; top: 14px; display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px">
            <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">TREE <span class="g-sep" aria-hidden="true"></span> {source.archived ? 'ARCHIVED' : 'LIVE'}<span class="tree-hint">&nbsp;· SWIPE ◂ ▸</span></div>
            <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{session.agentsTotal} AGENTS · {session.agentsRunning} RUNNING · T+{runtime}</div>
          </div>
          <div class="tree-scroll">
          <div class="tree-graph" style="position: relative; width: 100%; max-width: 1180px; aspect-ratio: 900 / 680; margin: 0 auto">
            <div class="stage-glow" aria-hidden="true" style="position: absolute; left: 34%; top: {(-8 + dyPct).toFixed(2)}%; width: 54%; height: 48%; pointer-events: none"></div>
            <div class="stage-floor" aria-hidden="true" style="position: absolute; left: 0; right: 0; bottom: 0; height: 34%; overflow: hidden; perspective: 420px; pointer-events: none; -webkit-mask-image: linear-gradient(to top, black, transparent); mask-image: linear-gradient(to top, black, transparent)"><div style="position: absolute; inset: 0; transform: rotateX(62deg); transform-origin: 50% 100%; background: linear-gradient(color-mix(in oklch, var(--edge) 40%, transparent) 1px, transparent 1px) 0 0/100% 34px, linear-gradient(90deg, color-mix(in oklch, var(--edge) 40%, transparent) 1px, transparent 1px) 0 0/48px 100%"></div></div>

            <svg width="100%" height="100%" viewBox="0 0 900 680" style="position: absolute; left: 0; top: 0; overflow: visible" aria-hidden="true">
              <!-- a wire in each of its parts and a head in each of its shapes: the wire-style and wire-head tokens show one (lookCss.ts) -->
              {#snippet wire(d: string, color: string, op: number, wk: number)}
                <path class="wsh" d={d} fill="none" stroke-opacity={op} style="color: {color}; --wk: {wk}"></path>
                <path class="wb" d={d} stroke={color} stroke-opacity={op} fill="none" style="--wk: {wk}"></path>
                <path class="wcut" d={d} fill="none" style="--wk: {wk}"></path>
                <path class="wsn" d={d} fill="none" style="color: {color}; --wk: {wk}"></path>
              {/snippet}
              <!-- what travels a wire, in every flow style (wire-flow shows one): a packet and its trail, square bits with theirs, a lit
                   pulse, and a stream of each glyph set. `at` is the head's start; the trail runs behind it -->
              {#snippet travel(path: string, dur: number, at: number, op: number | null)}
                {#each TRAIL as [r, top, off] (r)}
                  <circle class="pk" r={r} fill="currentColor" fill-opacity={r === 4 ? op : top} style="offset-path: path('{path}'); --pk-dur: {dur}s; --pk-at: {f(at + off)}s"></circle>
                  <rect class="blk" x={-r * 0.875} y={-r * 0.875} width={r * 1.75} height={r * 1.75} fill="currentColor" fill-opacity={r === 4 ? op : top} style="offset-path: path('{path}'); --pk-dur: {dur}s; --pk-at: {f(at + off)}s"></rect>
                {/each}
                <path class="pulse" d={path} pathLength="100" stroke="currentColor" stroke-width="3" stroke-linecap="round" fill="none" style="--pk-dur: {dur}s; --pk-at: {f(at)}s"></path>
                <g class="gly">
                  {#each GLYPH_SETS as [set, chars] (set)}
                    <g class="gs-{set}">
                      {#each chars as ch, j (j)}<text class="glyph" fill-opacity={1 - j * 0.2} style="offset-path: path('{path}'); --pk-dur: {dur}s; --pk-at: {f(at + j * 0.11)}s">{ch}</text>{/each}
                    </g>
                  {/each}
                </g>
              {/snippet}
              {#snippet head(h: Heads, fill: string, op: number | null = null)}
                {#each HEAD_SHAPES as k (k)}<path class="h-{k}" d={h[k]} fill={fill} fill-opacity={op}></path>{/each}
              {/snippet}
              <!-- the lead and advisor pair: fixed coordinates, shifted as a unit when there are no sub-agents -->
              <g transform="translate(0 {dy})">
              <!-- orbit rings around the lead -->
              <g class="orbit"><circle cx="555" cy="172" r="104" fill="none" stroke="var(--role-lead)" stroke-opacity="0.28" stroke-width="1" stroke-dasharray="2 9"></circle></g>
              <g class="orbit2"><circle cx="555" cy="172" r="148" fill="none" stroke="color-mix(in oklab, var(--role-lead) 65%, var(--bright))" stroke-opacity="0.14" stroke-width="1" stroke-dasharray="22 14"></circle></g>
              <circle r="3" fill="color-mix(in oklab, var(--role-lead) 65%, var(--bright))" class="comet orbit-pk" style="color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); offset-path: path('M451 172 a104 104 0 1 0 208 0 a104 104 0 1 0 -208 0'); --pk-dur: 16s"></circle>

              <!-- advisor links -->
              <g class="wire" style="color: var(--role-advisor)">
                {@render wire('M200 172 H435', 'var(--role-advisor)', 0.5, 0.75)}
              </g>
              {@render head(LINK_HEADS, 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))')}
              <!-- lead and advisor trade a message: lit only right after a consult -->
              {#if lit}
                {#each LIT as t (t.key)}
                  <g class="comet" style="color: {t.color}">
                    {@render travel(t.path, 2.4, t.at, null)}
                  </g>
                {/each}
              {/if}
              </g>

              {#if hasTree}
              <g in:glowIn out:glowOut>
              <!-- advisor links to the sub-agent row and the return box -->
              <g class="wire" style="color: var(--role-advisor)">
                {@render wire(geo.wires.advA, 'var(--role-advisor)', 0.22, 0.75)}
                {@render wire(geo.wires.advR, 'var(--role-advisor)', 0.22, 0.75)}
              </g>
              {@render head(geo.wires.advAHeads, 'var(--role-advisor)', 0.45)}
              {@render head(geo.wires.advRHeads, 'var(--role-advisor)', 0.45)}

              <!-- trunk, bus, branches -->
              <g class="wire" style="color: var(--role-agent)">
                {@render wire(geo.wires.trunk, 'var(--role-lead)', 0.7, 1)}
                {@render wire(geo.wires.bus, 'var(--role-agent)', 0.55, 1)}
                {@render wire(geo.wires.branches, 'var(--role-agent)', 0.55, 1)}
              </g>
              <path class="flow" d={geo.wires.flowDown} stroke="var(--tx)" stroke-opacity="0.8" stroke-width="1.5" stroke-dasharray="3 21" stroke-linecap="round" fill="none"></path>
              {@render head(geo.wires.heads, 'color-mix(in oklab, var(--edge) 75%, var(--bright))')}

              <!-- stems + return -->
              <g class={returning ? 'wire' : ''} style="color: var(--tx)">
                {@render wire(geo.wires.stems, 'var(--tx)', returning ? 0.38 : 0.16, 1)}
              </g>
              {#if returning}
                <path class="flowr" d={geo.wires.stems} stroke="var(--bright)" stroke-opacity="0.7" stroke-width="1.5" stroke-dasharray="3 21" stroke-linecap="round" fill="none"></path>
              {/if}
              {@render head(geo.wires.retHead, 'var(--tx)', returning ? 1 : 0.3)}

              <!-- loop back to lead: static and dim until an agent actually returns -->
              <g class={returning ? 'wire' : ''} style="color: var(--tx)">
                {@render wire(geo.wires.loop, 'var(--tx)', returning ? 0.3 : 0.14, 0.75)}
              </g>
              {#if returning}
                <path class="flowr" d={geo.wires.loop} stroke="var(--tx)" stroke-opacity="0.7" stroke-width="1.5" stroke-dasharray="3 21" stroke-linecap="round" fill="none"></path>
              {/if}
              {@render head(geo.wires.loopHead, 'var(--tx)', returning ? 0.85 : 0.25)}

              <!-- junction nodes -->
              {#each geo.junctions as j (j.key)}
                <g>
                  <circle cx={j.x} cy={j.y} r="5" fill="var(--panel)" stroke={j.color} stroke-width="1.5"></circle>
                  <circle class="node" cx={j.x} cy={j.y} r="3" fill={j.color}></circle>
                </g>
              {/each}

              <!-- comets: one set per running agent slot, both directions -->
              {#each geo.comets as m (m.id)}
                {#if m.r === 4}
                  <g class="comet" style="color: {m.color}">
                    {@render travel(m.path, m.dur, Number(m.begin), m.op)}
                  </g>
                {/if}
              {/each}
              <!-- return loop to lead: comets only while an agent has just come back -->
              {#if returning}
                {#each geo.loopComets as m (m.id)}
                  {#if m.r === 4}
                    <g class="comet" style="color: var(--tx)">
                      {@render travel(m.path, 3.4, Number(m.begin), m.op)}
                    </g>
                  {/if}
                {/each}
              {/if}
              </g>
              {/if}
            </svg>

            <div style="position: absolute; left: 25%; top: {(21.4 + dyPct).toFixed(2)}%; padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--role-advisor) 16%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 45%, transparent); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright))">ASK ⇄ ANSWER{lastAdvisorAt ? ' · ' + ago(lastAdvisorAt, source.now()).toUpperCase() : ''}</div>
            {#if hasTree}
              <div in:bootIn out:bootOut style="position: absolute; left: 63%; top: 34.6%; padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--edge) 14%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--tx)" class="cap">Delegate ×{agents.length}</div>
            {/if}

            <!-- advisor rail -->
            <button class="surf brk" onclick={() => (sel = ADVISOR)} ondblclick={() => (location.hash = source.href.advisor())} aria-pressed={selKind === 'advisor'} aria-label="Show advisor details" style="--c: var(--role-advisor); position: absolute; left: 0; top: 2.94%; width: 22.22%; height: 95.59%; box-sizing: border-box; text-align: left; cursor: pointer; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; outline-offset: calc(var(--look-focus, 3px) + 1px); --rim: {selKind === 'advisor' ? 'color-mix(in oklab, var(--edge) 45%, var(--bright))' : 'color-mix(in oklch, var(--role-advisor) 35%, transparent)'}; --r: var(--radius); --wash: linear-gradient(180deg, color-mix(in oklch, var(--role-advisor) 16%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 38%); --halo: 0 0 30px color-mix(in oklch, var(--role-advisor) calc(14% * var(--glow)), transparent), inset 0 0 30px color-mix(in oklch, var(--role-advisor) calc(5% * var(--glow)), transparent); padding: 16px 14px">
              <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))">Advisor</div>
              <div class="hud" style="font-weight: 700; font-size: 25px; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright)); margin-top: 4px; text-shadow: 0 0 18px color-mix(in oklch, var(--role-advisor) calc(70% * var(--glow)), transparent)">{modelName(session.advisorModel)}</div>
              <div style="display: flex; align-items: center; gap: 7px; margin-top: 8px; font-size: 11px; color: color-mix(in oklab, var(--role-advisor) 35%, var(--tx))"><span class={['led', lit ? 'blink' : '']} style="flex-shrink: 0; width: 7px; height: 7px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent); opacity: {session.advisorCalls ? 1 : 0.4}"></span>{session.advisorCalls ? `CONSULTED · ${session.advisorCalls} ${session.advisorCalls === 1 ? 'CALL' : 'CALLS'}` : 'NOT CONSULTED'}</div>
            </button>
            {#if advisorLines.length}
              {#each advisorLines.slice(0, 3) as l, i (l.at + '|' + l.text + '|' + i)}
                <div style="position: absolute; left: 1.33%; top: {['22.06%', '56.03%', '87.21%'][i]}; width: 19.56%; pointer-events: none; box-sizing: border-box; padding: 10px 12px; border-radius: var(--radius); background: {i === 0 ? 'color-mix(in oklch, var(--role-advisor) 20%, transparent)' : 'color-mix(in oklch, var(--role-advisor) 6%, transparent)'}; border: 1px var(--border-style) {i === 0 ? 'color-mix(in oklch, var(--role-advisor) 70%, transparent)' : 'color-mix(in oklch, var(--role-advisor) 22%, transparent)'}; box-shadow: {i === 0 ? '0 0 18px color-mix(in oklch, var(--role-advisor) calc(30% * var(--glow)), transparent)' : 'none'}; font-size: 12px; line-height: 1.35; color: {i === 0 ? 'color-mix(in oklab, var(--role-advisor) 25%, var(--bright))' : 'color-mix(in oklab, var(--role-advisor) 25%, var(--mu))'}; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; word-break: break-word"><span><span class="g-bullet" aria-hidden="true"></span> {l.text}</span></div>
              {/each}
            {:else}
              <div style="position: absolute; left: 1.33%; top: 22.06%; width: 19.56%; pointer-events: none; box-sizing: border-box; padding: 10px 12px; border-radius: var(--radius); background: color-mix(in oklch, var(--role-advisor) 6%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); font-size: 12px; line-height: 1.35; color: color-mix(in oklab, var(--role-advisor) 25%, var(--mu))"><span><span class="g-bullet" aria-hidden="true"></span> no consults yet</span></div>
            {/if}

            <!-- lead -->
            <button class="ring" onclick={() => (sel = 'lead')} ondblclick={() => (location.hash = source.href.agent('lead'))} aria-pressed={selKind === 'lead'} aria-label="Show lead details" style:outline={selKind === 'lead' ? '1px solid color-mix(in oklch, var(--tx) 55%, transparent)' : null} style="position: absolute; left: 48.33%; top: {(19.12 + dyPct).toFixed(2)}%; width: 26.67%; height: 12.35%; box-sizing: border-box; text-align: left; cursor: pointer; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; outline-offset: 4px; border-radius: calc(var(--radius) * 1.5); padding: 12px 16px; display: flex; flex-direction: column; justify-content: space-between">
              <div style="display: flex; justify-content: space-between; align-items: baseline; gap: 8px; min-width: 0">
                <span class="hud cap" style="font-weight: 700; font-size: 20px; letter-spacing: calc(var(--tracking) * 0.7143); color: color-mix(in oklab, var(--role-lead) 50%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--role-lead) calc(70% * var(--glow)), transparent)">Lead</span>
                <span style="font-size: 12px; color: color-mix(in oklab, var(--role-lead) 60%, var(--tx)); white-space: nowrap">{modelName(session.model)}</span>
              </div>
              <div data-content style="font-size: 11px; color: color-mix(in oklab, var(--role-lead) 40%, var(--bright)); letter-spacing: calc(var(--tracking) * 0.2857); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{session.status === 'waiting' ? `WAITING · ${(session.waitingFor ?? 'input needed').toUpperCase()}` : session.activity || (session.status === 'busy' ? 'WORKING' : 'IDLE')}</div>
              <div class="seg" style="height: 4px; position: relative; background: repeating-linear-gradient(90deg,color-mix(in oklab, var(--role-lead) 30%, var(--bg)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)">{#if session.status === 'busy'}<div class="segon-o" style="position: absolute; inset: 0"></div>{/if}</div>
            </button>

            <!-- agent cards -->
            {#each real as a, i (a.id)}
              {@const lk = lookOf(a)}
              {@const isSel = selId === a.id}
              <button class="surf card {a.status === 'running' ? 'live' : ''}" in:bootIn={{ index: i, skip: Date.now() - born < 800 }} out:bootOut onclick={() => (sel = a.id)} ondblclick={() => (location.hash = source.href.agent(a.id))} aria-pressed={isSel} style="position: absolute; top: 43.53%; left: {geo.pos[i].left}%; width: {geo.pos[i].width}%; height: 31.47%; transition: left .45s cubic-bezier(.2,.7,.2,1), width .45s cubic-bezier(.2,.7,.2,1); box-sizing: border-box; text-align: left; cursor: pointer; --rim: {isSel ? 'color-mix(in oklab, var(--edge) 45%, var(--bright))' : lk.border}; --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--raise) 92%, transparent), color-mix(in oklch, var(--panel) 97%, transparent) 55%); padding: 14px; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); display: flex; flex-direction: column; gap: 9px; outline-offset: calc(var(--look-focus, 3px) + 1px); opacity: {a.status === 'stalled' ? 0.82 : 1}">
                <div class="stripe" style="position: absolute; left: 0; top: 0; width: 100%; height: 2px; background: linear-gradient(90deg, {lk.dot}, transparent); box-shadow: 0 0 12px color-mix(in oklch, {lk.dot} calc(100% * var(--glow)), transparent)"></div>
                <div class="port-t" style="position: absolute; left: 50%; width: 8px; height: 8px; background: var(--panel); border: 1px var(--border-style) color-mix(in oklab, var(--edge) 75%, var(--bright)); transform: translateX(-50%) rotate(45deg)"></div>
                <div class="port-b" style="position: absolute; left: 50%; width: 8px; height: 8px; background: var(--panel); border: 1px var(--border-style) var(--tx); transform: translateX(-50%) rotate(45deg)"></div>
                <div style="display: flex; align-items: center; gap: 8px; min-width: 0">
                  <span class="led" style="flex-shrink: 0; width: 8px; height: 8px; background: {lk.dot}; box-shadow: 0 0 10px color-mix(in oklch, {lk.dot} calc(100% * var(--glow)), transparent)"></span>
                  <span data-content class="hud" style="flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 700; font-size: 19px; letter-spacing: calc(var(--tracking) * 0.4286); color: color-mix(in oklab, var(--edge) 45%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--edge) calc(60% * var(--glow)), transparent)">{agentName(a)}</span>
                  {#if isTeammateAgent(a)}<span title="A teammate: its own claude, started by a lead" style="flex-shrink: 0; padding: 1px 6px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {TEAMMATE_COLORS.border}; background: {TEAMMATE_COLORS.bg}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: {TEAMMATE_COLORS.fg}">{TEAMMATE_LABEL}</span>{/if}
                  <span style="flex-shrink: 0; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.7143); color: color-mix(in oklab, var(--mu) 75%, var(--panel))">{labels.get(a.id) ?? ''}</span>
                </div>
                <div style="font-size: 10px; letter-spacing: var(--tracking); color: var(--mu)">{modelName(a.model)}</div>
                <div style="display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: {lk.dot}">
                  <span>▮ {lk.label}</span>
                  {#if a.nested > 0}<span style="flex-shrink: 0; padding: 1px 6px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 35%, transparent); background: color-mix(in oklch, var(--edge) 10%, transparent); font-size: 10px; color: color-mix(in oklab, var(--edge) 55%, var(--bright))">+{a.nested} nested</span>{/if}
                </div>
                <div data-content style="font-size: 12px; line-height: 1.4; color: var(--tx); flex: 1; overflow: hidden; word-break: break-all"><span style="color: var(--edge)">$ </span>{a.lastAction}</div>
                {#if a.status === 'running'}
                  <div class="seg" style="height: 4px; position: relative"><div class="segon" style="position: absolute; inset: 0"></div></div>
                {:else if agentWaits(a, source.archived)}
                  <div style="height: 4px; background: repeating-linear-gradient(90deg,color-mix(in oklab, var(--mu) 55%, var(--panel)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div>
                {:else if a.status === 'done'}
                  <div style="height: 4px; background: repeating-linear-gradient(90deg,var(--ok) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px); filter: drop-shadow(0 0 4px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent))"></div>
                {:else}
                  <div style="height: 4px; background: repeating-linear-gradient(90deg,color-mix(in oklab, var(--warn) 50%, var(--panel)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div>
                {/if}
                <div style="font-size: 11px; color: var(--mu)">{a.calls} {a.calls === 1 ? 'CALL' : 'CALLS'} · {runtimeOf(a)}</div>
              </button>
            {/each}
            {#if hasMore}
              <a class="card" in:bootIn out:bootOut href={source.href.agent(null)} style="position: absolute; top: 43.53%; left: {geo.pos[slots - 1].left}%; width: {geo.pos[slots - 1].width}%; height: 31.47%; box-sizing: border-box; border: 1px dashed color-mix(in oklch, var(--edge) 55%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--edge) 7%, transparent); padding: 14px; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); display: flex; flex-direction: column; justify-content: center; gap: 8px; text-decoration: none">
                <div class="port-t" style="position: absolute; left: 50%; width: 8px; height: 8px; background: var(--panel); border: 1px var(--border-style) color-mix(in oklab, var(--edge) 75%, var(--bright)); transform: translateX(-50%) rotate(45deg)"></div>
                <div class="port-b" style="position: absolute; left: 50%; width: 8px; height: 8px; background: var(--panel); border: 1px var(--border-style) var(--tx); transform: translateX(-50%) rotate(45deg)"></div>
                <div class="hud cap" style="font-weight: 700; font-size: 26px; color: color-mix(in oklab, var(--edge) 45%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--edge) calc(60% * var(--glow)), transparent)">+{hidden.length} More</div>
                {#if hiddenWaiting}<div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--warn)">{hiddenWaiting} NEEDS INPUT</div>{/if}
                <div style="font-size: 11px; line-height: 1.5; color: var(--mu)">{hiddenRunning} running · {hiddenDone} done{hidden.length - hiddenRunning - hiddenDone > 0 ? ` · ${hidden.length - hiddenRunning - hiddenDone} stalled` : ''}</div>
                <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--edge) 75%, var(--bright))"><span class="cap">View all <span class="g-fwd" aria-hidden="true"></span></span></div>
              </a>
            {/if}

            <!-- return -->
            {#if hasTree}
              <div class="surf" in:bootIn out:bootOut style="position: absolute; left: 48.33%; top: 85.29%; width: 26.67%; height: 10.29%; display: flex; box-sizing: border-box; --rim: color-mix(in oklch, var(--tx) calc({returning ? 0.4 : 0.18} * 100%), transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--tx) calc({returning ? 0.12 : 0.05} * 100%), transparent), color-mix(in oklch, var(--panel) 90%, transparent)); --halo: {returning ? '0 0 20px color-mix(in oklch, var(--tx) calc(10% * var(--glow)), transparent)' : '0 0 transparent'}; padding: 12px 16px; flex-direction: column; justify-content: center; gap: 4px">
                <div class="hud cap" style="font-weight: 700; font-size: 16px; letter-spacing: calc(var(--tracking) * 0.7143); color: {returning ? 'var(--bright)' : 'var(--mu)'}; text-shadow: {returning ? '0 0 12px color-mix(in oklch, var(--tx) calc(50% * var(--glow)), transparent)' : 'none'}">Return → lead</div>
                <div style="font-size: 11px; color: var(--mu); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{retLine}</div>
              </div>
            {/if}
          </div>
          </div>
        </section>

        <!-- session log -->
        <section aria-label="Session log" class="surf brk" style="--c: color-mix(in oklch, var(--edge) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 78%, transparent); --rim: color-mix(in oklch, var(--edge) 16%, transparent); --r: var(--radius); padding: 16px 20px">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 10px">Session log</div>
          {#each log as l (l.key)}
            <div in:rowIn={{ skip: Date.now() - born < 800 }} out:rowOut style="display: flex; gap: 14px; padding: 4px 0; font-size: 12px; line-height: 1.5; opacity: {l.o}">
              <span style="color: color-mix(in oklab, var(--mu) 85%, var(--panel)); flex-shrink: 0">{l.t}</span>
              <span style="color: {l.color}; flex-shrink: 0; width: 70px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-shadow: 0 0 10px color-mix(in oklch, {l.color} calc(100% * var(--glow)), transparent)">{l.who}</span>
              <span style="color: var(--tx); min-width: 0; overflow-wrap: anywhere">{l.text}</span>
            </div>
          {/each}
          {#if log.length === 0}
            <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel)); padding: 4px 0">Nothing logged yet.</div>
          {/if}
          <div style="font-size: 12px; color: var(--edge); padding-top: 4px"><span class="g-bullet" aria-hidden="true"></span><span class="cursor"></span></div>
        </section>
      </div>

      <!-- right column: the selected lead, advisor or agent -->
      {#snippet tile(label: string, value: string, color: string = 'color-mix(in oklab, var(--tx) 60%, var(--bright))')}
        <div style="background: color-mix(in oklch, var(--edge) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
          <div class="cap" style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)">{label}</div>
          <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; color: {color}; text-shadow: 0 0 14px color-mix(in oklch, var(--edge) calc(50% * var(--glow)), transparent)">{value}</div>
        </div>
      {/snippet}
      <aside aria-label="Selected {selKind}" class="surf brk" style="--c: var(--edge); flex: 0 1 340px; min-width: min(280px, 100%); --wash: linear-gradient(180deg, color-mix(in oklch, var(--edge) 10%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 30%); --rim: color-mix(in oklch, var(--edge) 26%, transparent); --r: var(--radius); padding: 22px; display: flex; flex-direction: column; gap: 20px; backdrop-filter: var(--look-surf-blur, blur(6px)); --halo: inset 0 1px 0 color-mix(in oklch, var(--bright) 7%, transparent), 0 0 36px color-mix(in oklch, var(--edge) calc(10% * var(--glow)), transparent)">
        <div class="beam" aria-hidden="true" style="animation-duration: 24s"></div>
        {#if selKind === 'advisor'}
          <div>
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Advisor <span class="g-sep" aria-hidden="true"></span> selected</div>
            <div style="display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 10px; margin-top: 8px">
              <div class="hud cap" style="font-weight: 700; font-size: 34px; color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright)); letter-spacing: calc(var(--tracking) * 0.2857); overflow-wrap: anywhere; text-shadow: -1px 0 color-mix(in oklch, var(--bad) calc(55% * var(--glow)), transparent), 1px 0 color-mix(in oklch, var(--ac) calc(55% * var(--glow)), transparent), 0 0 22px color-mix(in oklch, var(--role-advisor) calc(70% * var(--glow)), transparent)">Advisor</div>
              <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); color: var(--mu)">{modelName(session.advisorModel)}</div>
            </div>
            <div style="display: flex; align-items: center; gap: 8px; margin-top: 8px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--role-advisor)"><span class={['led', answering ? 'blink' : '']} style="width: 8px; height: 8px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent); opacity: {consults.length ? 1 : 0.4}"></span>{answering ? 'ANSWERING' : unanswered ? 'UNANSWERED' : consults.length ? 'CONSULTED' : 'NOT CONSULTED'}</div>
          </div>

          <div style="display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px">
            {@render tile('Consults', String(consults.length))}
            {@render tile('Avg time', finished.length ? dur(avgMs) : '—')}
            {@render tile('Total time', finished.length ? dur(totalMs) : '—')}
            {@render tile('Last ago', lastAdvisorAt ? dur(source.now() - lastAdvisorAt) : '—')}
          </div>

          <div>
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 10px">Recent consults</div>
            {#each consults.slice(0, 3) as c (c.id)}
              <div style="margin-bottom: 12px">
                <div style="display: flex; align-items: baseline; justify-content: space-between; gap: 8px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714)">
                  <span style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: {c.by === 'lead' ? 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' : 'color-mix(in oklab, var(--edge) 55%, var(--bright))'}">{c.byLabel}</span>
                  <span style="flex-shrink: 0; color: var(--mu)">{ago(c.at, source.now())} · {c.endedAt === null ? (source.archived ? 'UNANSWERED' : 'ANSWERING') : dur(c.endedAt - c.at)}</span>
                </div>
                {#if c.before}
                  <div style="margin-top: 4px; font-size: 12px; line-height: 1.45; color: var(--tx); overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow-wrap: anywhere"><span class="g-bullet" aria-hidden="true" style="color: var(--role-advisor)"></span>{c.before}</div>
                {/if}
              </div>
            {/each}
            {#if consults.length === 0}
              <div style="font-size: 12px; line-height: 1.6; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">The advisor has not been consulted yet.</div>
            {/if}
          </div>

          <a class="tab" href={source.href.advisor()} style="display: flex; align-items: center; justify-content: center; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 50%, transparent); background: color-mix(in oklch, var(--edge) 12%, transparent); color: var(--tx); font-size: 12px; letter-spacing: var(--tracking); text-decoration: none; box-shadow: 0 0 18px color-mix(in oklch, var(--edge) calc(18% * var(--glow)), transparent)"><span class="cap">Open advisor detail <span class="g-fwd" aria-hidden="true"></span></span></a>
        {:else if shown}
          {@const lk = shownLook}
          {@const isLead = selKind === 'lead'}
          <div>
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{isLead ? 'Lead' : 'Agent'} <span class="g-sep" aria-hidden="true"></span> selected</div>
            <div style="display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 10px; margin-top: 8px">
              <div data-content class="hud" style="font-weight: 700; font-size: 34px; color: {isLead ? 'color-mix(in oklab, var(--role-lead) 50%, var(--bright))' : 'color-mix(in oklab, var(--edge) 45%, var(--bright))'}; letter-spacing: calc(var(--tracking) * 0.2857); overflow-wrap: anywhere; text-shadow: -1px 0 color-mix(in oklch, var(--bad) calc(55% * var(--glow)), transparent), 1px 0 color-mix(in oklch, var(--ac) calc(55% * var(--glow)), transparent), 0 0 22px {isLead ? 'color-mix(in oklch, var(--role-lead) calc(70% * var(--glow)), transparent)' : 'color-mix(in oklch, var(--edge) calc(70% * var(--glow)), transparent)'}">{agentName(shown)}</div>
              {#if isTeammateAgent(shown)}<span title="A teammate: its own claude, started by a lead" style="flex-shrink: 0; padding: 1px 6px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {TEAMMATE_COLORS.border}; background: {TEAMMATE_COLORS.bg}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: {TEAMMATE_COLORS.fg}">{TEAMMATE_LABEL}</span>{/if}
              <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); color: var(--mu)">{modelName(shown.model || session.model)}</div>
            </div>
            <div style="display: flex; align-items: center; gap: 8px; margin-top: 8px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: {lk.dot}"><span class="led" style="width: 8px; height: 8px; background: {lk.dot}; box-shadow: 0 0 10px color-mix(in oklch, {lk.dot} calc(100% * var(--glow)), transparent)"></span>{lk.label}{#if !isLead && shown.nested > 0}<span style="color: var(--mu)">· +{shown.nested} nested</span>{/if}</div>
          </div>

          <div style="display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px">
            {@render tile('Tool calls', String(shown.calls))}
            {@render tile('Runtime', isLead ? dur(source.now() - shown.startedAt) : runtimeOf(shown))}
            {@render tile('Errors', String(shown.errors), shown.errors ? 'color-mix(in oklab, var(--bad) 70%, var(--bright))' : 'color-mix(in oklab, var(--tx) 60%, var(--bright))')}
            {@render tile('Output', tokens(shown.tokensOut))}
            {@render tile('Est. cost', `~${usd(shown.costUsd)}`)}
            {@render tile('Session cost', `~${usd(session.costUsd)}${session.costPartial ? '+' : ''}`)}
          </div>

          {#if shown.task}
            <div>
              <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 10px">Task</div>
              <div style="font-size: 12px; line-height: 1.5; color: var(--tx); overflow: hidden; display: -webkit-box; -webkit-line-clamp: 6; line-clamp: 6; -webkit-box-orient: vertical; overflow-wrap: anywhere">{shown.task}</div>
            </div>
          {/if}

          <div>
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 10px">Last action</div>
            <div style="font-size: 12px; line-height: 1.45; color: var(--tx); overflow-wrap: anywhere">{#if /^[A-Z][A-Za-z_]+  /.test(shown.lastAction)}<span style="color: var(--edge)">$ </span>{/if}{shown.lastAction}</div>
          </div>

          {#if mix.length}
            <div>
              <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 12px">Tool mix</div>
              {#each mix as m (m.name)}
                <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 9px; font-size: 12px">
                  <span title={m.name} style="width: 64px; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--tx)">{m.name}</span>
                  <div class="seg" style="flex: 1; height: 6px; position: relative"><div style="position: absolute; left: 0; top: 0; bottom: 0; width: {m.pct}%; overflow: hidden; filter: drop-shadow(0 0 4px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent))"><div style="width: 1000px; height: 100%; background: repeating-linear-gradient(90deg,color-mix(in oklab, var(--edge) 75%, var(--bright)) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div></div></div>
                  <span style="width: 28px; text-align: right; color: var(--mu)">{m.n}</span>
                </div>
              {/each}
            </div>
          {/if}

          <a class="tab" href={source.href.agent(isLead ? 'lead' : shown.id)} style="display: flex; align-items: center; justify-content: center; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 50%, transparent); background: color-mix(in oklch, var(--edge) 12%, transparent); color: var(--tx); font-size: 12px; letter-spacing: var(--tracking); text-decoration: none; box-shadow: 0 0 18px color-mix(in oklch, var(--edge) calc(18% * var(--glow)), transparent)"><span><span class="cap">{isLead ? 'Open lead detail' : 'Open agent detail'} <span class="g-fwd" aria-hidden="true"></span></span></span></a>
        {:else}
          <div>
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Selected</div>
            <div style="margin-top: 12px; font-size: 12px; line-height: 1.6; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">Nothing to show.</div>
          </div>
        {/if}
      </aside>
    </main>
    <StatusFooter s={session} archived={source.archived} />
  {/if}
</div>
