<script lang="ts">
  import { agentDot, agentName, splitSwitcher } from './format.ts'
  import { bootIn, bootOut } from './fx.ts'
  import type { SessionSource } from './source.svelte.ts'
  import type { Agent } from './types.ts'

  /**
   * The row of chips that moves between a session's agents and its advisor. With many agents it keeps the lead and
   * whatever is still going in view and folds the finished ones (and the long-stalled ones, marked) behind one toggle,
   * the latest finish first. The agent being looked at stays in the list as well, so picking one keeps the list open,
   * in the same order, with focus on the row that was picked.
   */
  let { source, roster, labels, selected, advisor = false, waiting = new Set<string>() }: { source: SessionSource; roster: Agent[]; labels: Map<string, string>; selected: string | null; advisor?: boolean; waiting?: ReadonlySet<string> } = $props()

  const WAIT = 'var(--warn)'
  const isLead = (a: Agent) => a.id === 'lead'

  const split = $derived(splitSwitcher(roster, selected, source.now(), source.archived, waiting))
  const done = $derived(split.finished.filter(a => a.status === 'done').length)
  const stalled = $derived(split.finished.length - done)
  let open = $state(false)
</script>

{#snippet chip(a: Agent, compact = false)}
  {@const sel = a.id === selected}
  {@const l = isLead(a)}
  {@const w = waiting.has(a.id)}
  <a class="tab" in:bootIn out:bootOut href={source.href.agent(a.id)} aria-current={sel ? 'page' : undefined} style="text-decoration: none; display: flex; align-items: center; gap: 9px; padding: {compact ? '5px 12px' : '8px 16px'}; min-height: {compact ? 34 : 44}px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) {sel ? (l ? 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' : 'color-mix(in oklab, var(--role-agent) 45%, var(--bright))') : l ? 'color-mix(in oklch, var(--role-lead) 40%, transparent)' : 'color-mix(in oklch, var(--role-agent) 22%, transparent)'}; background: {sel ? (l ? 'color-mix(in oklch, var(--role-lead) 16%, transparent)' : 'color-mix(in oklch, var(--role-agent) 16%, transparent)') : l ? 'color-mix(in oklch, var(--role-lead) 5%, transparent)' : 'transparent'}; color: {sel ? (l ? 'color-mix(in oklab, var(--role-lead) 30%, var(--bright))' : 'color-mix(in oklab, var(--tx) 50%, var(--bright))') : l ? 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))' : 'var(--mu)'}; font-size: 13px"><span class="led" style="width: 8px; height: 8px; background: {w ? WAIT : agentDot(a, source.archived)}; box-shadow: 0 0 8px color-mix(in oklch, {w ? WAIT : agentDot(a, source.archived)} calc(100% * var(--glow)), transparent)"></span>{l ? 'LEAD' : `${labels.get(a.id) ?? ''} · ${agentName(a)}`}{#if w}<span style="padding: 1px 6px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--warn) 50%, transparent); background: color-mix(in oklch, var(--warn) 12%, transparent); color: {WAIT}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)" class="cap">Needs input</span>{/if}{#if compact && a.status === 'stalled'}<span style="margin-left: auto; padding-left: 12px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--role-lead)" class="cap">Stalled</span>{/if}</a>
{/snippet}

<div role="group" aria-label="Switch agent" style="display: flex; flex-wrap: wrap; gap: 8px; max-width: 100%">
  {#each split.shown as a (a.id)}
    {@render chip(a)}
  {/each}
  <a class="tab" href={source.href.advisor()} aria-current={advisor ? 'page' : undefined} style="text-decoration: none; display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) {advisor ? 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))' : 'color-mix(in oklch, var(--role-advisor) 40%, transparent)'}; background: {advisor ? 'color-mix(in oklch, var(--role-advisor) 16%, transparent)' : 'color-mix(in oklch, var(--role-advisor) 5%, transparent)'}; color: {advisor ? 'color-mix(in oklab, var(--role-advisor) 30%, var(--bright))' : 'var(--role-advisor)'}; font-size: 13px"><span class="led" style="width: 8px; height: 8px; background: var(--role-advisor); box-shadow: 0 0 8px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)"></span>ADVISOR</a>
  {#if split.finished.length}
    <button type="button" class="tab" onclick={() => (open = !open)} aria-expanded={open} aria-controls="finished-agents" style="font: inherit; font-size: 13px; cursor: pointer; display: flex; align-items: center; gap: 9px; padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px dashed color-mix(in oklch, var(--edge) 30%, transparent); background: transparent; color: color-mix(in oklab, var(--mu) 85%, var(--panel)); letter-spacing: calc(var(--tracking) * 0.5714)"><span aria-hidden="true">{open ? '▾' : '▸'}</span>{[done && `DONE · ${done}`, stalled && `STALLED · ${stalled}`].filter(Boolean).join(' / ')}</button>
  {/if}
</div>
{#if open && split.finished.length}
  <!-- one compact row per agent in a box that scrolls, so ninety finished agents are a short list, not a wall of chips; a stalled one says so -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div id="finished-agents" role="group" tabindex="0" aria-label="Done and stalled agents, latest first" style="display: flex; flex-direction: column; gap: 4px; max-width: 100%; max-height: 260px; overflow-y: auto; margin-top: 8px; padding: 6px; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); border-radius: var(--radius)">
    {#each split.finished as a (a.id)}
      {@render chip(a, true)}
    {/each}
  </div>
{/if}
