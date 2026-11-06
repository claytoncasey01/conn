<script lang="ts">
  // Interrupt: sends Ctrl-C to a hosted program (interrupt.ts), from a press only. Says what happened, briefly.
  import { INTERRUPT_HELP, interruptSession } from './interrupt.ts'

  let { id, compact = false }: { id: string; compact?: boolean } = $props()

  let step = $state<'idle' | 'working' | 'sent' | 'error'>('idle')
  let error = $state('')

  async function press(event: MouseEvent) {
    if (!event.isTrusted || step === 'working') return
    step = 'working'
    const r = await interruptSession(id)
    if (r.ok) step = 'sent'
    else {
      error = r.error
      step = 'error'
    }
  }

  const btn =
    'font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 4px 10px; border-radius: calc(var(--radius) * 0.75); cursor: pointer; background: transparent; color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--warn) 45%, transparent);'
</script>

<span style="display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap">
  <button type="button" class="cap" onclick={press} title={INTERRUPT_HELP} aria-label="Interrupt: {INTERRUPT_HELP}" aria-busy={step === 'working'} style="{btn} min-height: {compact ? '28px' : '44px'}">Interrupt</button>
  {#if step === 'sent'}
    <span role="status" style="font-size: 11px; color: var(--mu)">Ctrl-C sent</span>
  {:else if step === 'error'}
    <span role="alert" style="font-size: 11px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{error}</span>
  {/if}
</span>
