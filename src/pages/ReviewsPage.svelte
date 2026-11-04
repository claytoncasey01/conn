<script lang="ts">
  import { tick } from 'svelte'
  import { live } from '../lib/live.svelte.ts'
  import Brand from '../lib/Brand.svelte'
  import Nav from '../lib/Nav.svelte'
  import FilterBar from '../lib/FilterBar.svelte'
  import ReviewRow from './ReviewRow.svelte'
  import { barGroups, emptySelection, isActive, matches, toggle } from '../lib/filters.ts'
  import { reviewGroups } from '../lib/reviewGroups.ts'
  import { reviewFilter } from '../lib/reviewFilter.svelte.ts'
  import { refreshReviews } from '../lib/reviewsApi.ts'
  import { refreshBusy } from '../lib/refreshBusy.ts'
  import { markShown } from '../lib/reviewSeen.svelte.ts'
  import { ago, whenLabel } from '../lib/format.ts'
  import { href } from '../lib/router.svelte.ts'

  const view = $derived(live.snap?.reviews)
  // An older server sends no sources; a missing field is not "all off"
  // With "every open PR" on, Slack being down hides only its links; otherwise it hides what only Slack lists (spec wording)
  const slackDown = $derived(view?.sources?.allOpen ? 'Slack could not be read: Slack links are not shown until it can be.' : 'Slack could not be read: showing only PRs that request you.')
  const noSource = $derived(!!view?.sources && !view.sources.requested && !view.sources.channels.length && !view.sources.dms && !view.sources.allOpen)
  // Rows keep the order the server sends them in
  const rows = $derived(view?.rows ?? [])
  const groups = $derived(reviewGroups(rows, reviewFilter.selection))
  const shown = $derived(rows.filter((r) => matches(r, groups, reviewFilter.selection)))
  const bar = $derived(barGroups(rows, groups, reviewFilter.selection))
  const filtering = $derived(isActive(reviewFilter.selection))
  // Being on this page is what makes a waiting PR seen, so the tab badge clears; a PR a filter hides was not seen and keeps it
  $effect(() => markShown(shown))
  // When GitHub was never read, the reason is the error itself: say it once
  const sameError = $derived(!!view && view.error === view.disabledReason)
  // A live session's page is keyed by its id; the row only knows its name
  const sidOf = (name: string) => live.snap?.sessions.find((s) => s.name === name)?.id ?? null

  let filterBar: ReturnType<typeof FilterBar> | undefined = $state()
  async function clearFilters() {
    reviewFilter.selection = emptySelection()
    await tick()
    filterBar?.focusFirst()
  }

  // REFRESH polls GitHub now and asks a signed-out Slack again at once; the new rows arrive on the next snapshot. The button stays disabled until the
  // server says that poll (and any scan it started) has finished: `posting` is the HTTP round trip, `waitingFor` the press number the server answered.
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
    const r = await refreshReviews()
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
</script>

