<script lang="ts">
  import type { SessionSource } from './source.svelte.ts'

  /** What an archived page shows until its entry is there: loading, not in History, or why it could not be read. */
  let { source }: { source: SessionSource } = $props()

  const phase = $derived(source.state())
</script>

<section aria-label="History entry" role="status" class="brk" style="--c: {phase === 'loading' ? 'color-mix(in oklch, var(--edge) 60%, transparent)' : 'color-mix(in oklch, var(--role-lead) 60%, transparent)'}; padding: 28px 40px; border: 1px dashed {phase === 'loading' ? 'color-mix(in oklch, var(--edge) 30%, transparent)' : 'color-mix(in oklch, var(--role-lead) 35%, transparent)'}; border-radius: calc(var(--radius) * 1.5); text-align: center; font-size: 12px; line-height: 1.6; letter-spacing: calc(var(--tracking) * 0.7143); color: {phase === 'loading' ? 'var(--mu)' : 'color-mix(in oklab, var(--role-lead) 50%, var(--mu))'}; background: color-mix(in oklch, var(--panel) 80%, transparent)">
  {#if phase === 'loading'}
    <div class="hud" style="font-size: 18px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--edge) 45%, var(--bright))">LOADING<span class="blink">…</span></div>
    <div style="margin-top: 6px">Reading this session from History.</div>
  {:else if phase === 'error'}
    <div class="hud cap" style="font-size: 18px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">History unavailable</div>
    <div style="margin-top: 6px; overflow-wrap: anywhere">The entry could not be read: {source.error()}</div>
  {:else}
    <div class="hud cap" style="font-size: 18px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">Not in history</div>
    <div style="margin-top: 6px">This session is not in History.</div>
  {/if}
  {#if phase !== 'loading'}
    <a class="tab" href={source.href.back()} style="display: inline-flex; align-items: center; justify-content: center; margin-top: 16px; padding: 8px 18px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 50%, transparent); background: color-mix(in oklch, var(--edge) 12%, transparent); color: var(--tx); font-size: 12px; letter-spacing: var(--tracking); text-decoration: none"><span class="cap"><span class="g-back" aria-hidden="true"></span> History</span></a>
  {/if}
</section>
