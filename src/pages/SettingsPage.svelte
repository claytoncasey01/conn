<script lang="ts">
  import { tick } from 'svelte'
  import { live } from '../lib/live.svelte.ts'
  import Brand from '../lib/Brand.svelte'
  import Nav from '../lib/Nav.svelte'
  import SettingsAlerts from './SettingsAlerts.svelte'
  import SettingsAppIcon from './SettingsAppIcon.svelte'
  import SettingsProvided from './SettingsProvided.svelte'
  import SettingsServices from './SettingsServices.svelte'
  import ReviewLegacy from './ReviewLegacy.svelte'
  import { plainText } from '../lib/ask.ts'
  import { browseGithub, getSettings, putSection, withSavedRepos, type Owner, type SettingsView } from '../lib/settingsApi.ts'
  import {
    MAX_PATTERNS,
    addCheck,
    addPattern,
    covered,
    folderProblem,
    howText,
    matchedCount,
    matchedText,
    nextFocusIndex,
    ownerCountText,
    removePattern,
    saveClones,
    savePatterns,
    saveProblem,
    sameList,
    suggestions,
    wroteClones,
    wrotePatterns,
  } from '../lib/settingsModel.ts'

  // What the server has, and the drafts the person is editing. A save is explicit and writes only its own block.
  let view = $state<SettingsView | null>(null)
  let loadError = $state<string | null>(null)

  // Setup is what the answer says; until it comes, what the snapshot says
  const setup = $derived(view ? view.setup : live.snap?.setup === true)

  // ---- Repositories ----
  const saved = $derived(view?.settings.repos.allow ?? [])
  let draftAllow = $state<string[]>([])
  let addText = $state('')
  let addMsg = $state<string | null>(null)
  let addInput = $state<HTMLInputElement>()
  let listEl = $state<HTMLElement>()
  let chipsEl = $state<HTMLElement>()
  let patSaving = $state(false)
  let patError = $state<string | null>(null)
  let patWrote = $state<string | null>(null)
  // The add box shows what is wrong with its text once the person leaves it or presses Enter, not from the first keystroke
  let touched = $state(false)

  const check = $derived(addCheck(addText, draftAllow))
  const showErr = $derived(touched && check.state === 'error')
  const patDirty = $derived(!sameList(draftAllow, saved))
  const patProblem = $derived(saveProblem(draftAllow))
  const seenRepos = $derived((view?.seen ?? []).map((s) => s.repo))
  const chips = $derived(suggestions(view?.seen ?? [], draftAllow))
  const matched = (p: string) => (view && p in view.matched && !patDirty ? view.matched[p] : matchedCount(p, seenRepos))
  $effect(() => {
    if (!addText.trim()) touched = false
  })

  // Any edit to the list ends the last save's message: it was about a list that is no longer on screen
  function edited() {
    patError = null
    patWrote = null
    addMsg = null
  }
  function add(raw: string, fromBox: boolean) {
    const r = addPattern(draftAllow, raw)
    if (r.ok) {
      draftAllow = r.list
      if (fromBox) addText = ''
      edited()
    } else if (!fromBox) addMsg = r.reason
    return r.ok
  }
  // A chip that was pressed is gone: focus the chip that took its place, else the add box
  async function addChip(repo: string, at: number) {
    if (!add(repo, false)) return
    await tick()
    const left = chipsEl?.querySelectorAll('button') ?? []
    const k = nextFocusIndex(left.length, at)
    if (k < 0) addInput?.focus()
    else (left[k] as HTMLElement).focus()
  }
  async function remove(p: string) {
    const at = draftAllow.indexOf(p)
    draftAllow = removePattern(draftAllow, p)
    edited()
    await tick()
    const left = listEl?.querySelectorAll('button[data-remove]') ?? []
    const k = nextFocusIndex(left.length, at)
    if (k < 0) addInput?.focus()
    else (left[k] as HTMLElement).focus()
  }
  // A button a repo was already added by stays focusable (aria-disabled), so pressing it does not lose the person's place
  function addFromBrowse(repo: string) {
    if (!covered(repo, draftAllow)) add(repo, false)
  }
  function submitAdd(e: SubmitEvent) {
    e.preventDefault()
    touched = true
    if (check.state === 'ok') add(addText, true)
  }

  async function savePatternsNow() {
    if (!view || patProblem || patSaving) return
    patSaving = true
    patError = null
    const sent = [...draftAllow]
    const body = savePatterns(view.settings.repos, sent)
    const r = await putSection('repos', body)
    patSaving = false
    // a refusal keeps the draft and shows the server's words
    if (!r.ok) return void (patError = r.error)
    // The server has it. Show what was sent as saved, whether or not the read-back that follows works or is overtaken.
    if (view) view = withSavedRepos(view, body)
    draftAllow = sent
    patWrote = wrotePatterns(sent)
    await load('patterns')
  }

  // ---- BROWSE GITHUB: the one GitHub call this page can make, and only when pressed ----
  let browse = $state<{ state: 'idle' | 'loading' | 'done' | 'error'; owners: Owner[]; failed: string[]; error: string }>({ state: 'idle', owners: [], failed: [], error: '' })
  let expanded = $state<Record<string, boolean>>({})
  async function browseNow() {
    if (browse.state === 'loading') return
    browse = { state: 'loading', owners: [], failed: [], error: '' }
    const r = await browseGithub()
    browse = r.ok ? { state: 'done', owners: r.owners, failed: r.failed, error: '' } : { state: 'error', owners: [], failed: [], error: r.error }
  }

  // ---- Local clones ----
  let cloneDraft = $state<Record<string, string>>({})
  let rootDraft = $state<string | null>(null)
  let cloneSaving = $state(false)
  let cloneError = $state<string | null>(null)
  let cloneWrote = $state<string | null>(null)

  const rows = $derived(view?.clones ?? [])
  const savedFolder = (repo: string) => view?.settings.repos.clones[repo.toLowerCase()] ?? ''
  const folderOf = (repo: string) => cloneDraft[repo] ?? savedFolder(repo)
  const rootOf = $derived(rootDraft ?? view?.settings.repos.cloneRoot ?? '')
  const problems = $derived(Object.fromEntries(rows.map((r) => [r.repo, folderProblem(folderOf(r.repo))])))
  const rootProblem = $derived(folderProblem(rootOf))
  const cloneDirty = $derived(rows.some((r) => folderOf(r.repo).trim() !== savedFolder(r.repo)) || rootOf.trim() !== (view?.settings.repos.cloneRoot ?? ''))
  const cloneBlocked = $derived(rootProblem !== null || rows.some((r) => problems[r.repo] !== null))

  async function saveClonesNow() {
    if (!view || cloneBlocked || cloneSaving) return
    cloneSaving = true
    cloneError = null
    const drafts = Object.fromEntries(rows.map((r) => [r.repo, folderOf(r.repo)]))
    const body = saveClones(view.settings.repos, rows.map((r) => r.repo), drafts, rootOf)
    const r = await putSection('repos', body)
    cloneSaving = false
    if (!r.ok) return void (cloneError = r.error)
    // as for the patterns: what was sent is what was saved, and the drafts start over from it
    if (view) view = withSavedRepos(view, body)
    cloneDraft = {}
    rootDraft = null
    cloneWrote = wroteClones(body.clones, body.cloneRoot)
    await load('clones')
  }
  function cloneEdited() {
    cloneError = null
    cloneWrote = null
  }

  // ---- Loading ----
  let focused = false
  let syncedTo: boolean | null = null
  // `reset` says which drafts start over from what the server now holds; an unsaved edit in the other block stays
  let latest = 0
  async function load(reset: 'all' | 'patterns' | 'clones' | 'none') {
    const mine = ++latest
    const r = await getSettings()
    // an older answer that lands after a newer request would put an old view back
    if (mine !== latest) return
    if (!r.ok) {
      // a failed background read leaves the page as it is
      if (!view || reset === 'all') loadError = r.error
      return
    }
    loadError = null
    view = r.view
    syncedTo = live.snap?.setup ?? null
    if (reset === 'all' || reset === 'patterns') draftAllow = r.view.settings.repos.allow ?? []
    if (reset === 'all' || reset === 'clones') {
      cloneDraft = {}
      rootDraft = null
    }
    // first run puts the keyboard at the first step (Services) where there is a keyboard to type with; a phone would open its keyboard over the page
    if (reset === 'all' && r.view.setup && !focused) {
      focused = true
      if (matchMedia('(hover: hover) and (pointer: fine)').matches) {
        await tick()
        document.getElementById('services-h')?.focus()
      }
    }
  }
  void load('all')

  // Setup changed under this page (the file was edited or deleted, or saved in another tab): read it again, keeping the drafts
  $effect(() => {
    const s = live.snap?.setup
    if (view && typeof s === 'boolean' && s !== view.setup && s !== syncedTo && !patSaving && !cloneSaving) {
      syncedTo = s
      void load('none')
    }
  })

  // A session started or ended: the repos conn has seen, and their clones, may have changed. Read them again, keeping the drafts
  let sessionCount: number | undefined
  $effect(() => {
    const n = live.snap?.sessions.length
    if (n === undefined || n === sessionCount) return
    const first = sessionCount === undefined
    sessionCount = n
    if (view && !first && !patSaving && !cloneSaving) void load('none')
  })

