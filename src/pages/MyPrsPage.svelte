<script lang="ts">
  import { tick } from 'svelte'
  import { live } from '../lib/live.svelte.ts'
  import Brand from '../lib/Brand.svelte'
  import Nav from '../lib/Nav.svelte'
  import FilterBar from '../lib/FilterBar.svelte'
  import { barGroups, emptySelection, matches, repoGroups, toggle } from '../lib/filters.ts'
  import { myPrsFilter } from '../lib/myPrsFilter.svelte.ts'
  import MyPrRow from '../lib/MyPrRow.svelte'
  import { refreshMyPrs } from '../lib/myPrsApi.ts'
  import { errorText, listState } from '../lib/myPrsList.ts'
  import { loadSort, saveSort, sortRows, type MyPrsSort } from '../lib/myPrsSort.ts'
  import { refreshBusy } from '../lib/refreshBusy.ts'
  import { href } from '../lib/routes.ts'
  import { clock } from '../lib/myPrsDraftView.ts'

  const view = $derived(live.snap?.myPrs)
  // Sorted to the viewer's choice, remembered in this browser; without storage it still applies to this visit
  let by = $state<MyPrsSort>(loadSort())
  function pick(next: MyPrsSort) {
    by = next
    saveSort(next)
  }
  const allRows = $derived(sortRows(view?.rows ?? [], by))
  // The repo filter: chips are the repos of the list, none for one repo or none; the whole list is here, so it filters on the client
  const groups = $derived(repoGroups(allRows, myPrsFilter.selection))
  const rows = $derived(allRows.filter(r => matches(r, groups, myPrsFilter.selection)))
  const bar = $derived(barGroups(allRows, groups, myPrsFilter.selection))
  let filterBar: ReturnType<typeof FilterBar> | undefined = $state()
  async function clearFilters() {
    myPrsFilter.selection = emptySelection()
    await tick()
    filterBar?.focusFirst()
  }
  const shown = $derived(view ? listState(view) : 'loading')
  // From the whole list, not the filtered rows: a filter that hides every row must not hide that the list is the last good one
  const errorLine = $derived(view ? errorText(view) : null)

  // REFRESH polls GitHub now; the new rows arrive on the next snapshot. The button stays disabled until the
  // server says that poll has finished: `posting` is the HTTP round trip, `waitingFor` the press number the server answered.
  const WAIT_LIMIT_MS = 180_000
  // The POST answer usually arrives before the frame (sent every 600 ms) that shows the press: a snapshot older than the press is taken as busy for this long
  const STALE_FRAME_MS = 2500
  let posting = $state(false)
  let waitingFor = $state<number | null>(null)
  let waitingSince = $state(0)
  const staleOk = $derived(waitingFor !== null && live.now - waitingSince < STALE_FRAME_MS)
  let waitTimer: ReturnType<typeof setTimeout> | undefined
  let refreshError = $state<string | null>(null)
  const busy = $derived(posting || refreshBusy(view?.refresh, waitingFor, staleOk))
  function stopWaiting() {
    clearTimeout(waitTimer)
    waitTimer = undefined
    waitingFor = null
  }
  // The wait ends when the server reports it done (or has restarted and forgotten it); the timer is the page's own way out
  $effect(() => {
    if (waitingFor !== null && !refreshBusy(view?.refresh, waitingFor, staleOk)) stopWaiting()
  })
  $effect(() => () => clearTimeout(waitTimer))
  async function refresh() {
    posting = true
    refreshError = null
    const r = await refreshMyPrs()
    posting = false
    if (!r.ok) {
      stopWaiting()
      refreshError = r.error ?? 'the refresh did not go through'
    } else if (r.seq !== undefined) {
      clearTimeout(waitTimer)
      waitingFor = r.seq
      waitingSince = Date.now()
      waitTimer = setTimeout(stopWaiting, WAIT_LIMIT_MS)
    }
  }

  const banner = 'position: relative; padding: 9px 32px; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714); overflow-wrap: anywhere'
  const SORTS: { id: MyPrsSort; label: string }[] = [
    { id: 'opened', label: 'Opened' },
    { id: 'comment', label: 'Last comment' },
  ]
</script>

