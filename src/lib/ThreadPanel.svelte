<script lang="ts">
  import { tick } from 'svelte'
  import { postThread } from './myPrsApi.ts'
  import { canPress, liveDraft, newerProposal, nextFocus, overLimit, panelState, REPLY_LIMIT, threadLabel, type Draft } from './threadPanel.ts'
  import type { MyPrRow, RowThread } from './types.ts'

  // Every string here came from GitHub (author, path, comment) or from the session (the proposed reply) and is only ever shown as text.
  // One thread per click: no control posts for several threads, and nothing is remembered but the box edits and the SKIPs of this page visit.
  let { row }: { row: MyPrRow } = $props()
  const uid = $props.id()

  let drafts = $state<Record<string, Draft>>({})
  let busy = $state<Record<string, boolean>>({})
  // A click that went through, until the snapshot shows its proposal as posted (or the thread gone): the button stays off meanwhile
  let sent = $state<Record<string, boolean>>({})
  let errors = $state<Record<string, string>>({})
  // SKIP only folds the thread away for this page visit; the server never hears of it
  let skipped = $state<Record<string, boolean>>({})

  const view = (t: RowThread) => panelState(t, liveDraft(t, drafts[t.id]))
  const held = (t: RowThread) => !!busy[t.id] || (!!sent[t.id] && t.proposal?.state !== 'posted')

  $effect(() => {
    const open = new Map(row.threads.map(t => [t.id, t]))
    for (const id of Object.keys(sent)) if (!open.has(id) || open.get(id)?.proposal?.state === 'posted') delete sent[id]
  })

  // Focus goes on to the next thread's box once this one's is disabled or folded away, and to the heading when none is left, never to the page
  let heading: HTMLElement | undefined = $state()
  async function focusAfter(t: RowThread) {
    await tick()
    const usable = (id: string) => {
      const x = row.threads.find(y => y.id === id)
      return !!x && !skipped[id] && !held(x) && x.proposal?.state !== 'posted'
    }
    const id = nextFocus(row.threads.map(x => x.id), t.id, usable)
    const box = id === null ? null : document.getElementById(`${uid}-${id}`)
    ;(box ?? heading)?.focus()
  }

  function skip(t: RowThread) {
    skipped[t.id] = true
    void focusAfter(t)
  }

  // The proposal the draft began on is kept while Casey types on, so a newer one stays on offer until USE IT
  function keep(t: RowThread, text: string) {
    drafts[t.id] = { base: drafts[t.id]?.base ?? t.proposal?.text ?? '', text }
  }

  async function press(t: RowThread) {
    const s = view(t)
    if (!canPress(s, held(t))) return
    // What is sent stays in the box if the click fails, whatever proposal a later round brings
    if (!s.readOnly) keep(t, s.text)
    busy[t.id] = true
    delete errors[t.id]
    const r = await postThread(row.repo, row.number, t.id, s.text)
    busy[t.id] = false
    if (r.ok) {
      sent[t.id] = true
      void focusAfter(t)
    } else errors[t.id] = r.error ?? 'the thread was not posted'
  }

  const btn = 'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius)'
  const note = 'font-size: 12px; line-height: 1.5; overflow-wrap: anywhere'
</script>

