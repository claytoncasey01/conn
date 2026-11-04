<script lang="ts">
  import { live, resumeEntry, summarizeEntry } from '../lib/live.svelte.ts'
  import type { EntryActionResult } from '../lib/entryAction.ts'
  import { COST_NOTE, dur, modelName, usd, whenLabel } from '../lib/format.ts'
  import { kindLabel } from '../lib/kindLabel.ts'
  import type { SessionSource } from '../lib/source.svelte.ts'

  /** The top of a History entry's Overview: what the session was, its At a Glance summary, and the two things to do with it. */
  let { source }: { source: SessionSource } = $props()

  const entry = $derived(source.entry())
  const row = $derived(entry?.row)
  const summary = $derived(entry?.summary)
  const sumState = $derived(summary?.state)
  const generating = $derived(sumState === 'queued' || sumState === 'running')
  const transcriptGone = $derived(entry ? entry.detail.archived?.transcriptAvailable !== true : false)

  // The server starts a summary when an entry with none is opened, so there is nothing to POST on open. While it queues
  // or runs, look again every 3 s; for an entry that reads 'none' look once more, since the request that started it has
  // already answered. Re-run (and so stop the old timer) when the entry or its state changes, and on unmount.
  $effect(() => {
    const src = source
    const st = sumState
    if (st === 'queued' || st === 'running') {
      const h = setInterval(() => void src.refresh(), 3000)
      return () => clearInterval(h)
    }
    if (st === 'none') {
      const h = setTimeout(() => void src.refresh(), 1500)
      return () => clearTimeout(h)
    }
  })

  // What was asked for ('retry': the server's default model), while the request is out
  let asking = $state<'haiku' | 'sonnet' | 'retry' | null>(null)
  let summaryNote = $state('')
  async function regenerate(model?: 'haiku' | 'sonnet') {
    if (asking) return
    const src = source
    asking = model ?? 'retry'
    summaryNote = ''
    const r = await summarizeEntry(src.id, model)
    if (src.id === source.id) {
      asking = null
      if (!r.ok) summaryNote = r.error ?? 'The summary could not be started.'
      else if (r.accepted === false) summaryNote = 'A summary is already being generated for this session.'
    }
    await src.refresh()
  }

  let resuming = $state(false)
  let resumed = $state<EntryActionResult | null>(null)
  let copied = $state(false)
  async function resume() {
    if (resuming) return
    const src = source
    resuming = true
    resumed = null
    copied = false
    const r = await resumeEntry(src.id)
    if (src.id === source.id) {
      resumed = r
      resuming = false
    }
  }
  // The entry changed under this component: drop what was said about the last one
  $effect(() => {
    source.id
    asking = null
    summaryNote = ''
    resuming = false
    resumed = null
    copied = false
    copyFailed = false
  })

  let copyFailed = $state(false)
  async function copy(text: string, from: HTMLElement | null) {
    copyFailed = false
    try {
      await navigator.clipboard.writeText(text)
      copied = true
    } catch {
      // No clipboard access (an insecure origin, or the browser said no): leave the command selected to copy by hand
      copied = false
      copyFailed = true
      if (from) getSelection()?.selectAllChildren(from)
    }
  }

  const pill = 'padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); white-space: nowrap'
  const btn = 'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); background: transparent'
</script>

