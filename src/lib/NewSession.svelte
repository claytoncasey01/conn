<script lang="ts">
  import { tick } from 'svelte'
  import { NAME_BYTES_MAX, fetchChoices, nameTooLong, startSession, type NewSessionChoices } from './newSession.ts'
  import { href } from './router.svelte.ts'
  import { showTerminal } from './terminalPanel.svelte.ts'

  let step = $state<'closed' | 'loading' | 'form' | 'starting'>('closed')
  let choices = $state<NewSessionChoices | null>(null)
  let repo = $state('')
  let name = $state('')
  let error = $state('')
  /** A line the person runs themselves: sessions open as a command, or a start that did not happen */
  let manual = $state('')
  /** What a dry run would have run */
  let dryCommand = $state('')
  /** What the last start did, once the form is closed */
  let done = $state('')

  let opener: HTMLButtonElement | undefined = $state()
  let picker: HTMLSelectElement | undefined = $state()
  let cancel: HTMLButtonElement | undefined = $state()
  let starter: HTMLButtonElement | undefined = $state()
  /** A name past the server's byte limit is said here and not sent */
  const longName = $derived(nameTooLong(name))
  let form: HTMLFormElement | undefined = $state()

  async function load(): Promise<void> {
    const got = await fetchChoices()
    if ('error' in got) {
      choices = null
      error = got.error
      return
    }
    choices = got
    if (!got.repos.includes(repo)) repo = got.repos[0] ?? ''
  }

  async function open(): Promise<void> {
    step = 'loading'
    error = manual = dryCommand = done = ''
    await load()
    step = 'form'
    await tick()
    if (choices?.repos.length) picker?.focus()
    else cancel?.focus()
  }

  async function close(): Promise<void> {
    step = 'closed'
    error = manual = dryCommand = ''
    await tick()
    opener?.focus()
  }

  async function start(event: SubmitEvent): Promise<void> {
    event.preventDefault()
    if (!repo || step === 'starting' || longName) return
    // The control pressed (Start, or the name field's Enter) is disabled while the start runs, which drops its focus: it
    // gets the focus back when the form is shown again.
    const returnTo = document.activeElement instanceof HTMLElement ? document.activeElement : null
    step = 'starting'
    error = manual = dryCommand = ''
    const result = await startSession(repo, name)
    step = 'form'
    if (result.dry || !result.ok) {
      dryCommand = result.dry ? (result.command ?? '') : ''
      error = result.dry ? '' : (result.error ?? 'the session did not start')
      manual = result.dry ? '' : (result.manualCommand ?? '')
      await tick()
      ;(returnTo && form?.contains(returnTo) ? returnTo : starter)?.focus()
      return
    }
    done = result.id ? `Started in ${repo}.` : `Opened in a new terminal workspace for ${repo}.`
    name = ''
    if (result.id) showTerminal(result.id)
    await close()
  }

  /** Escape anywhere in the form (a field, Start or Cancel) closes it, as Cancel does */
  function escape(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    if (!(event.target instanceof Node) || !form?.contains(event.target)) return
    void close()
  }

  const btn =
    'display: inline-flex; align-items: center; min-height: 44px; padding: 0 16px; border-radius: var(--radius); font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer;'
  const plain = `${btn} border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); background: transparent; color: var(--tx);`
  const field =
    'min-height: 44px; min-width: 0; box-sizing: border-box; padding: 0 10px; color: var(--tx); background: var(--sunk); border: 1px var(--border-style) var(--line2); border-radius: var(--radius); font-family: var(--font-mono), monospace; font-size: 13px;'
</script>

<svelte:window onkeydown={escape} />

