<script lang="ts">
  import { untrack } from 'svelte'
  import { answerSession, live, openTerminal } from './live.svelte.ts'
  import { ARM_MS, STUCK_MS, begin, isLocked, isStuck, mustScrollFirst, newPanel, onResult, panelView, pendingBody, plainText, pressable, readGate, shownAsk, statusLine, sync, waitedFor } from './ask.ts'
  import { dur } from './format.ts'
  import ReplyBox from './ReplyBox.svelte'
  import type { Ask, SessionDetail } from './types.ts'

  /** `session` is the one whose terminal holds the question; `via` is set on a subagent's page, which has no terminal of its own: the page that has the answer. */
  let { session, via, agentName }: { session: SessionDetail; via: { name: string; href: string } | null; agentName: string } = $props()
  const uid = $props.id()

  // The delays run on the monotonic clock: the wall clock can be set back
  const mono = () => performance.now()

  // All the state that decides what is shown and what a click may do is in ask.ts's PanelState
  let st = $state.raw(newPanel(mono()))
  let sending = $state(false)
  // Moves when the arming delay of the shown prompt, or the wait after a 200, has run, so the view re-reads its state
  let tick = $state(mono())
  // A permission's command box: the lines still below its view, and the prompt whose command has been seen to its end
  let more = $state({ id: '', n: 0 })
  let readTo = $state('')

  // `ask` can be missing on a row from older history, so only ever test it for truthiness
  const current = $derived(session.ask ?? null)
  const waiting = $derived(session.status === 'waiting')
  // Each snapshot ends a 409's override: it says what the screen shows now
  const snapAt = $derived(live.snap?.at ?? 0)
  const ask = $derived(shownAsk(st, current, waiting))
  // The parser gives `since` 0; the Store fills it in. Without one, the wait began when the session last moved.
  const withSince = $derived<Ask | null>(ask && !ask.since ? { ...ask, since: session.updatedAt } : ask)
  const view = $derived(panelView(session, !!via, ask))
  const locked = $derived(isLocked(st, ask?.id ?? ''))
  const busy = $derived(!pressable(st, ask, sending, tick))
  const stuck = $derived(locked && isStuck(st, tick))
  const pending = $derived(pendingBody(st, locked))
  const status = $derived(statusLine(st, sending, locked))
  const unread = $derived(view.view === 'permission' && !!ask && mustScrollFirst(readTo, ask))
  const moreLines = $derived(ask && more.id === ask.id ? more.n : 0)
  // Out of the keyed block and kept through a gap in the read, so a draft survives a failed read
  const replying = $derived(view.view === 'text' || (view.view === 'pending' && pending === 'reply'))

  // Before the DOM is drawn, so a new prompt is never on screen with the old one's state
  $effect.pre(() => {
    const next = sync(untrack(() => st), current, waiting, mono(), snapAt)
    if (next !== untrack(() => st)) st = next
  })
  // The buttons arm ARM_MS after the shown prompt first appeared
  $effect(() => {
    const at = st.shownAt
    const t = setTimeout(() => (tick = Math.max(mono(), at + ARM_MS)), Math.max(0, at + ARM_MS - mono()) + 20)
    return () => clearTimeout(t)
  })
  $effect(() => {
    const at = st.sent?.at
    if (at === undefined) return
    const t = setTimeout(() => (tick = Math.max(mono(), at + STUCK_MS)), Math.max(0, at + STUCK_MS - mono()) + 20)
    return () => clearTimeout(t)
  })

  /** Measures a permission's command box when it is drawn, scrolled or resized: what is below the view, and whether it has been read to the end. */
  function measure(node: HTMLElement, askId: string) {
    let id = askId
    const run = () => {
      const g = readGate(untrack(() => readTo), id, { scrollTop: node.scrollTop, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight, lineHeight: parseFloat(getComputedStyle(node).lineHeight) || 18 })
      more = { id, n: g.more }
      readTo = g.readTo
    }
    untrack(run)
    node.addEventListener('scroll', run, { passive: true })
    const ro = new ResizeObserver(run)
    ro.observe(node)
    return {
      update(next: string) {
        id = next
        untrack(run)
      },
      destroy() {
        node.removeEventListener('scroll', run)
        ro.disconnect()
      },
    }
  }

  // Sends the prompt on show only if it is still the one armed when the click lands; ALLOW ONCE also needs its command read
  async function answer(key: string, allow = false) {
    const id = pressable(st, ask, sending, mono())
    if (!id || (allow && unread)) return
    sending = true
    st = begin(st)
    const r = await answerSession(session.id, id, key)
    sending = false
    st = sync(onResult(st, id, ask, r, mono(), snapAt), current, waiting, mono(), snapAt)
  }

  async function terminal() {
    if (sending) return
    sending = true
    st = begin(st)
    const r = await openTerminal(session.id)
    sending = false
    st = r.ok ? { ...st, opened: true } : { ...st, error: r.error }
  }

  const replyS = $derived({ id: session.id, name: session.name, status: session.status, waitingFor: session.waitingFor, canReply: session.canReply, ask: ask ?? st.last })
</script>