</script>

<svelte:head><title>Settings</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 24px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) var(--look-head-fill, 72%), transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="settings" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span><span><span class="cap">Link established</span> · {location.host}</span></div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span><span><span class="cap">Link lost</span> · {location.host}</span></div>
    {/if}
  </header>

  <main class="settings" aria-label="Settings">
    <h1 class="hud cap">Settings</h1>

    {#if loadError}
      <div class="notice bad" role="alert">
        <p data-content>{plainText(loadError)}</p>
        <button type="button" class="btn cap" onclick={() => load('all')}>Try again</button>
      </div>
    {:else if !view}
      <p class="sub" role="status">Reading settings…</p>
    {:else}

      {#if view.backedUp}
        <div class="notice" role="status">
          <p>Your old settings file could not be used, so conn kept it as <code data-content>{plainText(view.backedUp.split('/').pop() ?? 'settings.json.bak')}</code> next to the new one before saving. Nothing in it was lost.</p>
        </div>
      {/if}

      {#if view.dropped.length}
        <div class="notice warn" role="status">
          <p>conn ignored these parts of settings.json, so nothing they said is in effect. Saving the section they came from writes the file again without them.</p>
          <ul class="dropped" data-content>
            {#each view.dropped as d (d)}<li>{plainText(d)}</li>{/each}
          </ul>
        </div>
      {/if}

      {#if setup}
        <section class="surf card first" aria-labelledby="first-h" style="--rim: color-mix(in oklch, var(--ac) 55%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--ac) 9%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
          <h2 id="first-h" class="hud cap">First launch</h2>
          <p class="lead"><b>Choose what conn may read.</b> Pick the repositories below. This page makes no GitHub call until you press Browse GitHub.</p>
          <ol class="steps" aria-label="Steps">
            <li><b class="cap">1 Services</b></li>
            <li><b class="cap">2 Repositories</b></li>
          </ol>
        </section>
      {/if}

      <!-- One place for the Services block, whatever setup says: ending setup moves it (CSS order) and never rebuilds it, so a draft in it survives -->
      <div class="svc" class:later={!setup}>
        <SettingsServices {view} {setup} apply={(v) => (view = v)} reload={() => load('none')} />
      </div>

      <section class="surf card" aria-labelledby="repos-h" style="--rim: {setup ? 'color-mix(in oklch, var(--ac) 55%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, {setup ? 'var(--ac)' : 'var(--edge)'} {setup ? '9%' : '6%'}, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
        <h2 id="repos-h" class="hud cap">{#if setup}Step 2: Repositories{:else}Repositories{/if}</h2>
        <p class="sub">{setup ? 'Add owner/repo or owner/*. conn only reads what matches.' : 'conn reads only these. Anything else is never fetched, scanned or stored.'}</p>

        {#if draftAllow.length}
          <ul class="rows" aria-label="Allowed repositories" bind:this={listEl}>
            {#each draftAllow as p (p)}
              <li class="row">
                <code data-content class="pat">{plainText(p)}</code>
                <span class="n">{matchedText(matched(p))}</span>
                <button type="button" class="btn small cap" data-remove onclick={() => remove(p)} aria-label="Remove {plainText(p)}">Remove</button>
              </li>
            {/each}
          </ul>
        {/if}

        <form class="field" onsubmit={submitAdd}>
          <label class="sr" for="add-repo">Repository to allow</label>
          <input id="add-repo" bind:this={addInput} bind:value={addText} type="text" class="fld" placeholder="acme/* or jane/widgets" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="140" aria-describedby="add-hint" aria-invalid={showErr} onblur={() => (touched = true)} onkeydown={(e) => { if (e.key === 'Enter') touched = true }} />
          <button type="submit" class="btn primary cap" disabled={check.state !== 'ok'}>Add</button>
          <button type="button" class="btn cap" onclick={browseNow} aria-busy={browse.state === 'loading'} disabled={browse.state === 'loading'}>{#if browse.state === 'loading'}Asking GitHub…{:else}Browse GitHub{/if}</button>
        </form>
        <div id="add-hint">
          {#if showErr && check.state === 'error'}
            <p class="hint err" role="alert">{check.reason}</p>
          {:else}
            <p class="hint">{#if check.state === 'ok'}Will add <code data-content>{check.pattern}</code>{:else}Up to {MAX_PATTERNS} patterns. An owner and a star, as <code>acme/*</code>, covers every repo of that owner.{/if}</p>
          {/if}
        </div>
        {#if addMsg}<p class="err" role="alert">{addMsg}</p>{/if}

        {#if chips.length}
          <div class="chips" role="group" aria-label="Repositories conn has seen" bind:this={chipsEl}>
            {#each chips as repo, i (repo)}
              <button type="button" class="chip" data-content onclick={() => addChip(repo, i)} title="Seen in a session, a PR or History">+ {plainText(repo)}</button>
            {/each}
          </div>
          <p class="hint">Suggested from folders your sessions ran in and PRs conn has listed. Browse GitHub lists what your <code>gh</code> login can reach, and runs only when you press it.</p>
        {:else}
          <p class="hint">Browse GitHub lists what your <code>gh</code> login can reach, and runs only when you press it.</p>
        {/if}

        {#if browse.state === 'error'}
          <p class="err" role="alert" data-content>{plainText(browse.error)}</p>
        {:else if browse.state === 'done'}
          {#if browse.failed.length}
            <p class="err" role="alert" data-content>GitHub could not list: {browse.failed.map(plainText).join(', ')}</p>
          {/if}
          {#if browse.owners.length === 0}
            <p class="hint" role="status">GitHub listed no repositories for this login.</p>
          {:else}
            <ul class="owners" aria-label="GitHub owners">
              {#each browse.owners as o (o.owner)}
                <li class="owner">
                  <div class="orow">
                    <code data-content class="pat">{plainText(o.owner)}</code>
                    <span class="n">{ownerCountText(o.repos.length)}</span>
                    <button type="button" class="btn small" data-content onclick={() => addFromBrowse(`${o.owner}/*`)} aria-disabled={draftAllow.includes(`${o.owner.toLowerCase()}/*`)}>{draftAllow.includes(`${o.owner.toLowerCase()}/*`) ? 'Added ' : 'Add '}{plainText(o.owner)}/*</button>
                    {#if o.repos.length}
                      <button type="button" class="btn small cap" aria-expanded={!!expanded[o.owner]} onclick={() => (expanded[o.owner] = !expanded[o.owner])}>{#if expanded[o.owner]}Hide repos{:else}Show repos{/if}</button>
                    {/if}
                  </div>
                  {#if expanded[o.owner]}
                    <div class="chips">
                      {#each o.repos as repo (repo)}
                        <button type="button" class="chip" class:in={covered(repo, draftAllow)} data-content aria-disabled={covered(repo, draftAllow)} onclick={() => addFromBrowse(repo)}>{covered(repo, draftAllow) ? '' : '+ '}{plainText(repo)}</button>
                      {/each}
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        {/if}

        <div class="save">
          <button type="button" class="btn primary cap" onclick={savePatternsNow} disabled={!!patProblem || (!patDirty && !view.dropped.length) || patSaving} aria-busy={patSaving}>{#if patSaving}Saving…{:else if setup && patProblem}Add a repository to continue{:else if setup}Save and start{:else}Save list{/if}</button>
          {#if patProblem && !setup}<span class="hint">{patProblem}: a list cannot be empty.</span>{/if}
          {#if patError}<span class="err" role="alert" data-content>{plainText(patError)}</span>{/if}
          {#if patWrote && !patError}<span class="ok" role="status" data-content>{plainText(patWrote)}</span>{/if}
        </div>
      </section>

      <!-- Shown at first launch too, so turning alerts on is offered from the start -->
      <SettingsAlerts />

      {#if !setup}
        <!-- Inside Conn.app only: the component shows nothing in a browser -->
        <SettingsAppIcon />

        <section class="surf card clones" aria-labelledby="clones-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
          <h2 id="clones-h" class="hud cap">Local clones</h2>
          <p class="sub">Reviews and builds work in a git worktree of a clone. conn looks in the clones folder, then in <code>~/projects/&lt;repo&gt;</code>, and uses a folder only when its origin is that repo.</p>

          <div class="field">
            <label class="lab" for="clone-root">Clones live in</label>
            <input id="clone-root" class="fld" type="text" value={rootOf} oninput={(e) => { rootDraft = e.currentTarget.value; cloneEdited() }} placeholder="/Users/jane/work" autocomplete="off" spellcheck="false" aria-invalid={rootProblem !== null} aria-describedby="clone-root-err" />
          </div>
          <p id="clone-root-err" class="err">{rootProblem ?? ''}</p>

          {#if rows.length}
            <ul class="rows" aria-label="Clone folders">
              {#each rows as r (r.repo)}
                <li class="clone">
                  <div class="orow">
                    <code data-content class="pat">{plainText(r.repo)}</code>
                    {#if r.path}
                      <span class="found">Found: <code data-content>{plainText(r.path)}</code> {#if howText(r.how)}<span class="how">({howText(r.how)})</span>{/if}</span>
                    {:else}
                      <span class="miss">No clone found. Start review is unavailable until you set a folder.</span>
                    {/if}
                  </div>
                  <label class="sr" for="clone-{r.repo}">Clone folder for {plainText(r.repo)}</label>
                  <input id="clone-{r.repo}" class="fld" type="text" value={folderOf(r.repo)} oninput={(e) => { cloneDraft[r.repo] = e.currentTarget.value; cloneEdited() }} placeholder="/path/to/{plainText(r.repo.split('/')[1] ?? 'repo')}" autocomplete="off" spellcheck="false" aria-invalid={problems[r.repo] !== null} aria-describedby="clone-err-{r.repo}" />
                  <p id="clone-err-{r.repo}" class="err">{problems[r.repo] ?? ''}</p>
                </li>
              {/each}
            </ul>
          {:else}
            <p class="hint">No repository has shown up yet. A repo appears here once a session runs in it or My PRs lists one.</p>
          {/if}

          <div class="save">
            <button type="button" class="btn primary cap" onclick={saveClonesNow} disabled={(!cloneDirty && !view.dropped.length) || cloneBlocked || cloneSaving} aria-busy={cloneSaving}>{#if cloneSaving}Saving…{:else}Save folders{/if}</button>
            {#if cloneError}<span class="err" role="alert" data-content>{plainText(cloneError)}</span>{/if}
            {#if cloneWrote && !cloneError}<span class="ok" role="status" data-content>{plainText(cloneWrote)}</span>{/if}
          </div>
        </section>

        <SettingsProvided />

        <ReviewLegacy allow={view.settings.repos.allow} seen={view.seen.map((s) => s.repo)} />
      {/if}
    {/if}
  </main>
</div>

<style>
  /* Tokens only, so the page reads in every theme */
  .settings {
    position: relative;
    z-index: 6; /* above the faint scanline layer */
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 16px;
    width: 100%;
    max-width: 980px;
    box-sizing: border-box;
    margin: 0 auto;
    padding: 24px 32px 48px;
  }
  h1 {
    margin: 0;
    font-size: 20px;
    font-weight: 700;
    color: var(--bright);
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
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  /* Services sits above Repositories on first run and below it after: one element, moved by order */
  .svc {
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
  }
  .svc.later {
    order: 1;
  }
  .clones {
    order: 2;
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
  .lead {
    font-size: 14px;
    line-height: 1.5;
    color: var(--tx);
  }
  .lead b {
    color: var(--bright);
    font-weight: 600;
  }
  .steps {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 18px;
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 12px;
    color: var(--mu);
  }
  .steps b {
    color: color-mix(in oklab, var(--ac) 40%, var(--bright));
    font-weight: 600;
  }
  .rows,
  .owners {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .row,
  .owner,
  .clone {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 12px;
    padding: 8px 0;
    border-top: 1px var(--border-style) var(--line);
  }
  .rows > :first-child,
  .owners > :first-child {
    border-top: 0;
  }
  .owner,
  .clone {
    flex-direction: column;
    align-items: stretch;
  }
  .orow {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 12px;
    min-width: 0;
  }
  .pat {
    font-size: 13px;
  }
  .n {
    margin-left: auto;
    color: var(--mu);
    font-size: 12px;
  }
  .orow .n {
    margin-left: 0;
  }
  .field {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
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
  /* in a column the 200px flex basis would be a height */
  .clone .fld {
    flex: none;
    width: 100%;
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
  .btn.primary {
    color: var(--onac);
    background: var(--ac);
    border-color: var(--ac);
    font-weight: 600;
  }
  .btn.primary:hover:not(:disabled):not([aria-disabled='true']) {
    background: var(--ac);
    filter: brightness(1.1);
  }
  .btn:disabled,
  .btn[aria-disabled='true'] {
    cursor: default;
    opacity: 0.5;
  }
  .btn[aria-busy='true'] {
    cursor: wait;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .chip {
    min-height: 36px;
    max-width: 100%;
    padding: 0 12px;
    color: var(--mu);
    background: transparent;
    border: 1px dashed var(--line2);
    border-radius: 999px;
    font-family: var(--font-mono), monospace;
    font-size: 12px;
    overflow-wrap: anywhere;
    cursor: pointer;
  }
  .chip:hover:not(:disabled):not([aria-disabled='true']) {
    color: var(--tx);
    border-style: solid;
  }
  .chip.in {
    color: var(--tx);
    border-style: solid;
    cursor: default;
  }
  .save {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 14px;
    margin-top: 6px;
  }
  .found,
  .miss,
  .ok,
  .err {
    font-size: 12px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .found {
    color: color-mix(in oklab, var(--ok) 60%, var(--bright));
  }
  .how {
    color: var(--mu);
  }
  .miss {
    color: color-mix(in oklab, var(--warn) 60%, var(--bright));
  }
  .ok {
    color: color-mix(in oklab, var(--ok) 60%, var(--bright));
  }
  .err {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  .err:empty {
    display: none;
  }
  .hint.err {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  .notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 14px;
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
    flex-direction: column;
    align-items: stretch;
  }
  .dropped {
    margin: 0;
    padding-left: 18px;
    color: var(--mu);
    overflow-wrap: anywhere;
  }
  .notice.bad {
    border-color: var(--bad);
    overflow-wrap: anywhere;
  }

  @media (max-width: 600px) {
    .card {
      padding: 14px;
    }
    .n {
      margin-left: 0;
    }
  }
  @media (prefers-reduced-motion: no-preference) {
    .btn,
    .chip,
    .fld {
      transition: border-color 0.16s, background 0.16s, color 0.16s;
    }
  }
</style>
