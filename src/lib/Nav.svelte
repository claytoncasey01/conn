<script lang="ts">
  import { href } from './router.svelte.ts'
  import { alerts, toggleAlerts } from './notify.svelte.ts'
  import { live } from './live.svelte.ts'
  import { badgeCount } from './reviewGroups.ts'
  import { seen } from './reviewSeen.svelte.ts'
  import { readyCount } from './myPrsBadge.ts'
  import ThemeMenu from './ThemeMenu.svelte'

  // 'themes' lights no tab: that page is reached from the theme menu
  let { active }: { active: 'sessions' | 'work' | 'reviews' | 'myprs' | 'history' | 'settings' | 'themes' } = $props()
  // Sessions the archive could not capture
  const failed = $derived(live.snap?.history?.failed ?? 0)
  // History is off, or its database cannot be read
  const unavailable = $derived(live.snap?.history?.error ?? null)

  // PRs that wait on a review from me (new, or asked again) and that I have not yet seen on the Reviews page
  const waiting = $derived(badgeCount(live.snap?.reviews, seen.map))

  // PRs that wait on me: a saved Slack draft to post, or a failed one to copy by hand
  const ready = $derived(readyCount(live.snap?.myPrs))

  const base = 'padding: 10px 18px; min-height: 44px; box-sizing: border-box; display: flex; align-items: center; border-radius: var(--radius); font-size: 12px; letter-spacing: var(--tracking); text-decoration: none'
  const idle = `${base}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); color: var(--mu)`
  const on = `${base}; border: 1px var(--border-style) color-mix(in oklch, var(--ac) 55%, transparent); background: color-mix(in oklch, var(--ac) 10%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); box-shadow: 0 0 18px color-mix(in oklch, var(--ac) calc(18% * var(--glow)), transparent)`
</script>

<nav aria-label="Screens" style="display: flex; flex-wrap: wrap; gap: 8px; flex: 1 1 300px">
  <a class="tabc cap" href={href.sessions()} aria-current={active === 'sessions' ? 'page' : undefined} style={active === 'sessions' ? on : idle}>Sessions</a>
  {#if live.snap?.openWork}<a class="tabc cap" href={href.work()} aria-current={active === 'work' ? 'page' : undefined} style={active === 'work' ? on : idle}>Open work</a>{/if}
  <a class="tabc" href={href.reviews()} aria-current={active === 'reviews' ? 'page' : undefined} style="{active === 'reviews' ? on : idle}; gap: 8px"><span class="cap">Reviews</span>{#if waiting > 0}<span title="{waiting} {waiting === 1 ? 'PR waits' : 'PRs wait'} for your review" aria-label="{waiting} {waiting === 1 ? 'PR waits' : 'PRs wait'} for your review" style="padding: 1px 7px; border-radius: calc(var(--radius) * 2.25); background: color-mix(in oklch, var(--role-lead) 18%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 50%, transparent); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.2857)">{waiting}</span>{/if}</a>
  <a class="tabc" href={href.myprs()} aria-current={active === 'myprs' ? 'page' : undefined} style="{active === 'myprs' ? on : idle}; gap: 8px"><span class="cap">My PRs</span>{#if ready > 0}<span title="{ready} {ready === 1 ? 'PR needs' : 'PRs need'} you: a Slack draft to post or copy" aria-label="{ready} {ready === 1 ? 'PR needs' : 'PRs need'} you: a Slack draft to post or copy" style="padding: 1px 7px; border-radius: calc(var(--radius) * 2.25); background: color-mix(in oklch, var(--ok) 16%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--ok) 50%, transparent); color: color-mix(in oklab, var(--ok) 50%, var(--bright)); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.2857)">{ready}</span>{/if}</a>
  <a class="tabc" href={href.history()} aria-current={active === 'history' ? 'page' : undefined} style="{active === 'history' ? on : idle}; gap: 8px"><span class="cap">History</span>{#if unavailable}<span title="HISTORY UNAVAILABLE: {unavailable}" aria-label="History unavailable: {unavailable}" style="color: color-mix(in oklab, var(--bad) 70%, var(--bright))" class="cap">⚠ Unavailable</span>{:else if failed > 0}<span title="{failed} {failed === 1 ? 'session' : 'sessions'} could not be saved to History" aria-label="{failed} {failed === 1 ? 'session' : 'sessions'} could not be saved to History" style="color: var(--warn)">⚠ {failed}</span>{/if}</a>
  <a class="tabc cap" href={href.settings()} aria-current={active === 'settings' ? 'page' : undefined} style={active === 'settings' ? on : idle}>Settings</a>
  {#if alerts.supported}
    <button
      type="button"
      onclick={toggleAlerts}
      disabled={alerts.blocked}
      aria-pressed={alerts.enabled}
      title={alerts.blocked ? 'Notifications are blocked for this site in the browser settings' : 'Pop up a notification when a session needs input or finishes while this tab is in the background'}
      style="{base}; margin-left: auto; font: inherit; font-size: 12px; cursor: {alerts.blocked ? 'not-allowed' : 'pointer'}; background: {alerts.enabled ? 'color-mix(in oklch, var(--warn) 10%, transparent)' : 'transparent'}; border: 1px var(--border-style) {alerts.enabled ? 'color-mix(in oklch, var(--warn) 50%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; color: {alerts.enabled ? 'color-mix(in oklab, var(--warn) 60%, var(--bright))' : 'var(--mu)'}; opacity: {alerts.blocked ? 0.5 : 1}"
     class="cap">{alerts.blocked ? 'Alerts blocked' : alerts.enabled ? '🔔 Alerts on' : 'Alerts off'}</button>
  {/if}
  <ThemeMenu />
</nav>
