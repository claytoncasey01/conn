<script lang="ts">
  import { dur, whenLabel } from './format.ts'
  import { startSelf } from './myPrsApi.ts'
  import { clock, draftView, statusText } from './myPrsDraftView.ts'
  import { settleStarted, startedShows, type StartedAt } from './myPrsStarted.ts'
  import ThreadPanel from './ThreadPanel.svelte'
  import { ignoredNote } from './threadPanel.ts'
  import { safeUrl } from './reviewGroups.ts'
  import { loadModel, saveModel } from './reviewsApi.ts'
  import AdvisorPick from './AdvisorPick.svelte'
  import type { AdvisorPick as Pick } from './advisor.ts'
  import type { MyPrRow, MyPrStatus } from './types.ts'

  // Everything in `row` came from GitHub: it is only ever shown as text, and a link is used only when it is https.
  let { row, now, channel }: { row: MyPrRow; now: number; channel: string | null } = $props()

  const STATUS: Record<MyPrStatus, { fg: string; line: string }> = {
    'no-clone': { fg: 'var(--mu)', line: 'color-mix(in oklch, var(--edge) 30%, transparent)' },
    fork: { fg: 'var(--mu)', line: 'color-mix(in oklch, var(--edge) 30%, transparent)' },
    'draft-pr': { fg: 'var(--mu)', line: 'color-mix(in oklch, var(--edge) 30%, transparent)' },
    running: { fg: 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))', line: 'color-mix(in oklch, var(--role-lead) 50%, transparent)' },
    waiting: { fg: 'color-mix(in oklab, var(--warn) 60%, var(--bright))', line: 'color-mix(in oklch, var(--warn) 45%, transparent)' },
    findings: { fg: 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))', line: 'color-mix(in oklch, var(--role-advisor) 55%, transparent)' },
    ready: { fg: 'color-mix(in oklab, var(--ok) 50%, var(--bright))', line: 'color-mix(in oklch, var(--ok) 50%, transparent)' },
    'draft-failed': { fg: 'color-mix(in oklab, var(--bad) 70%, var(--bright))', line: 'color-mix(in oklch, var(--bad) 50%, transparent)' },
    idle: { fg: 'color-mix(in oklab, var(--ac) 60%, var(--bright))', line: 'color-mix(in oklch, var(--ac) 40%, transparent)' },
  }
  // A status this build does not know (a newer server) must not take the whole list down
  const tone = $derived(STATUS[row.status] ?? STATUS.idle)
  const statusLine = $derived(statusText(row))
  const dv = $derived(draftView(row))
  // Where this draft was saved (the channel it was saved in; the setting may have changed since), else the channel now; none is left out
  const draftIn = $derived(row.slackDraft?.channel ?? channel)
  const github = $derived(safeUrl(row.url))
  const ignored = $derived(ignoredNote(row.ignoredReplies))
  const uid = $props.id()
  // The replies panel: opened from the row, closed again by the same button
  let threadsOpen = $state(false)

  // The same remembered SONNET | OPUS choice as the REVIEW button on the Reviews tab
  let model = $state<'sonnet' | 'opus'>(loadModel())
  function pick(m: 'sonnet' | 'opus') {
    model = m
    saveModel(m)
  }
  // The advisor for this one round: Settings' unless picked here, and not remembered
  let advisor = $state<Pick>('')

  let busy = $state(false)
  // What the row looked like when a press started a round: the note goes once the row no longer looks so
  let started = $state<StartedAt | null>(null)
  $effect(() => {
    if (started) {
      const next = settleStarted(started, row)
      if (next !== started) started = next
    }
  })
  // The server's refusal or failure, and the command to paste when the session did not start (or opens as a command): shown as text only
  let failure = $state<{ error: string; manualCommand?: string } | null>(null)
  let copied = $state(false)
  let copyFailed = $state(false)
  async function start() {
    busy = true
    failure = null
    started = null
    copied = false
    copyFailed = false
    const at = { status: row.status, round: row.round }
    const r = await startSelf(row.repo, row.number, model, advisor)
    busy = false
    if (r.ok) started = at
    else failure = { error: r.error ?? 'the round did not start', manualCommand: r.manualCommand }
  }

  let cmd: HTMLElement | undefined = $state()
  // The failed Slack draft's text: a read-only box, selected for a hand copy when the clipboard is refused
  let draftBox: HTMLTextAreaElement | undefined = $state()
  let draftCopied = $state(false)
  let draftCopyFailed = $state(false)
  async function copyDraft(text: string) {
    draftCopyFailed = false
    try {
      await navigator.clipboard.writeText(text)
      draftCopied = true
    } catch {
      draftCopied = false
      draftCopyFailed = true
      draftBox?.select()
    }
  }
  async function copy(text: string) {
    copyFailed = false
    try {
      await navigator.clipboard.writeText(text)
      copied = true
    } catch {
      // No clipboard access (an insecure origin, or the browser said no): leave the command selected to copy by hand
      copied = false
      copyFailed = true
      if (cmd) getSelection()?.selectAllChildren(cmd)
    }
  }

  const tag = 'padding: 2px 7px; border-radius: calc(var(--radius) * 0.75); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); white-space: normal; overflow-wrap: anywhere'
  const link = 'display: inline-flex; flex-wrap: wrap; align-items: center; gap: 0 10px; min-height: 44px; box-sizing: border-box; color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none'
  const btn = 'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius)'
