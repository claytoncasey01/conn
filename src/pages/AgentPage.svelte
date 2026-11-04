<script lang="ts">
  import { live } from '../lib/live.svelte.ts'
  import { dur, modelName, agentLabels, agentName, agentDot, agentWaits } from '../lib/format.ts'
  import { TEAMMATE_COLORS, TEAMMATE_LABEL, isTeammateAgent } from '../lib/team.ts'
  import { rowIn, rowOut } from '../lib/fx.ts'
  import Brand from '../lib/Brand.svelte'
  import AgentSwitcher from '../lib/AgentSwitcher.svelte'
  import StatusFooter from '../lib/StatusFooter.svelte'
  import ArchiveNotice from '../lib/ArchiveNotice.svelte'
  import ArchivedMark from '../lib/ArchivedMark.svelte'
  import AskPanel from '../lib/AskPanel.svelte'
  import { askOwner, viaName, waitingAgents } from '../lib/ask.ts'
  import type { SessionSource } from '../lib/source.svelte.ts'
  import type { Agent, AgentDetail, ToolCall } from '../lib/types.ts'

  let { source, aid }: { source: SessionSource; aid: string | null } = $props()

  const ERR = 'var(--bad)'
  const OK = 'var(--ok)'
  const LIST_CAP = 150
  const TOOL: Record<string, string> = { Bash: 'var(--role-agent)', Read: 'var(--ac)' }
  const PALETTE = ['var(--k-epic)', 'color-mix(in oklab, var(--role-lead) 80%, var(--bright))', 'var(--k-story)', 'var(--warn)', 'var(--teammate)', 'color-mix(in oklab, var(--k-subtask) 60%, var(--tx))']
  const OTHER = 'var(--idle)'

  const colorOf = (name: string): string => {
    if (TOOL[name]) return TOOL[name]
    let h = 0
    for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0
    return PALETTE[h % PALETTE.length]
  }
  const isLead = (a: Agent) => a.id === 'lead'
  const statusDot = (a: Agent) => agentDot(a, source.archived)
  // The lead never finishes, nor does a live session's teammate: between messages they wait, they are not done.
  const statusText = (a: Agent) => (a.status === 'running' ? 'RUNNING' : agentWaits(a, source.archived) ? 'IDLE' : a.status === 'done' ? 'DONE ✓' : 'STALLED')
  const pad = (x: number) => String(x).padStart(2, '0')
  const clock = (at: number) => {
    const d = new Date(at)
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  }
  const fmtMs = (ms: number | null) => (ms === null ? '…' : ms < 1000 ? `${Math.round(ms)}ms` : ms < 60_000 ? `${(ms / 1000).toFixed(1)}s` : dur(ms))

  const session = $derived(source.session())
  const agents = $derived(session?.agents ?? [])
  // The lead is agent-shaped and goes first in the switcher; N-01 … count only the real agents.
  const roster = $derived(session?.lead ? [session.lead, ...agents] : agents)
  const labels = $derived(agentLabels(agents))
  const labelOf = (a: Agent) => (isLead(a) ? 'LEAD' : (labels.get(a.id) ?? ''))

  const selectedId = $derived.by(() => {
    if (!roster.length) return null
    if (aid) return roster.some(a => a.id === aid) ? aid : null
    const running = agents.find(a => a.status === 'running')
    if (running) return running.id
    if (!agents.length) return roster[0].id
    return agents.reduce((best, a) => (a.startedAt > best.startedAt ? a : best), agents[0]).id
  })
  const agent = $derived(roster.find(a => a.id === selectedId))
  const lead = $derived(agent ? isLead(agent) : false)
  const running = $derived(agent?.status === 'running')
  // Whose terminal holds this agent's question; an archive has none
  const waiting = $derived(!source.archived && session ? waitingAgents(session, live.snap?.sessions ?? []) : new Set<string>())
  const owner = $derived(!source.archived && session && agent ? askOwner(session, agent.id, live.snap?.sessions ?? []) : null)
  // A subagent's page points to the page with the answer: the lead's, or its teammate's
  const via = $derived(owner?.via && session ? { name: viaName(session, owner.via), href: source.href.agent(owner.via) } : null)
  // While the lead waits, its panel is where it is answered: the footer offers no second reply box
  const panelAnswers = $derived(!!owner && !owner.viaLead && owner.session.id === session?.id && owner.session.status === 'waiting')

  // ---- tool calls: fetched on selection, refetched (throttled) while the agent runs ----
  let detail = $state<AgentDetail | null>(null)
  let loaded = $state(false)
  let pickedCall = $state<string | null>(null)
  let seq = 0
  let lastFetch = 0
  let loadedKey = ''
  let prevRunning = false

  async function load(src: SessionSource, a: string) {
    const mine = ++seq
    lastFetch = Date.now()
    try {
      const d = await src.agent(a)
      if (mine !== seq) return
      if (d) detail = d
    } catch {
      if (mine !== seq) return
    }
    loaded = true
  }

  $effect(() => {
    const src = source
    const a = selectedId
    // An archive never moves, so it is fetched once per agent and never polled
    const at = src.archived ? null : live.snap?.at
    const isRunning = !src.archived && running
    void at
    if (!a) {
      loadedKey = ''
      seq++
      detail = null
      loaded = false
      return
    }
    const key = `${src.href.overview()}/${a}`
    if (key !== loadedKey) {
      loadedKey = key
      prevRunning = isRunning
      detail = null
      loaded = false
      pickedCall = null
      load(src, a)
      return
    }
    const wasRunning = prevRunning
    prevRunning = isRunning
    if (!isRunning && !wasRunning) return
    const timer = setTimeout(() => load(src, a), Math.max(0, 1500 - (Date.now() - lastFetch)))
    return () => clearTimeout(timer)
  })

  const rows = $derived(
    [...(detail?.toolCalls ?? [])].sort((x, y) => x.at - y.at).map((c, i) => ({ c, n: i + 1 })),
  )
  const newest = $derived(rows.slice(-LIST_CAP).reverse())
  const cur = $derived(rows.find(r => r.c.id === pickedCall) ?? rows[rows.length - 1])

  // ---- timeline ----
  const t0 = $derived(agent?.startedAt ?? 0)
  const t1 = $derived(agent ? (agent.endedAt ?? source.now()) : 0)
  const total = $derived(Math.max(t1 - t0, 1))
  const pct = (v: number) => Math.min(100, Math.max(0, ((v - t0) / total) * 100))

  const lanes = $derived.by(() => {
    const counts = new Map<string, number>()
    for (const { c } of rows) counts.set(c.name, (counts.get(c.name) ?? 0) + 1)
    const top = [...counts.entries()].sort((x, y) => y[1] - x[1]).slice(0, 3).map(e => e[0])
    const out = top.map(name => ({ name, color: colorOf(name), rows: rows.filter(r => r.c.name === name) }))
    const rest = rows.filter(r => !top.includes(r.c.name))
    if (rest.length) out.push({ name: 'OTHER', color: OTHER, rows: rest })
    return out
  })
  const TICKS = [0, 25, 50, 75, 100]

  // ---- right column ----
  const avgMs = $derived.by(() => {
    const ms = rows.map(r => r.c.ms).filter((m): m is number => m !== null)
    return ms.length ? ms.reduce((a, b) => a + b, 0) / ms.length : null
  })
  const mix = $derived.by(() => {
    const e = Object.entries(agent?.tools ?? {}).sort((x, y) => y[1] - x[1]).slice(0, 8)
    const top = e[0]?.[1] || 1
    return e.map(([name, n]) => ({ name, n, pct: Math.round((n / top) * 100), color: colorOf(name) }))
  })
  const files = $derived.by(() => {
    const seen: string[] = []
    for (let i = rows.length - 1; i >= 0 && seen.length < 6; i--) {
      const c: ToolCall = rows[i].c
      if (c.name === 'Read' && c.summary && !seen.includes(c.summary)) seen.push(c.summary)
    }
    return seen
  })
