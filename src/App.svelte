<script lang="ts">
  import { startLive } from './lib/live.svelte.ts'
  import { live } from './lib/live.svelte.ts'
  import { href, router } from './lib/router.svelte.ts'
  import { needsSetup } from './lib/routes.ts'
  import { pruneShown } from './lib/reviewSeen.svelte.ts'
  import { archivedSource, liveSource } from './lib/source.svelte.ts'
  import Sessions from './pages/Sessions.svelte'
  import Overview from './pages/Overview.svelte'
  import AdvisorPage from './pages/AdvisorPage.svelte'
  import AgentPage from './pages/AgentPage.svelte'
  import HistoryPage from './pages/HistoryPage.svelte'
  import OpenWork from './pages/OpenWork.svelte'
  import ReviewsPage from './pages/ReviewsPage.svelte'
  import MyPrsPage from './pages/MyPrsPage.svelte'
  import SettingsPage from './pages/SettingsPage.svelte'
  import ThemesPage from './pages/ThemesPage.svelte'
  import TicketRun from './pages/TicketRun.svelte'

  startLive()

  // First launch: until a repository list is saved every page but the theme gallery and Settings is Settings. The page switches at
  // once; the address follows (replace, so Back does not bounce between the two).
  const setup = $derived(needsSetup(router.route, live.snap))
  const route = $derived(setup ? { page: 'settings' as const } : router.route)
  $effect(() => {
    if (setup) location.replace(href.settings())
  })

  // A PR that stopped waiting on me can raise the tab badge again the next time it is asked for another review
  $effect(() => {
    const rows = live.snap?.reviews?.rows
    if (rows) pruneShown(rows)
  })
</script>

{#if live.locked}
  <div role="alert" style="padding: 8px 32px; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 35%, transparent); font-size: 11px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--bad) 55%, var(--bright))">This page has no key for this Conn. Open the address <code>conn serve</code> printed, or the app's window.</div>
{/if}

<!-- Each page kind has its own branch, so moving between a live session and an archived one remounts the page -->
{#if route.page === 'sessions'}
  <Sessions />
{:else if route.page === 'overview'}
  <Overview source={liveSource(route.sid)} />
{:else if route.page === 'advisor'}
  <AdvisorPage source={liveSource(route.sid)} />
{:else if route.page === 'agent'}
  <AgentPage source={liveSource(route.sid)} aid={route.aid} />
{:else if route.page === 'work'}
  <OpenWork />
{:else if route.page === 'run'}
  {#key route.key}
    <TicketRun key={route.key} />
  {/key}
{:else if route.page === 'reviews'}
  <ReviewsPage />
{:else if route.page === 'myprs'}
  <MyPrsPage />
{:else if route.page === 'history'}
  <HistoryPage />
{:else if route.page === 'themes'}
  <ThemesPage />
{:else if route.page === 'settings'}
  <SettingsPage />
{:else if route.page === 'entry'}
  <Overview source={archivedSource(route.uuid)} />
{:else if route.page === 'entryAdvisor'}
  <AdvisorPage source={archivedSource(route.uuid)} />
{:else if route.page === 'entryAgent'}
  <AgentPage source={archivedSource(route.uuid)} aid={route.aid} />
{/if}