</script>

<article class="surf" aria-label="{row.repo} PR {row.number}" style="position: relative; display: flex; flex-direction: column; gap: 6px; box-sizing: border-box; padding: 12px 18px; --rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
  <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 0 14px">
    <span style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu); overflow-wrap: anywhere">{row.repo}</span>
    {#if github}
      <a href={github} target="_blank" rel="noopener noreferrer" aria-label="PR {row.number} on GitHub: {row.title}" style="{link}; flex: 1 1 240px; min-width: 0"><span class="hud" style="font-weight: 700; font-size: 18px; letter-spacing: calc(var(--tracking) * 0.2857)">#{row.number}</span><span style="font-size: 14px; line-height: 1.4; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); overflow-wrap: anywhere">{row.title}</span></a>
    {:else}
      <span style="flex: 1 1 240px; min-width: 0; display: inline-flex; flex-wrap: wrap; gap: 0 10px"><span class="hud" style="font-weight: 700; font-size: 18px; color: color-mix(in oklab, var(--ac) 60%, var(--bright))">#{row.number}</span><span style="font-size: 14px; line-height: 1.4; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); overflow-wrap: anywhere">{row.title}</span></span>
    {/if}
    {#if row.draft}<span style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--mu)" class="cap">Draft</span>{/if}
    <span style="{tag}; border: 1px var(--border-style) {tone.line}; color: {tone.fg}" class="cap">{statusLine}</span>
  </div>

  <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 6px 14px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
    <span title="Opened {whenLabel(row.createdAt, now)}">{dur(now - row.createdAt)} <span class="cap">old</span></span>
    <span title={row.lastCommentAt ? `Last comment ${dur(now - row.lastCommentAt)} ago` : undefined}><span class="cap">Last comment</span> {row.lastCommentAt ? whenLabel(row.lastCommentAt, now) : '—'}</span>
    <span title="Open threads started by a review bot" style="color: {row.openBot ? 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))' : 'var(--mu)'}">{row.openBot} <span class="cap">bot</span></span>
    <span title="Open threads started by anyone else" style="color: {row.openHuman ? 'color-mix(in oklab, var(--warn) 60%, var(--bright))' : 'var(--mu)'}">{row.openHuman} <span class="cap">human</span></span>
    {#if row.outdated}<span title="Unresolved threads whose line a later commit changed: not counted as open">{row.outdated} <span class="cap">outdated</span></span>{/if}
    {#if row.partial}<span title="The PR has more review threads than the newest 100 conn reads, or GitHub could not read some of its lists, so these counts may be short" style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--warn) 45%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))" class="cap">Partial</span>{/if}
    {#if row.round > 0}<span><span class="cap">Round</span> {row.round}</span>{/if}
    <span style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-left: auto">
      <span role="group" aria-label="Model for PR {row.number}" style="display: flex">
        {#each ['sonnet', 'opus'] as const as m (m)}
          <button
            type="button"
            class="rb rb-model cap"
            onclick={() => pick(m)}
            aria-pressed={model === m}
            style="{btn}; padding: 8px 12px; cursor: pointer; {m === 'sonnet' ? 'border-radius: var(--radius) 0 0 var(--radius)' : 'border-radius: 0 var(--radius) var(--radius) 0; border-left: 0'}"
          >{m.charAt(0).toUpperCase() + m.slice(1)}</button>
        {/each}
      </span>
      <AdvisorPick bind:value={advisor} label="Advisor for PR {row.number}" disabled={busy} />
      <button
        type="button"
        class="rb rb-go cap"
        onclick={start}
        disabled={busy || !row.canStart}
        title={row.reason ?? undefined}
        style="{btn}; cursor: {busy ? 'wait' : row.canStart ? 'pointer' : 'not-allowed'}; opacity: {!row.canStart && !busy ? 0.5 : 1}"
      >{busy ? 'Starting…' : row.label}</button>
    </span>
  </div>

  {#if row.threads.length > 0}
    <div>
      <button type="button" class="rb cap" onclick={() => (threadsOpen = !threadsOpen)} aria-expanded={threadsOpen} aria-controls="{uid}-threads" style="{btn}; font-size: 11px; cursor: pointer">{threadsOpen ? 'Hide' : 'Show'} {row.threads.length} unresolved {row.threads.length === 1 ? 'thread' : 'threads'}</button>
    </div>
    <!-- Hidden, not unmounted: HIDE must not drop an unsent edit, a request still out or a SKIP -->
    <div id="{uid}-threads" hidden={!threadsOpen}><ThreadPanel {row} /></div>
  {/if}
  {#if ignored}<div role="status" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--warn) 60%, var(--bright)); overflow-wrap: anywhere">{ignored}</div>{/if}
  {#if row.error}
    <div role="alert" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{row.error}</div>
  {/if}
  {#if row.slackDraft && dv.kind === 'saved'}
    <div role="status" style="font-size: 12px; color: {dv.stale ? 'var(--mu)' : 'color-mix(in oklab, var(--ok) 50%, var(--bright))'}; overflow-wrap: anywhere">draft saved{draftIn ? ` in ${draftIn}` : ''}{dv.stale ? ' (out of date)' : ''}</div>
  {:else if row.slackDraft && dv.kind === 'failed'}
    <div role="alert" style="display: flex; flex-direction: column; gap: 8px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--bad) 7%, transparent)">
      <div style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">Could not save the Slack draft{draftIn ? ` in ${draftIn}` : ''}{dv.stale ? ' (out of date)' : ''}{row.slackDraft.error ? `: ${row.slackDraft.error}` : ''}{dv.retryAt !== null ? '.' : ''}</div>
      {#if dv.retryAt !== null}<div style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--warn) 60%, var(--bright))">conn tries again at {clock(dv.retryAt)}.</div>{/if}
      {#if row.slackDraft.text}
        {#if dv.byHand}<div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">Post this yourself instead:</div>{:else}<div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">The draft's text:</div>{/if}
        <textarea bind:this={draftBox} readonly rows="3" value={row.slackDraft.text} aria-label="Slack draft text for PR {row.number}" style="margin: 0; padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 40%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 20%, transparent); font: inherit; font-size: 12px; line-height: 1.5; color: var(--tx); resize: vertical; width: 100%; box-sizing: border-box"></textarea>
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px">
          <button type="button" class="rb rb-copy cap" onclick={() => copyDraft(row.slackDraft!.text)} style="{btn}; cursor: pointer">{draftCopied ? 'Copied ✓' : 'Copy'}</button>
          {#if draftCopyFailed}<span role="status" style="font-size: 12px; color: var(--warn)">The browser would not copy it. The text is selected: press Ctrl+C or Cmd+C.</span>{/if}
        </div>
      {/if}
    </div>
  {/if}
  {#if startedShows(started, row)}
    <div role="status" style="font-size: 12px; color: color-mix(in oklab, var(--ok) 50%, var(--bright))">Started. The session shows here in a moment.</div>
  {/if}
  {#if failure}
    <div role="alert" style="display: flex; flex-direction: column; gap: 8px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--bad) 7%, transparent)">
      <div style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{failure.error}</div>
      {#if failure.manualCommand}
        <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">Run this in a terminal instead:</div>
        <pre bind:this={cmd} style="margin: 0; padding: 10px 12px; border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--sunk) 40%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 20%, transparent); font: inherit; font-size: 12px; line-height: 1.5; color: var(--tx); white-space: pre-wrap; overflow-wrap: anywhere; user-select: all">{failure.manualCommand}</pre>
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px">
          <button type="button" class="rb rb-copy cap" onclick={() => copy(failure!.manualCommand!)} style="{btn}; cursor: pointer">{copied ? 'Copied ✓' : 'Copy'}</button>
          {#if copyFailed}<span role="status" style="font-size: 12px; color: var(--warn)">The browser would not copy it. The command is selected: press Ctrl+C or Cmd+C.</span>{/if}
        </div>
      {/if}
    </div>
  {/if}
</article>

<style>
  .rb { background: transparent; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); color: var(--mu); transition: background .15s, border-color .15s, color .15s, box-shadow .15s; }
  .rb:hover:not(:disabled) { background: color-mix(in oklch, var(--edge) 10%, transparent); border-color: color-mix(in oklch, var(--edge) 45%, transparent); color: var(--tx); }
  .rb:active:not(:disabled) { background: color-mix(in oklch, var(--edge) 18%, transparent); }
  .rb-model[aria-pressed='true'] { border-color: color-mix(in oklch, var(--role-lead) 50%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 30%, var(--bright)); }
  .rb-model[aria-pressed='true']:hover { background: color-mix(in oklch, var(--role-lead) 18%, transparent); border-color: color-mix(in oklch, var(--role-lead) 80%, transparent); color: color-mix(in oklab, var(--role-lead) 15%, var(--bright)); }
  .rb-copy { border-color: color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); }
  .rb-go { border-color: color-mix(in oklch, var(--ac) 50%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); }
  .rb-go:hover:not(:disabled) { background: color-mix(in oklch, var(--ac) 16%, transparent); border-color: color-mix(in oklch, var(--ac) 90%, transparent); color: var(--bright); box-shadow: 0 0 16px color-mix(in oklch, var(--ac) calc(28% * var(--glow)), transparent); }
  .rb-go:active:not(:disabled) { background: color-mix(in oklch, var(--ac) 28%, transparent); }
  @media (prefers-reduced-motion: reduce) { .rb { transition: none; } }
</style>
