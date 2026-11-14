<script lang="ts">
  import { COST_NOTE, modeLabel, usd } from './format.ts'
  import { href } from './router.svelte.ts'
  import { TEAMMATE_COLORS, TEAMMATE_LABEL } from './team.ts'
  import StopSession from './StopSession.svelte'
  import ReplyBox from './ReplyBox.svelte'
  import type { SessionSummary, SessionTeam } from './types.ts'

  /** archived: a History entry, which can be neither replied to nor stopped; reply false: the page answers the session itself (its NEEDS INPUT panel) */
  let { s, archived = false, reply = true }: { s: SessionSummary & { team?: SessionTeam | null }; archived?: boolean; reply?: boolean } = $props()
</script>

<footer
  class="page-foot"
  aria-label="Session status"
  style="position: sticky; bottom: 0; z-index: 6; display: flex; flex-wrap: wrap; align-items: center; gap: 8px 22px; padding: 11px 32px; background: color-mix(in oklch, var(--panel) var(--look-foot-fill, 90%), transparent); -webkit-backdrop-filter: var(--look-foot-blur, blur(14px)); backdrop-filter: var(--look-foot-blur, blur(14px)); border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714); font-family: var(--font-mono), monospace"
>
  {#if s.team}
    <span title="A teammate: its own claude, started by a lead; its cost is counted in the lead's" style="padding: 1px 6px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {TEAMMATE_COLORS.border}; background: {TEAMMATE_COLORS.bg}; color: {TEAMMATE_COLORS.fg}">{TEAMMATE_LABEL}</span>
    {#if s.team.leadId}<a href={href.overview(s.team.leadId)} style="color: {TEAMMATE_COLORS.fg}"><span class="cap">Lead <span class="g-fwd" aria-hidden="true"></span></span></a>{/if}
    <span style="color: color-mix(in oklab, var(--mu) 70%, var(--bg))">·</span>
  {/if}
  {#if s.mode !== 'default'}
    <span style="color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); text-shadow: 0 0 12px color-mix(in oklch, var(--role-lead) calc(50% * var(--glow)), transparent)">⏵⏵ {modeLabel(s.mode)}</span>
  {/if}
  {#if s.pr}
    {#if s.mode !== 'default'}<span style="color: color-mix(in oklab, var(--mu) 70%, var(--bg))">·</span>{/if}
    <a href={s.pr.url} target="_blank" rel="noreferrer" style="color: color-mix(in oklab, var(--ac) 60%, var(--bright))">PR #{s.pr.number} ↗</a>
  {/if}
  {#if s.mode !== 'default' || s.pr}<span style="color: color-mix(in oklab, var(--mu) 70%, var(--bg))">·</span>{/if}
  <span title={COST_NOTE} style="color: var(--tx)">~{usd(s.costUsd)}{s.costPartial ? '+' : ''} EST</span>
  {#if s.shells}
    <span style="color: color-mix(in oklab, var(--mu) 70%, var(--bg))">·</span>
    <span style="color: var(--tx)">{s.shells} SHELL{s.shells === 1 ? '' : 'S'}</span>
  {/if}
  <span style="flex: 1"></span>
  {#if s.branch}<span style="color: color-mix(in oklab, var(--mu) 85%, var(--panel))">⎇ {s.branch}</span>{/if}
  <!-- Keyed by session: the tab strip and notification clicks switch sessions without remounting this footer, and a
       draft typed for one session (or a stop confirmation) must not carry over to the next. -->
  {#if !archived}
    {#key s.id}
      <!-- hidden, not unmounted, while the panel answers: a draft typed here survives -->
      <div style="display: {reply ? 'contents' : 'none'}"><ReplyBox {s} /></div>
      <StopSession id={s.id} name={s.name} busy={s.status !== 'idle'} />
    {/key}
  {/if}
</footer>
