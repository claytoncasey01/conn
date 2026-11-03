<script lang="ts">
  import { tick } from 'svelte'
  import { live } from '../lib/live.svelte.ts'
  import { href } from '../lib/router.svelte.ts'
  import { flip } from 'svelte/animate'
  import { bootIn, bootOut } from '../lib/fx.ts'
  import Brand from '../lib/Brand.svelte'
  import StopSession from '../lib/StopSession.svelte'
  import Nav from '../lib/Nav.svelte'
  import FilterBar from '../lib/FilterBar.svelte'
  import { barGroups, emptySelection, matches, toggle } from '../lib/filters.ts'
  import { emptyState, sessionGroups } from '../lib/sessionGroups.ts'
  import { sessionFilter } from '../lib/sessionFilter.svelte.ts'
  import { COST_NOTE, dur, modelName, tokens, modeLabel, usd } from '../lib/format.ts'
  import { TEAMMATE_COLORS, TEAMMATE_LABEL, fleetTotals } from '../lib/team.ts'

  // Sessions stopped on a prompt come first: they are the ones holding things up.
  const sessions = $derived([...(live.snap?.sessions ?? [])].sort((a, b) => Number(b.status === 'waiting') - Number(a.status === 'waiting')))
  const busyCount = $derived(sessions.filter(s => s.status === 'busy').length)
  const waitCount = $derived(sessions.filter(s => s.status === 'waiting').length)
  const idleCount = $derived(sessions.filter(s => s.status === 'idle').length)
  let filterBar: ReturnType<typeof FilterBar> | undefined = $state()
  async function clearFilters() {
    sessionFilter.selection = emptySelection()
    await tick()
    filterBar?.focusFirst()
  }
  const groups = $derived(sessionGroups(sessions, sessionFilter.selection))
  const shown = $derived(sessions.filter(s => matches(s, groups, sessionFilter.selection)))
  const bar = $derived(barGroups(sessions, groups, sessionFilter.selection))
  const empty = $derived(emptyState(sessions.length, shown.length, sessionFilter.selection))
  const fleet = $derived(fleetTotals(sessions))

  const ST = { run: 'var(--info)', done: 'var(--ok)' }
  /** Up to four drawn nodes: running ones first, the rest finished. */
  const nodesFor = (total: number, running: number) => {
    const n = Math.min(total, 4)
    return Array.from({ length: n }, (_, i) => (i < Math.min(running, n) ? 'run' : 'done') as 'run' | 'done')
  }
  const pct = (tokensUsed: number, window: number) =>
    window > 0 ? Math.max(0, Math.min(100, Math.round((tokensUsed / window) * 100))) : 0
</script>