<svelte:head><title>Reviews</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) 72%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="reviews" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>LINK ESTABLISHED · {location.host}</div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span>LINK LOST · {location.host}</div>
    {/if}
  </header>

  {#if view?.disabledReason}
    <div role="alert" style="{banner}; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright))"><span class="hud cap" style="font-weight: 700; letter-spacing: calc(var(--tracking) * 1.1429)">Reviews off:</span> {view.disabledReason}</div>
  {/if}
  {#if view?.error && !sameError}
    <div role="alert" style="{banner}; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright))"><span class="hud cap" style="font-weight: 700; letter-spacing: calc(var(--tracking) * 1.1429)">GitHub:</span> {view.error}{rows.length ? ' The list below is from the last time it could be read.' : ''}</div>
  {/if}
  {#if view?.slack === 'needs-auth'}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ {slackDown} Slack is signed out: sign in to the claude.ai Slack connector.</div>
  {:else if view?.slack === 'error'}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ {slackDown} <span data-content>{view.slackError ?? 'unknown error'}</span></div>
  {/if}
  {#if view?.storeError}
    <div role="alert" style="{banner}; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright))"><span class="hud cap" style="font-weight: 700; letter-spacing: calc(var(--tracking) * 1.1429)">Not saved:</span> <span data-content>{view.storeError}</span>. The list still works; what Reviews learns meanwhile (posts found) is lost when conn restarts.</div>
  {/if}
  {#if view?.pausedUntil}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ GitHub's rate limit is used up: the list below is from the last poll, and conn asks again at {new Date(view.pausedUntil).toLocaleTimeString()}.</div>
  {/if}
  {#each view?.failed ?? [] as f (f.pattern)}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ GitHub could not search <span data-content>{f.pattern}</span>, so its rows are from the last poll: <span data-content>{f.error}</span></div>
  {/each}
  {#if view?.capped?.length}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ Every open PR: the list for <span data-content>{view.capped.join(', ')}</span> stops at 200, so some open PRs are not shown. A narrower pattern in Settings lists them all.</div>
  {/if}
  {#if noSource}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ Every review source is off, so Reviews lists nothing. <a href={href.settings()} style="color: inherit">Choose them in Settings</a>.</div>
  {/if}
  {#if view?.dmError}
    <div role="status" style="{banner}; background: color-mix(in oklch, var(--warn) 8%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--warn) 35%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))">⚠ Slack DMs could not be read: PRs sent to you as a DM are not shown until they can be. <span data-content>{view.dmError}</span></div>
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
      <div role="status" style="padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--bad) 30%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center; font-size: 12px; color: var(--mu)">This server does not report reviews. Restart it from the current version.</div>
    {:else}

    <!-- strip: when GitHub and Slack were last read -->
    <div style="display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px">
      <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Reviews <span class="g-sep" aria-hidden="true"></span> {shown.length} of {rows.length} shown</div>
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 4px 20px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
        <span title={view.lastPollAt ? whenLabel(view.lastPollAt, live.now) : undefined}><span class="cap">Last poll</span> {view.lastPollAt ? ago(view.lastPollAt, live.now) : '—'}</span>
        <span title={view.lastScanAt ? whenLabel(view.lastScanAt, live.now) : undefined}><span class="cap">Last scan</span> {view.lastScanAt ? ago(view.lastScanAt, live.now) : '—'}</span>
        {#if refreshError}<span role="alert" style="color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{refreshError}</span>{/if}
        <button
          type="button"
          class="tab cap"
          onclick={refresh}
          disabled={busy}
          aria-busy={busy}
          aria-label="Refresh reviews now"
          style="font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); background: transparent; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); cursor: {busy ? 'wait' : 'pointer'}"
        >{busy ? 'Refreshing…' : 'Refresh'}</button>
      </div>
    </div>

    <FilterBar
      bind:this={filterBar}
      label="Filter reviews"
      groups={bar}
      selection={reviewFilter.selection}
      ontoggle={(groupId, chipId) => (reviewFilter.selection = toggle(reviewFilter.selection, groups.find((g) => g.id === groupId)!, chipId))}
      onclear={() => (reviewFilter.selection = emptySelection())}
    />

    {#if rows.length === 0}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        {#if view.disabledReason || view.error}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No list yet</div>
          <div style="font-size: 12px; color: var(--mu)">GitHub has not been read successfully, so there is nothing to show. The reason is above.</div>
        {:else}
          <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No PRs need you</div>
          <div style="font-size: 12px; color: var(--mu)">A PR in an allowed repository that asks for your review shows up here. <a href={href.settings()} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright))">Settings</a> choose the repositories and what counts as asking.</div>
        {/if}
      </div>
    {:else if shown.length === 0}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No PRs match these filters</div>
        {#if filtering}
          <button
            class="tab cap"
            onclick={clearFilters}
            aria-label="Clear filters"
            style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
          >Clear ✕</button>
        {/if}
      </div>
    {:else}
      <section aria-label="Pull requests" style="display: flex; flex-direction: column; gap: 10px">
        {#each shown as r (r.key)}
          <ReviewRow row={r} now={live.now} sid={r.session ? sidOf(r.session.name) : null} offReason={view.disabledReason ?? (view.storeError ? 'reviews.json cannot be saved: see the notice above' : null)} />
        {/each}
      </section>
    {/if}
    {/if}
  </main>
</div>