<div style="display: flex; flex-direction: column; align-items: flex-end; gap: 10px; min-width: 0; max-width: 100%">
  {#if step === 'closed'}
    <div style="display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 10px">
      {#if done}
        <span role="status" data-content style="font-size: 12px; color: var(--mu)">{done}</span>
      {/if}
      <button bind:this={opener} type="button" class="tab cap" onclick={open} style="{btn} border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 45%, transparent); background: color-mix(in oklch, var(--role-lead) 10%, transparent); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">New session</button>
    </div>
  {:else if step === 'loading'}
    <span role="status" class="cap" style="font-size: 12px; color: var(--mu)">Loading repositories…</span>
  {:else}
    <form
      bind:this={form}
      aria-label="New session"
      onsubmit={start}
      class="surf"
      style="display: flex; flex-wrap: wrap; align-items: flex-end; gap: 10px; padding: 12px 14px; max-width: 100%; box-sizing: border-box; --wash: linear-gradient(160deg, color-mix(in oklch, var(--edge) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--edge) 22%, transparent); --r: var(--radius)"
    >
      {#if choices?.dry}
        <p class="cap" style="flex-basis: 100%; margin: 0; font-size: 11px; color: color-mix(in oklab, var(--warn) 55%, var(--bright))">Dry run: nothing is started</p>
      {/if}
      {#if choices && choices.repos.length}
        <label style="display: flex; flex-direction: column; gap: 4px; min-width: 0; flex: 1 1 180px">
          <span class="cap" style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 50%, var(--tx))">Repository</span>
          <select bind:this={picker} bind:value={repo} disabled={step === 'starting'} style={field}>
            {#each choices.repos as choice (choice)}
              <option value={choice}>{choice}</option>
            {/each}
          </select>
        </label>
        <label style="display: flex; flex-direction: column; gap: 4px; min-width: 0; flex: 1 1 180px">
          <span class="cap" style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 50%, var(--tx))">Name (optional)</span>
          <input bind:value={name} type="text" maxlength={NAME_BYTES_MAX} aria-invalid={longName} aria-describedby={longName ? 'new-session-name-long' : undefined} autocomplete="off" autocapitalize="off" spellcheck="false" disabled={step === 'starting'} style={field} />
        </label>
        <button bind:this={starter} type="submit" class="cap" disabled={step === 'starting' || longName} style="{btn} border: 1px var(--border-style) var(--ac); background: var(--ac); color: var(--onac); font-weight: 700">
          {#if step === 'starting'}Starting…{:else}Start{/if}
        </button>
      {:else if choices}
        <p style="flex: 1 1 220px; margin: 0; font-size: 12px; color: var(--tx)">No allowed repository has a local clone Conn can use yet: set a clone folder in <a href={href.settings()} style="color: inherit">Settings</a>. A clone must be a folder only you may write (your private group may too), as must every folder above it.</p>
      {/if}
      <button bind:this={cancel} type="button" class="cap" onclick={close} style={plain}>Cancel</button>
      {#if longName}
        <p id="new-session-name-long" role="alert" style="flex-basis: 100%; margin: 0; font-size: 12px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">The name is longer than {NAME_BYTES_MAX} bytes: shorten it.</p>
      {/if}
      {#if error}
        <p role="alert" data-content style="flex-basis: 100%; margin: 0; font-size: 12px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); overflow-wrap: anywhere">{error}</p>
      {/if}
      {#if manual}
        <div style="flex-basis: 100%; display: flex; flex-direction: column; gap: 4px; min-width: 0">
          <span class="cap" style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 50%, var(--tx))">Run this in a terminal</span>
          <code data-content style="display: block; padding: 8px 10px; background: var(--sunk); border-radius: var(--radius); font-size: 12px; color: var(--tx); overflow-wrap: anywhere; user-select: all">{manual}</code>
        </div>
      {/if}
      {#if dryCommand}
        <div role="status" style="flex-basis: 100%; display: flex; flex-direction: column; gap: 4px; min-width: 0">
          <span class="cap" style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 50%, var(--tx))">Would run</span>
          <code data-content style="display: block; padding: 8px 10px; background: var(--sunk); border-radius: var(--radius); font-size: 12px; color: var(--tx); overflow-wrap: anywhere; user-select: all">{dryCommand}</code>
        </div>
      {/if}
    </form>
  {/if}
</div>