<svelte:head><title>All sessions</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) 72%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="sessions" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>LINK ESTABLISHED · {location.host}</div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span>LINK LOST · {location.host}</div>
    {/if}
  </header>

  {#if !live.connected && live.snap}
    <div role="alert" style="position: relative; padding: 9px 32px; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714)">SERVER DISCONNECTED · showing the last snapshot, reconnecting…</div>
  {/if}

  <main style="flex: 1; display: flex; flex-direction: column; gap: 20px; padding: 24px 32px 40px; width: 100%; max-width: 1480px; box-sizing: border-box; margin: 0 auto">

    {#if !live.snap}
      <div role="status" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 40vh; text-align: center">
        <span class="led blink" style="width: 10px; height: 10px; background: var(--edge); box-shadow: 0 0 12px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></span>
        <div class="hud cap" style="font-size: 13px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{live.connected ? 'Loading' : 'Connecting…'}</div>
      </div>
    {:else}

    <!-- fleet summary -->
    <section aria-label="Fleet summary" class="tiles" style="--cols: 4">
      <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--role-lead) 13%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--role-lead) 28%, transparent); --r: calc(var(--radius) * 1.5); padding: 14px 16px">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))" class="cap">Sessions</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--role-lead) calc(60% * var(--glow)), transparent)">{fleet.sessions}</div>
        <div style="font-size: 11px; color: color-mix(in oklab, var(--role-lead) 30%, var(--mu)); margin-top: 2px">{busyCount} live · {waitCount} waiting · {idleCount} idle · <span title={COST_NOTE}>~{usd(fleet.cost)}</span></div>
      </div>
      <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--edge) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--edge) 28%, transparent); --r: calc(var(--radius) * 1.5); padding: 14px 16px">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--edge); box-shadow: 0 0 10px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Agents running</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--edge) 55%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--edge) calc(60% * var(--glow)), transparent)">{fleet.agents}</div>
        <div style="font-size: 11px; color: var(--mu); margin-top: 2px">across live sessions</div>
      </div>
      <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--role-advisor) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--role-advisor) 28%, transparent); --r: calc(var(--radius) * 1.5); padding: 14px 16px">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--role-advisor) 45%, var(--mu))" class="cap">Advisor calls</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--role-advisor) calc(60% * var(--glow)), transparent)">{fleet.advisor}</div>
        <div style="font-size: 11px; color: color-mix(in oklab, var(--role-advisor) 10%, var(--mu)); margin-top: 2px">since sessions started</div>
      </div>
      <div class="surf" style="position: relative; overflow: hidden; --wash: linear-gradient(160deg, color-mix(in oklch, var(--ac) 12%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--ac) 26%, transparent); --r: calc(var(--radius) * 1.5); padding: 14px 16px">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--ac); box-shadow: 0 0 10px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--ac) 40%, var(--mu))" class="cap">Open PRs</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--ac) calc(50% * var(--glow)), transparent)">{fleet.prs}</div>
        <div style="font-size: 11px; color: color-mix(in oklab, var(--ac) 10%, var(--mu)); margin-top: 2px">tied to a session</div>
      </div>
    </section>

    <!-- filters -->
    <div style="display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px">
      <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Sessions <span class="g-sep" aria-hidden="true"></span> {shown.length} of {sessions.length} shown</div>
      <FilterBar
        bind:this={filterBar}
        label="Filter sessions"
        groups={bar}
        selection={sessionFilter.selection}
        ontoggle={(groupId, chipId) => (sessionFilter.selection = toggle(sessionFilter.selection, groups.find(g => g.id === groupId)!, chipId))}
        onclear={() => (sessionFilter.selection = emptySelection())}
      />
    </div>

    <!-- session cards -->
    {#if empty}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        {#if empty === 'no-sessions'}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No running sessions</div>
          <div style="font-size: 12px; color: var(--mu)">Start one with <span style="color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">claude</span> in a terminal and it shows up here.</div>
        {:else}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No sessions match these filters</div>
          <button
            class="tab cap"
            onclick={clearFilters}
            aria-label="Clear filters"
            style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
          >Clear ✕</button>
        {/if}
      </div>
    {:else}
      <section aria-label="Sessions" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 380px), 1fr)); gap: 16px">
        {#each shown as s, i (s.id)}
          {@const waiting = s.status === 'waiting'}
          {@const busy = s.status === 'busy'}
          {@const hot = busy || waiting}
          {@const dot = waiting ? 'var(--warn)' : busy ? 'var(--role-lead)' : 'color-mix(in oklab, var(--mu) 65%, var(--bg))'}
          {@const accent = waiting ? 'var(--warn)' : busy ? 'var(--role-lead)' : 'color-mix(in oklab, var(--mu) 55%, var(--bg))'}
          {@const nodes = nodesFor(s.agentsTotal, s.agentsRunning)}
          {@const ctx = pct(s.ctxTokens, s.ctxWindow)}
          <div style="position: relative; display: flex" in:bootIn={{ index: i }} out:bootOut animate:flip={{ duration: 380 }}>
          <a
            class="surf sess brk"
            href={href.overview(s.id)}
            style="--c: {accent}; flex: 1; min-width: 0; position: relative; display: flex; flex-direction: column; gap: 14px; box-sizing: border-box; padding: 18px 20px 16px; --rim: {waiting ? 'color-mix(in oklch, var(--warn) 55%, transparent)' : busy ? 'color-mix(in oklch, var(--role-lead) 40%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, {waiting ? 'color-mix(in oklch, var(--warn) 12%, transparent)' : busy ? 'color-mix(in oklch, var(--role-lead) 10%, transparent)' : 'color-mix(in oklch, var(--edge) 6%, transparent)'}, color-mix(in oklch, var(--panel) 92%, transparent) 55%); backdrop-filter: blur(6px); text-decoration: none; color: inherit"
          >
            <div class="stripe" style="position: absolute; left: 0; top: 0; width: 100%; height: 2px; background: linear-gradient(90deg, {accent}, transparent); box-shadow: 0 0 12px color-mix(in oklch, {accent} calc(100% * var(--glow)), transparent)"></div>

            <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px">
              <div style="display: flex; align-items: center; gap: 10px; min-width: 0">
                <span class="led" style="flex-shrink: 0; width: 9px; height: 9px; background: {dot}; box-shadow: 0 0 10px color-mix(in oklch, {dot} calc(100% * var(--glow)), transparent)"></span>
                <span data-content class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.2857); color: color-mix(in oklab, var(--tx) 50%, var(--bright)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{s.name}</span>
                {#if s.team}
                  <span title="A teammate: its own claude, started by a lead" style="flex-shrink: 0; padding: 1px 6px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {TEAMMATE_COLORS.border}; background: {TEAMMATE_COLORS.bg}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: {TEAMMATE_COLORS.fg}">{TEAMMATE_LABEL}</span>
                {/if}
              </div>
              <span style="flex-shrink: 0; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: {dot}" class="cap">{waiting ? 'Needs input' : busy ? 'Live' : 'Idle'}</span>
            </div>

            <div style="font-size: 12px; color: var(--mu); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">⎇ {s.branch || '—'}</div>
            <div style="font-size: 12px; line-height: 1.45; color: var(--tx); min-height: 34px"><span class="g-bullet" aria-hidden="true" style="color: {dot}"></span>{waiting ? `waiting · ${s.waitingFor ?? 'input needed'}` : s.activity || (busy ? 'working' : 'idle')}</div>

            <div style="display: flex; flex-wrap: wrap; gap: 14px 18px; align-items: center">
              <div aria-hidden="true" style="flex-shrink: 0; width: 96px; display: flex; flex-direction: column; align-items: center">
                <div style="width: 34px; height: 14px; border-radius: calc(var(--radius) * 0.75); background: var(--role-lead); box-shadow: 0 0 12px color-mix(in oklch, var(--role-lead) calc(60% * var(--glow)), transparent)"></div>
                <div style="width: 2px; height: 8px; background: color-mix(in oklch, var(--edge) 50%, transparent)"></div>
                <div style="height: 2px; width: {Math.max(0, (nodes.length - 1) * 26)}px; background: color-mix(in oklch, var(--edge) 50%, transparent)"></div>
                <div style="display: flex; gap: 12px; margin-top: 0">
                  {#each nodes as n, i (i)}
                    <div style="display: flex; flex-direction: column; align-items: center"><div style="width: 2px; height: 7px; background: color-mix(in oklch, var(--edge) 50%, transparent)"></div><div style="width: 14px; height: 14px; border-radius: calc(var(--radius) * 0.75); background: {ST[n]}; box-shadow: {n === 'run' ? '0 0 10px color-mix(in oklch, var(--edge) calc(70% * var(--glow)), transparent)' : 'none'}"></div></div>
                  {/each}
                </div>
              </div>
              <div style="flex: 1; min-width: 150px; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px">
                <div>
                  <div style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Agents</div>
                  <div class="hud" style="font-weight: 700; font-size: 19px; margin-top: 2px; color: color-mix(in oklab, var(--edge) 45%, var(--bright))">{s.agentsTotal ? `${s.agentsRunning} / ${s.agentsTotal}` : '0'}</div>
                </div>
                <div>
                  <div style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Advisor</div>
                  <div class="hud" style="font-weight: 700; font-size: 19px; margin-top: 2px; color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright))">{s.advisorCalls}</div>
                </div>
                <div>
                  <div style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Up</div>
                  <div class="hud" style="font-weight: 700; font-size: 19px; margin-top: 2px">{dur(live.now - s.startedAt)}</div>
                </div>
              </div>
            </div>

            <div style="display: flex; align-items: center; gap: 10px; font-size: 11px; color: var(--mu)">
              <span style="letter-spacing: calc(var(--tracking) * 0.8571)">{modelName(s.model)}</span>
              <div class="seg" style="flex: 1; height: 4px; position: relative"><div style="position: absolute; left: 0; top: 0; bottom: 0; width: {ctx}%; overflow: hidden"><div style="width: 400px; height: 100%; background: repeating-linear-gradient(90deg,var(--tx) 0 var(--look-seg-on, 8px),transparent var(--look-seg-on, 8px) 8px)"></div></div></div>
              <span>CTX {tokens(s.ctxTokens)}{s.ctxWindow ? ` / ${tokens(s.ctxWindow)}` : ''}</span>
            </div>

            <div style="display: flex; flex-wrap: wrap; gap: 6px; padding-top: 12px; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 14%, transparent)">
              {#if s.mode !== 'default'}
              <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 40%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714)">⏵⏵ {modeLabel(s.mode)}</span>
              {/if}
              {#if s.pr}
                <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--ac) 45%, transparent); background: color-mix(in oklch, var(--ac) 10%, transparent); color: color-mix(in oklab, var(--ac) 60%, var(--bright)); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714)">PR #{s.pr.number}</span>
              {:else}
                <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: color-mix(in oklab, var(--mu) 75%, var(--panel)); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714)" class="cap">No PR</span>
              {/if}
              <span title={COST_NOTE} style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); background: transparent; color: var(--tx); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714)">~{usd(s.costUsd)}{s.costPartial ? '+' : ''}</span>
              {#if s.shells > 0}
                <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); background: transparent; color: var(--tx); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714)">{s.shells} SHELL{s.shells === 1 ? '' : 'S'}</span>
              {/if}
            </div>
          </a>
          <div style="position: absolute; right: 14px; bottom: 12px; display: flex; align-items: center; gap: 8px; justify-content: flex-end; max-width: calc(100% - 28px); padding: 2px 0 2px 8px; background: color-mix(in oklch, var(--panel) 96%, transparent)">
            {#if s.team?.leadId}
              <a class="tab" href={href.overview(s.team.leadId)} aria-label="Open the lead of {s.name}" style="display: flex; align-items: center; min-height: 28px; padding: 0 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {TEAMMATE_COLORS.border}; color: {TEAMMATE_COLORS.fg}; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); text-decoration: none"><span class="cap">Lead <span class="g-fwd" aria-hidden="true"></span></span></a>
            {/if}
            <StopSession id={s.id} name={s.name} busy={hot} compact /></div>
          </div>
        {/each}
      </section>
    {/if}
    {/if}
  </main>
</div>

<style>
  :global {
    a:focus-visible { outline: 2px solid color-mix(in oklab, var(--edge) 55%, var(--bright)); outline-offset: 3px; }
    .sess { transition: transform .18s, border-color .18s, box-shadow .18s; }
    .sess:hover { transform: translateY(calc(-3px * var(--look-lift, 0))); --lit: 0 0 34px color-mix(in oklch, var(--edge) calc(20% * var(--glow)), transparent); }
    @media (prefers-reduced-motion: reduce) { .sess { transition: none; } }
  }
</style>