<div role="group" aria-label="Unresolved threads on PR {row.number}" style="display: flex; flex-direction: column; gap: 10px">
  <h3 bind:this={heading} tabindex="-1" style="margin: 0; font-size: 11px; font-weight: 400; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--mu)" class="cap">Threads to reply to and resolve</h3>
  {#each row.threads as t (t.id)}
    {@const s = view(t)}
    {@const shown = errors[t.id] ?? s.failed}
    {@const newer = newerProposal(t, drafts[t.id])}
    {#if skipped[t.id]}
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
        <span style="overflow-wrap: anywhere">{threadLabel(t)}: skipped for now</span>
        <button type="button" class="tb cap" onclick={() => delete skipped[t.id]} style="{btn}; font-size: 11px; padding: 4px 12px; cursor: pointer">Show</button>
      </div>
    {:else}
      <section aria-label="Thread by {t.by || 'unknown author'}" style="display: flex; flex-direction: column; gap: 8px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--sunk) 20%, transparent)">
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 4px 10px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
          <span style="overflow-wrap: anywhere">{threadLabel(t)}</span>
          {#if t.outdated}<span title="A later commit changed this line, the usual sign the finding was fixed: not counted as open, but still unresolved" style="padding: 1px 6px; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); border-radius: calc(var(--radius) * 0.75); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)" class="cap">Outdated</span>{/if}
        </div>
        <div style="{note}; color: var(--tx); white-space: pre-wrap">{t.text}</div>
        <label for="{uid}-{t.id}" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)" class="cap">
          {s.readOnly ? 'Reply posted' : 'Your reply (empty: resolve without a comment)'}
        </label>
        <textarea
          id="{uid}-{t.id}"
          rows="3"
          value={s.text}
          readonly={s.readOnly}
          disabled={held(t)}
          aria-label="Reply to the thread by {t.by || 'unknown author'} on {t.path ?? 'the PR'}"
          oninput={e => keep(t, e.currentTarget.value)}
          style="margin: 0; padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 40%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 20%, transparent); font: inherit; font-size: 12px; line-height: 1.5; color: var(--tx); resize: vertical; width: 100%; box-sizing: border-box"
        ></textarea>
        {#if newer !== null && !held(t)}
          <div role="status" style="display: flex; flex-direction: column; gap: 6px; {note}; color: color-mix(in oklab, var(--warn) 60%, var(--bright))">
            <span>A newer proposal arrived. Your text is kept; USE IT replaces it with this:</span>
            <div style="color: var(--tx); white-space: pre-wrap">{newer}</div>
            <div><button type="button" class="tb cap" onclick={() => delete drafts[t.id]} style="{btn}; font-size: 11px; padding: 4px 12px; cursor: pointer">Use it</button></div>
          </div>
        {/if}
        {#if !s.readOnly}
          <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: {overLimit(s.text) ? 'color-mix(in oklab, var(--bad) 55%, var(--bright))' : 'var(--mu)'}" role={overLimit(s.text) ? 'alert' : undefined}>{s.text.length} / {REPLY_LIMIT}{overLimit(s.text) ? ': too long to post' : ''}</div>
        {/if}
        {#if shown}<div role="alert" style="{note}; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{shown}</div>{/if}
        {#if s.button === null}
          <div role="status" style="{note}; color: color-mix(in oklab, var(--ok) 50%, var(--bright))">Posted and resolved. Waiting for GitHub to show the thread as resolved.</div>
        {:else if s.readOnly}
          <div style="{note}; color: color-mix(in oklab, var(--warn) 60%, var(--bright))">The reply is already on the thread. It will not be posted again.</div>
        {/if}
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">
          {#if s.button}
            <button type="button" class="tb tb-go" onclick={() => press(t)} disabled={!canPress(s, held(t))} style="{btn}; cursor: {held(t) ? 'wait' : 'pointer'}">{busy[t.id] ? 'SENDING…' : s.button}</button>
          {/if}
          <button type="button" class="tb cap" onclick={() => skip(t)} disabled={!!busy[t.id]} style="{btn}; cursor: pointer">Skip</button>
        </div>
      </section>
    {/if}
  {/each}
</div>

<style>
  .tb { background: transparent; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); color: var(--mu); transition: background .15s, border-color .15s, color .15s; }
  .tb:hover:not(:disabled) { background: color-mix(in oklch, var(--edge) 10%, transparent); border-color: color-mix(in oklch, var(--edge) 45%, transparent); color: var(--tx); }
  .tb:disabled { opacity: 0.5; cursor: not-allowed; }
  .tb-go { border-color: color-mix(in oklch, var(--ac) 50%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); }
  .tb-go:hover:not(:disabled) { background: color-mix(in oklch, var(--ac) 16%, transparent); border-color: color-mix(in oklch, var(--ac) 90%, transparent); color: var(--bright); }
  @media (prefers-reduced-motion: reduce) { .tb { transition: none; } }
</style>
