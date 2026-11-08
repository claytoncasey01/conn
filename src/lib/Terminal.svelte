<script lang="ts">
  // A hosted session's terminal panel. What reaches the program and what the output may do are decided in terminal.ts and
  // terminalMount.ts; this file hands them the page's real element, xterm.js, socket and timers, and draws the view.
  // The pages load it with import() (terminalPanel.svelte.ts), so xterm.js is not in the page's first chunk.
  import { onMount } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import { Unicode11Addon } from '@xterm/addon-unicode11'
  import '@xterm/xterm/css/xterm.css'
  import { termSocket } from './api.ts'
  import { plainText } from './ask.ts'
  import { statusText } from './terminal.ts'
  import { interruptOffered } from './interrupt.ts'
  import InterruptButton from './InterruptButton.svelte'
  import { LEAVE_HINT, mount, type Look, type Panel, type PanelView } from './terminalMount.ts'
  import { terminalFont, terminalTheme } from './terminalTheme.ts'
  import { theme } from './theme/theme.svelte.ts'

  let { sessionId, name, onclose }: { sessionId: string; name: string; onclose: () => void } = $props()

  let host: HTMLDivElement | undefined = $state()
  let detachButton: HTMLButtonElement | undefined = $state()
  let view = $state<PanelView>({ phase: 'connecting', exitCode: null, notice: null, note: '', lagging: false, held: false, pasteClosed: false, ask: null, hover: '' })
  let panel: Panel | null = null

  // The confirmation takes the keyboard while it is open: its first button has the focus, Escape cancels, and the focus
  // goes back where it was (the terminal) once it closes, whichever way.
  let askFirst: HTMLButtonElement | undefined = $state()
  let askReturn: HTMLElement | null = null
  const askTitleId = $derived(`ask-title-${sessionId}`)
  const askTextId = $derived(`ask-text-${sessionId}`)
  let asking = false
  $effect(() => {
    const open = view.ask !== null
    if (open === asking) return
    asking = open
    if (open) {
      askReturn = document.activeElement instanceof HTMLElement ? document.activeElement : null
      queueMicrotask(() => askFirst?.focus())
      return
    }
    askReturn?.focus()
    askReturn = null
  })

  function askKey(event: KeyboardEvent) {
    if (event.key !== 'Escape') return
    event.preventDefault()
    panel?.cancel()
  }

  // Dismiss stays in place, disabled, while it has the focus and there is nothing left to dismiss, so the focus is not
  // dropped on the page when its note goes.
  let dismissFocused = $state(false)
  const dismissable = $derived(view.phase === 'live' && (view.held || view.pasteClosed))

  const FONT_SIZE = 13
  /** The longest the panel waits for the theme's font before it opens with the fallback. */
  const FONT_WAIT_MS = 1500

  const lookNow = (): Look => ({ theme: terminalTheme(theme.resolved.tokens), font: terminalFont(theme.resolved.tokens), size: FONT_SIZE })

  /** Waits, at most FONT_WAIT_MS, for the font: xterm.js measures its cells once, from the font it has when it opens. */
  async function fontLoaded(family: string): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined
    const late = new Promise<void>(done => (timer = setTimeout(done, FONT_WAIT_MS)))
    try {
      await Promise.race([document.fonts.load(`${FONT_SIZE}px ${family}`), late])
    } catch {
      // the terminal opens with the fallback font
    } finally {
      clearTimeout(timer)
    }
  }

  function start(into: HTMLDivElement, look: Look): Panel {
    return mount({
      host: into,
      page: window,
      makeTerminal: options => new Terminal(options),
      makeFit: () => new FitAddon(),
      makeUnicode: () => new Unicode11Addon(),
      openSocket: () => termSocket(sessionId),
      watchSize: changed => {
        const observer = new ResizeObserver(changed)
        observer.observe(into)
        return observer
      },
      later: (run, ms) => {
        const timer = setTimeout(run, ms)
        return () => clearTimeout(timer)
      },
      frame: run => void requestAnimationFrame(run),
      // A link opens in the default browser: in the desktop app a new window is handed to the system's browser
      openLink: url => void window.open(url, '_blank', 'noopener,noreferrer'),
      ownOrigin: location.origin,
      leave: () => detachButton?.focus(),
      show: next => (view = next),
      look,
    })
  }

  onMount(() => {
    let gone = false
    const look = lookNow()
    void fontLoaded(look.font).then(() => {
      if (gone || !host) return
      panel = start(host, look)
    })
    return () => {
      gone = true
      panel?.stop()
      panel = null
    }
  })

  // The terminal follows the theme on screen, as the rest of the page does
  $effect(() => {
    const look = lookNow()
    panel?.setLook(look)
  })

  /** A click on Reconnect: a new terminal and socket. Never called otherwise. */
  function reconnect(event: MouseEvent) {
    if (!event.isTrusted) return
    panel?.reconnect()
  }