<svelte:head><title>My PRs</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) var(--look-head-fill, 72%), transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="myprs" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>LINK ESTABLISHED · {location.host}</div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span>LINK LOST · {location.host}</div>
    {/if}
  </header>

  {#if errorLine}
    <div role="alert" style="{banner}; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright))"><span class="hud cap" style="font-weight: 700; letter-spacing: calc(var(--tracking) * 1.1429)">GitHub:</span> {errorLine}</div>
  {/if}
  {#if view?.warning}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ {view.warning}{view.pausedUntil ? ` Next try about ${clock(view.pausedUntil)}.` : ''}</div>
  {/if}
  {#if view?.capped}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ Some of your open PRs may not be listed: GitHub's search hit its limit, or GitHub could not read them all.</div>
  {/if}
  {#if !live.connected && live.snap}
    <div role="alert" style="{banner}; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright))">SERVER DISCONNECTED · showing the last snapshot, reconnecting…</div>
  {/if}

  <main style="flex: 1; display: flex; flex-direction: column; gap: 20px; padding: 24px 32px 40px; width: 100%; max-width: 1480px; box-sizing: border-box; margin: 0 auto">

    {#if !live.snap}
      <div role="status" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 40vh; text-align: center">
        <span class="led blink" style="width: 10px; height: 10px; background: var(--edge); box-shadow: 0 0 12px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></span>
        <div class="hud cap" style="font-size: 13px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{live.connected ? 'Loading' : 'Connecting…'}</div>
      </div>
    {:else if !view}
      <div role="status" style="padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--bad) 30%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center; font-size: 12px; color: var(--mu)">This server does not report My PRs. Restart it from the current version.</div>
    {:else}

    <div style="display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px">
      <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">My PRs <span class="g-sep" aria-hidden="true"></span> {shown === 'loading' ? 'Loading' : `${rows.length} open`}<span data-content>{view.me ? ` · ${view.me}` : ''}</span>{#if view.rate}<span data-content title="GraphQL points the last poll cost, and what GitHub says is left until {clock(view.rate.resetAt)}"> · {view.rate.cost} pts, {view.rate.remaining} left</span>{/if}</div>
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 4px 20px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
        <span style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">
          <span id="myprs-sort" class="cap">Sort</span>
          <span role="group" aria-labelledby="myprs-sort" style="display: flex">
            {#each SORTS as s, i (s.id)}
              <button
                type="button"
                class="sort cap"
                onclick={() => pick(s.id)}
                aria-pressed={by === s.id}
                style="font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 12px; min-height: 44px; box-sizing: border-box; cursor: pointer; {i === 0 ? 'border-radius: var(--radius) 0 0 var(--radius)' : 'border-radius: 0 var(--radius) var(--radius) 0; border-left: 0'}"
              >{s.label}</button>
            {/each}
          </span>
        </span>
        {#if refreshError}<span role="alert" style="color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{refreshError}</span>{/if}
        <button
          type="button"
          class="tab cap"
          onclick={refresh}
          disabled={busy}
          aria-busy={busy}
          aria-label="Refresh my PRs now"
          style="font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); background: transparent; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); cursor: {busy ? 'wait' : 'pointer'}"
        >{busy ? 'Refreshing…' : 'Refresh'}</button>
      </div>
    </div>

    {#if bar.length > 0}
      <FilterBar
        bind:this={filterBar}
        label="Filter my PRs"
        groups={bar}
        selection={myPrsFilter.selection}
        ontoggle={(groupId, chipId) => (myPrsFilter.selection = toggle(myPrsFilter.selection, groups.find(g => g.id === groupId)!, chipId))}
        onclear={() => (myPrsFilter.selection = emptySelection())}
      />
    {/if}

    {#if shown === 'setup' || shown === 'github-off'}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        {#if shown === 'setup'}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">Nothing to list</div>
          <div style="font-size: 12px; color: var(--mu)">No repositories allowed. <a href={href.settings()} style="color: color-mix(in oklab, var(--ac) 40%, var(--bright))">Add one in Settings.</a></div>
        {:else}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">GitHub is off</div>
          <div style="font-size: 12px; color: var(--mu)">conn makes no GitHub calls, so there are no PRs to list. <a href={href.settings()} style="color: color-mix(in oklab, var(--ac) 40%, var(--bright))">Turn GitHub on in Settings.</a></div>
        {/if}
      </div>
    {:else if shown === 'loading'}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 12px; padding: 56px 20px; text-align: center">
        <span class="led blink" style="width: 10px; height: 10px; background: var(--edge); box-shadow: 0 0 12px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></span>
        <div class="hud cap" style="font-size: 13px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Loading</div>
      </div>
    {:else if rows.length === 0 && allRows.length > 0}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No PRs match these filters</div>
        <button
          type="button"
          class="tab cap"
          onclick={clearFilters}
          aria-label="Clear filters"
          style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
        >Clear ✕</button>
      </div>
    {:else if rows.length === 0}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        {#if shown === 'error'}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No list yet</div>
          <div style="font-size: 12px; color: var(--mu)">GitHub has not been read successfully, so there is nothing to show. The reason is above.</div>
        {:else}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No open PRs</div>
          <div style="font-size: 12px; color: var(--mu)">A PR you open on GitHub shows up here.</div>
        {/if}
      </div>
    {:else}
      <section aria-label="My pull requests" style="display: flex; flex-direction: column; gap: 10px">
        {#each rows as r (`${r.repo}#${r.number}`)}
          <MyPrRow row={r} now={live.now} channel={view.channel} />
        {/each}
      </section>
    {/if}
    {/if}
  </main>
</div>

<style>
  .sort { background: transparent; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); color: var(--mu); transition: background .15s, border-color .15s, color .15s; }
  .sort:hover { background: color-mix(in oklch, var(--edge) 10%, transparent); border-color: color-mix(in oklch, var(--edge) 45%, transparent); color: var(--tx); }
  .sort[aria-pressed='true'] { border-color: color-mix(in oklch, var(--role-lead) 50%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright)); }
  .sort[aria-pressed='true']:hover { background: color-mix(in oklch, var(--role-lead) 18%, transparent); border-color: color-mix(in oklch, var(--role-lead) 80%, transparent); color: color-mix(in oklab, var(--role-lead) 15%, var(--bright)); }
  @media (prefers-reduced-motion: reduce) { .sort { transition: none; } }
</style>
