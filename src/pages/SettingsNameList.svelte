<script lang="ts">
  // A list of names the person adds and removes (Slack channels, Jira project keys), validated as the server validates them. It holds
  // no save: the card that owns it sends the whole section when its button is pressed. Every name renders as text.
  import { tick } from 'svelte'
  import { plainText } from '../lib/ask.ts'
  import { MAX_LISTED, addName, nameCheck, nextFocusIndex, type NameResult } from '../lib/settingsModel.ts'

  let {
    id,
    label,
    noun,
    placeholder,
    items,
    parse,
    disabled = false,
    onchange,
  }: {
    id: string
    label: string
    noun: string
    placeholder: string
    items: string[]
    parse: (raw: string) => NameResult
    disabled?: boolean
    onchange: (next: string[]) => void
  } = $props()

  let text = $state('')
  let touched = $state(false)
  let input = $state<HTMLInputElement>()
  let listEl = $state<HTMLElement>()

  const check = $derived(nameCheck(text, items, parse))
  const showErr = $derived(touched && check.state === 'error')
  $effect(() => {
    if (!text.trim()) touched = false
  })

  function submit(e: SubmitEvent) {
    e.preventDefault()
    touched = true
    if (disabled) return
    const r = addName(items, text, parse)
    if (!r.ok) return
    text = ''
    onchange(r.list)
  }
  async function remove(name: string) {
    const at = items.indexOf(name)
    onchange(items.filter((x) => x !== name))
    await tick()
    const left = listEl?.querySelectorAll('button[data-remove]') ?? []
    const k = nextFocusIndex(left.length, at)
    if (k < 0) input?.focus()
    else (left[k] as HTMLElement).focus()
  }
</script>

<div class="names">
  <span class="lab" id="{id}-lab">{label}</span>
  {#if items.length}
    <ul class="chips" aria-labelledby="{id}-lab" bind:this={listEl}>
      {#each items as name (name)}
        <li class="chip in">
          <code data-content>{plainText(name)}</code>
          <button type="button" data-remove class="x" onclick={() => remove(name)} {disabled} aria-label="Remove {plainText(name)}"><span aria-hidden="true">×</span></button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="hint">None yet.</p>
  {/if}
  <form class="add" onsubmit={submit}>
    <label class="sr" for="{id}-add">Add {noun}</label>
    <input id="{id}-add" bind:this={input} bind:value={text} type="text" class="fld" {placeholder} {disabled} autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="100" aria-describedby="{id}-hint" aria-invalid={showErr} onblur={() => (touched = true)} onkeydown={(e) => { if (e.key === 'Enter') touched = true }} />
    <button type="submit" class="btn small cap" aria-disabled={disabled || check.state !== 'ok'}>Add</button>
  </form>
  <div id="{id}-hint">
    {#if showErr && check.state === 'error'}
      <p class="hint err" role="alert">{check.reason}</p>
    {:else if check.state === 'ok'}
      <p class="hint">Will add <code data-content>{check.name}</code></p>
    {:else}
      <p class="hint">Up to {MAX_LISTED}.</p>
    {/if}
  </div>
</div>

<style>
  .names {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .lab {
    color: var(--mu);
    font-size: 12px;
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  p {
    margin: 0;
  }
  code {
    font-family: var(--font-mono), monospace;
    color: var(--tx);
    overflow-wrap: anywhere;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    max-width: 100%;
    min-height: 36px;
    padding: 0 4px 0 12px;
    color: var(--tx);
    border: 1px var(--border-style) var(--line2);
    border-radius: 999px;
    font-size: 12px;
  }
  .x {
    min-width: 36px;
    min-height: 36px;
    padding: 0;
    color: var(--mu);
    background: transparent;
    border: 0;
    border-radius: 999px;
    font-size: 16px;
    line-height: 1;
    cursor: pointer;
  }
  .x:hover:not(:disabled) {
    color: var(--tx);
    background: var(--hover);
  }
  .x:disabled {
    cursor: default;
  }
  .add {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .fld {
    flex: 1 1 140px;
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
  .btn.small {
    min-height: 36px;
    padding: 0 12px;
  }
  .btn:hover:not(:disabled):not([aria-disabled='true']) {
    background: var(--hover);
  }
  .btn:disabled,
  .btn[aria-disabled='true'] {
    cursor: default;
    opacity: 0.5;
  }
  .hint {
    color: var(--mu);
    font-size: 12px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .hint.err {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  @media (prefers-reduced-motion: no-preference) {
    .btn,
    .x,
    .fld {
      transition: border-color 0.16s, background 0.16s, color 0.16s;
    }
  }
</style>
