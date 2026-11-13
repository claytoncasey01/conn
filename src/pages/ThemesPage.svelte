<script lang="ts">
  import { onMount, tick } from 'svelte'
  import defaultTheme from '../../themes/default/theme.json'
  import Brand from '../lib/Brand.svelte'
  import ConfirmAction from '../lib/ConfirmAction.svelte'
  import Nav from '../lib/Nav.svelte'
  import { live } from '../lib/live.svelte.ts'
  import type { ActionResult } from '../lib/live.svelte.ts'
  import { installTheme, loadGallery, uninstallTheme } from '../lib/theme/galleryClient.ts'
  import { cardsModel } from '../lib/theme/galleryModel.ts'
  import type { Card, CardFilter } from '../lib/theme/galleryModel.ts'
  import { accentNote, indexLine, isCurrent, manifestOf, modesNote, notice, previewMode, sourceNote } from '../lib/theme/galleryView.ts'
  import { previewAttrs, previewDoc } from '../lib/theme/preview.ts'
  import { needsCssConsent, refreshThemes, selectTheme, theme } from '../lib/theme/theme.svelte.ts'
  import type { ThemeManifest } from '../lib/theme/types.ts'
  import type { GalleryAnswer } from '../lib/theme/galleryTypes.ts'

  // The compiled-in Default: what a preview falls back to for any token a theme leaves out
  const FALLBACK = defaultTheme as unknown as ThemeManifest

  // null until the first read; the installed themes show meanwhile, since they come from the theme store
  let answer = $state<GalleryAnswer | null>(null)
  let checking = $state(false)
  let query = $state('')
  let filter = $state<CardFilter>('all')
  // A card is keyed by source and id: installed ids are not de-duplicated, so an id alone can repeat
  const keyOf = (c: Card) => `${c.source}:${c.id}`
  let search = $state<HTMLInputElement>()
  let grid = $state<HTMLUListElement>()
  let working = $state<Record<string, boolean>>({})
  let failures = $state<Record<string, string>>({})

  const index = $derived(answer?.themes ?? [])
  const cards = $derived(cardsModel(index, theme.list, filter, query))
  // The filter labels count what each would show before the search narrows it
  const counts = $derived({
    all: cardsModel(index, theme.list, 'all', '').length,
    installed: cardsModel(index, theme.list, 'installed', '').length,
    community: cardsModel(index, theme.list, 'community', '').length,
  })
  const filters: { id: CardFilter; label: string }[] = [
    { id: 'all', label: 'All' },
    { id: 'installed', label: 'Installed' },
    { id: 'community', label: 'Community' },
  ]
  // With the gallery off the server has no install or uninstall route, so no card offers one
  const canChange = $derived(answer?.enabled !== false)
  const why = $derived(notice(answer))
  const line = $derived(indexLine(answer, live.now))

  // Buttons that are busy or have nothing to do use aria-disabled and a guard, not `disabled`: a disabled button drops
  // keyboard focus to the page, and the person pressing Enter on it is left nowhere
  async function check() {
    if (checking) return
    checking = true
    answer = await loadGallery()
    checking = false
  }

  // Conn.app marks the page while the Mac's Reduce transparency is on, and changes the mark when the setting changes; the
  // previews run no script, so they are drawn with the page's mark
  const reducedNow = () => document.documentElement.getAttribute('data-transparency') === 'reduce'
  let reduced = $state(reducedNow())

  onMount(() => {
    void check()
    void refreshThemes()
    const watch = new MutationObserver(() => (reduced = reducedNow()))
    watch.observe(document.documentElement, { attributes: true, attributeFilter: ['data-transparency'] })
    return () => watch.disconnect()
  })

  // A press that removes or re-keys the control that had focus (an install turns a community card into an installed one,
  // which is a new element) would drop focus to the page: put it on the card's new main button, or the search box
  async function refocus(key: string) {
    await tick()
    const a = document.activeElement
    if (a && a !== document.body && document.contains(a)) return
    const li = [...(grid?.children ?? [])].find((e) => (e as HTMLElement).dataset.key === key)
    ;(li?.querySelector<HTMLElement>('.primary') ?? search)?.focus()
  }

  async function install(c: Card) {
    const k = keyOf(c)
    if (working[k]) return
    working[k] = true
    delete failures[k]
    const r = await installTheme(c.id)
    let next = k
    if (!r.ok) failures[k] = r.error ?? 'The install did not go through.'
    else {
      await refreshThemes()
      if (theme.error) failures[k] = 'Installed, but the list of themes could not be read again. Reload the page.'
      else next = `gallery:${c.id}`
    }
    delete working[k]
    await refocus(next)
  }

  function use(c: Card) {
    const k = keyOf(c)
    delete failures[k]
    // a user theme with custom CSS is reviewed in the menu first; selecting it here would do nothing
    if (c.source === 'user' && needsCssConsent(c.id)) failures[k] = 'This theme has custom CSS. Pick it in the Theme menu to review it first.'
    else selectTheme(c.id)
  }

  // Uninstalling the theme on screen falls back to Default when the refreshed list no longer has it
  async function uninstall(c: Card): Promise<ActionResult> {
    const r = await uninstallTheme(c.id)
    if (r.ok) {
      await refreshThemes()
      // the card turns back into an Install card when the index still lists it, else it is gone
      void refocus(`community:${c.id}`)
    }
    return r
  }

  async function clearSearch() {
    query = ''
    filter = 'all'
    await tick()
    search?.focus()
  }
