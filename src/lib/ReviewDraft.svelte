<script lang="ts">
  import { safeUrl } from './reviewGroups.ts'
  import { allTicked, APPROVE_BLOCKED, approveBlocked, draftKind, firstLine, placeNote, startingVerdict, tickedAfter, verdictAfter, type Verdict } from './reviewDraft.ts'
  import { postReview } from './reviewsApi.ts'
  import type { ReviewDraft } from './types.ts'

  // The summary, the findings and every problem came from the model's file and the PR: they are only ever shown as plain text (never as markup), and each sits in a data-content element so a theme's case does not rewrite it.
  let { rowKey, draft, greyed }: {
    /** `owner/repo#number`: what POST /api/reviews/post names the PR by */
    rowKey: string
    draft: ReviewDraft
    /** Why the draft cannot be posted any more (the PR has ended), or null */
    greyed: string | null
  } = $props()

  const uid = $props.id()
  const kind = $derived(draftKind(draft))

  // null is "not touched yet": everything ticked, and Claude's verdict. The row keys this panel by the file, so a new write starts over.
  let tickedPick = $state<number[] | null>(null)
  let verdictPick = $state<Verdict | null>(null)
  const ticked = $derived(tickedPick ?? allTicked(draft))
  const blocked = $derived(approveBlocked(draft, ticked))
  const verdict = $derived(verdictAfter(draft, ticked, verdictPick ?? startingVerdict(draft)))
  const count = $derived(draft.findings.length)
  const allOn = $derived(count > 0 && ticked.length === count)
  const someOn = $derived(ticked.length > 0 && ticked.length < count)

  let opened = $state<number[]>([])
  const toggleOpen = (index: number) => (opened = opened.includes(index) ? opened.filter((o) => o !== index) : [...opened, index])

  let busy = $state(false)
  let error = $state<string | null>(null)
  let postedNow = $state<{ url: string } | null>(null)
  const postedUrl = $derived(safeUrl(postedNow?.url || draft.url))
  const inert = $derived(busy || greyed !== null)

  async function post() {
    if (busy || greyed !== null) return
    busy = true
    error = null
    const r = await postReview(rowKey, draft.launchAt, ticked, verdict)
    busy = false
    if (r.ok) postedNow = { url: r.url }
    else error = r.error
  }

  const box = 'display: flex; flex-direction: column; gap: 10px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--sunk) 22%, transparent)'
  const note = 'font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--warn) 60%, var(--bright)); overflow-wrap: anywhere'
  const small = 'font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu)'
  const btn = 'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius)'
  const rowStyle = 'display: flex; align-items: flex-start; gap: 10px; min-height: 44px; box-sizing: border-box; padding: 6px 4px'
</script>

