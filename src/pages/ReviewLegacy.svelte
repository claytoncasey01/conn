<script lang="ts">
  // Old review records conn could not place in a repo when Reviews moved to several repos: kept, never listed on Reviews, and offered
  // here so the person can say which repo each one was. Hidden when there are none. Every string from outside renders as text.
  import { live } from '../lib/live.svelte.ts'
  import { plainText } from '../lib/ask.ts'
  import { ago } from '../lib/format.ts'
  import { legacyChoices, legacyCountText, legacyProblem } from '../lib/reviewLegacy.ts'
  import { placeLegacyReview } from '../lib/reviewsApi.ts'

  let { allow, seen }: {
    /** The saved allowlist */
    allow: string[] | null
    /** Repos conn has seen */
    seen: string[]
  } = $props()

  const records = $derived(live.snap?.reviews?.legacy ?? [])
  const choices = $derived(legacyChoices(allow, seen))
  let drafts = $state<Record<number, string>>({})
  let busy = $state<number | null>(null)
  let errors = $state<Record<number, string>>({})
  let placed = $state<string | null>(null)

  // Every old record was about one repo (the one conn reviewed then), so they can all be placed at once
  const everyOne = -1
  async function place(n: number) {
    const repo = (drafts[n] ?? '').trim()
    const problem = legacyProblem(repo, allow)
    if (problem) {
      errors = { ...errors, [n]: problem }
      return
    }
    busy = n
    const r = await placeLegacyReview(n === everyOne ? 'all' : n, repo)
    busy = null
    if (r.ok) {
      errors = { ...errors, [n]: '' }
      placed = n === everyOne ? `The old records are now records of ${repo}` : `PR #${n} is now a record of ${repo}`
    } else errors = { ...errors, [n]: r.error ?? 'it could not be placed' }
  }
</script>

{#if records.length}
  <section class="surf card" aria-labelledby="legacy-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
    <h2 id="legacy-h" class="hud cap">Old review records</h2>
    <p class="sub">{legacyCountText(records.length)}. They are from before Reviews read several repositories, and conn could not tell which repository each was for. They are kept and not listed on Reviews. Name the repository of one to bring it back.</p>
    <datalist id="legacy-repos">
      {#each choices as c (c)}<option value={c}></option>{/each}
    </datalist>
    {#if records.length > 1}
      <div class="field">
        <label class="lab" for="legacy-all">All of them belong to</label>
        <input id="legacy-all" class="fld" type="text" list="legacy-repos" value={drafts[everyOne] ?? ''} oninput={(e) => (drafts = { ...drafts, [everyOne]: e.currentTarget.value })} placeholder="owner/repo" autocomplete="off" spellcheck="false" aria-invalid={!!errors[everyOne]} aria-describedby="legacy-err-all" />
        <button type="button" class="btn cap" onclick={() => place(everyOne)} disabled={busy !== null} aria-busy={busy === everyOne}>{#if busy === everyOne}Placing…{:else}Place all{/if}</button>
      </div>
      <p id="legacy-err-all" class="err" role="alert" data-content>{plainText(errors[everyOne] ?? '')}</p>
    {/if}
    <ul class="rows" aria-label="Old review records">
      {#each records as rec (rec.number)}
        <li class="rec">
          <div class="orow">
            <span class="pat">PR #{rec.number}</span>
            {#if rec.folder}<span class="hint">clone folder <code data-content>{plainText(rec.folder)}</code></span>{/if}
            <span class="hint">touched {ago(rec.at, live.now)}</span>
          </div>
          <div class="field">
            <label class="sr" for="legacy-{rec.number}">Repository of PR #{rec.number}</label>
            <input id="legacy-{rec.number}" class="fld" type="text" list="legacy-repos" value={drafts[rec.number] ?? ''} oninput={(e) => (drafts = { ...drafts, [rec.number]: e.currentTarget.value })} placeholder="owner/repo" autocomplete="off" spellcheck="false" aria-invalid={!!errors[rec.number]} aria-describedby="legacy-err-{rec.number}" />
            <button type="button" class="btn cap" onclick={() => place(rec.number)} disabled={busy !== null} aria-busy={busy === rec.number}>{#if busy === rec.number}Placing…{:else}Place{/if}</button>
          </div>
          <p id="legacy-err-{rec.number}" class="err" role="alert" data-content>{plainText(errors[rec.number] ?? '')}</p>
        </li>
      {/each}
    </ul>
    {#if placed}<p class="ok" role="status" data-content>{plainText(placed)}</p>{/if}
  </section>
{/if}

<style>
  /* The Settings page's card and row look, in tokens. Last on the page: the page orders its cards (Services 1, Local clones 2) */
  .card {
    order: 3;
    display: flex;
    flex-direction: column;
    gap: 10px;
    box-sizing: border-box;
    min-width: 0;
    padding: 16px 18px;
  }
  h2 {
    margin: 0 0 4px;
    font-size: 15px;
    font-weight: 600;
    color: var(--bright);
  }
  p {
    margin: 0;
  }
  code {
    font-family: var(--font-mono), monospace;
    color: var(--tx);
    overflow-wrap: anywhere;
  }
  .sub,
  .hint {
    max-width: 78ch;
    color: var(--mu);
    font-size: 12px;
    line-height: 1.55;
    overflow-wrap: anywhere;
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  .rows {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .rec {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 0;
    border-top: 1px var(--border-style) var(--line);
  }
  .rows > :first-child {
    border-top: 0;
  }
  .orow,
  .field {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 12px;
    min-width: 0;
  }
  .pat {
    color: var(--tx);
    font-size: 13px;
  }
  .lab {
    color: var(--mu);
    font-size: 12px;
  }
  .fld {
    flex: 1 1 200px;
    min-width: 0;
    min-height: 44px;
    box-sizing: border-box;
    padding: 0 12px;
    color: var(--tx);
    background: var(--sunk);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-family: var(--font-mono), monospace;
    font-size: 13px;
  }
  .fld::placeholder {
    color: var(--mu);
  }
  .fld[aria-invalid='true'] {
    border-color: var(--bad);
  }
  .btn {
    min-height: 44px;
    padding: 0 16px;
    color: var(--tx);
    background: transparent;
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-size: 12px;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) {
    background: var(--hover);
  }
  .btn:disabled {
    cursor: default;
    opacity: 0.5;
  }
  .btn[aria-busy='true'] {
    cursor: wait;
  }
  .err {
    min-height: 0;
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .err:empty {
    display: none;
  }
  .ok {
    color: color-mix(in oklab, var(--ok) 50%, var(--bright));
    font-size: 12px;
    overflow-wrap: anywhere;
  }
</style>
