<script lang="ts">
  import { stopSession } from './live.svelte.ts'

  let { id, name, busy, compact = false }: { id: string; name: string; busy: boolean; compact?: boolean } = $props()

  let step = $state<'idle' | 'confirm' | 'working' | 'error'>('idle')
  let error = $state('')

  async function go() {
    step = 'working'
    const r = await stopSession(id, busy)
    if (r.ok) step = 'idle'
    else {
      error = r.busy ? 'Session started working; try again.' : (r.error ?? 'failed')
      step = 'error'
    }
  }

  const btn =
    'font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 4px 10px; border-radius: calc(var(--radius) * 0.75); cursor: pointer; background: transparent;'
</script>

<span style="display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap">
  {#if step === 'idle'}
    <button type="button" onclick={() => (step = 'confirm')} style="{btn} color: color-mix(in oklab, var(--bad) 70%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent)" class="cap">{compact ? 'Stop' : 'Stop session'}</button>
  {:else if step === 'confirm'}
    <span role="alert" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.4286); color: {busy ? 'color-mix(in oklab, var(--bad) 55%, var(--bright))' : 'var(--tx)'}">
      {busy ? `${name} is WORKING. Stopping it ends the run and loses any unsaved work.` : `Stop ${name}?`}
    </span>
    <button type="button" onclick={go} style="{btn} color: var(--onac); background: var(--bad); border: 1px var(--border-style) var(--bad); font-weight: 700" class="cap">{busy ? 'Stop anyway' : 'Stop'}</button>
    <button type="button" onclick={() => (step = 'idle')} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">Cancel</button>
  {:else if step === 'working'}
    <span style="font-size: 11px; color: var(--mu)" class="cap">Stopping…</span>
  {:else}
    <span role="alert" style="font-size: 11px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{error}</span>
    <button type="button" onclick={() => (step = 'idle')} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">OK</button>
  {/if}
</span>