{#snippet problems(heading: string, list: string[])}
  <div role="alert" style="display: flex; flex-direction: column; gap: 6px; padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--warn) 40%, transparent); border-radius: var(--radius)">
    <div style={note}>{heading}</div>
    <ul style="margin: 0; padding-left: 18px; display: flex; flex-direction: column; gap: 2px">
      {#each list as problem, index (index)}
        <li data-content style="font-size: 12px; line-height: 1.5; color: var(--tx); overflow-wrap: anywhere">{problem}</li>
      {/each}
    </ul>
    <div style="font-size: 12px; line-height: 1.5; color: var(--mu)">Ask the session to write .conn/review.json again.</div>
  </div>
{/snippet}

{#if kind === 'posted' || postedNow}
  <div role="status" style="display: flex; flex-wrap: wrap; align-items: center; gap: 0 14px; font-size: 12px; color: color-mix(in oklab, var(--ok) 50%, var(--bright))">
    <span class="cap">Posted</span>
    {#if postedUrl}
      <a href={postedUrl} target="_blank" rel="noopener noreferrer" class="cap" style="display: inline-flex; align-items: center; min-height: 44px; box-sizing: border-box; color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none">View the review on GitHub ↗</a>
    {/if}
  </div>
{:else if kind === 'unreadable'}
  {@render problems('The review file could not be read:', draft.problems)}
{:else}
  <section aria-label="Review draft for {rowKey}" style="{box}; opacity: {greyed ? 0.55 : 1}">
    {#if greyed}<div role="note" style={note}>{greyed}: the review can no longer be posted.</div>{/if}
    <div data-content style="font-size: 13px; line-height: 1.55; color: var(--tx); white-space: pre-wrap; overflow-wrap: anywhere">{draft.summary}</div>

    {#if draft.headMoved}
      <div role="note" style={note}>The PR has new commits since this review. It is posted against the commit that was reviewed ({draft.commit.slice(0, 7)}).</div>
    {/if}
    {#if draft.dropped > 0}
      <div role="note" style={note}>{draft.dropped} {draft.dropped === 1 ? 'finding' : 'findings'} dropped from the file.</div>
    {/if}

    {#if count > 0}
      <label style="{rowStyle}; align-items: center; cursor: {inert ? 'default' : 'pointer'}">
        <input type="checkbox" checked={allOn} indeterminate={someOn} disabled={inert} onchange={() => (tickedPick = allOn ? [] : allTicked(draft))} style="margin: 0; accent-color: var(--ac)" />
        <span class="cap" style="font-size: 12px; color: var(--tx)">Select all ({ticked.length} of {count})</span>
      </label>
      <ul style="margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 2px">
        {#each draft.findings as f, index (index)}
          {@const open = opened.includes(index)}
          {@const where = placeNote(f)}
          <li style="display: flex; align-items: flex-start; gap: 4px">
            <label style="{rowStyle}; flex: 1 1 auto; min-width: 0; cursor: {inert ? 'default' : 'pointer'}">
              <input type="checkbox" checked={ticked.includes(index)} disabled={inert} onchange={(e) => (tickedPick = tickedAfter(ticked, index, e.currentTarget.checked))} aria-label="Post {f.severity} comment on {f.path} line {f.line}" style="margin: 2px 0 0; accent-color: var(--ac)" />
              <span style="display: flex; flex-direction: column; gap: 2px; min-width: 0; flex: 1 1 auto">
                <span data-content style="font-size: 12px; color: var(--mu); overflow-wrap: anywhere"><span style="color: {f.severity === 'P0' || f.severity === 'P1' ? 'var(--bad)' : 'var(--tx)'}">{f.severity}</span> {f.path}:{f.line}</span>
                <span data-content style="font-size: 12px; line-height: 1.5; color: var(--tx); overflow-wrap: anywhere; {open ? 'white-space: pre-wrap' : 'white-space: nowrap; overflow: hidden; text-overflow: ellipsis'}">{open ? f.body : firstLine(f.body)}</span>
                {#if where}<span style={note}>{where}</span>{/if}
              </span>
            </label>
            <button type="button" class="rd rd-more cap" aria-expanded={open} aria-label="{open ? 'Show less of' : 'Show all of'} the {f.severity} comment on {f.path} line {f.line}" onclick={() => toggleOpen(index)} style="{btn}; padding: 8px 10px; cursor: pointer">{#if open}Less{:else}More{/if}</button>
          </li>
        {/each}
      </ul>
    {:else}
      <div style={small}>The review has no comments: it is the summary alone.</div>
    {/if}

    <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 18px">
      <fieldset style="display: flex; flex-wrap: wrap; align-items: center; gap: 0 14px; margin: 0; padding: 0; border: 0; min-width: 0" aria-describedby={blocked ? `${uid}-blocked` : undefined}>
        <legend class="cap" style="float: left; padding: 0; margin-right: 14px; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.5714); color: var(--mu); line-height: 44px">Verdict</legend>
        <label class="cap" title={blocked ? APPROVE_BLOCKED : undefined} style="display: inline-flex; align-items: center; gap: 6px; min-height: 44px; font-size: 12px; color: {blocked ? 'var(--mu)' : 'var(--tx)'}; opacity: {blocked ? 0.6 : 1}; cursor: {blocked || inert ? 'not-allowed' : 'pointer'}">
          <input type="radio" name="{uid}-verdict" value="approve" checked={verdict === 'approve'} disabled={blocked || inert} onchange={() => (verdictPick = 'approve')} style="margin: 0; accent-color: var(--ac)" />Approve
        </label>
        <label class="cap" style="display: inline-flex; align-items: center; gap: 6px; min-height: 44px; font-size: 12px; color: var(--tx); cursor: {inert ? 'not-allowed' : 'pointer'}">
          <input type="radio" name="{uid}-verdict" value="comment" checked={verdict === 'comment'} disabled={inert} onchange={() => (verdictPick = 'comment')} style="margin: 0; accent-color: var(--ac)" />Comment
        </label>
      </fieldset>
      <button type="button" class="rd rd-go cap" onclick={post} disabled={inert} title={greyed ?? undefined} style="{btn}; margin-left: auto; cursor: {busy ? 'wait' : greyed ? 'not-allowed' : 'pointer'}">{busy ? 'Posting…' : 'Post review'}</button>
    </div>
    {#if blocked}<div id="{uid}-blocked" style={small}>{APPROVE_BLOCKED}.</div>{/if}
    {#if error}
      <div role="alert" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere"><span data-content>{error}</span></div>
    {/if}
    {#if draft.problems.length > 0}{@render problems('The latest write of the review file could not be read, so this is the draft before it:', draft.problems)}{/if}
  </section>
{/if}

<style>
  .rd { background: transparent; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); color: var(--mu); transition: background .15s, border-color .15s, color .15s, box-shadow .15s; }
  .rd:hover:not(:disabled) { background: color-mix(in oklch, var(--edge) 10%, transparent); border-color: color-mix(in oklch, var(--edge) 45%, transparent); color: var(--tx); }
  .rd:disabled { opacity: 0.5; }
  .rd-go { border-color: color-mix(in oklch, var(--ac) 50%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); }
  .rd-go:hover:not(:disabled) { background: color-mix(in oklch, var(--ac) 16%, transparent); border-color: color-mix(in oklch, var(--ac) 90%, transparent); color: var(--bright); box-shadow: 0 0 16px color-mix(in oklch, var(--ac) calc(28% * var(--glow)), transparent); }
  .rd-go:active:not(:disabled) { background: color-mix(in oklch, var(--ac) 28%, transparent); }
  @media (prefers-reduced-motion: reduce) { .rd { transition: none; } }
</style>
