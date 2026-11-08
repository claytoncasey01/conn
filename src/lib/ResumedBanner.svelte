<script lang="ts">
  import { live, stopSession } from './live.svelte.ts'
  import { bannerShown, resumedLine, resumedOf, stopAll } from './resumed.ts'

  /** Says which hosted sessions this launch resumed from the last quit, with a Stop all: they were started with no one at the keyboard. */
  const sessions = $derived(resumedOf(live.snap))

  let dismissed = $state(new Set<string>())
  let step = $state<'idle' | 'confirm' | 'working'>('idle')
  let failed = $state<string[]>([])

  async function stop() {
    step = 'working'
    failed = await stopAll(sessions, stopSession)
    step = 'idle'
  }

  function dismiss() {
    dismissed = new Set([...dismissed, ...sessions.map(s => s.id)])
    failed = []
  }

  const btn =
    'font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 4px 10px; border-radius: calc(var(--radius) * 0.75); cursor: pointer; background: transparent;'
</script>

{#if bannerShown(sessions, dismissed)}
  <div role="status" aria-label="Resumed sessions" style="display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 8px 32px; background: color-mix(in oklch, var(--role-lead) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 35%, transparent); font-size: 11px; letter-spacing: var(--tracking); color: var(--tx)">
    <span style="overflow-wrap: anywhere">{resumedLine(sessions)}</span>
    {#if step === 'idle'}
      <button type="button" onclick={() => (step = 'confirm')} style="{btn} color: color-mix(in oklab, var(--bad) 70%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent)" class="cap">Stop all</button>
      <button type="button" onclick={dismiss} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">Dismiss</button>
    {:else if step === 'confirm'}
      <span role="alert">Stop every resumed session, whatever it is doing?</span>
      <button type="button" onclick={stop} style="{btn} color: var(--onac); background: var(--bad); border: 1px var(--border-style) var(--bad); font-weight: 700" class="cap">Stop all</button>
      <button type="button" onclick={() => (step = 'idle')} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">Cancel</button>
    {:else}
      <span style="color: var(--mu)" class="cap">Stopping…</span>
    {/if}
    {#each failed as line (line)}
      <span role="alert" style="color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{line}</span>
    {/each}
  </div>
{/if}
