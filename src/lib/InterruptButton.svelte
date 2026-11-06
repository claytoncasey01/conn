<script lang="ts">
  // Interrupt: sends Ctrl-C to a hosted program (interrupt.ts), from a press only. Says what happened, briefly. Once Ctrl-C
  // is sent it rests for the server's repeat window, so a second press cannot look like it did anything.
  import { INTERRUPT_HELP, INTERRUPT_REPEAT_MS, interruptSession } from './interrupt.ts'

  let { id, compact = false }: { id: string; compact?: boolean } = $props()

  let step = $state<'idle' | 'working' | 'sent' | 'error'>('idle')
  let error = $state('')
  let resting = $state(false)
  let restTimer: ReturnType<typeof setTimeout> | undefined

  $effect(() => () => clearTimeout(restTimer))

  async function press(event: MouseEvent) {
    if (!event.isTrusted || step === 'working' || resting) return
    step = 'working'
    const r = await interruptSession(id)
    if (r.ok) {
      step = 'sent'
      resting = true
      restTimer = setTimeout(() => (resting = false), INTERRUPT_REPEAT_MS)
    } else {
      error = r.error
      step = 'error'
    }
  }

  const btn =
    'font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 4px 10px; border-radius: calc(var(--radius) * 0.75); cursor: pointer; background: transparent; color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--warn) 45%, transparent);'
</script>

<span style="display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap">
  <button type="button" class="cap" onclick={press} title={INTERRUPT_HELP} aria-label="Interrupt: {INTERRUPT_HELP}" aria-busy={step === 'working'} disabled={resting} style="{btn} min-height: {compact ? '28px' : '44px'}; {resting ? 'cursor: default; opacity: 0.5' : ''}">Interrupt</button>
  {#if step === 'sent'}
    <span role="status" style="font-size: 11px; color: var(--mu)">Ctrl-C sent</span>
  {:else if step === 'error'}
    <span role="alert" style="font-size: 11px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{error}</span>
  {/if}
</span>
