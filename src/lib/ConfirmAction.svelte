<script lang="ts">
  import { tick } from 'svelte'
  import { adrift, firstFocus } from './confirmFocus.ts'
  import type { Tone } from './confirmFocus.ts'
  import type { ActionResult } from './live.svelte.ts'

  let {
    label,
    confirmText,
    confirmLabel,
    tone,
    run,
  }: { label: string; confirmText: string; confirmLabel: string; tone: Tone; run: () => Promise<ActionResult> } = $props()

  const T: Record<Tone, { line: string; fg: string; solid: string; ink: string }> = {
    red: { line: 'color-mix(in oklch, var(--bad) 40%, transparent)', fg: 'color-mix(in oklab, var(--bad) 70%, var(--bright))', solid: 'var(--bad)', ink: 'var(--onac)' },
    green: { line: 'color-mix(in oklch, var(--ok) 45%, transparent)', fg: 'color-mix(in oklab, var(--ok) 50%, var(--bright))', solid: 'var(--ok)', ink: 'var(--onac)' },
    cyan: { line: 'color-mix(in oklch, var(--ac) 50%, transparent)', fg: 'color-mix(in oklab, var(--ac) 60%, var(--bright))', solid: 'var(--ac)', ink: 'var(--onac)' },
  }
  const t = $derived(T[tone])

  type Step = 'idle' | 'confirm' | 'working' | 'error'
  let step = $state<Step>('idle')
  let error = $state('')
  let root = $state<HTMLSpanElement>()
  let askBtn = $state<HTMLButtonElement>()
  let confirmBtn = $state<HTMLButtonElement>()
  let cancelBtn = $state<HTMLButtonElement>()
  let okBtn = $state<HTMLButtonElement>()
  let statusEl = $state<HTMLSpanElement>()

  // Each step swaps the buttons and removes the one that had focus, which would drop it to the page: the next control takes it.
  // After a run it does so only if focus is still adrift, so a page that moved it itself (a card that replaced this one) keeps its choice.
  async function move(next: Step, to: () => HTMLElement | undefined, onlyIfAdrift = false) {
    step = next
    await tick()
    if (onlyIfAdrift && !adrift(document.activeElement, document.body, root)) return
    to()?.focus()
  }

  function ask() {
    void move('confirm', () => (firstFocus(tone) === 'cancel' ? cancelBtn : confirmBtn))
  }

  function back() {
    void move('idle', () => askBtn)
  }

  async function go() {
    // the status line is in the document once the block has rendered, hence the tick; the action starts first
    step = 'working'
    const started = run()
    await tick()
    statusEl?.focus()
    const r = await started
    if (r.ok) await move('idle', () => askBtn, true)
    else {
      error = r.error ?? 'failed'
      await move('error', () => okBtn, true)
    }
  }

  const btn =
    'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); cursor: pointer; background: transparent;'
</script>

<span bind:this={root} style="display: inline-flex; align-items: center; gap: 10px; flex-wrap: wrap">
  {#if step === 'idle'}
    <button type="button" bind:this={askBtn} onclick={ask} style="{btn} color: {t.fg}; border: 1px var(--border-style) {t.line}">{label}</button>
  {:else if step === 'confirm'}
    <span role="alert" style="font-size: 12px; letter-spacing: calc(var(--tracking) * 0.2857); color: var(--tx)">{confirmText}</span>
    <button type="button" bind:this={confirmBtn} onclick={go} style="{btn} color: {t.ink}; background: {t.solid}; border: 1px var(--border-style) {t.solid}; font-weight: 700">{confirmLabel}</button>
    <button type="button" bind:this={cancelBtn} onclick={back} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">Cancel</button>
  {:else if step === 'working'}
    <span role="status" tabindex="-1" bind:this={statusEl} style="font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714); text-transform: var(--case); color: var(--mu); outline: none" class="cap">Working…</span>
  {:else}
    <span role="alert" style="font-size: 12px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{error}</span>
    <button type="button" bind:this={okBtn} onclick={back} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">OK</button>
  {/if}
</span>
