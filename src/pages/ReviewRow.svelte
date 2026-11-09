<script lang="ts">
  import { href } from '../lib/router.svelte.ts'
  import { dur } from '../lib/format.ts'
  import { safeUrl } from '../lib/reviewGroups.ts'
  import { loadModel, saveModel, startReview } from '../lib/reviewsApi.ts'
  import AdvisorPick from '../lib/AdvisorPick.svelte'
  import type { AdvisorPick as Pick } from '../lib/advisor.ts'
  import type { ReviewRow, ReviewState } from '../lib/types.ts'

  // Everything in `row` came from GitHub or Slack: it is only ever shown as text, and a link is used only when it is https.
  let { row, now, sid, offReason }: {
    row: ReviewRow
    now: number
    /** The id of the live session named in row.session, when there is one */
    sid: string | null
    /** Reviews cannot be started right now (setup, GitHub off, or GitHub was never read) */
    offReason: string | null
  } = $props()

  let model = $state<'sonnet' | 'opus'>(loadModel())
  function pick(m: 'sonnet' | 'opus') {
    model = m
    saveModel(m)
  }
  // The advisor for this one review: Settings' unless picked here, and not remembered
  let advisor = $state<Pick>('')

  let copied = $state(false)
  let copyFailed = $state(false)
  let busy = $state(false)
  let failure = $state<{ error: string; manualCommand?: string } | null>(null)
  let started = $state(false)
  async function start() {
    busy = true
    failure = null
    started = false
    copied = false
    copyFailed = false
    const r = await startReview(row.repo, row.number, model, advisor)
    busy = false
    if (r.ok) started = true
    else failure = { error: r.error ?? 'the review did not start', manualCommand: r.manualCommand }
  }

  let cmd: HTMLElement | undefined = $state()
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

  const STATE: Record<ReviewState, { label: string; fg: string; line: string }> = {
    new: { label: 'New', fg: 'color-mix(in oklab, var(--ac) 60%, var(--bright))', line: 'color-mix(in oklch, var(--ac) 50%, transparent)' },
    reviewing: { label: 'Reviewing', fg: 'color-mix(in oklab, var(--role-lead) 65%, var(--bright))', line: 'color-mix(in oklch, var(--role-lead) 50%, transparent)' },
    waiting: { label: 'Waiting', fg: 'color-mix(in oklab, var(--warn) 60%, var(--bright))', line: 'color-mix(in oklch, var(--warn) 45%, transparent)' },
    rereview: { label: 'Re-review', fg: 'color-mix(in oklab, var(--role-advisor) 50%, var(--bright))', line: 'color-mix(in oklch, var(--role-advisor) 55%, transparent)' },
    done: { label: 'Done', fg: 'color-mix(in oklab, var(--mu) 85%, var(--panel))', line: 'color-mix(in oklch, var(--edge) 18%, transparent)' },
  }
  const CHECKS = { none: { label: 'No checks', fg: 'color-mix(in oklab, var(--mu) 85%, var(--panel))' }, pending: { label: 'Checks pending', fg: 'color-mix(in oklab, var(--warn) 60%, var(--bright))' }, passing: { label: 'Checks passing', fg: 'color-mix(in oklab, var(--ok) 50%, var(--bright))' }, failing: { label: 'Checks failing', fg: 'color-mix(in oklab, var(--bad) 70%, var(--bright))' } }

  const done = $derived(row.state === 'done')
  // A state this build does not know (a newer server) must not take the whole list down
  const tone = $derived(STATE[row.state] ?? STATE.new)
  const checks = $derived(CHECKS[row.checks] ?? CHECKS.none)
  const again = $derived(row.state === 'rereview' || row.launches > 0)
  const github = $derived(safeUrl(row.url))
  const slack = $derived(safeUrl(row.slackUrl))
  const dm = $derived(safeUrl(row.dmUrl))
  const staleText = $derived(row.stale ? `${row.repo} could not be searched at the last poll, so this row may be out of date` : null)
  const blocked = $derived(offReason ?? (row.ended ? `This PR is ${row.ended}` : row.session ? 'A review session is already running' : (staleText ?? row.noClone)))

  const tag = 'padding: 2px 7px; border-radius: calc(var(--radius) * 0.75); font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); white-space: nowrap'
  const link = 'display: inline-flex; align-items: center; min-height: 44px; box-sizing: border-box; color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none; letter-spacing: calc(var(--tracking) * 0.5714)'
  // Colours and hover live in the style block: an inline background would beat the :hover rule
  const btn = 'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius)'
</script>

