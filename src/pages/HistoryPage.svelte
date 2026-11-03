<script lang="ts">
  import { tick, untrack } from 'svelte'
  import { fetchHistory, live } from '../lib/live.svelte.ts'
  import { href } from '../lib/router.svelte.ts'
  import Brand from '../lib/Brand.svelte'
  import Nav from '../lib/Nav.svelte'
  import FilterBar from '../lib/FilterBar.svelte'
  import { isActive, toggle } from '../lib/filters.ts'
  import { historyFilter as f, resetHistoryFilter } from '../lib/historyFilter.svelte.ts'
  import { appendRows, historyBar, historyQueryString, refreshLimit } from '../lib/historyQuery.ts'
  import { COST_NOTE, dur, modelName, usd, whenLabel } from '../lib/format.ts'
  import { kindLabel } from '../lib/kindLabel.ts'
  import type { HistoryList, HistoryRow } from '../lib/types.ts'

  const PAGE = 100
  const now = Date.now()

  // The four typed filters wait for a pause in typing; chips and dates apply at once.
  let typed = $state({ text: f.text, ref: f.ref, branch: f.branch, minCost: f.minCost })
  $effect(() => {
    const t = { text: f.text, ref: f.ref, branch: f.branch, minCost: f.minCost }
    const h = setTimeout(() => (typed = t), 300)
    return () => clearTimeout(h)
  })
  // A ticket key is searched only while Open work is on (the Atlassian module); without it the field takes a PR number only
  const tickets = $derived(!!live.snap?.openWork)
  const ref = $derived(tickets || /^#?\d+$/.test(typed.ref.trim()) ? typed.ref : '')
  const qs = $derived(historyQueryString(f.selection, { ...typed, ref, from: f.from, to: f.to }))

  // Raw: a list is replaced whole, never edited
  let rows = $state.raw<HistoryRow[]>([])
  let info = $state.raw<HistoryList | null>(null)
  let loading = $state(false)
  let error = $state('')
  // Only the newest request may write: a slow answer for an older query must not replace a newer one
  let seq = 0

  // reset: a new query, first page. append: SHOW MORE. keep: a refresh that asks for as many rows as are shown (up to the
  // server's 500, beyond that it is the first 500), so what SHOW MORE loaded does not collapse.
  async function load(mode: 'reset' | 'append' | 'keep') {
    const append = mode === 'append'
    const mine = ++seq
    loading = true
    const limit = mode === 'keep' ? refreshLimit(rows.length, PAGE) : PAGE
    const q = [qs, `limit=${limit}`, append ? `offset=${rows.length}` : ''].filter(Boolean).join('&')
    const r = await fetchHistory(q)
    if (mine !== seq) return
    loading = false
    if (!r) {
      error = 'The server did not answer.'
      return
    }
    error = ''
    info = r
    rows = append ? appendRows(rows, r.rows) : r.rows
  }

  $effect(() => {
    qs
    untrack(() => load('reset'))
  })

  const backfill = $derived(live.snap?.history?.backfill ?? null)
  const failedCount = $derived(live.snap?.history?.failed ?? 0)
  // Set when History is off or its database cannot be read: the rest of the dashboard works without it
  const unavailable = $derived(live.snap?.history?.error ?? null)
  const off = $derived(unavailable !== null && !info)
  // Read again once it comes back
  let wasDown = false
  $effect(() => {
    if (unavailable !== null) wasDown = true
    else if (wasDown) {
      wasDown = false
      untrack(() => load('reset'))
    }
  })
  const importing = $derived(backfill !== null)

  // Entries arrive while the import runs; look again every few seconds, and once more when it ends
  $effect(() => {
    if (!importing) return
    const h = setInterval(() => untrack(() => load('keep')), 4000)
    return () => clearInterval(h)
  })
  let wasImporting = false
  $effect(() => {
    if (importing) wasImporting = true
    else if (wasImporting) {
      wasImporting = false
      untrack(() => load('keep'))
    }
  })

  // A session that ends is archived a moment later
  // A derived number, so the effect re-runs when the count changes and not on every snapshot (which would cancel the timer)
  const liveCount = $derived(live.snap?.sessions.length ?? -1)
  let lastLive = -1
  $effect(() => {
    const n = liveCount
    const changed = lastLive !== -1 && n !== -1 && n !== lastLive
    lastLive = n
    if (!changed) return
    const h = setTimeout(() => untrack(() => load('keep')), 2000)
    return () => clearTimeout(h)
  })

  // The debounced copy is cleared with the inputs, or the next request would still carry the old text
  function clearAll() {
    resetHistoryFilter()
    typed = { text: '', ref: '', branch: '', minCost: '' }
  }

  let filterBar: ReturnType<typeof FilterBar> | undefined = $state()
  const bar = $derived(historyBar(info?.counts ?? {}, info?.facets.models ?? [], f.selection, info?.facets.projects ?? [], info?.facets.repos ?? []))
  const typedAny = $derived(!!(f.text.trim() || f.ref.trim() || f.branch.trim() || f.minCost.trim()))
  const filtering = $derived(isActive(f.selection) || typedAny)
  const custom = $derived((f.selection.range ?? []).includes('custom'))
  const total = $derived(info?.total ?? 0)

  const toneOf = (outcome: string) =>
    outcome === 'merged' ? { fg: 'color-mix(in oklab, var(--ok) 50%, var(--bright))', line: 'color-mix(in oklch, var(--ok) 45%, transparent)' } : outcome === 'PR opened' ? { fg: 'color-mix(in oklab, var(--ac) 60%, var(--bright))', line: 'color-mix(in oklch, var(--ac) 45%, transparent)' } : { fg: 'var(--mu)', line: 'color-mix(in oklch, var(--edge) 22%, transparent)' }

  const glance = (r: HistoryRow): { text: string; dim: boolean } => {
    if (r.glance) return { text: r.glance, dim: false }
    if (r.summaryState === 'queued' || r.summaryState === 'running') return { text: 'summarizing…', dim: true }
    if (r.summaryState === 'failed') return { text: 'summary failed', dim: true }
    return { text: 'no summary yet', dim: true }
  }

  const field = 'box-sizing: border-box; min-height: 44px; padding: 8px 12px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: color-mix(in oklch, var(--panel) 60%, transparent); color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.2857)'
  const label = 'display: flex; flex-direction: column; gap: 6px; font-size: 10px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))'
</script>

<svelte:head><title>History</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) 72%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="history" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>LINK ESTABLISHED · {location.host}</div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span>LINK LOST · {location.host}</div>
    {/if}
  </header>

  {#if unavailable}
    <div role="alert" style="position: relative; padding: 9px 32px; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714)"><span class="hud cap" style="font-weight: 700; letter-spacing: calc(var(--tracking) * 1.1429)">History unavailable:</span> {unavailable}</div>
  {/if}
  {#if backfill}
    <div role="status" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px; padding: 9px 32px; background: color-mix(in oklch, var(--edge) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: color-mix(in oklab, var(--edge) 45%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.7143)">
      <span class="led blink" aria-hidden="true" style="width: 8px; height: 8px; background: var(--edge); box-shadow: 0 0 10px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></span>
      <span class="hud" style="font-weight: 700; letter-spacing: calc(var(--tracking) * 1.1429)">IMPORTING {backfill.done} / {backfill.total}</span>
      <span style="color: var(--mu); letter-spacing: calc(var(--tracking) * 0.2857)">past sessions are being added to History from their transcripts</span>
    </div>
  {/if}
  {#if failedCount > 0}
    <div role="status" style="position: relative; padding: 9px 32px; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714)">⚠ {failedCount} {failedCount === 1 ? 'SESSION' : 'SESSIONS'} COULD NOT BE SAVED TO HISTORY</div>
  {/if}
  {#if !live.connected && live.snap}
    <div role="alert" style="position: relative; padding: 9px 32px; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714)">SERVER DISCONNECTED · reconnecting…</div>
  {/if}

  <main style="flex: 1; display: flex; flex-direction: column; gap: 20px; padding: 24px 32px 40px; width: 100%; max-width: 1480px; box-sizing: border-box; margin: 0 auto">

    <div style="display: flex; flex-direction: column; gap: 14px">
      <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))"><span class="cap">History</span> <span class="g-sep" aria-hidden="true"></span> {info ? `${rows.length} OF ${total} SHOWN` : off ? 'UNAVAILABLE' : 'LOADING'}</div>
      {#if !off}
      <FilterBar
        bind:this={filterBar}
        label="Filter history"
        groups={bar}
        selection={f.selection}
        active={typedAny}
        ontoggle={(groupId, chipId) => (f.selection = toggle(f.selection, bar.find(g => g.id === groupId)!, chipId))}
        onclear={clearAll}
      />
      <div style="display: flex; flex-wrap: wrap; align-items: flex-end; gap: 12px 16px">
        {#if custom}
          <label style={label}>FROM<input type="date" bind:value={f.from} style="{field}; width: 160px" /></label>
          <label style={label}>TO<input type="date" bind:value={f.to} style="{field}; width: 160px" /></label>
        {/if}
        <label style="{label}; flex: 1 1 260px; min-width: 0">SEARCH<input type="search" bind:value={f.text} maxlength="200" autocomplete="off" placeholder="words in the session or its summary" style="{field}; width: 100%" /></label>
        <label style="{label}; flex: 0 1 180px; min-width: 0">{tickets ? 'PR OR TICKET' : 'PR'}<input type="text" bind:value={f.ref} maxlength="40" autocomplete="off" placeholder={tickets ? 'ticket key or PR number' : 'PR number'} style="{field}; width: 100%" /></label>
        <label style="{label}; flex: 0 1 200px; min-width: 0">BRANCH<input type="text" bind:value={f.branch} maxlength="100" autocomplete="off" placeholder="part of a branch name" style="{field}; width: 100%" /></label>
        <label style="{label}; flex: 0 1 130px; min-width: 0">MIN COST $<input type="text" inputmode="decimal" bind:value={f.minCost} maxlength="10" autocomplete="off" placeholder="0.50" style="{field}; width: 100%" /></label>
      </div>
      {/if}
    </div>

    {#if error && !unavailable}
      <div role="alert" style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px; padding: 12px 16px; border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--bad) 8%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.2857)">
        <span>History could not be read: {error}{info ? ' The list below is from the last time it could.' : ''}</span>
        <button class="tab cap" onclick={() => load('keep')} style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); background: transparent; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer">Retry</button>
      </div>
    {/if}

    {#if off}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--bad) 30%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">History is not available</div>
        <div style="font-size: 12px; color: var(--mu)">Sessions and Open work are not affected. History does not delete or replace the history file.</div>
      </div>
    {:else if !info && !error}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 30vh; text-align: center">
        <span class="led blink" style="width: 10px; height: 10px; background: var(--edge); box-shadow: 0 0 12px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></span>
        <div class="hud cap" style="font-size: 13px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Loading</div>
      </div>
    {:else if info && rows.length === 0}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        {#if filtering}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No sessions match these filters</div>
          <button
            class="tab cap"
            onclick={async () => {
              clearAll()
              await tick()
              filterBar?.focusFirst()
            }}
            aria-label="Clear filters"
            style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
          >Clear ✕</button>
        {:else}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">Nothing in History yet</div>
          {#if backfill}
            <div style="font-size: 12px; color: var(--mu)">Past sessions are being imported ({backfill.done} of {backfill.total} so far) and will show up here.</div>
          {:else}
            <div style="font-size: 12px; color: var(--mu)">A session is saved here when it ends.</div>
          {/if}
        {/if}
      </div>
    {:else if info}
      <section aria-label="History" style="display: flex; flex-direction: column; gap: 10px">
        {#each rows as r (r.id)}
          {@const g = glance(r)}
          {@const tone = toneOf(r.outcome)}
          <a
            class="surf hrow"
            href={href.entry(r.id)}
            style="position: relative; display: flex; flex-direction: column; gap: 8px; box-sizing: border-box; min-height: 44px; padding: 14px 18px; --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%); text-decoration: none; color: inherit"
          >
            <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px">
              <span data-content class="hud" style="min-width: 0; font-weight: 700; font-size: 18px; letter-spacing: calc(var(--tracking) * 0.2857); color: color-mix(in oklab, var(--tx) 50%, var(--bright)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{r.name || '(unnamed)'}</span>
              <span style="flex-shrink: 0; padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {tone.line}; color: {tone.fg}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)">{r.outcome.toUpperCase()}</span>
            </div>
            <div style="font-size: 12px; line-height: 1.45; color: {g.dim ? 'color-mix(in oklab, var(--mu) 75%, var(--panel))' : 'var(--tx)'}; font-style: {g.dim ? 'italic' : 'normal'}; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{g.text}</div>
            <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 6px 14px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
              <span>{whenLabel(r.startedAt, now)}</span>
              <span class="cap" style="padding: 2px 7px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: var(--mu); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)">{kindLabel(r.kind)}</span>
              <span>{dur(r.endedAt - r.startedAt)}</span>
              <span title={COST_NOTE}>~{usd(r.costUsd)}</span>
              <span>{r.agents} {r.agents === 1 ? 'AGENT' : 'AGENTS'}</span>
              {#if r.model}<span>{modelName(r.model)}</span>{/if}
              {#if r.errors > 0}<span style="color: color-mix(in oklab, var(--bad) 70%, var(--bright))">{r.errors} {r.errors === 1 ? 'ERROR' : 'ERRORS'}</span>{/if}
            </div>
          </a>
        {/each}
        {#if rows.length < total}
          <button
            class="tab"
            onclick={() => load('append')}
            disabled={loading}
            style="align-self: center; padding: 8px 20px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); background: transparent; color: var(--tx); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: {loading ? 'wait' : 'pointer'}"
          >{loading ? 'LOADING…' : `SHOW MORE · ${total - rows.length} LEFT`}</button>
        {/if}
      </section>
    {/if}
  </main>
</div>

<style>
  :global {
    .hrow { transition: border-color .16s, box-shadow .16s, transform .16s; }
    .hrow:hover { transform: translateY(calc(-2px * var(--look-lift, 0))); border-color: color-mix(in oklch, var(--edge) 45%, transparent); --lit: 0 0 26px color-mix(in oklch, var(--edge) calc(14% * var(--glow)), transparent); }
    input::placeholder { color: color-mix(in oklab, var(--mu) 70%, var(--bg)); }
    @media (prefers-reduced-motion: reduce) { .hrow { transition: none; } .hrow:hover { transform: none; } }
  }
</style>
