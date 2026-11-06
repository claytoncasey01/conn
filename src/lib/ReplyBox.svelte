<script lang="ts">
  import { replySession } from './live.svelte.ts'
  import { href } from './router.svelte.ts'
  import type { SessionSummary } from './types.ts'

  // `ask` can be missing on a row from older history
  let { s }: { s: Pick<SessionSummary, 'id' | 'name' | 'status' | 'waitingFor' | 'canReply' | 'hosted'> & { ask?: SessionSummary['ask'] } } = $props()

  /** A dialog (a question, a permission, anything not understood) is answered on the agent page, not typed into. */
  // A waiting session whose screen has not been read yet may be on a permission prompt: no REPLY for the server to refuse.
  const dialog = $derived(s.ask ? s.ask.kind !== 'text' : s.status === 'waiting')

  let open = $state(false)
  let text = $state('')
  let step = $state<'edit' | 'sending' | 'sent' | 'error'>('edit')
  let error = $state('')

  async function send(e: Event) {
    e.preventDefault()
    if (!text.trim() || step === 'sending') return
    step = 'sending'
    const r = await replySession(s.id, text)
    if (r.ok) {
      text = ''
      step = 'sent'
      setTimeout(() => step === 'sent' && ((step = 'edit'), (open = false)), 1800)
    } else {
      error = r.error ?? 'failed'
      step = 'error'
    }
  }

  const btn = 'font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 4px 10px; border-radius: calc(var(--radius) * 0.75); cursor: pointer; background: transparent;'
</script>

<!-- No terminal first: the agent page cannot answer such a session either. A hosted session has one until it ends -->
{#if !s.canReply}
  {#if s.hosted}
    <span style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.4286); color: color-mix(in oklab, var(--mu) 70%, var(--bg))" title="This session has ended, so nothing can be typed into it" class="cap">Ended</span>
  {:else}
    <span style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.4286); color: color-mix(in oklab, var(--mu) 70%, var(--bg))" title="Replies are typed into the session's cmux terminal; this one is not running in cmux" class="cap">No cmux terminal</span>
  {/if}
{:else if dialog}
  <a href={href.agent(s.id, 'lead')} style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.4286); color: var(--warn)">ANSWER ON THE AGENT PAGE{s.waitingFor ? ` · ${s.waitingFor.toUpperCase()}` : ''}</a>
{:else if !open}
  <button type="button" onclick={() => (open = true)} style="{btn} color: color-mix(in oklab, var(--edge) 55%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent)" class="cap">Reply</button>
{:else}
  <form onsubmit={send} style="display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap; flex: 1 1 320px; min-width: 0">
    <input
      bind:value={text}
      maxlength="4000"
      placeholder="Message to {s.name}…"
      aria-label="Message to {s.name}"
      disabled={step === 'sending'}
      style="flex: 1 1 200px; min-width: 0; font: inherit; font-size: 12px; padding: 5px 9px; border-radius: calc(var(--radius) * 0.75); color: color-mix(in oklab, var(--tx) 50%, var(--bright)); background: color-mix(in oklch, var(--panel) 90%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 35%, transparent)"
    />
    <button type="submit" disabled={!text.trim() || step === 'sending'} style="{btn} color: var(--onac); background: color-mix(in oklab, var(--edge) 55%, var(--bright)); border: 1px var(--border-style) color-mix(in oklab, var(--edge) 55%, var(--bright)); font-weight: 700" class="cap">{step === 'sending' ? 'Sending…' : 'Send'}</button>
    <button type="button" onclick={() => ((open = false), (step = 'edit'))} style="{btn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">Close</button>
    <!-- always there while the form is, so a screen reader says SENT once -->
    <span role="status" style="font-size: 11px; color: var(--ok)" class="cap">{step === 'sent' ? 'Sent ✓' : ''}</span>
    {#if step === 'error'}<span role="alert" style="font-size: 11px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{error}</span>{/if}
  </form>
{/if}
