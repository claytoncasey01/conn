<script lang="ts">
  // The agents and skills conn provides. Agents are linked into Claude Code's folder when the server starts; a skill is linked only
  // when INSTALL is pressed here, and REMOVE takes only conn's own link away. The state is read from the disk by the server each
  // time; this block keeps nothing but the last answer.
  import { onMount } from 'svelte'
  import { plainText } from '../lib/ask.ts'
  import { changeSkill, loadProvided, type ProvidedRow, type ProvidedView } from '../lib/providedApi.ts'

  let view = $state<ProvidedView | null>(null)
  let loadError = $state('')
  let busy = $state('')
  let problem = $state('')

  onMount(async () => {
    const result = await loadProvided()
    if (result.ok) view = result.view
    else loadError = result.error
  })

  async function press(action: 'install' | 'remove', name: string) {
    busy = name
    problem = ''
    const result = await changeSkill(action, name)
    if (result.ok) view = result.view
    else problem = result.error
    busy = ''
  }

  const word = (row: ProvidedRow) => (row.state === 'installed' ? 'Linked' : row.state === 'conflict' ? 'In the way' : 'Not linked')
</script>

{#if view}
  <section class="surf card" aria-labelledby="provided-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
    <h2 id="provided-h" class="hud cap">Agents and skills</h2>
    <p class="sub">
      conn carries a few agents and skills for Claude Code. A copy of each is kept in <code data-content>{plainText(view.copyDir)}</code>, and installing one links it from
      <code data-content>{plainText(view.linkDir)}</code>. Nothing is replaced: a file or folder of yours with the same name stays as it is.
    </p>

    <h3 class="cap">Skills</h3>
    {#if view.skills.length === 0}
      <p class="hint">This version of conn provides no skills.</p>
    {:else}
      <ul class="rows" aria-label="Provided skills">
        {#each view.skills as row (row.name)}
          <li class="row">
            <span class="tag" class:on={row.state === 'installed'} class:bad={row.state === 'conflict'}>{word(row)}</span>
            <div class="what">
              <b data-content>{plainText(row.name)}</b>
              {#if row.note}<p class="hint" data-content>{plainText(row.note)}. Move it away to install this skill.</p>{/if}
            </div>
            {#if row.state === 'installed'}
              <button type="button" class="btn small cap" onclick={() => press('remove', row.name)} disabled={!view.writable || busy !== ''} aria-busy={busy === row.name}>Remove</button>
            {:else}
              <button type="button" class="btn primary small cap" onclick={() => press('install', row.name)} disabled={!view.writable || row.state === 'conflict' || busy !== ''} aria-busy={busy === row.name}>Install</button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if !view.writable}<p class="hint">This is a dry run: nothing is linked.</p>{/if}
    {#if problem}<p class="err" role="alert" data-content>{plainText(problem)}</p>{/if}

    <h3 class="cap">Agents</h3>
    <p class="hint">Agents are linked for you when conn starts, and a link that goes missing comes back at the next start.</p>
    {#if view.agents.length}
      <ul class="rows" aria-label="Provided agents">
        {#each view.agents as row (row.name)}
          <li class="row">
            <span class="tag" class:on={row.state === 'installed'} class:bad={row.state === 'conflict'}>{word(row)}</span>
            <div class="what">
              <b data-content>{plainText(row.name)}</b>
              {#if row.note}<p class="hint" data-content>{plainText(row.note)} is at its place in Claude Code's agents folder, so conn left it alone.</p>{/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{:else if loadError}
  <p class="err" role="alert" data-content>Could not read the agents and skills: {plainText(loadError)}</p>
{/if}

<style>
  /* Tokens only, so the block reads in every theme */
  h2 {
    margin: 0 0 4px;
    font-size: 15px;
    font-weight: 600;
    color: var(--bright);
  }
  h3 {
    margin: 6px 0 0;
    font-size: 14px;
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
  .card {
    display: flex;
    flex-direction: column;
    gap: 10px;
    box-sizing: border-box;
    min-width: 0;
    padding: 16px 18px;
  }
  .sub,
  .hint {
    max-width: 78ch;
    color: var(--mu);
    font-size: 12px;
    line-height: 1.55;
    overflow-wrap: anywhere;
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
    padding: 10px 12px;
    background: color-mix(in oklch, var(--sunk) 55%, transparent);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
  }
  .what {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
    overflow-wrap: anywhere;
  }
  .tag {
    flex: none;
    box-sizing: border-box;
    min-width: 78px;
    padding: 3px 8px;
    color: var(--mu);
    border: 1px var(--border-style) var(--line2);
    border-radius: 999px;
    font-size: 11px;
    text-align: center;
  }
  .tag.on {
    color: color-mix(in oklab, var(--ok) 60%, var(--bright));
    border-color: var(--ok);
  }
  .tag.bad {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
    border-color: var(--bad);
  }
  .err {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
    font-size: 12px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  @media (max-width: 600px) {
    .row {
      flex-wrap: wrap;
    }
  }
</style>