<article class="surf" aria-label="PR {row.repo}#{row.number}" style="position: relative; display: flex; flex-direction: column; gap: 6px; box-sizing: border-box; padding: 12px 18px; --rim: {done ? 'color-mix(in oklch, var(--edge) 10%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) calc({done ? 0.025 : 0.06} * 100%), transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
  <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 0 14px">
    <span data-content style="font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu); overflow-wrap: anywhere">{row.repo}</span>
    {#if github}
      <a href={github} target="_blank" rel="noopener noreferrer" aria-label="PR {row.repo}#{row.number} on GitHub" class="hud" style="{link}; font-weight: 700; font-size: 18px; letter-spacing: calc(var(--tracking) * 0.2857)">#{row.number}</a>
    {:else}
      <span class="hud" style="font-weight: 700; font-size: 18px; color: color-mix(in oklab, var(--ac) 60%, var(--bright))">#{row.number}</span>
    {/if}
    <span style="flex: 1 1 240px; min-width: 0; font-size: 14px; line-height: 1.4; color: {done ? 'color-mix(in oklab, var(--mu) 85%, var(--panel))' : 'color-mix(in oklab, var(--tx) 50%, var(--bright))'}; overflow-wrap: anywhere">{row.title}</span>
    <span style="{tag}; border: 1px var(--border-style) {tone.line}; color: {tone.fg}" class="cap">{tone.label}</span>
  </div>

  <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 6px 14px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)">
    <span style="overflow-wrap: anywhere">{row.author}</span>
    <span title="Opened {dur(now - row.createdAt)} ago">{dur(now - row.createdAt)} <span class="cap">old</span></span>
    <span class="cap" style="color: {checks.fg}">{checks.label}</span>
    <span>{row.baseRef}</span>
    {#if row.reAsked}<span style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--role-advisor) 50%, transparent); color: color-mix(in oklab, var(--role-advisor) 50%, var(--bright))" class="cap">Re-review requested ({row.reAsked === 'github' ? 'GitHub' : 'Slack'})</span>{/if}
    {#if row.updated}<span style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--warn) 45%, transparent); color: color-mix(in oklab, var(--warn) 60%, var(--bright))" class="cap">Updated</span>{/if}
    {#if !row.inChannel}<span style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--mu)" class="cap">Not in a channel</span>{/if}
    {#if row.ended}<span style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: var(--mu)" class="cap">{row.ended.charAt(0).toUpperCase() + row.ended.slice(1)}</span>{/if}
  </div>

  <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 0 18px; font-size: 12px">
    {#if slack}<a href={slack} target="_blank" rel="noopener noreferrer" style={link} class="cap">Slack Thread ↗</a>{/if}
    {#if dm}<a href={dm} target="_blank" rel="noopener noreferrer" aria-label="Slack DM about PR {row.repo}#{row.number}" style={link}><span style="{tag}; border: 1px var(--border-style) color-mix(in oklch, var(--ac) 50%, transparent); color: color-mix(in oklab, var(--ac) 60%, var(--bright))" class="cap">DM ↗</span></a>{/if}
    {#if row.session}
      {#if sid}
        <a href={href.overview(sid)} style="{link}"><span>{row.session.name} · <span class="cap">{row.session.status.charAt(0).toUpperCase() + row.session.status.slice(1)}</span> <span class="g-go" aria-hidden="true"></span></span></a>
      {:else}
        <span style="color: var(--mu)">{row.session.name} · <span class="cap">{row.session.status.charAt(0).toUpperCase() + row.session.status.slice(1)}</span></span>
      {/if}
    {/if}
    <span class="rb-actions" style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">
      <span role="group" aria-label="Model for PR {row.repo}#{row.number}" style="display: flex">
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
      <AdvisorPick bind:value={advisor} label="Advisor for PR {row.repo}#{row.number}" disabled={busy} />
      <button
        type="button"
        class="rb rb-go cap"
        onclick={start}
        disabled={busy || blocked !== null}
        title={blocked ?? undefined}
        style="{btn}; cursor: {busy ? 'wait' : blocked ? 'not-allowed' : 'pointer'}; opacity: {blocked && !busy ? 0.5 : 1}"
      >{busy ? 'Starting…' : again ? 'Re-review' : 'Review'}</button>
    </span>
  </div>

  {#if staleText && !row.ended}
    <div role="note" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--warn) 60%, var(--bright)); overflow-wrap: anywhere"><span data-content>{staleText}</span></div>
  {/if}
  {#if row.noClone && !row.ended}
    <div role="note" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--warn) 60%, var(--bright)); overflow-wrap: anywhere"><span data-content>{row.noClone}</span> <a href={href.settings()} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright))">Open Settings</a></div>
  {/if}
  {#if started}
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
  .rb-actions { margin-left: auto; }
  @media (max-width: 520px) { .rb-actions { margin-left: 0; } }
  @media (prefers-reduced-motion: reduce) { .rb { transition: none; } }
</style>
