<!-- The advisor one start picks for its session. "As in Settings" sends none, so the server starts the session on the saved one; a
     resumed session keeps the advisor it started with, so only a start has this. -->
<script lang="ts">
  import { ADVISOR_PICKS, type AdvisorPick } from './advisor.ts'

  let {
    value = $bindable(''),
    label,
    prefix = 'Advisor: ',
    disabled = false,
    style = '',
  }: {
    value?: AdvisorPick
    /** The accessible name, which says what the session is for */
    label: string
    /** What each option starts with, so a closed picker says what it picks; '' beside a caption that already does */
    prefix?: string
    disabled?: boolean
    style?: string
  } = $props()
</script>

<select class="advisor-pick" aria-label={label} bind:value {disabled} {style}>
  {#each ADVISOR_PICKS as pick (pick.value)}<option value={pick.value}>{prefix}{pick.label}</option>{/each}
</select>

<style>
  /* Tokens only, so the picker reads in every theme */
  .advisor-pick {
    font: inherit;
    font-size: 12px;
    min-height: 44px;
    max-width: 100%;
    box-sizing: border-box;
    padding: 8px 10px;
    border-radius: var(--radius);
    border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent);
    background: var(--panel);
    color: var(--tx);
    cursor: pointer;
  }
  .advisor-pick:hover:not(:disabled) {
    border-color: color-mix(in oklch, var(--edge) 45%, transparent);
  }
  .advisor-pick:disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }
</style>