</script>

<div class="agp page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <!-- top bar -->
  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) 72%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <nav aria-label="Breadcrumb" style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px; flex: 1 1 360px; font-size: 13px">
      {#if session}
        {#if source.archived}
          <a class="tab" href={source.href.back()} style="text-decoration: none; display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--ac) 55%, transparent); background: color-mix(in oklch, var(--ac) 10%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); font-size: 12px; letter-spacing: var(--tracking); box-shadow: 0 0 18px color-mix(in oklch, var(--ac) calc(18% * var(--glow)), transparent)"><span class="cap"><span class="g-back" aria-hidden="true"></span> History</span></a>
        {/if}
        <a class="tab" href={source.href.overview()} style="text-decoration: none; display: flex; align-items: center; gap: 8px; padding: 8px 14px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright))"><span class={['led', source.archived ? undefined : 'blink']} style="width: 8px; height: 8px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></span><span data-content><span class="g-back" aria-hidden="true"></span> {session.name}</span></a>
        {#if agent}
          <span style="color: color-mix(in oklab, var(--mu) 70%, var(--bg))">/</span>
          <span style="color: color-mix(in oklab, var(--edge) 45%, var(--bright)); letter-spacing: calc(var(--tracking) * 0.4286)">agent · {labelOf(agent)}</span>
        {/if}
      {:else}
        <a class="tab" href={source.href.back()} style="text-decoration: none; display: flex; align-items: center; gap: 8px; padding: 8px 14px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright))"><span><span class="g-back" aria-hidden="true"></span> {source.archived ? 'history' : 'sessions'}</span></a>
      {/if}
    </nav>
    {#if source.archived}
      <ArchivedMark endedAt={source.now()} />
    {:else}
    <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)">
      <span class="led blink" style="width: 8px; height: 8px; background: {source.connected() ? OK : ERR}; box-shadow: 0 0 10px color-mix(in oklch, {source.connected() ? OK : ERR} calc(100% * var(--glow)), transparent)"></span>{source.connected() ? 'LINK ESTABLISHED' : 'LINK LOST'} · {location.host}
    </div>
    {/if}
  </header>

  {#if !source.connected() && source.state() !== 'loading'}
    <div role="status" style="position: relative; z-index: 6; padding: 8px 32px; background: color-mix(in oklch, var(--bad) 12%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 35%, transparent); color: color-mix(in oklab, var(--bad) 40%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714)">SERVER DISCONNECTED · showing the last known state, reconnecting…</div>
  {/if}

  <main style="flex: 1; display: flex; flex-direction: column; gap: 20px; padding: 24px 32px 40px; width: 100%; max-width: 1480px; box-sizing: border-box; margin: 0 auto">

    {#if source.archived && source.state() !== 'ready'}
      <ArchiveNotice {source} />
    {:else if source.state() === 'loading'}
      <section aria-label="Connecting" class="surf brk" style="--c: color-mix(in oklch, var(--edge) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: var(--radius); padding: 28px; color: var(--mu); letter-spacing: calc(var(--tracking) * 0.7143)">
        CONNECTING<span class="cursor"></span>
      </section>
    {:else if !session}
      <section aria-label="Session not found" class="surf brk" style="--c: var(--role-lead); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-lead) 28%, transparent); --r: var(--radius); padding: 28px; display: flex; flex-direction: column; gap: 14px; align-items: flex-start">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))">Session not found</div>
        <div style="color: var(--tx); line-height: 1.6">No session {source.id} is known to the server. It may have ended.</div>
        <a class="tab" href={source.href.back()} style="text-decoration: none; padding: 8px 14px; min-height: 44px; box-sizing: border-box; display: inline-flex; align-items: center; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright))"><span><span class="g-back" aria-hidden="true"></span> All sessions</span></a>
      </section>
    {:else if !roster.length}
      <section aria-label="No agents" class="surf brk" style="--c: color-mix(in oklch, var(--edge) 75%, transparent); --wash: linear-gradient(120deg, color-mix(in oklch, var(--edge) 13%, transparent), color-mix(in oklch, var(--panel) 85%, transparent) 55%); --rim: color-mix(in oklch, var(--edge) 24%, transparent); --r: var(--radius); padding: 28px; display: flex; flex-direction: column; gap: 14px; align-items: flex-start">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">No agents</div>
        <div style="color: var(--tx); line-height: 1.6">{session.name} has not spawned any agents yet. They appear here as soon as the lead launches one.</div>
        <a class="tab" href={source.href.overview()} style="text-decoration: none; padding: 8px 14px; min-height: 44px; box-sizing: border-box; display: inline-flex; align-items: center; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); color: color-mix(in oklab, var(--edge) 45%, var(--bright))"><span><span class="g-back" aria-hidden="true"></span> Back to overview</span></a>
      </section>
    {:else if !agent}
      <section aria-label="Agent not found" class="surf brk" style="--c: var(--role-lead); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-lead) 28%, transparent); --r: var(--radius); padding: 28px; display: flex; flex-direction: column; gap: 14px; align-items: flex-start">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))">Agent not found</div>
        <div style="color: var(--tx); line-height: 1.6; word-break: break-all">No agent {aid} in {session.name}.</div>
        <a class="tab" href={source.href.overview()} style="text-decoration: none; padding: 8px 14px; min-height: 44px; box-sizing: border-box; display: inline-flex; align-items: center; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright))"><span><span class="g-back" aria-hidden="true"></span> Back to overview</span></a>
      </section>
    {:else}

    {#if owner && agent}
      {#key `${owner.session.id}/${agent.id}`}
        <AskPanel session={owner.session} {via} agentName={agentName(agent)} />
      {/key}
    {/if}

    <!-- hero -->
    <section aria-label="Agent summary" class="surf brk" style="--c: color-mix(in oklch, var(--edge) 75%, transparent); position: relative; display: flex; flex-wrap: wrap; gap: 20px 40px; align-items: center; justify-content: space-between; --wash: linear-gradient(120deg, color-mix(in oklch, var(--edge) 13%, transparent), color-mix(in oklch, var(--panel) 85%, transparent) 55%); --rim: color-mix(in oklch, var(--edge) 24%, transparent); --r: var(--radius); padding: 22px 28px; backdrop-filter: blur(6px); --halo: inset 0 1px 0 color-mix(in oklch, var(--bright) 6%, transparent)">
      <div class="beam" aria-hidden="true"></div>
      <div style="flex: 1 1 380px; min-width: 0">
        <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))"><span class="cap">Agent</span> <span class="g-sep" aria-hidden="true"></span> {labelOf(agent)}</div>
        <div style="display: flex; flex-wrap: wrap; align-items: baseline; gap: 6px 14px; margin-top: 6px">
          <div data-content class="hud" style="font-weight: 700; font-size: 46px; line-height: 1; color: {lead ? 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' : 'color-mix(in oklab, var(--edge) 45%, var(--bright))'}; letter-spacing: calc(var(--tracking) * 0.2857); text-shadow: -1px 0 color-mix(in oklch, var(--bad) calc(50% * var(--glow)), transparent), 1px 0 color-mix(in oklch, var(--ac) calc(50% * var(--glow)), transparent), 0 0 26px {lead ? 'color-mix(in oklch, var(--role-lead) calc(70% * var(--glow)), transparent)' : 'color-mix(in oklch, var(--edge) calc(70% * var(--glow)), transparent)'}; overflow-wrap: anywhere">{lead ? 'LEAD' : isTeammateAgent(agent) ? TEAMMATE_LABEL : agent.type}</div>
          <div style="font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--mu)">{modelName(agent.model)}</div>
          <div style="display: flex; align-items: center; gap: 8px; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); color: {statusDot(agent)}"><span class="led" style="width: 8px; height: 8px; background: {statusDot(agent)}; box-shadow: 0 0 10px color-mix(in oklch, {statusDot(agent)} calc(100% * var(--glow)), transparent)"></span>{statusText(agent)}</div>
        </div>
        {#if agent.description}
          <div style="margin-top: 12px; max-width: 620px; font-size: 14px; line-height: 1.4; color: color-mix(in oklab, var(--tx) 50%, var(--bright))">{agent.description}</div>
        {/if}
        {#if agent.task}
          <div title={agent.task} style="margin-top: 10px; max-width: 620px; font-size: 13px; line-height: 1.55; color: var(--tx); display: -webkit-box; -webkit-line-clamp: 6; line-clamp: 6; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere"><span style="color: color-mix(in oklab, var(--mu) 85%, var(--panel)); letter-spacing: var(--tracking); font-size: 11px; margin-right: 8px" class="cap">Task</span>{agent.task}</div>
        {/if}
      </div>
      <AgentSwitcher {source} {roster} {labels} selected={agent.id} {waiting} />
    </section>

    <div style="display: flex; flex-wrap: wrap; gap: 20px; align-items: flex-start">

      <!-- left: timeline, stream, call detail -->
      <div style="flex: 1 1 640px; min-width: 0; display: flex; flex-direction: column; gap: 20px">

        <section aria-label="Activity timeline" class="surf brk" style="--c: color-mix(in oklch, var(--edge) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: var(--radius); padding: 18px 22px 16px; backdrop-filter: blur(6px)">
          <div style="display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px; margin-bottom: 14px">
            <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))"><span class="cap">Activity</span> <span class="g-sep" aria-hidden="true"></span> {dur(total)}</div>
            <div style="display: flex; flex-wrap: wrap; gap: 14px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
              {#each lanes as ln (ln.name)}
                <span style="display: flex; align-items: center; gap: 6px"><span style="width: 10px; height: 10px; border-radius: calc(var(--radius) * 0.5); background: {ln.color}"></span>{ln.name}</span>
              {/each}
              <span style="display: flex; align-items: center; gap: 6px"><span style="width: 10px; height: 10px; border-radius: calc(var(--radius) * 0.5); background: {ERR}"></span>Error</span>
            </div>
          </div>
          {#if !rows.length}
            <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel)); letter-spacing: calc(var(--tracking) * 0.5714); margin-bottom: 10px" class="cap">{loaded ? 'No tool calls yet' : 'Loading calls…'}</div>
          {/if}
          {#each lanes as ln (ln.name)}
            <div style="display: flex; align-items: center; gap: 12px; margin-bottom: 10px">
              <span title={ln.name} style="width: 56px; flex-shrink: 0; font-size: 12px; color: var(--mu); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{ln.name}</span>
              <div style="position: relative; flex: 1; height: 26px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--edge) 6%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 12%, transparent)">
                {#each ln.rows as r (r.c.id)}
                  {@const left = pct(r.c.at)}
                  {@const w = Math.min(Math.max(((r.c.ms ?? 0) / total) * 100, 0.9), 100 - left)}
                  <button class="bar" type="button" onclick={() => (pickedCall = r.c.id)} aria-label="Call {r.n} {r.c.name} {r.c.summary}" style="position: absolute; top: 3px; bottom: 3px; left: {left.toFixed(2)}%; width: {w.toFixed(2)}%; min-width: 6px; padding: 0; border: 0; border-radius: calc(var(--radius) * 0.5); background: {r.c.error ? ERR : ln.color}; box-shadow: {r.c.id === cur?.c.id ? '0 0 0 2px var(--bright)' : r.c.error ? '0 0 8px color-mix(in oklch, ' + ERR + ' calc(100% * var(--glow)), transparent)' : 'none'}; cursor: pointer"></button>
                {/each}
              </div>
            </div>
          {/each}
          <div style="display: flex; align-items: center; gap: 12px">
            <span style="width: 56px; flex-shrink: 0"></span>
            <div style="position: relative; flex: 1; height: 16px; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 25%, transparent); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">
              {#each TICKS as p (p)}
                <span class="tick" style="position: absolute; top: 3px; left: {p}%; transform: {p === 0 ? 'none' : p === 100 ? 'translateX(-100%)' : 'translateX(-50%)'}">{dur((total * p) / 100)}</span>
              {/each}
            </div>
          </div>
        </section>

        <section aria-label="Tool calls" class="surf brk" style="--c: color-mix(in oklch, var(--edge) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: blur(6px)">
          <div style="display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; margin-bottom: 8px">
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Tool calls <span class="g-sep" aria-hidden="true"></span> newest first</div>
            <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{rows.length > LIST_CAP ? `NEWEST ${LIST_CAP} OF ` : ''}{rows.length} TOTAL</div>
          </div>
          <div style="max-height: 360px; overflow-y: auto">
            {#if !rows.length}
              <div style="padding: 14px 10px; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 12%, transparent); font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{loaded ? 'No tool calls recorded for this agent yet.' : 'Loading…'}</div>
            {/if}
            {#each newest as r (r.c.id)}
              {@const on = r.c.id === cur?.c.id}
              <button class="row rw" type="button" onclick={() => (pickedCall = r.c.id)} aria-pressed={on} style="width: 100%; display: flex; flex-wrap: var(--rw-wrap, nowrap); align-items: center; gap: 12px; padding: 9px 10px; min-height: 40px; box-sizing: border-box; border: 0; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 12%, transparent); border-left: 2px solid {on ? 'color-mix(in oklab, var(--edge) 75%, var(--bright))' : 'transparent'}; background: {on ? 'color-mix(in oklch, var(--edge) 12%, transparent)' : 'transparent'}; color: var(--tx); font-size: 12px; text-align: left; cursor: pointer">
                <span class="rw-n" style="width: 38px; flex-shrink: 0; color: color-mix(in oklab, var(--mu) 70%, var(--bg))">#{r.n}</span>
                <span class="rw-t" style="width: 64px; flex-shrink: 0; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{clock(r.c.at)}</span>
                <span data-content style="width: 56px; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: {r.c.error ? ERR : colorOf(r.c.name)}">{r.c.name}</span>
                <span data-content class="rw-s" style="flex: 1; min-width: var(--rw-sum-min, 0); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{r.c.summary}</span>
                <span style="width: 50px; flex-shrink: 0; text-align: right; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{fmtMs(r.c.ms)}</span>
                <span style="width: 40px; flex-shrink: 0; text-align: right; letter-spacing: calc(var(--tracking) * 0.4286); color: {r.c.error ? ERR : r.c.ms === null ? 'var(--info)' : OK}" class="cap">{r.c.error ? 'Err ✕' : r.c.ms === null ? 'Run' : 'OK'}</span>
              </button>
            {/each}
          </div>
        </section>

        <section aria-label="Call detail" class="surf brk" style="--c: var(--edge); --wash: linear-gradient(180deg, color-mix(in oklch, var(--edge) 10%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 40%); --rim: color-mix(in oklch, var(--edge) 26%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: blur(6px); --halo: 0 0 30px color-mix(in oklch, var(--edge) calc(8% * var(--glow)), transparent)">
          {#if cur}
            <div style="display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px; margin-bottom: 12px">
              <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))"><span class="cap">Call</span> #{cur.n} <span class="g-sep" aria-hidden="true"></span> <span data-content>{cur.c.name}</span></div>
              <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: {cur.c.error ? ERR : cur.c.ms === null ? 'var(--info)' : OK}">{cur.c.error ? 'ERR ✕' : cur.c.ms === null ? 'RUNNING' : 'OK'} · {fmtMs(cur.c.ms)} · {clock(cur.c.at)}</div>
            </div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 6px" class="cap">Input</div>
            <div style="padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 35%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 14%, transparent); font-size: 12px; line-height: 1.5; color: var(--tx); word-break: break-all">{#if cur.c.name === 'Bash'}<span style="color: var(--edge)">$ </span>{/if}{cur.c.summary}</div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin: 14px 0 6px" class="cap">Result</div>
            <div style="padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 35%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 14%, transparent); font-size: 12px; line-height: 1.5; color: {cur.c.error ? ERR : 'var(--tx)'}; word-break: break-all; white-space: pre-wrap; max-height: 280px; overflow-y: auto">{cur.c.result || (cur.c.ms === null ? 'Waiting for the result…' : 'No result text.')}</div>
          {:else}
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 10px">Call detail</div>
            <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{loaded ? 'No tool calls to show.' : 'Loading…'}</div>
          {/if}
        </section>
      </div>

      <!-- right: stats -->
      <aside aria-label="Agent stats" style="flex: 0 1 360px; min-width: min(280px, 100%); display: flex; flex-direction: column; gap: 20px">

        <section class="surf brk" style="--c: var(--edge); display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; --wash: linear-gradient(180deg, color-mix(in oklch, var(--edge) 10%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 30%); --rim: color-mix(in oklch, var(--edge) 26%, transparent); --r: var(--radius); padding: 18px; backdrop-filter: blur(6px)">
          <div style="background: color-mix(in oklch, var(--edge) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Tool calls</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; text-shadow: 0 0 14px color-mix(in oklch, var(--edge) calc(50% * var(--glow)), transparent)">{agent.calls}</div>
          </div>
          <div style="background: color-mix(in oklch, var(--edge) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Runtime</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; text-shadow: 0 0 14px color-mix(in oklch, var(--edge) calc(50% * var(--glow)), transparent)">{dur(total)}</div>
          </div>
          <div style="background: color-mix(in oklch, var(--edge) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Errors</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; color: {agent.errors ? ERR : 'color-mix(in oklab, var(--tx) 60%, var(--bright))'}">{agent.errors}</div>
          </div>
          <div style="background: color-mix(in oklch, var(--edge) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Avg call</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; text-shadow: 0 0 14px color-mix(in oklch, var(--edge) calc(50% * var(--glow)), transparent)">{avgMs === null ? '—' : fmtMs(avgMs)}</div>
          </div>
        </section>

        <section class="surf brk" style="--c: color-mix(in oklch, var(--edge) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: blur(6px)">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 12px">Tool mix</div>
          {#each mix as m (m.name)}
            <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 9px; font-size: 12px">
              <span title={m.name} style="width: 64px; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--tx)">{m.name}</span>
              <div class="seg" style="flex: 1; height: 6px; position: relative"><div style="position: absolute; left: 0; top: 0; bottom: 0; width: {m.pct}%; overflow: hidden; filter: drop-shadow(0 0 4px color-mix(in oklch, {m.color} calc(100% * var(--glow)), transparent))"><div style="width: 1000px; height: 100%; background: repeating-linear-gradient(90deg,{m.color} 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div></div></div>
              <span style="width: 30px; flex-shrink: 0; text-align: right; color: var(--mu)">{m.n}</span>
            </div>
          {:else}
            <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">No tool calls yet.</div>
          {/each}
        </section>

        <section class="surf brk" style="--c: color-mix(in oklch, var(--edge) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: blur(6px)">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-bottom: 10px">Files read</div>
          {#each files as f (f)}
            <div style="padding: 6px 0; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 12%, transparent); font-size: 12px; line-height: 1.45; color: var(--tx); word-break: break-all"><span class="g-bullet" aria-hidden="true" style="color: var(--ac)"></span>{f}</div>
          {:else}
            <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">No files read yet.</div>
          {/each}
        </section>

        <section class="surf brk" style="--c: var(--role-lead); --wash: linear-gradient(180deg, color-mix(in oklch, var(--role-lead) 9%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 40%); --rim: color-mix(in oklch, var(--role-lead) 28%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: blur(6px)">
          {#if lead}
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu)); margin-bottom: 10px">Lead output</div>
            <div style="font-size: 12px; line-height: 1.6; color: color-mix(in oklab, var(--role-lead) 45%, var(--bright))">The lead's output is the conversation itself, so there is no separate result.</div>
          {:else}
          <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu)); margin-bottom: 10px">{running ? 'AWAITING RESULT' : agentWaits(agent, source.archived) ? 'IDLE · LAST REPLY' : agent.status === 'done' ? 'RETURNED TO LEAD' : 'STALLED'}</div>
          <div style="font-size: 12px; line-height: 1.6; color: color-mix(in oklab, var(--role-lead) 45%, var(--bright)); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 420px; overflow-y: auto">{(detail?.agent.id === agent.id ? detail.agent.result : agent.result) || (running ? 'still running…' : agent.status === 'done' ? 'No result text was returned.' : 'No activity for a while and no result returned.')}</div>
          {#if running}
            <div class="seg" style="height: 4px; margin-top: 12px; position: relative"><div class="segon" style="position: absolute; inset: 0"></div></div>
          {/if}
          {/if}
        </section>
      </aside>
    </div>
    {/if}
  </main>

  {#if session}
    <StatusFooter s={session} archived={source.archived} reply={!panelAnswers} />
  {/if}
</div>

<style>
  :global {
    .agp .row{transition:background .15s}
    .agp .row:hover{background:color-mix(in oklch, var(--edge) 8%, transparent)}
    .agp .bar{transition:filter .15s}
    .agp .bar:hover{filter:brightness(1.4)}
    .agp a:focus-visible{outline:2px solid color-mix(in oklab, var(--edge) 55%, var(--bright));outline-offset:3px}
    @media (prefers-reduced-motion: reduce){.agp .row,.agp .bar{transition:none}}
  }
</style>
