<script lang="ts">
  import { dur, ago, modelName, agentLabels } from '../lib/format.ts'
  import { rowIn, rowOut } from '../lib/fx.ts'
  import { live } from '../lib/live.svelte.ts'
  import { waitingAgents } from '../lib/ask.ts'
  import Brand from '../lib/Brand.svelte'
  import AgentSwitcher from '../lib/AgentSwitcher.svelte'
  import StatusFooter from '../lib/StatusFooter.svelte'
  import ArchiveNotice from '../lib/ArchiveNotice.svelte'
  import ArchivedMark from '../lib/ArchivedMark.svelte'
  import type { SessionSource } from '../lib/source.svelte.ts'

  let { source }: { source: SessionSource } = $props()

  const ERR = 'var(--bad)'
  const OK = 'var(--ok)'
  const LIST_CAP = 150
  const LEAD = 'var(--role-lead)'
  const AGENT = 'var(--role-agent)'

  const pad = (x: number) => String(x).padStart(2, '0')
  const clock = (at: number) => {
    const d = new Date(at)
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  }
  const fmtMs = (ms: number | null) => (ms === null ? '…' : ms < 1000 ? `${Math.round(ms)}ms` : ms < 60_000 ? `${(ms / 1000).toFixed(1)}s` : dur(ms))
  const askerColor = (by: string) => (by === 'lead' ? LEAD : AGENT)

  const session = $derived(source.session())
  const agents = $derived(session?.agents ?? [])
  const roster = $derived(session?.lead ? [session.lead, ...agents] : agents)
  const waiting = $derived(!source.archived && session ? waitingAgents(session, live.snap?.sessions ?? []) : new Set<string>())
  const labels = $derived(agentLabels(agents))

  // ---- consults ----
  const ordered = $derived(
    [...(session?.consults ?? [])].sort((x, y) => x.at - y.at).map((c, i) => ({ c, n: i + 1 })),
  )
  const newest = $derived(ordered.slice(-LIST_CAP).reverse())
  let picked = $state<string | null>(null)
  const cur = $derived(ordered.find(r => r.c.id === picked) ?? ordered[ordered.length - 1])

  // ---- timeline across the session runtime ----
  const t0 = $derived(session?.startedAt ?? 0)
  const total = $derived(Math.max(source.now() - t0, 1))
  const pct = (v: number) => Math.min(100, Math.max(0, ((v - t0) / total) * 100))
  const TICKS = [0, 25, 50, 75, 100]

  // ---- stats: only finished consults have a duration ----
  const spans = $derived(ordered.flatMap(r => (r.c.endedAt === null ? [] : [r.c.endedAt - r.c.at])))
  const sumMs = $derived(spans.reduce((a, b) => a + b, 0))
  const avgMs = $derived(spans.length ? sumMs / spans.length : null)
  const maxMs = $derived(spans.length ? Math.max(...spans) : null)
  // A consult with no answer. On an archive nothing is still answering it, so it is UNANSWERED and nothing animates.
  const unanswered = $derived(ordered.some(r => r.c.endedAt === null))
  const inFlight = $derived(!source.archived && unanswered)
  const askers = $derived.by(() => {
    const m = new Map<string, { by: string; label: string; n: number }>()
    for (const { c } of ordered) {
      const e = m.get(c.by)
      if (e) e.n++
      else m.set(c.by, { by: c.by, label: c.byLabel, n: 1 })
    }
    const e = [...m.values()].sort((x, y) => y.n - x.n).slice(0, 8)
    const top = e[0]?.n || 1
    return e.map(x => ({ ...x, pct: Math.round((x.n / top) * 100), color: askerColor(x.by) }))
  })
</script>