</script>

<svelte:head><title>Themes</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) var(--look-head-fill, 72%), transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="themes" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>LINK ESTABLISHED · {location.host}</div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span>LINK LOST · {location.host}</div>
    {/if}
  </header>

  <main class="themes" aria-label="Themes">
    <div class="top">
      <h1>Themes</h1>
      <p class="tally hud" role="status">{cards.length} of {counts.all} shown</p>
    </div>

    <div class="tools">
      <label class="search">
        <span class="sr">Search themes</span>
        <input type="search" bind:this={search} bind:value={query} placeholder="Search by name or author" autocomplete="off" spellcheck="false" />
      </label>
      <div class="filters" role="group" aria-label="Show themes">
        {#each filters as f (f.id)}
          <button type="button" aria-pressed={filter === f.id} onclick={() => (filter = f.id)}>{f.label} <span class="n">{counts[f.id]}</span></button>
        {/each}
      </div>
    </div>

    {#if line}
      <p class="index">
        <span>Community themes come from <code data-content>{line.repo}</code>, {line.when}.{#if answer?.dropped} {answer.dropped} {answer.dropped === 1 ? 'entry was' : 'entries were'} skipped because {answer.dropped === 1 ? 'it is' : 'they are'} not valid themes.{/if}</span>
        <button type="button" class="link" onclick={check} aria-disabled={checking} aria-busy={checking}>{checking ? 'Checking…' : 'Check again'}</button>
      </p>
    {:else if !answer}
      <p class="index" role="status">Reading the theme index…</p>
    {/if}

    {#if why}
      <div class="notice" class:warn={why.kind === 'error'} role="status">
        <p>{why.text}</p>
        {#if why.reason}<p class="reason" data-content>{why.reason}</p>{/if}
        {#if why.kind === 'error' && !line}<button type="button" class="link" onclick={check} aria-disabled={checking} aria-busy={checking}>{checking ? 'Checking…' : 'Check again'}</button>{/if}
      </div>
    {/if}

    {#if answer?.enabled}
      <p class="trust"><b>Community themes are colors and settings only.</b> They cannot run code, load anything from the web or change what a button says.</p>
    {/if}

    {#if cards.length === 0}
      <div class="empty" role="status">
        <p>{query.trim() ? 'No theme matches that search.' : filter === 'community' ? 'No community themes to show.' : 'No themes to show.'}</p>
        {#if query.trim() || filter !== 'all'}<button type="button" class="btn" onclick={clearSearch}>Show all themes</button>{/if}
      </div>
    {:else}
      <ul class="grid" bind:this={grid}>
        {#each cards as c (keyOf(c))}
          {@const k = keyOf(c)}
          {@const current = isCurrent(c, theme.resolved.entry)}
          <li class="tile" data-key={k} class:current class:broken={c.invalid !== undefined}>
            {#if c.invalid !== undefined}
              <div class="body">
                <h2 data-content>This theme is broken</h2>
                <p class="by" data-content>{c.id}</p>
                <p class="reason" data-content>{c.invalid}</p>
                <p class="meta">{sourceNote(c.source)}. {c.action === 'Update' ? 'The index has a copy you can install over it.' : canChange ? 'Uninstall it to clear it from the list.' : 'With the gallery off, its folder has to be removed by hand.'}</p>
                <div class="acts">
                  {#if c.action === 'Update'}
                    <button type="button" class="btn primary" aria-disabled={working[k]} aria-busy={working[k]} onclick={() => install(c)}>{working[k] ? 'Updating…' : 'Update'}</button>
                  {/if}
                  {#if canChange}
                    <ConfirmAction label="Uninstall" confirmText="Uninstall {c.id}?" confirmLabel="Uninstall" tone="red" run={() => uninstall(c)} />
                  {/if}
                </div>
                {#if failures[k]}<p class="err" role="alert" data-content>{failures[k]}</p>{/if}
              </div>
            {:else}
              {@const m = manifestOf(c, theme.list)}
              {#if m}
                <iframe class="pv" tabindex="-1" title="Preview of {c.name}" height="252" loading="lazy" sandbox={previewAttrs.sandbox} csp={previewAttrs.csp} srcdoc={previewDoc(m, previewMode(m, theme.resolved.mode), FALLBACK, reduced)}></iframe>
              {:else}
                <div class="pv none">No preview</div>
              {/if}
              <div class="body">
                <div class="head">
                  <h2 data-content>{c.name}</h2>
                  <span class="ver" data-content>v{c.version}</span>
                </div>
                <p class="by" data-content>by {c.author}</p>
                {#if m?.description}<p class="desc" data-content>{m.description}</p>{/if}
                <p class="meta">{sourceNote(c.source)}. {m ? `${modesNote(c.modes)}. ${accentNote(m)}.` : ''}</p>
                {#if c.action === 'Update' && c.entry}
                  <p class="flag">Update available: v{c.version} to v{c.entry.version}.</p>
                {:else if c.source === 'community' && c.entry}
                  <p class="meta">Pinned to commit <code data-content>{c.entry.commit.slice(0, 7)}</code>.</p>
                {/if}
                <div class="acts">
                  {#if c.action === 'Install' || c.action === 'Update'}
                    <button type="button" class="btn primary" aria-disabled={working[k]} aria-busy={working[k]} onclick={() => install(c)}>{working[k] ? (c.action === 'Update' ? 'Updating…' : 'Installing…') : c.action}</button>
                  {:else}
                    <!-- one element for Use and In use, so pressing Use keeps focus on the button that turns into In use -->
                    <button type="button" class="btn" class:primary={!current} aria-disabled={current} onclick={() => !current && use(c)}>{current ? 'In use' : 'Use'}</button>
                  {/if}
                  {#if c.source === 'gallery' && canChange}
                    <ConfirmAction label="Uninstall" confirmText="Uninstall {c.name}?" confirmLabel="Uninstall" tone="red" run={() => uninstall(c)} />
                  {/if}
                </div>
                {#if failures[k]}<p class="err" role="alert" data-content>{failures[k]}</p>{/if}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </main>
</div>

<style>
  /* Tokens only, so the page reads in every theme; the previews are the one place a theme's own look shows */
  .themes {
    position: relative;
    z-index: 6; /* above the faint scanline layer, so a preview is not tinted by the page's own look */
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 18px;
    width: 100%;
    max-width: 1480px;
    box-sizing: border-box;
    margin: 0 auto;
    padding: 24px 32px 48px;
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
  }

  .top {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: 4px 20px;
  }
  .tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px 20px;
  }
  .search {
    flex: 1 1 240px;
    max-width: 420px;
    min-width: 0;
  }
  .search input {
    width: 100%;
    min-height: 44px;
    box-sizing: border-box;
    padding: 0 12px;
    color: var(--tx);
    background: var(--sunk);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-size: 13px;
  }
  .search input::placeholder {
    color: var(--mu);
  }
  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: 2px;
    padding: 3px;
    background: var(--sunk);
    border-radius: var(--radius);
  }
  .filters button {
    min-height: 44px;
    padding: 0 14px;
    color: var(--mu);
    background: transparent;
    border: 1px solid transparent;
    border-radius: calc(var(--radius) - 2px);
    font-size: 12px;
    cursor: pointer;
  }
  .filters button[aria-pressed='true'] {
    color: var(--tx);
    background: var(--raise);
    border-color: var(--line2);
    font-weight: 600;
  }
  .n {
    color: var(--mu);
    font-weight: 400;
  }
  .tally {
    font-size: 11px;
    color: var(--mu);
  }

  .index {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
    color: var(--mu);
    font-size: 12px;
    line-height: 1.5;
  }
  .link {
    min-height: 44px;
    padding: 0 12px;
    color: var(--tx);
    background: transparent;
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-size: 12px;
    cursor: pointer;
  }
  .link[aria-disabled='true'] {
    cursor: wait;
    color: var(--mu);
  }
  .trust {
    max-width: 78ch;
    padding: 10px 14px;
    background: var(--panel);
    border-left: 3px solid var(--ac);
    border-radius: var(--radius);
    font-size: 12px;
    line-height: 1.55;
    color: var(--mu);
  }
  .trust b {
    color: var(--tx);
    font-weight: 600;
  }
  .notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 14px;
    padding: 10px 14px;
    color: var(--tx);
    background: var(--panel);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-size: 12px;
    line-height: 1.5;
  }
  .notice.warn {
    border-color: var(--warn);
  }
  .notice .reason {
    flex: 1 0 100%;
    order: 3;
    color: var(--mu);
    overflow-wrap: anywhere;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
    padding: 36px 20px;
    color: var(--mu);
    border: 1px dashed var(--line2);
    border-radius: var(--radius);
  }

  .grid {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr));
    gap: 16px;
    align-items: stretch;
  }
  .tile {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow: hidden;
    background: var(--panel);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
  }
  .tile.current {
    border-color: var(--ac);
    box-shadow: 0 0 0 1px var(--ac);
  }
  .tile.broken {
    border-style: dashed;
    border-color: var(--bad);
  }
  /* a preview is a whole document with a fixed 252px body: the sample inside is 245px tall at 280px wide */
  .pv {
    display: block;
    width: 100%;
    height: 252px;
    border: 0;
    border-bottom: 1px var(--border-style) var(--line);
    background: var(--sunk);
  }
  .pv.none {
    display: grid;
    place-items: center;
    color: var(--mu);
  }
  .body {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 12px 14px 14px;
    min-width: 0;
  }
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
  }
  /* a long name or version wraps inside the row instead of widening the card */
  .head > * {
    min-width: 0;
  }
  h1 {
    margin: 0;
    font-size: 20px;
    font-weight: 700;
    color: var(--bright);
  }
  h2 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--bright);
    overflow-wrap: anywhere;
  }
  .ver,
  .by,
  .meta {
    color: var(--mu);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .desc {
    overflow-wrap: anywhere;
    font-size: 12px;
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .flag {
    overflow-wrap: anywhere;
    font-size: 12px;
    font-weight: 600;
    color: color-mix(in oklab, var(--warn) 60%, var(--bright));
  }
  .reason {
    font-size: 12px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .broken .reason {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  .acts {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 10px;
    margin-top: auto;
    padding-top: 8px;
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
  .btn.primary {
    color: var(--onac);
    background: var(--ac);
    border-color: var(--ac);
    font-weight: 600;
  }
  .btn[aria-disabled='true'] {
    cursor: default;
    opacity: 0.7;
  }
  .btn.primary[aria-disabled='true'] {
    cursor: wait;
  }
  .err {
    font-size: 12px;
    line-height: 1.5;
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
    overflow-wrap: anywhere;
  }

  @media (max-width: 600px) {
    /* the three filters and their counts fit one row of a 320px screen */
    .filters {
      width: 100%;
      box-sizing: border-box;
    }
    .filters button {
      flex: 1 1 auto;
      padding: 0 4px;
    }
  }
  @media (prefers-reduced-motion: no-preference) {
    .tile {
      transition: border-color 0.16s;
    }
  }
</style>
