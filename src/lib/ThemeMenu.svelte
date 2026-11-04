<script lang="ts">
  import { reduced } from './fx.ts'
  import { href, router } from './router.svelte.ts'
  import { accentTokens, DEFAULT_ACCENT, SWATCHES } from './theme/accent.ts'
  import { canAccept, menuModel, needsReview, pinNote, shownVariant } from './theme/menu.ts'
  import type { Accept, Pending } from './theme/menu.ts'
  import { withPin } from './theme/storage.ts'
  import { acceptCss, acceptedCss, needsCssConsent, selectTheme, setAccent, setEffects, setMode, setVariant, theme } from './theme/theme.svelte.ts'
  import type { ModeSetting } from './theme/types.ts'
  import { tick } from 'svelte'

  // Everything lives in a panel the trigger opens, so the header stays one 44px control at 1280 and at 320.
  // The panel is a popover (top layer): the page below has its own stacking contexts and would otherwise paint over it.
  let open = $state(false)
  // A user theme with custom CSS that is waiting for "Use anyway": nothing is applied until then
  // (with the hash it was asked about, so only that CSS can be accepted)
  let pending = $state<Pending | null>(null)
  // Why a prompt ended without applying anything
  let note = $state<string | null>(null)
  const NOTES: Record<Exclude<Accept, 'ok'>, string> = {
    changed: 'This theme changed while you were deciding. Pick it again to review it.',
    gone: 'This theme is no longer available.',
  }
  let root: HTMLDivElement
  let trigger: HTMLButtonElement
  let picker: HTMLSelectElement
  let panel: HTMLDivElement
  let cancel = $state<HTMLButtonElement>()

  const uid = $props.id()
  // Before the first read of the list (or when it failed) the menu still names the compiled-in Default
  const list = $derived(theme.list.length ? theme.list : [theme.resolved.entry])
  // The choice as the page wears it: a link's ?theme= and ?mode= are on screen, so the menu names them (never the URL itself, only the store's copy).
  // Named from that, not `resolved`, which is only Default's look before the list arrives
  const shown = $derived(withPin(theme.choice, theme.pin))
  const model = $derived(menuModel(list, shown.id))
  const current = $derived(model.themes.find((t) => t.active))
  const savedName = $derived(menuModel(list, theme.choice.id).themes.find((t) => t.active)?.label ?? theme.choice.id)
  const pinText = $derived(pinNote(theme.pin, { theme: current?.label ?? shown.id, mode: shown.mode }, { id: theme.choice.id, theme: savedName, mode: theme.choice.mode }))
  // The theme on screen lost its CSS to an edit: re-picking it fires no change, so the menu asks here (recomputed with the model)
  const review = $derived(needsReview(model, list, acceptedCss()))
  const modes: { value: ModeSetting; label: string }[] = [
    { value: 'light', label: 'Light' },
    { value: 'dark', label: 'Dark' },
    { value: 'system', label: 'System' },
  ]
  // A one-mode theme ignores the setting, so the control shows the mode on screen
  const shownMode = $derived(model.modeEnabled ? shown.mode : theme.resolved.mode)
  const shownVar = $derived(shownVariant(model.variants, theme.choice.variant))
  const shownAccent = $derived(theme.choice.accent?.name ?? DEFAULT_ACCENT.name)
  // The swatch is the accent as the page would wear it in the mode on screen
  const swatch = (s: { hue: number; chroma: number }) => accentTokens(theme.resolved.mode, s.hue, s.chroma).ac
  // reduced() is a plain read, so the OS setting is followed by a listener: it can change while the panel is open
  let motionOff = $state(reduced())
  $effect(() => {
    if (typeof matchMedia !== 'function') return
    const mq = matchMedia('(prefers-reduced-motion: reduce)')
    const on = () => (motionOff = mq.matches)
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  })
  const quiet = $derived(theme.choice.effects === 'auto' && motionOff)
  const effectsOn = $derived(theme.choice.effects === 'on' || (theme.choice.effects === 'auto' && !motionOff))

  function showCurrent() {
    // the DOM select is not bound: put it back on the row that is active (not on a same-id invalid row)
    if (!picker) return
    const i = model.themes.findIndex((t) => t.active)
    // no row is active only when the choice is unlisted: the placeholder option is first then
    picker.selectedIndex = i < 0 ? 0 : i
  }

  // The select is not bound, and an option the person or a script has selected ignores its `selected` attribute from then on,
  // so every change of the active row (consent, a focus refresh, the pin) has to be pushed into it
  $effect(showCurrent)

  // Under the trigger, right edges aligned, and never wider than the viewport less an 8px gutter (six 44px swatches need 264px inside it)
  function place() {
    if (!open || !panel) return
    const r = trigger.getBoundingClientRect()
    const w = Math.min(352, innerWidth - 16)
    const left = Math.min(Math.max(8, r.right - w), innerWidth - 8 - w)
    const top = r.bottom + 6
    panel.style.cssText = `top: ${top}px; left: ${left}px; width: ${w}px; max-height: ${Math.max(160, innerHeight - top - 16)}px`
  }

  $effect(() => {
    if (!panel) return
    if (open) {
      place()
      if (!panel.matches(':popover-open')) panel.showPopover()
    } else if (panel.matches(':popover-open')) panel.hidePopover()
  })

  function close(refocus = false) {
    open = false
    pending = null
    note = null
    showCurrent()
    if (refocus) trigger?.focus()
  }

  async function choose(key: string) {
    note = null
    const row = model.themes.find((t) => t.key === key)
    if (!row || row.disabled) return showCurrent()
    if (needsCssConsent(row.id)) {
      pending = { id: row.id, label: row.label, sha: row.cssSha256 }
      showCurrent()
      // the buttons exist once the block has rendered
      await tick()
      cancel?.focus()
      return
    }
    pending = null
    selectTheme(row.id)
  }

  // Both buttons are removed with the prompt, which would drop focus to the page (and Escape with it): the select takes it
  async function leavePrompt() {
    pending = null
    showCurrent()
    await tick()
    picker?.focus()
  }

  async function accept() {
    const p = pending
    if (!p) return
    const r = canAccept(p, theme.list)
    if (r === 'ok') acceptCss(p.id)
    else note = NOTES[r]
    await leavePrompt()
  }

  function keydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && open) {
      e.stopPropagation()
      close(true)
    }
  }

  function leave(e: FocusEvent) {
    if (open && e.relatedTarget instanceof Node && !root.contains(e.relatedTarget)) close()
  }
