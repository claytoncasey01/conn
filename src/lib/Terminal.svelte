<script lang="ts">
  // A hosted session's terminal: its screen (the replay, then live output), the keyboard, and its size. What reaches the
  // program and what the output may do are decided in terminal.ts; this file puts them on screen.
  import { onMount } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import { Unicode11Addon } from '@xterm/addon-unicode11'
  import '@xterm/xterm/css/xterm.css'
  import { termSocket } from './api.ts'
  import { plainText } from './ask.ts'
  import { attach, fitSize, linkTarget, RESIZE_DEBOUNCE_MS, silence, terminalOptions, type Link, type View } from './terminal.ts'
  import { terminalFont, terminalTheme } from './terminalTheme.ts'
  import { theme } from './theme/theme.svelte.ts'

  let { sessionId, name, onclose }: { sessionId: string; name: string; onclose: () => void } = $props()

  let host: HTMLDivElement | undefined = $state()
  let view = $state<View>({ phase: 'connecting', exitCode: null, notice: null, note: '' })
  let term: Terminal | null = null
  let fit: FitAddon | null = null
  let link: Link | null = null
  // Whether the terminal has the keyboard: a mouse report is typed only then
  let focused = false

  const FONT_SIZE = 13
  /** The longest the panel waits for the theme's font before it opens with the fallback. */
  const FONT_WAIT_MS = 1500
  const STATUS: Record<View['phase'], string> = { connecting: 'Connecting…', replaying: 'Catching up…', live: 'Live', ended: 'Ended', closed: 'Closed' }

  /** A hyperlink in the output opens in the default browser through the link policy, and never moves this window. */
  function openLink(text: string) {
    const target = linkTarget(text, location.origin)
    if (target === null) return
    window.open(target, '_blank', 'noopener,noreferrer')
  }

  /** Fits the screen to the panel and asks the host for that size; nothing when the panel is not drawn. */
  function measure() {
    if (!term || !fit) return
    const size = fitSize(fit.proposeDimensions())
    if (!size) return
    if (size.cols !== term.cols || size.rows !== term.rows) term.resize(size.cols, size.rows)
    link?.resize(size)
  }

  /** Attaches, or attaches again: the screen is cleared first, so the replay is drawn once. */
  function connect() {
    link?.detach()
    if (!term) return
    link = attach(() => termSocket(sessionId), term, next => (view = next))
    measure()
  }

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

  /** Opens the terminal in `into` and attaches; returns what closes it all. */
  function start(into: HTMLDivElement, family: string): () => void {
    const t = new Terminal({ ...terminalOptions(openLink), theme: terminalTheme(theme.resolved.tokens), fontFamily: family, fontSize: FONT_SIZE })
    // Before anything is written: the queries are taken by these, not by xterm.js's own answers
    silence(t.parser)
    t.loadAddon(new Unicode11Addon())
    // The host's screen model counts emoji two cells wide, as unicode 11 does
    t.unicode.activeVersion = '11'
    const fitter = new FitAddon()
    t.loadAddon(fitter)
    t.open(into)
    term = t
    fit = fitter
    // The output's own title is never used; only the session's name, from the session list, labels the panel
    const typed = t.onData(data => link?.input(data, focused))
    const gained = () => (focused = true)
    const lost = () => (focused = false)
    into.addEventListener('focusin', gained)
    into.addEventListener('focusout', lost)
    let wait: ReturnType<typeof setTimeout> | undefined
    const observer = new ResizeObserver(() => {
      clearTimeout(wait)
      wait = setTimeout(measure, RESIZE_DEBOUNCE_MS)
    })
    observer.observe(into)
    connect()
    return () => {
      clearTimeout(wait)
      observer.disconnect()
      into.removeEventListener('focusin', gained)
      into.removeEventListener('focusout', lost)
      typed.dispose()
      link?.detach()
      link = null
      term = null
      fit = null
      t.dispose()
    }
  }

  onMount(() => {
    let stop = () => {}
    let gone = false
    const family = terminalFont(theme.resolved.tokens)
    void fontLoaded(family).then(() => {
      if (gone || !host) return
      stop = start(host, family)
    })
    return () => {
      gone = true
      stop()
    }
  })

  // The terminal follows the theme on screen, as the rest of the page does
  $effect(() => {
    const colors = terminalTheme(theme.resolved.tokens)
    const family = terminalFont(theme.resolved.tokens)
    if (!term) return
    term.options.theme = colors
    if (term.options.fontFamily === family) return
    term.options.fontFamily = family
    measure()
  })
</script>

<section aria-label="Terminal of {plainText(name)}" class="surf brk panel" style="--c: var(--edge)">
  <div class="head">
    <div class="hud title">TERMINAL · <span data-content>{plainText(name)}</span></div>
    <div class="status" class:live={view.phase === 'live'} class:bad={view.phase === 'closed'}>{view.phase === 'ended' && view.exitCode !== null ? `Exited · ${view.exitCode}` : STATUS[view.phase]}</div>
    <div class="actions">
      {#if view.notice?.reconnect}
        <button type="button" class="btn cap" onclick={connect}>Reconnect</button>
      {/if}
      <button type="button" class="btn cap" onclick={onclose} aria-label="Detach from the terminal of {plainText(name)}">Detach</button>
    </div>
  </div>
  <div class="screen" bind:this={host}></div>
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
  .actions { display: flex; gap: 8px; margin-left: auto; }
  .btn { font: inherit; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); padding: 6px 12px; min-height: 32px; box-sizing: border-box; cursor: pointer; border-radius: calc(var(--radius) * 0.75); background: transparent; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); }
  .btn:hover { border-color: color-mix(in oklch, var(--edge) 70%, transparent); }
  .screen { height: min(70vh, 640px); min-height: 220px; min-width: 0; overflow: hidden; padding: 6px; box-sizing: border-box; background: var(--bg); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: calc(var(--radius) * 0.75); }
  .notice { font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); }
  .notice:empty { display: none; }
  .note { font-size: 12px; color: color-mix(in oklab, var(--warn) 60%, var(--bright)); }
</style>