{#if entry && row}
  <section aria-label="History entry" class="surf brk" style="--c: var(--role-advisor); position: relative; display: flex; flex-direction: column; gap: 16px; padding: 18px 22px; --rim: color-mix(in oklch, var(--role-advisor) 30%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--role-advisor) 10%, transparent), color-mix(in oklch, var(--panel) 90%, transparent) 60%); backdrop-filter: blur(6px)">
    <div class="stripe" style="position: absolute; left: 0; top: 0; width: 100%; height: 2px; background: linear-gradient(90deg, var(--role-advisor), transparent); box-shadow: 0 0 12px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)"></div>

    <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 10px">
      <span style="{pill}; border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 50%, transparent); background: color-mix(in oklch, var(--role-advisor) 12%, transparent); color: color-mix(in oklab, var(--role-advisor) 30%, var(--bright))" class="cap">Archived</span>
      <span title={row.source === 'live' ? 'Captured when this server saw the session end' : 'Replayed from a transcript found on disk later'} style="{pill}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: var(--mu)" class="cap">{row.source === 'live' ? 'Captured live' : 'Backfilled'}</span>
      <span class="cap" style="{pill}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: var(--mu)">{kindLabel(row.kind)}</span>
      <span style="{pill}; border: 1px var(--border-style) {row.outcome === 'merged' ? 'color-mix(in oklch, var(--ok) 45%, transparent)' : row.outcome === 'PR opened' ? 'color-mix(in oklch, var(--ac) 45%, transparent)' : 'color-mix(in oklch, var(--edge) 22%, transparent)'}; color: {row.outcome === 'merged' ? 'color-mix(in oklab, var(--ok) 50%, var(--bright))' : row.outcome === 'PR opened' ? 'color-mix(in oklab, var(--ac) 60%, var(--bright))' : 'var(--mu)'}">{row.outcome.toUpperCase()}</span>
      {#if row.prNumber !== null}<span style="{pill}; border: 1px var(--border-style) color-mix(in oklch, var(--ac) 45%, transparent); color: color-mix(in oklab, var(--ac) 60%, var(--bright))">PR #{row.prNumber}</span>{/if}
      {#if row.ticketKey && live.snap?.openWork}<span style="{pill}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: color-mix(in oklab, var(--edge) 45%, var(--bright))">{row.ticketKey}</span>{/if}
    </div>

    <div data-content class="hud" title={row.name} style="font-weight: 700; font-size: 28px; line-height: 1.15; letter-spacing: calc(var(--tracking) * 0.2143); color: var(--bright); overflow-wrap: anywhere; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 3; line-clamp: 3; overflow: hidden">{row.name || '(unnamed)'}</div>

    <div style="display: flex; flex-wrap: wrap; gap: 6px 22px; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286); color: var(--mu)">
      <span>{whenLabel(row.startedAt, Date.now())} → {whenLabel(row.endedAt, Date.now())}</span>
      <span>DURATION <span style="color: color-mix(in oklab, var(--tx) 60%, var(--bright))">{dur(row.endedAt - row.startedAt)}</span></span>
      <span title={COST_NOTE}>COST <span style="color: color-mix(in oklab, var(--tx) 60%, var(--bright))">~{usd(row.costUsd)}</span></span>
      <span><span class="cap">Agents</span> <span style="color: color-mix(in oklab, var(--tx) 60%, var(--bright))">{row.agents}</span></span>
      {#if row.model}<span>{modelName(row.model)}</span>{/if}
      {#if row.branch}<span style="overflow-wrap: anywhere">⎇ {row.branch}</span>{/if}
    </div>

    <!-- At a Glance -->
    <div role="region" aria-label="At a Glance" style="display: flex; flex-direction: column; gap: 10px; padding: 14px 16px; border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 22%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--sunk) 28%, transparent)">
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px">
        <span class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--role-advisor) 55%, var(--mu))">At a glance</span>
        {#if generating}
          <span role="status" style="display: inline-flex; align-items: center; gap: 8px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--role-advisor) 35%, var(--tx))"><span class="led blink" style="width: 7px; height: 7px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)"></span>{sumState === 'queued' ? 'QUEUED' : 'GENERATING'}…</span>
        {:else if sumState === 'ready' && summary}
          <span style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">written by {modelName(summary.model)} · {whenLabel(summary.at, Date.now())}</span>
        {/if}
      </div>

      {#if sumState === 'failed'}
        <div role="alert" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">The summary could not be written{summary?.error ? `: ${summary.error}` : '.'}</div>
        <div><button type="button" class="tab cap" onclick={() => regenerate()} disabled={asking !== null} style="{btn}; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); cursor: {asking ? 'wait' : 'pointer'}">{asking === 'retry' ? 'Asking…' : 'Retry'}</button></div>
      {/if}
      {#if summary?.text}
        <!-- Model output taken from a transcript: plain text, never markup -->
        <div style="white-space: pre-wrap; overflow-wrap: anywhere; font-size: 13px; line-height: 1.55; color: {generating || sumState === 'failed' ? 'var(--mu)' : 'var(--tx)'}">{summary.text}</div>
      {:else if !generating && sumState !== 'failed'}
        <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 75%, var(--panel)); font-style: italic">{sumState === 'none' ? 'No summary yet.' : 'The summary is empty.'}</div>
      {/if}

      <div role="group" aria-label="Regenerate the summary" style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">
        <span style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Regenerate</span>
        {#each ['haiku', 'sonnet'] as const as m (m)}
          <button type="button" class="tab" onclick={() => regenerate(m)} disabled={generating || asking !== null} style="{btn}; color: {generating || asking ? 'color-mix(in oklab, var(--mu) 70%, var(--bg))' : 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))'}; border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 35%, transparent); cursor: {generating || asking ? 'not-allowed' : 'pointer'}">{asking === m ? 'ASKING…' : m.toUpperCase()}</button>
        {/each}
        {#if summaryNote}<span role="status" style="font-size: 12px; color: var(--warn); overflow-wrap: anywhere">{summaryNote}</span>{/if}
      </div>
    </div>

    <!-- Resume -->
    <div style="display: flex; flex-direction: column; gap: 10px">
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px">
        <button
          type="button"
          class="tab cap"
          onclick={resume}
          disabled={transcriptGone || resuming}
          aria-describedby={transcriptGone ? 'resume-why' : undefined}
          style="{btn}; padding: 8px 20px; color: {transcriptGone ? 'color-mix(in oklab, var(--mu) 70%, var(--bg))' : 'color-mix(in oklab, var(--ok) 60%, var(--bright))'}; border: 1px var(--border-style) {transcriptGone ? 'color-mix(in oklch, var(--edge) 14%, transparent)' : 'color-mix(in oklch, var(--ok) 50%, transparent)'}; cursor: {transcriptGone ? 'not-allowed' : resuming ? 'wait' : 'pointer'}"
        >{resuming ? 'Opening…' : 'Resume'}</button>
        {#if transcriptGone}
          <span id="resume-why" style="font-size: 12px; color: color-mix(in oklab, var(--role-lead) 50%, var(--mu))">The transcript is gone, so this conversation cannot be resumed.</span>
        {:else}
          <span style="font-size: 12px; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Opens this conversation again in a new cmux workspace.</span>
        {/if}
      </div>

      {#if resumed}
        {#if !resumed.ok}
          <div role="alert" style="display: flex; flex-direction: column; gap: 8px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--bad) 7%, transparent)">
            <div style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{resumed.error}</div>
            {#if resumed.manualCommand}
              <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">Run this in a terminal instead:</div>
              <pre id="manual-command" style="margin: 0; padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 40%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 20%, transparent); font: inherit; font-size: 12px; line-height: 1.5; color: var(--tx); white-space: pre-wrap; overflow-wrap: anywhere; user-select: all">{resumed.manualCommand}</pre>
              <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px">
                <button type="button" class="tab cap" onclick={() => copy(resumed!.manualCommand!, document.getElementById('manual-command'))} style="{btn}; color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); cursor: pointer">{copied ? 'Copied ✓' : 'Copy'}</button>
                {#if copyFailed}<span role="status" style="font-size: 12px; color: var(--warn)">The browser would not copy it. The command is selected: press Ctrl+C or Cmd+C.</span>{/if}
              </div>
            {/if}
          </div>
        {:else if resumed.dry}
          <div role="status" style="display: flex; flex-direction: column; gap: 8px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--warn) 40%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--warn) 6%, transparent)">
            <div style="font-size: 12px; color: color-mix(in oklab, var(--warn) 60%, var(--bright))">DRY RUN · nothing was opened. It would have run:</div>
            {#if resumed.command}<pre style="margin: 0; padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 40%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 20%, transparent); font: inherit; font-size: 12px; line-height: 1.5; color: var(--tx); white-space: pre-wrap; overflow-wrap: anywhere; user-select: all">{resumed.command}</pre>{/if}
          </div>
        {:else}
          <div role="status" style="font-size: 12px; color: color-mix(in oklab, var(--ok) 50%, var(--bright))">Opened in a new cmux workspace. It shows up under SESSIONS in a few seconds.</div>
        {/if}
      {/if}
    </div>
  </section>
{/if}