</script>

<svelte:window onpointerdown={(e) => open && !root.contains(e.target as Node) && close()} onresize={place} onscroll={place} />

<div class="theme-menu" class:open bind:this={root} onkeydown={keydown} onfocusout={leave} role="presentation">
  <button
    type="button"
    class="trigger"
    bind:this={trigger}
    aria-expanded={open}
    aria-controls="{uid}-panel"
    onclick={() => (open ? close() : (open = true))}
  >
    <span class="dot" aria-hidden="true"></span>
    <span class="mu">Theme</span>
    <span data-content class="name">{current?.label ?? shown.id}</span>
  </button>

  <div class="panel" id="{uid}-panel" popover="manual" bind:this={panel}>
    <div class="row">
      <label class="lab" for="{uid}-theme">Theme</label>
      <select id="{uid}-theme" bind:this={picker} aria-describedby={pinText ? `${uid}-pin` : undefined} onchange={(e) => choose(e.currentTarget.value)}>
        {#if !current}<option value="" selected disabled>{shown.id}{theme.ready ? ' (not installed)' : ''}</option>{/if}
        {#each model.themes as t (t.key)}
          <option value={t.key} selected={t.active} disabled={t.disabled}>{t.label}{t.customCss ? ' (custom CSS)' : ''}{t.hint ? ` (unavailable: ${t.hint})` : ''}</option>
        {/each}
      </select>
      {#if pinText}<p class="hint" id="{uid}-pin">{pinText}</p>{/if}
    </div>

    {#if review && !pending}
      <div class="row">
        <p class="hint warn" role="status">This theme's CSS changed. Review it to use it again.</p>
        <button type="button" class="btn" onclick={() => review && choose(review.key)}>Review CSS</button>
      </div>
    {/if}

    {#if pending}
      <div class="confirm" role="alertdialog" aria-labelledby="{uid}-ct" aria-describedby="{uid}-cd">
        <p id="{uid}-ct" class="ct">Use {pending.label}?</p>
        <p id="{uid}-cd">This theme includes custom CSS. CSS can change how any part of conn looks, including what a button says. Use it only if you wrote it or have read it.</p>
        <div class="acts">
          <button type="button" class="btn" bind:this={cancel} onclick={leavePrompt}>Cancel</button>
          <button type="button" class="btn solid" onclick={accept}>Use anyway</button>
        </div>
      </div>
    {/if}

    <div class="row">
      <span class="lab" id="{uid}-mode">Mode</span>
      <div class="seg" role="radiogroup" aria-labelledby="{uid}-mode" aria-describedby={model.modeEnabled ? undefined : `${uid}-mode-hint`}>
        {#each modes as m (m.value)}
          <label class="opt">
            <input type="radio" name="{uid}-mode" value={m.value} checked={shownMode === m.value} disabled={!model.modeEnabled} onchange={() => setMode(m.value)} />
            <span>{m.label}</span>
          </label>
        {/each}
      </div>
      {#if !model.modeEnabled}<p class="hint" id="{uid}-mode-hint">This theme comes in one mode only.</p>{/if}
    </div>

    {#if model.accent === 'free'}
      <div class="row">
        <span class="lab" id="{uid}-acc">Accent</span>
        <div class="swatches" role="radiogroup" aria-labelledby="{uid}-acc">
          {#each SWATCHES as s (s.name)}
            <label class="sw" title={s.name}>
              <input type="radio" name="{uid}-acc" value={s.name} aria-label={s.name} checked={shownAccent === s.name} onchange={() => setAccent(s)} />
              <span class="disc" style="background: {swatch(s)}"></span>
            </label>
          {/each}
        </div>
      </div>
    {:else if model.accent === 'variants'}
      <div class="row">
        <span class="lab" id="{uid}-var">Variant</span>
        <div class="seg" role="radiogroup" aria-labelledby="{uid}-var">
          {#each model.variants as v, i (i)}
            <label class="opt">
              <input type="radio" name="{uid}-var" value={v} checked={shownVar === v} onchange={() => setVariant(v)} />
              <span>{v}</span>
            </label>
          {/each}
        </div>
      </div>
    {/if}

    <div class="row">
      <span class="lab" id="{uid}-fx">Effects</span>
      <button type="button" class="fx" aria-pressed={effectsOn} aria-labelledby="{uid}-fx" aria-describedby={quiet ? `${uid}-fx-hint` : undefined} onclick={() => setEffects(effectsOn ? 'off' : 'on')}>
        <span class="track" aria-hidden="true"><span class="knob"></span></span>
        <span aria-hidden="true">{effectsOn ? 'On' : 'Off'}</span>
      </button>
      {#if quiet}<p class="hint" id="{uid}-fx-hint">Off while your system asks for less motion. Switch on to override.</p>{/if}
    </div>

    <!-- last in the panel, so it is the last stop in the tab order; the page it opens mounts its own menu, closed, so nothing here has to close -->
    {#if router.route.page !== 'themes'}
      <div class="row">
        <a class="browse" href={href.themes()}>Browse themes</a>
      </div>
    {/if}

    {#if note}<p class="hint warn" role="status">{note}</p>{/if}

    {#if theme.error}<p class="hint warn" role="status">{theme.error}</p>{/if}
  </div>
</div>

<style>
  .theme-menu {
    font-family: var(--font-ui, system-ui);
    font-size: 13px;
    color: var(--tx);
  }
  /* The same footprint as the tabs beside it, drawn from theme tokens so it follows the look on screen */
  button,
  select,
  .opt,
  .sw {
    font: inherit;
    min-height: 44px;
    min-width: 44px;
    box-sizing: border-box;
    cursor: pointer;
  }
  .trigger {
    display: flex;
    align-items: center;
    gap: 8px;
    max-width: 100%;
    padding: 0 14px;
    color: var(--tx);
    background: var(--panel);
    border: 1px solid var(--line2);
    border-radius: var(--radius, 6px);
  }
  .trigger[aria-expanded='true'] {
    background: var(--raise);
    border-color: var(--ac-line);
  }
  .trigger .mu,
  .lab,
  .hint {
    color: var(--mu);
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 14ch;
    font-weight: 500;
  }
  .dot {
    flex: none;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--ac);
    box-shadow: 0 0 0 3px var(--ac-soft);
  }
  /* place() sets top, left, width and max-height; the rest overrides the popover's centered default.
     --panel is translucent in CRT and Matrix, so it is layered over an opaque bg: a popover never shows the page through. */
  .panel {
    inset: auto;
    margin: 0;
    box-sizing: border-box;
    overflow: auto;
    overflow-wrap: anywhere;
    padding: 6px 16px 14px;
    color: var(--tx);
    background-color: var(--bg);
    background-image: linear-gradient(var(--panel), var(--panel));
    border: 1px solid var(--line2);
    border-radius: var(--radius, 8px);
    box-shadow: var(--lift, none);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 12px;
    padding: 8px 0;
  }
  .row + .row {
    border-top: 1px solid var(--line);
  }
  .lab {
    flex: 0 0 4.5rem;
  }
  select {
    flex: 1 1 10rem;
    min-width: 0;
    padding: 0 10px;
    color: var(--tx);
    background: var(--sunk);
    border: 1px solid var(--line2);
    border-radius: var(--radius, 6px);
  }
  .hint {
    flex: 1 0 100%;
    margin: 0;
    font-size: 12px;
    line-height: 1.4;
  }
  .hint.warn {
    color: var(--warn);
  }

  /* Native radios, visually hidden: arrow keys, roving focus and the radio role come with them */
  input[type='radio'] {
    position: absolute;
    inset: 0;
    margin: 0;
    opacity: 0;
    cursor: inherit;
  }
  .seg {
    display: flex;
    flex-wrap: wrap;
    gap: 2px;
    padding: 3px;
    background: var(--sunk);
    border-radius: var(--radius, 8px);
  }
  .opt {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 12px;
    color: var(--mu);
    border-radius: calc(var(--radius, 6px) - 2px);
  }
  .opt:has(input:checked) {
    color: var(--tx);
    background: var(--raise);
    box-shadow: var(--lift, none);
    font-weight: 500;
  }
  .opt:has(input:disabled) {
    cursor: not-allowed;
    opacity: 0.6;
  }
  /* forced colors drop every background, so the checked segment is told apart by an outline in a system color */
  @media (forced-colors: active) {
    .opt:has(input:checked) {
      outline: 2px solid CanvasText;
      outline-offset: -2px;
    }
  }
  /* no gap: the 44px hit areas touch and the 24px discs are what show, so all six fit one line at 320 */
  .swatches {
    display: flex;
    flex-wrap: wrap;
  }
  .sw {
    position: relative;
    display: grid;
    place-items: center;
  }
  .disc {
    width: 24px;
    height: 24px;
    border-radius: 50%;
    border: 1px solid var(--line2);
  }
  .sw:has(input:checked) .disc {
    outline: 2px solid var(--tx);
    outline-offset: 3px;
  }
  .fx {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 12px 0 0;
    color: var(--tx);
    background: transparent;
    border: 0;
  }
  .track {
    width: 36px;
    height: 20px;
    padding: 2px;
    box-sizing: border-box;
    background: var(--sunk);
    border: 1px solid var(--line2);
    border-radius: 10px;
  }
  .knob {
    display: block;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--mu);
  }
  .fx[aria-pressed='true'] .track {
    background: var(--ac);
    border-color: var(--ac);
  }
  .fx[aria-pressed='true'] .knob {
    margin-left: 16px;
    background: var(--onac);
  }

  .browse {
    display: flex;
    align-items: center;
    min-height: 44px;
    color: var(--tx);
    text-decoration: underline;
    text-underline-offset: 3px;
  }

  .confirm {
    margin: 4px 0 8px;
    padding: 10px 12px;
    background: var(--sunk);
    border: 1px solid var(--warn);
    border-radius: var(--radius, 6px);
  }
  .confirm p {
    margin: 0 0 8px;
    line-height: 1.45;
  }
  .confirm .ct {
    font-weight: 600;
  }
  .acts {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .btn {
    padding: 0 16px;
    color: var(--tx);
    background: transparent;
    border: 1px solid var(--line2);
    border-radius: var(--radius, 6px);
  }
  .btn.solid {
    color: var(--onac);
    background: var(--ac);
    border-color: var(--ac);
    font-weight: 600;
  }

  button:focus-visible,
  select:focus-visible,
  .browse:focus-visible,
  .opt:has(input:focus-visible) {
    outline: 2px solid var(--ac);
    outline-offset: 2px;
  }
  .sw input:focus-visible + .disc {
    outline: 2px solid var(--ac);
    outline-offset: 3px;
  }
</style>