</script>

<section aria-label="Terminal of {plainText(name)}" class="surf brk panel" style="--c: var(--edge)">
  <div class="head">
    <div class="hud title">TERMINAL · <span data-content>{plainText(name)}</span></div>
    <div class="status" class:live={view.phase === 'live'} class:bad={view.phase === 'closed'}>{statusText(view)}</div>
    <div class="hint">{LEAVE_HINT} leaves the terminal</div>
    <div class="actions">
      {#if dismissable || dismissFocused}
        <button type="button" class="btn cap" disabled={!dismissable} onfocus={() => (dismissFocused = true)} onblur={() => (dismissFocused = false)} onclick={event => panel?.dismiss(event)}>Dismiss</button>
      {/if}
      {#if view.notice?.reconnect}
        <button type="button" class="btn cap" onclick={reconnect}>Reconnect</button>
      {/if}
      {#if interruptOffered(view)}
        <InterruptButton id={sessionId} />
      {/if}
      <button type="button" class="btn cap" bind:this={detachButton} onclick={onclose} aria-label="Detach from the terminal of {plainText(name)}">Detach</button>
    </div>
  </div>
  {#if view.ask}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div role="alertdialog" aria-modal="true" aria-labelledby="{askTitleId}" aria-describedby="{askTextId}" class="ask" onkeydown={askKey} tabindex="-1">
      <div id={askTitleId} class="asktitle">{view.ask.kind === 'link' ? 'Open a link' : 'Paste lines'}</div>
      <div id={askTextId}>
        {#if view.ask.kind === 'link'}
          <div>Open a page on <strong data-content>{plainText(view.ask.host)}</strong> in your browser?</div>
          <div class="url" data-content>{plainText(view.ask.url)}</div>
        {:else}
          <div>The program has not asked for pastes, so each of these {view.ask.lines} lines would be sent as Enter. Paste anyway?</div>
        {/if}
      </div>
      <div class="actions">
        {#if view.ask.kind === 'link'}
          <button type="button" class="btn cap" bind:this={askFirst} onclick={event => panel?.confirm(event)}>Open</button>
        {:else}
          <button type="button" class="btn cap" bind:this={askFirst} onclick={event => panel?.confirm(event)}>Paste</button>
        {/if}
        <button type="button" class="btn cap" onclick={() => panel?.cancel()}>Cancel</button>
      </div>
    </div>
  {/if}
  <div class="screen" bind:this={host}></div>
  <div class="hover" data-content>{plainText(view.hover)}</div>
  <div role="status" class="notice">{view.notice?.text ?? ''}</div>
  {#if view.note}<div role="alert" class="note">{view.note}</div>{/if}
</section>

<style>
  .panel { display: flex; flex-direction: column; gap: 10px; padding: 14px 16px; min-width: 0; --r: var(--radius); --rim: color-mix(in oklch, var(--edge) 30%, transparent); --wash: color-mix(in oklch, var(--panel) 85%, transparent); }
  .head { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px; }
  .title { font-size: 11px; font-weight: 700; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--edge) 50%, var(--bright)); overflow-wrap: anywhere; min-width: 0; }
  .status { font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: var(--mu); }
  .status.live { color: var(--ok); }
  .status.bad { color: color-mix(in oklab, var(--bad) 60%, var(--bright)); }
  .hint { font-size: 11px; color: var(--mu); }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-left: auto; }
  .btn { font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); padding: 6px 12px; min-height: 32px; box-sizing: border-box; cursor: pointer; border-radius: calc(var(--radius) * 0.75); background: transparent; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); }
  .btn:hover { border-color: color-mix(in oklch, var(--edge) 70%, transparent); }
  .ask { display: flex; flex-direction: column; gap: 8px; padding: 10px 12px; font-size: 12px; line-height: 1.5; color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--warn) 50%, transparent); border-radius: calc(var(--radius) * 0.75); background: color-mix(in oklch, var(--warn) 8%, transparent); }
  .ask .actions { margin-left: 0; }
  .asktitle { font-weight: 700; }
  .url { overflow-wrap: anywhere; color: var(--mu); }
  .screen { height: min(70vh, 640px); min-height: 220px; min-width: 0; overflow: hidden; padding: 6px; box-sizing: border-box; background: var(--bg); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: calc(var(--radius) * 0.75); }
  .hover { font-size: 11px; color: var(--mu); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-height: 1.4em; }
  .notice { font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); }
  .notice:empty { display: none; }
  .note { font-size: 12px; color: color-mix(in oklab, var(--warn) 60%, var(--bright)); }
</style>