<div class="adv page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column; --orb-1: var(--role-advisor); --orb-1-a: 22%; --orb-2: var(--edge); --orb-2-a: 12%; --orb-3: var(--role-advisor); --orb-3-a: 9%">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <!-- top bar -->
  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) var(--look-head-fill, 72%), transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <nav aria-label="Breadcrumb" style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px; flex: 1 1 360px; font-size: 13px">
      {#if session}
        {#if source.archived}
          <a class="tab" href={source.href.back()} style="text-decoration: none; display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--ac) 55%, transparent); background: color-mix(in oklch, var(--ac) 10%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); font-size: 12px; letter-spacing: var(--tracking); box-shadow: 0 0 18px color-mix(in oklch, var(--ac) calc(18% * var(--glow)), transparent)"><span class="cap"><span class="g-back" aria-hidden="true"></span> History</span></a>
        {/if}
        <a class="tab" href={source.href.overview()} style="text-decoration: none; display: flex; align-items: center; gap: 8px; padding: 8px 14px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright))"><span class={['led', source.archived ? undefined : 'blink']} style="width: 8px; height: 8px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></span><span data-content><span class="g-back" aria-hidden="true"></span> {session.name}</span></a>
        <span style="color: color-mix(in oklab, var(--mu) 70%, var(--bg))">/</span>
        <span style="color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright)); letter-spacing: calc(var(--tracking) * 0.4286)">advisor · {modelName(session.advisorModel)}</span>
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
      <section aria-label="Connecting" class="surf brk" style="--c: color-mix(in oklch, var(--role-advisor) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-advisor) 18%, transparent); --r: var(--radius); padding: 28px; color: var(--mu); letter-spacing: calc(var(--tracking) * 0.7143)">
        CONNECTING<span class="cursor"></span>
      </section>
    {:else if !session}
      <section aria-label="Session not found" class="surf brk" style="--c: var(--role-lead); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-lead) 28%, transparent); --r: var(--radius); padding: 28px; display: flex; flex-direction: column; gap: 14px; align-items: flex-start">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))">Session not found</div>
        <div style="color: var(--tx); line-height: 1.6">No session {source.id} is known to the server. It may have ended.</div>
        <a class="tab" href={source.href.back()} style="text-decoration: none; padding: 8px 14px; min-height: 44px; box-sizing: border-box; display: inline-flex; align-items: center; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright))"><span><span class="g-back" aria-hidden="true"></span> All sessions</span></a>
      </section>
    {:else}

    <!-- hero -->
    <section aria-label="Advisor summary" class="surf brk" style="--c: var(--role-advisor); position: relative; display: flex; flex-wrap: wrap; gap: 20px 40px; align-items: center; justify-content: space-between; --wash: linear-gradient(120deg, color-mix(in oklch, var(--role-advisor) 16%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 55%); --rim: color-mix(in oklch, var(--role-advisor) 30%, transparent); --r: var(--radius); padding: 22px 28px; backdrop-filter: var(--look-surf-blur, blur(6px)); --halo: 0 0 30px color-mix(in oklch, var(--role-advisor) calc(10% * var(--glow)), transparent), inset 0 1px 0 color-mix(in oklch, var(--bright) 6%, transparent)">
      <div class="beam" aria-hidden="true"></div>
      <div style="flex: 1 1 380px; min-width: 0">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))">Advisor</div>
        <div style="display: flex; flex-wrap: wrap; align-items: baseline; gap: 6px 14px; margin-top: 6px">
          <div class="hud" style="font-weight: 700; font-size: 46px; line-height: 1; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright)); letter-spacing: calc(var(--tracking) * 0.2857); text-shadow: -1px 0 color-mix(in oklch, var(--bad) calc(50% * var(--glow)), transparent), 1px 0 color-mix(in oklch, var(--ac) calc(50% * var(--glow)), transparent), 0 0 26px color-mix(in oklch, var(--role-advisor) calc(70% * var(--glow)), transparent); overflow-wrap: anywhere">{modelName(session.advisorModel)}</div>
          <div style="display: flex; align-items: center; gap: 8px; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--role-advisor) 35%, var(--tx))"><span class={['led', inFlight ? 'blink' : '']} style="width: 8px; height: 8px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent); opacity: {ordered.length ? 1 : 0.4}"></span>{inFlight ? 'ANSWERING' : unanswered ? 'UNANSWERED' : 'ON CALL'}</div>
        </div>
        <div style="margin-top: 12px; font-size: 14px; line-height: 1.4; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright))">on call · {ordered.length} {ordered.length === 1 ? 'consult' : 'consults'}</div>
      </div>
      <AgentSwitcher {source} {roster} {labels} selected={null} advisor {waiting} />
    </section>

    {#if !ordered.length}
      <section aria-label="No consults" class="surf brk" style="--c: var(--role-advisor); --wash: linear-gradient(180deg, color-mix(in oklch, var(--role-advisor) 10%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 40%); --rim: color-mix(in oklch, var(--role-advisor) 26%, transparent); --r: var(--radius); padding: 28px; display: flex; flex-direction: column; gap: 14px; align-items: flex-start">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))">Not consulted yet</div>
        <div style="color: var(--tx); line-height: 1.6; max-width: 620px">The advisor has not been called in {session.name} yet. Each time the lead or an agent asks it for a second opinion, the call shows up here with who asked and how long it took.</div>
        <a class="tab" href={source.href.overview()} style="text-decoration: none; padding: 8px 14px; min-height: 44px; box-sizing: border-box; display: inline-flex; align-items: center; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 40%, transparent); color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright))"><span><span class="g-back" aria-hidden="true"></span> Back to overview</span></a>
      </section>
    {:else}

    <div style="display: flex; flex-wrap: wrap; gap: 20px; align-items: flex-start">

      <!-- left: timeline, list, detail -->
      <div style="flex: 1 1 640px; min-width: 0; display: flex; flex-direction: column; gap: 20px">

        <section aria-label="Consult timeline" class="surf brk" style="--c: color-mix(in oklch, var(--role-advisor) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-advisor) 20%, transparent); --r: var(--radius); padding: 18px 22px 16px; backdrop-filter: var(--look-surf-blur, blur(6px))">
          <div style="display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px; margin-bottom: 14px">
            <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))"><span class="cap">Consults</span> <span class="g-sep" aria-hidden="true"></span> {dur(total)}</div>
            <div style="display: flex; flex-wrap: wrap; gap: 14px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
              <span style="display: flex; align-items: center; gap: 6px"><span style="width: 10px; height: 10px; border-radius: calc(var(--radius) * 0.5); background: {LEAD}"></span>Lead</span>
              <span style="display: flex; align-items: center; gap: 6px"><span style="width: 10px; height: 10px; border-radius: calc(var(--radius) * 0.5); background: {AGENT}"></span>Agent</span>
            </div>
          </div>
          <div style="display: flex; align-items: center; gap: 12px; margin-bottom: 10px">
            <span style="width: 56px; flex-shrink: 0; font-size: 12px; color: var(--mu)">Advisor</span>
            <div style="position: relative; flex: 1; height: 34px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--role-advisor) 6%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 14%, transparent)">
              {#each ordered as r (r.c.id)}
                {@const left = pct(r.c.at)}
                {@const w = Math.min(Math.max((((r.c.endedAt ?? source.now()) - r.c.at) / total) * 100, 0.9), 100 - left)}
                {@const col = askerColor(r.c.by)}
                <button class="bar {r.c.endedAt === null && !source.archived ? 'live' : ''}" type="button" onclick={() => (picked = r.c.id)} aria-label="Consult {r.n} by {r.c.byLabel} at {clock(r.c.at)}" style="position: absolute; top: 3px; bottom: 3px; left: {left.toFixed(2)}%; width: {w.toFixed(2)}%; min-width: 6px; padding: 0; border: 0; border-radius: calc(var(--radius) * 0.5); background: {col}; box-shadow: {r.c.id === cur?.c.id ? '0 0 0 2px var(--bright)' : '0 0 8px color-mix(in oklch, ' + col + ' calc(100% * var(--glow)), transparent)'}; cursor: pointer"></button>
              {/each}
            </div>
          </div>
          <div style="display: flex; align-items: center; gap: 12px">
            <span style="width: 56px; flex-shrink: 0"></span>
            <div style="position: relative; flex: 1; height: 16px; border-top: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 25%, transparent); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">
              {#each TICKS as p (p)}
                <span class="tick" style="position: absolute; top: 3px; left: {p}%; transform: {p === 0 ? 'none' : p === 100 ? 'translateX(-100%)' : 'translateX(-50%)'}">{dur((total * p) / 100)}</span>
              {/each}
            </div>
          </div>
        </section>

        <section aria-label="Consult list" class="surf brk" style="--c: color-mix(in oklch, var(--role-advisor) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-advisor) 20%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: var(--look-surf-blur, blur(6px))">
          <div style="display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; margin-bottom: 8px">
            <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))">Consults <span class="g-sep" aria-hidden="true"></span> newest first</div>
            <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{ordered.length > LIST_CAP ? `NEWEST ${LIST_CAP} OF ` : ''}{ordered.length} TOTAL</div>
          </div>
          <div style="max-height: 360px; overflow-y: auto">
            {#each newest as r (r.c.id)}
              {@const on = r.c.id === cur?.c.id}
              <button class="row rw" type="button" in:rowIn out:rowOut onclick={() => (picked = r.c.id)} aria-pressed={on} style="width: 100%; display: flex; flex-wrap: var(--rw-wrap, nowrap); align-items: center; gap: 12px; padding: 9px 10px; min-height: 40px; box-sizing: border-box; border: 0; border-top: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 14%, transparent); border-left: 2px solid {on ? 'var(--role-advisor)' : 'transparent'}; background: {on ? 'color-mix(in oklch, var(--role-advisor) 14%, transparent)' : 'transparent'}; color: var(--tx); font-size: 12px; text-align: left; cursor: pointer">
                <span class="rw-n" style="width: 38px; flex-shrink: 0; color: color-mix(in oklab, var(--mu) 70%, var(--bg))">#{r.n}</span>
                <span class="rw-t" style="width: 64px; flex-shrink: 0; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{clock(r.c.at)}</span>
                <span data-content title={r.c.byLabel} style="width: 84px; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: {askerColor(r.c.by)}">{r.c.byLabel}</span>
                <span style="width: 50px; flex-shrink: 0; text-align: right; color: {r.c.endedAt === null && !source.archived ? 'var(--role-advisor)' : 'color-mix(in oklab, var(--mu) 85%, var(--panel))'}">{r.c.endedAt === null && source.archived ? '—' : fmtMs(r.c.endedAt === null ? null : r.c.endedAt - r.c.at)}</span>
                <span data-content class="rw-s" style="flex: 1; min-width: var(--rw-sum-min, 0); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{r.c.before.replace(/\s+/g, ' ')}</span>
              </button>
            {/each}
          </div>
        </section>

        <section aria-label="Consult detail" class="surf brk" style="--c: var(--role-advisor); --wash: linear-gradient(180deg, color-mix(in oklch, var(--role-advisor) 12%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 40%); --rim: color-mix(in oklch, var(--role-advisor) 30%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: var(--look-surf-blur, blur(6px)); --halo: 0 0 30px color-mix(in oklch, var(--role-advisor) calc(10% * var(--glow)), transparent)">
          {#if cur}
            {@const c = cur.c}
            <div style="display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px; margin-bottom: 14px">
              <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))"><span class="cap">Consult</span> #{cur.n}</div>
              <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: {c.endedAt === null ? (source.archived ? 'color-mix(in oklab, var(--warn) 50%, var(--mu))' : 'var(--role-advisor)') : OK}">{c.endedAt === null ? (source.archived ? 'UNANSWERED' : 'ANSWERING') : 'ANSWERED'} · {clock(c.at)}</div>
            </div>
            <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(min(150px, 100%), 1fr)); gap: 10px; margin-bottom: 14px">
              <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px; min-width: 0">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Who asked</div>
                <div style="margin-top: 4px; font-size: 14px; color: {askerColor(c.by)}; overflow-wrap: anywhere">{c.byLabel}</div>
              </div>
              <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px; min-width: 0">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">When</div>
                <div style="margin-top: 4px; font-size: 14px; color: color-mix(in oklab, var(--tx) 60%, var(--bright))">{clock(c.at)} <span style="font-size: 11px; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">· {ago(c.at, source.now())}</span></div>
              </div>
              <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px; min-width: 0">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Duration</div>
                <div style="margin-top: 4px; font-size: 14px; color: color-mix(in oklab, var(--tx) 60%, var(--bright))">{c.endedAt === null ? (source.archived ? 'no answer recorded' : `… ${dur(source.now() - c.at)} so far`) : fmtMs(c.endedAt - c.at)}</div>
              </div>
            </div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu)); margin-bottom: 6px" class="cap">Context before the call</div>
            <div style="padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 35%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 16%, transparent); font-size: 12px; line-height: 1.5; color: var(--tx); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 280px; overflow-y: auto">{c.before || 'Nothing was recorded just before this call.'}</div>
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu)); margin: 14px 0 6px" class="cap">What happened next</div>
            <div style="padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 35%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 16%, transparent); font-size: 12px; line-height: 1.5; color: var(--tx); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 280px; overflow-y: auto">{c.after || (c.endedAt === null ? (source.archived ? 'No answer was recorded before the session ended.' : 'waiting…') : 'Nothing was recorded after the answer.')}</div>
            <div role="note" style="margin-top: 14px; padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--role-advisor) 10%, transparent); border: 1px dashed color-mix(in oklch, var(--role-advisor) 50%, transparent); font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright))">The advisor's reply is encrypted in the transcript, so its text can't be shown here.</div>
          {/if}
        </section>
      </div>

      <!-- right: stats -->
      <aside aria-label="Advisor stats" style="flex: 0 1 360px; min-width: min(280px, 100%); display: flex; flex-direction: column; gap: 20px">

        <section class="surf brk" style="--c: var(--role-advisor); display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; --wash: linear-gradient(180deg, color-mix(in oklch, var(--role-advisor) 12%, transparent), color-mix(in oklch, var(--panel) 88%, transparent) 30%); --rim: color-mix(in oklch, var(--role-advisor) 30%, transparent); --r: var(--radius); padding: 18px; backdrop-filter: var(--look-surf-blur, blur(6px))">
          <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Consults</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--role-advisor) calc(50% * var(--glow)), transparent)">{ordered.length}</div>
          </div>
          <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Avg time</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--role-advisor) calc(50% * var(--glow)), transparent)">{avgMs === null ? '—' : fmtMs(avgMs)}</div>
          </div>
          <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Total time</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--role-advisor) calc(50% * var(--glow)), transparent)">{spans.length ? fmtMs(sumMs) : '—'}</div>
          </div>
          <div style="background: color-mix(in oklch, var(--role-advisor) 8%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); padding: 10px 12px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Longest</div>
            <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 2px; color: color-mix(in oklab, var(--role-advisor) 40%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--role-advisor) calc(50% * var(--glow)), transparent)">{maxMs === null ? '—' : fmtMs(maxMs)}</div>
          </div>
        </section>

        <section class="surf brk" style="--c: color-mix(in oklch, var(--role-advisor) 50%, transparent); --wash: color-mix(in oklch, var(--panel) 80%, transparent); --rim: color-mix(in oklch, var(--role-advisor) 20%, transparent); --r: var(--radius); padding: 18px 22px; backdrop-filter: var(--look-surf-blur, blur(6px))">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu)); margin-bottom: 12px">Who asked</div>
          {#each askers as m (m.by)}
            <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 9px; font-size: 12px">
              <span title={m.label} style="width: 64px; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--tx)">{m.label}</span>
              <div class="seg" style="flex: 1; height: 6px; position: relative"><div style="position: absolute; left: 0; top: 0; bottom: 0; width: {m.pct}%; overflow: hidden; filter: drop-shadow(0 0 4px color-mix(in oklch, {m.color} calc(100% * var(--glow)), transparent))"><div style="width: 1000px; height: 100%; background: repeating-linear-gradient(90deg,{m.color} 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div></div></div>
              <span style="width: 30px; flex-shrink: 0; text-align: right; color: var(--mu)">{m.n}</span>
            </div>
          {/each}
        </section>
      </aside>
    </div>
    {/if}
    {/if}
  </main>

  {#if session}
    <StatusFooter s={session} archived={source.archived} />
  {/if}
</div>

<style>
  :global {
    .adv .row{transition:background .15s}
    .adv .row:hover{background:color-mix(in oklch, var(--role-advisor) 10%, transparent)}
    .adv .bar{transition:filter .15s}
    .adv .bar:hover{filter:brightness(1.4)}
    .adv a:focus-visible{outline:2px solid color-mix(in oklab, var(--role-advisor) 50%, var(--bright));outline-offset:3px}
    @media (prefers-reduced-motion: reduce){.adv .row,.adv .bar{transition:none}}
  }
</style>