{#if view.view !== 'none'}
  <section role="group" aria-label="Needs input" class="surf brk panel" style="--c: var(--warn)">
    <div class="hud head">NEEDS INPUT · <span data-content>{agentName}</span></div>
    <div class="meta">
      {#if session.waitingFor}<span>{session.waitingFor}</span><span class="dim">·</span>{/if}
      <span>waiting {withSince ? waitedFor(withSince, live.now) : dur(live.now - session.updatedAt)}</span>
    </div>

    {#if via}
      <div class="line">Waiting through {via.name}. The answer goes on that page.</div>
      <a class="btn link" href={via.href}><span>OPEN {via.name.toUpperCase()} <span class="g-fwd" aria-hidden="true"></span></span></a>
    {:else}
      <!-- A new prompt is new DOM: no button, focus or text of the old one carries over -->
      {#key ask?.id ?? ''}
        {#if ask && view.view !== 'text' && view.view !== 'pending'}
          {#if ask.title}<div class="title">{plainText(ask.title)}</div>{/if}
          {#if ask.text && view.view === 'permission'}
            <!-- the whole command, up to half the window, then a scroll; focusable so the keyboard can scroll it -->
            <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
            <pre class="text full" use:measure={ask.id} role="region" tabindex="0" aria-label="The command to allow or deny">{plainText(ask.text)}</pre>
          {:else if ask.text}
            <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
            <pre class="text" role="region" tabindex="0" aria-label="What the agent is asking">{plainText(ask.text)}</pre>
          {/if}
        {/if}

        {#if view.view === 'nocmux'}
          <div class="line">not running in a cmux terminal, so it cannot be answered here</div>
        {:else if view.view === 'pending'}
          {#if pending === 'unread'}
            <div class="line dim">The screen has not been read yet.</div>
          {:else if pending === 'reread'}
            <div class="line dim">The screen is being read again.</div>
          {/if}
        {:else if view.view === 'permission' && view.allow && view.deny}
          {@const allow = view.allow}
          {@const deny = view.deny}
          {#if moreLines}<div id="{uid}-more" class="more">scroll: {moreLines} more line{moreLines === 1 ? '' : 's'}{unread ? ' before ALLOW ONCE' : ''}</div>{/if}
          <div class="row">
            <button type="button" class="btn allow cap" disabled={busy || unread} aria-describedby={unread && moreLines ? `${uid}-more` : undefined} onclick={() => answer(allow.key, true)}>Allow once</button>
            <button type="button" class="btn deny cap" disabled={busy} onclick={() => answer(deny.key)}>Deny</button>
          </div>
        {:else if view.view === 'question' && ask}
          <div class="row">
            {#each ask.options as o (o.key)}
              <button data-content type="button" class="btn opt" disabled={busy} onclick={() => answer(o.key)}>{plainText(o.label)}</button>
            {/each}
          </div>
        {/if}
      {/key}

      {#if replying}
        <div class="row"><ReplyBox s={replyS} /></div>
      {/if}

      {#if view.view === 'unknown' || (view.view === 'pending' && (pending === 'unread' || pending === 'reread'))}
        <!-- outside the keyed block: it presses nothing, so a screen that keeps changing must not re-arm it or move its focus -->
        <div class="row">
          <button type="button" class="btn opt cap" disabled={sending} onclick={terminal}>Open terminal</button>
        </div>
      {:else if stuck}
        {#if st.sent?.sure}<div class="line">if the terminal did not move, answer it there</div>{/if}
        <div class="row">
          <button type="button" class="btn opt cap" disabled={sending} onclick={terminal}>Open terminal</button>
        </div>
      {/if}

      <!-- always there, so a screen reader says each change of it once -->
      <div role="status" class="status" class:warn={locked && st.sent && !st.sent.sure}>{status}</div>
    {/if}

    <div aria-live="polite" class="notice">{st.notice}</div>
    {#if st.error}<div role="alert" class="err">{plainText(st.error)}</div>{/if}
  </section>
{/if}

<style>
  .panel { display: flex; flex-direction: column; gap: 12px; padding: 18px 22px; --r: var(--radius); --rim: color-mix(in oklch, var(--warn) 55%, transparent); --wash: linear-gradient(160deg, color-mix(in oklch, var(--warn) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); }
  .head { font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: var(--warn); font-weight: 700; }
  .meta { display: flex; flex-wrap: wrap; gap: 4px 10px; font-size: 12px; color: var(--tx); }
  .dim { color: var(--mu); }
  .line { font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); }
  .title { font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--warn) 65%, var(--bright)); overflow-wrap: anywhere; }
  .text { margin: 0; padding: 10px 12px; max-height: 260px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--tx) 50%, var(--bright)); background: color-mix(in oklch, var(--panel) 90%, transparent); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 0.75); }
  .text.full { max-height: 50vh; }
  .more { font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); color: var(--warn); }
  .row { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
  .btn { font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.7143); padding: 10px 16px; min-height: 44px; box-sizing: border-box; cursor: pointer; border-radius: var(--radius); background: transparent; text-align: left; overflow-wrap: anywhere; }
  .btn:disabled { opacity: 0.45; cursor: not-allowed; }
  .opt { color: color-mix(in oklab, var(--tx) 50%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); }
  .opt:hover:not(:disabled) { border-color: color-mix(in oklch, var(--warn) 70%, transparent); }
  .allow { color: var(--ok); border: 1px var(--border-style) color-mix(in oklch, var(--ok) 50%, transparent); }
  .deny { color: color-mix(in oklab, var(--bad) 70%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--bad) 45%, transparent); }
  .link { align-self: flex-start; display: inline-flex; align-items: center; text-decoration: none; color: color-mix(in oklab, var(--warn) 65%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--warn) 50%, transparent); }
  .notice { font-size: 12px; color: var(--warn); }
  .notice:empty { margin-top: -12px; }
  .status { font-size: 11px; color: var(--ok); }
  .status.warn { color: var(--warn); }
  .status:empty { margin-top: -12px; }
  .err { font-size: 12px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); }
  @media (prefers-reduced-motion: reduce) { .btn { transition: none; } }
</style>
