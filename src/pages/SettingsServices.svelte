<script lang="ts">
  // The Services cards (GitHub, Slack, Atlassian), the Reviews sources block and where sessions open. A card's switch and settings are a draft until its own
  // save button is pressed (one PUT of its own section, nothing on a timer, nothing on input). CHECK is the only thing here that asks
  // the server to probe a module, and only a press does. The page owns the view; this block reports back what it saved or checked.
  import { tick, untrack } from 'svelte'
  import { plainText } from '../lib/ask.ts'
  import { checkModule, putSection, withSavedSection, withStatus, type ModuleName, type SettingsView } from '../lib/settingsApi.ts'
  import {
    STATE_TONES,
    REVIEW_MOVES,
    SESSION_LAYOUTS,
    atlassianSection,
    baseBranchProblems,
    baseBranchRepos,
    buildRepoOptions,
    covered,
    draftChannelOptions,
    listsSentence,
    parseBaseBranch,
    parseChannel,
    parseJiraKey,
    sameAtlassian,
    sameSlack,
    slackReads,
    stateWord,
    slackSection,
    statusLine,
    wroteAtlassian,
    wroteGithub,
    wroteReviews,
    wroteSlack,
    type AtlassianSettings,
    type ModuleStatus,
    type ReviewMove,
    type SessionLayout,
    type SlackSettings,
  } from '../lib/settingsModel.ts'
  import { WHERE_CHOICES, sameSessions, sessionsSection, wroteSessions, type SessionsSettings } from '../lib/settingsSessions.ts'
  import { ADVISOR_CHOICES, advisorOf } from '../lib/advisor.ts'
  import SettingsNameList from './SettingsNameList.svelte'

  let { view, setup, apply, reload }: { view: SettingsView; setup: boolean; apply: (v: SettingsView) => void; reload: () => Promise<void> } = $props()

  const NAMES: Record<ModuleName, string> = { github: 'GitHub', slack: 'Slack', atlassian: 'Atlassian' }
  const OFF_TEXT = 'Off: conn makes no calls'

  const copySlack = (s: SlackSettings): SlackSettings => ({ ...s, channels: [...s.channels] })
  const copyAtl = (a: AtlassianSettings): AtlassianSettings => ({
    enabled: a.enabled,
    jira: { projects: [...a.jira.projects], buildRepo: a.jira.buildRepo, baseBranch: a.jira.baseBranch ?? null, baseBranches: { ...a.jira.baseBranches }, reviewMove: a.jira.reviewMove, sessionLayout: a.jira.sessionLayout },
  })

  // ---- drafts: what is on screen; what the server holds is view.settings ----
  let ghOn = $state(untrack(() => view.settings.github.enabled))
  let slack = $state<SlackSettings>(untrack(() => copySlack(view.settings.slack)))
  let atl = $state<AtlassianSettings>(untrack(() => copyAtl(view.settings.atlassian)))
  let requested = $state(untrack(() => view.settings.reviews.requested))
  let allOpen = $state(untrack(() => view.settings.reviews.allOpen))
  let where = $state(untrack(() => view.settings.sessions.where))
  let resume = $state(untrack(() => view.settings.sessions.resumeOnLaunch))
  let sessionsAdvisor = $state(untrack(() => view.settings.sessions.advisor))
  const sessionsDraft = $derived<SessionsSettings>({ where, resumeOnLaunch: resume, advisor: sessionsAdvisor })

  // A draft nobody has touched follows what the server now holds (a save from another tab); an edited one stays.
  let baseGh = untrack(() => view.settings.github)
  let baseSlack = untrack(() => view.settings.slack)
  let baseAtl = untrack(() => view.settings.atlassian)
  let baseRev = untrack(() => view.settings.reviews)
  let baseSessions = untrack(() => view.settings.sessions)
  $effect(() => {
    const g = view.settings.github
    if (g === baseGh) return
    if (untrack(() => ghOn) === baseGh.enabled) ghOn = g.enabled
    baseGh = g
  })
  $effect(() => {
    const s = view.settings.slack
    if (s === baseSlack) return
    if (untrack(() => sameSlack(slack, baseSlack))) slack = copySlack(s)
    baseSlack = s
  })
  $effect(() => {
    const a = view.settings.atlassian
    if (a === baseAtl) return
    if (untrack(() => sameAtlassian(atl, baseAtl))) atl = copyAtl(a)
    baseAtl = a
  })
  $effect(() => {
    const r = view.settings.reviews
    if (r === baseRev) return
    if (untrack(() => requested === baseRev.requested && allOpen === baseRev.allOpen)) {
      requested = r.requested
      allOpen = r.allOpen
    }
    baseRev = r
  })

  $effect(() => {
    const held = view.settings.sessions
    if (held === baseSessions) return
    if (untrack(() => sameSessions(sessionsDraft, baseSessions))) {
      where = held.where
      resume = held.resumeOnLaunch
      sessionsAdvisor = held.advisor
    }
    baseSessions = held
  })

  const ghDirty = $derived(ghOn !== view.settings.github.enabled)
  const slackDirty = $derived(!sameSlack(slack, view.settings.slack))
  const atlDirty = $derived(!sameAtlassian(atl, view.settings.atlassian))
  const revDirty = $derived(requested !== view.settings.reviews.requested || allOpen !== view.settings.reviews.allOpen)
  const sessionsDirty = $derived(!sameSessions(sessionsDraft, view.settings.sessions))

  // ---- the status line: what the server holds for a saved-on module, else the off line ----
  const savedOn = $derived<Record<ModuleName, boolean>>({ github: view.settings.github.enabled, slack: view.settings.slack.enabled, atlassian: view.settings.atlassian.enabled })
  let now = $state(Date.now())
  $effect(() => {
    // so "3 min ago" moves; a clock only, it never asks the server anything
    const t = setInterval(() => (now = Date.now()), 30_000)
    return () => clearInterval(t)
  })
  function shown(name: ModuleName): ModuleStatus {
    if (!savedOn[name]) return { state: 'off', detail: OFF_TEXT, at: null }
    const s = view.modules[name]
    return s.state === 'off' ? { state: 'unchecked', detail: 'Not checked yet', at: null } : s
  }

  // ---- CHECK ----
  // what a CHECK answered, said once to a screen reader; the status lines themselves carry a clock and are not live
  let announce = $state('')
  let checking = $state<Record<ModuleName, boolean>>({ github: false, slack: false, atlassian: false })
  let checkError = $state<Record<ModuleName, string | null>>({ github: null, slack: null, atlassian: null })
  async function check(name: ModuleName) {
    if (checking[name] || !savedOn[name]) return
    checking[name] = true
    checkError[name] = null
    const r = await checkModule(name)
    checking[name] = false
    if (!r.ok) return void (checkError[name] = r.error)
    apply(withStatus(view, name, r.status))
    // cleared first, so the same answer twice is said twice
    announce = ''
    await tick()
    announce = `${NAMES[name]}: ${stateWord(r.status.state)} ${r.status.detail}`.replace(/\s+/g, ' ')
  }

  // ---- saves: one section each, only from its button ----
  type Section = 'github' | 'slack' | 'atlassian' | 'reviews' | 'sessions'
  let saving = $state<Record<Section, boolean>>({ github: false, slack: false, atlassian: false, reviews: false, sessions: false })
  let saveError = $state<Record<Section, string | null>>({ github: null, slack: null, atlassian: null, reviews: null, sessions: null })
  let wrote = $state<Record<Section, string | null>>({ github: null, slack: null, atlassian: null, reviews: null, sessions: null })
  const edited = (s: Section) => {
    saveError[s] = null
    wrote[s] = null
  }

  async function saveGithub() {
    if (saving.github) return
    saving.github = true
    saveError.github = null
    const body = { enabled: ghOn }
    const r = await putSection('github', body)
    saving.github = false
    if (!r.ok) return void (saveError.github = r.error)
    apply(withSavedSection(view, 'github', body))
    wrote.github = wroteGithub(body.enabled)
    await reload()
  }
  async function saveSlack() {
    if (saving.slack) return
    saving.slack = true
    saveError.slack = null
    const body = slackSection(slack)
    const sent = copySlack(slack)
    const r = await putSection('slack', body)
    saving.slack = false
    if (!r.ok) return void (saveError.slack = r.error)
    // what was typed while the save was in flight stays: the draft is put back to the saved form only if it is still what was sent
    if (sameSlack(slack, sent)) slack = copySlack(body)
    apply(withSavedSection(view, 'slack', body))
    wrote.slack = wroteSlack(body)
    await reload()
  }
  async function saveAtlassian() {
    if (saving.atlassian) return
    saving.atlassian = true
    saveError.atlassian = null
    const body = atlassianSection(atl)
    const sent = copyAtl(atl)
    const r = await putSection('atlassian', body)
    saving.atlassian = false
    if (!r.ok) return void (saveError.atlassian = r.error)
    if (sameAtlassian(atl, sent)) atl = copyAtl(body)
    apply(withSavedSection(view, 'atlassian', body))
    wrote.atlassian = wroteAtlassian(body)
    await reload()
  }
  async function saveReviews() {
    if (saving.reviews) return
    saving.reviews = true
    saveError.reviews = null
    const body = { requested, allOpen }
    const r = await putSection('reviews', body)
    saving.reviews = false
    if (!r.ok) return void (saveError.reviews = r.error)
    apply(withSavedSection(view, 'reviews', body))
    wrote.reviews = wroteReviews(body)
    await reload()
  }
  async function saveSessions() {
    if (saving.sessions) return
    saving.sessions = true
    saveError.sessions = null
    const body = sessionsSection(sessionsDraft)
    const r = await putSection('sessions', body)
    saving.sessions = false
    if (!r.ok) return void (saveError.sessions = r.error)
    apply(withSavedSection(view, 'sessions', body))
    wrote.sessions = wroteSessions(body)
    await reload()
  }

  // ---- derived text and choices ----
  const draftOptions = $derived(draftChannelOptions(slack.channels))
  const buildOptions = $derived(buildRepoOptions(view.settings.repos.allow, view.clones, atl.jira.buildRepo))
  // the base branch as typed: a name the server would refuse is said under the box, and the card's Save waits for a good one
  const baseCheck = $derived(parseBaseBranch(atl.jira.baseBranch ?? ''))
  const baseProblem = $derived(baseCheck.ok ? null : baseCheck.reason)
  // each repository's own base branch as typed: the repositories that get a box, and the ones whose text a save would refuse
  const baseRepos = $derived(baseBranchRepos(view.settings.repos.allow, view.clones, atl.jira.baseBranches))
  const repoProblems = $derived(baseBranchProblems(atl.jira.baseBranches))
  const anyBaseProblem = $derived(baseProblem !== null || Object.keys(repoProblems).length > 0)
  const setReviewMove = (value: string) => {
    atl.jira.reviewMove = (REVIEW_MOVES.find((m) => m.value === value)?.value ?? 'all') satisfies ReviewMove
    edited('atlassian')
  }
  const setSessionLayout = (value: string) => {
    atl.jira.sessionLayout = (SESSION_LAYOUTS.find((l) => l.value === value)?.value ?? 'shared') satisfies SessionLayout
    edited('atlassian')
  }
  // a chosen build repo the list no longer covers stays on screen, named, and the card says why a save will be refused
  const buildStale = $derived(atl.jira.buildRepo !== null && !covered(atl.jira.buildRepo, view.settings.repos.allow))
  // the modules and Slack's channels as they are SAVED (they are what conn does now); the two review switches as edited, which is the point of a live sentence
  const sources = $derived({ github: view.settings.github.enabled, slackOn: view.settings.slack.enabled, channels: view.settings.slack.channels, dms: view.settings.slack.dms, requested, allOpen })
  const reads = $derived(slackReads(sources))
  const lists = $derived(listsSentence(sources))
  const checkTitle = (name: ModuleName) => (name === 'github' ? 'Runs gh auth status once' : `Runs one short ${NAMES[name]} check through claude, once`)
  const pending = (name: ModuleName, draft: boolean) => (draft === savedOn[name] ? null : `Save to turn ${NAMES[name]} ${draft ? 'on' : 'off'}.`)
</script>

{#snippet sw(on: boolean, label: string, toggle: () => void, labelledby?: string)}
  <button type="button" role="switch" class="sw" class:on aria-checked={on} aria-label={labelledby ? undefined : label} aria-labelledby={labelledby} onclick={toggle}><span class="knob" aria-hidden="true"></span></button>
{/snippet}

{#snippet status(name: ModuleName)}
  {@const s = shown(name)}
  <div class="stat">
    <p class="st {STATE_TONES[s.state]}"><span class="dot" aria-hidden="true"></span><span data-content>{plainText(statusLine(s, now))}</span></p>
    <button type="button" class="btn small cap" onclick={() => check(name)} aria-disabled={!savedOn[name] || checking[name]} aria-busy={checking[name]} title={checkTitle(name)}>{#if checking[name]}Checking…{:else}Check{/if}</button>
  </div>
  {#if checkError[name]}<p class="err" role="alert" data-content>{plainText(checkError[name] ?? '')}</p>{/if}
{/snippet}

{#snippet saveRow(section: Section, dirty: boolean, onsave: () => void)}
  <div class="save">
    <button type="button" class="btn primary cap" onclick={() => dirty && !saving[section] && onsave()} aria-disabled={!dirty || saving[section]} aria-busy={saving[section]}>{#if saving[section]}Saving…{:else}Save{/if}</button>
    {#if saveError[section]}<span class="err" role="alert" data-content>{plainText(saveError[section] ?? '')}</span>{/if}
    {#if wrote[section] && !saveError[section] && !dirty}<span class="ok" role="status" data-content>{plainText(wrote[section] ?? '')}</span>{/if}
  </div>
{/snippet}

<p class="sr" role="status" data-content>{plainText(announce)}</p>

<section class="surf card" aria-labelledby="services-h" style="--rim: {setup ? 'color-mix(in oklch, var(--ac) 55%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, {setup ? 'var(--ac)' : 'var(--edge)'} {setup ? '9%' : '6%'}, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
  <h2 id="services-h" class="hud cap" tabindex="-1">{#if setup}Step 1: Services{:else}Services{/if}</h2>
  <p class="sub">Switch a service off and conn stops calling it and hides the screens that need it. Each card saves on its own.</p>

  <div class="grid">
    <!-- GitHub -->
    <article class="mod" class:off={!ghOn} aria-labelledby="mod-github">
      <header class="mhead">
        {@render sw(ghOn, 'GitHub', () => { ghOn = !ghOn; edited('github') }, 'mod-github')}
        <h3 id="mod-github" class="hud cap">GitHub</h3>
      </header>
      {@render status('github')}
      {#if pending('github', ghOn)}<p class="hint">{pending('github', ghOn)}</p>{/if}
      <p class="info"><b>Reads:</b> your open PRs and the PRs that ask for your review, in the repositories you allow. Needs the <code>gh</code> CLI signed in.</p>
      <p class="info"><b>Writes:</b> a reply to a review thread and its resolve on your own PR, only when you press that button on My PRs.</p>
      <p class="info"><b>Off stops:</b> My PRs (replies and resolves too), Reviews and the PR links on sessions. History and Sessions stay.</p>
      {#if !savedOn.github}<p class="off-note">{OFF_TEXT}</p>{/if}
      {@render saveRow('github', ghDirty, saveGithub)}
    </article>

    <!-- Slack -->
    <article class="mod" class:off={!slack.enabled} aria-labelledby="mod-slack">
      <header class="mhead">
        {@render sw(slack.enabled, 'Slack', () => { slack.enabled = !slack.enabled; edited('slack') }, 'mod-slack')}
        <h3 id="mod-slack" class="hud cap">Slack</h3>
      </header>
      {@render status('slack')}
      {#if pending('slack', slack.enabled)}<p class="hint">{pending('slack', slack.enabled)}</p>{/if}
      <p class="info"><b>Reads:</b> the channels below and your DMs, for posts that link a PR (a short metered <code>claude -p</code> run per scan, through your Slack connector).</p>
      <p class="info"><b>Writes:</b> a Slack draft of a ready-for-review message, never sent, when My PRs makes one.</p>
      <p class="info"><b>Off stops:</b> the channel scan, the DM scan and that draft. Reviews then lists from GitHub alone.</p>
      {#if !savedOn.slack}<p class="off-note">{OFF_TEXT}</p>{/if}
      <fieldset class="mset" disabled={!slack.enabled}>
        <legend class="sr">Slack settings</legend>
        <SettingsNameList
          id="slack-channels"
          label="Channels to scan for review requests"
          noun="channel"
          placeholder="#code-review"
          items={slack.channels}
          parse={parseChannel}
          disabled={!slack.enabled}
          onchange={(next) => { slack = slackSection({ ...slack, channels: next, draftChannel: slack.draftChannel }); edited('slack') }}
        />
        <div class="row">
          {@render sw(slack.dms, 'Scan my DMs for review requests', () => { slack.dms = !slack.dms; edited('slack') }, 'slack-dms-lab')}
          <span id="slack-dms-lab">Scan my DMs for review requests</span>
        </div>
        <div class="selrow">
          <label class="lab" for="slack-draft">Draft ready-for-review messages in</label>
          {#if draftOptions.length}
            <select id="slack-draft" class="sel" value={slack.draftChannel ?? ''} onchange={(e) => { slack.draftChannel = e.currentTarget.value || null; edited('slack') }}>
              {#each draftOptions as o (o.value ?? '')}<option value={o.value ?? ''}>{plainText(o.label)}</option>{/each}
            </select>
          {:else}
            <select id="slack-draft" class="sel" disabled><option>No channel yet</option></select>
            <p class="hint">With no channel My PRs has no draft button.</p>
          {/if}
        </div>
      </fieldset>
      {@render saveRow('slack', slackDirty, saveSlack)}
    </article>

    <!-- Atlassian -->
    <article class="mod" class:off={!atl.enabled} aria-labelledby="mod-atlassian">
      <header class="mhead">
        {@render sw(atl.enabled, 'Atlassian', () => { atl.enabled = !atl.enabled; edited('atlassian') }, 'mod-atlassian')}
        <h3 id="mod-atlassian" class="hud cap">Atlassian</h3>
      </header>
      {@render status('atlassian')}
      {#if pending('atlassian', atl.enabled)}<p class="hint">{pending('atlassian', atl.enabled)}</p>{/if}
      <p class="info"><b>Reads:</b> Jira tickets in the projects below, for Open work (a metered <code>claude -p</code> run, through your Atlassian connector).</p>
      <p class="info"><b>Writes:</b> a ticket's status, only when you press MOVE JIRA TO.</p>
      <p class="info"><b>Off stops:</b> Open work, ticket sessions and the PR OR TICKET filter, and ticket links.</p>
      {#if !savedOn.atlassian}<p class="off-note">{OFF_TEXT}</p>{/if}
      <fieldset class="mset" disabled={!atl.enabled}>
        <legend class="sr">Atlassian settings</legend>
        <SettingsNameList
          id="jira-projects"
          label="Jira project keys"
          noun="project key"
          placeholder="PROJ"
          items={atl.jira.projects}
          parse={parseJiraKey}
          disabled={!atl.enabled}
          onchange={(next) => { atl.jira.projects = next; edited('atlassian') }}
        />
        <div class="selrow">
          <label class="lab" for="jira-build">Default repository (optional)</label>
          <select id="jira-build" class="sel" aria-describedby="jira-build-hint" value={atl.jira.buildRepo ?? ''} onchange={(e) => { atl.jira.buildRepo = e.currentTarget.value || null; edited('atlassian') }}>
            <option value="">None</option>
            {#if buildStale && atl.jira.buildRepo}<option value={atl.jira.buildRepo}>{plainText(atl.jira.buildRepo)} (not allowed)</option>{/if}
            {#each buildOptions as r (r.toLowerCase())}<option value={r.toLowerCase()}>{plainText(r)}</option>{/each}
          </select>
          {#if buildStale}<p class="hint err" data-content>{plainText(atl.jira.buildRepo ?? '')} is not on the allowed list, so a save is refused. Pick another repository, or None.</p>{/if}
          <p id="jira-build-hint" class="hint">Preselected in the picker when this browser has no earlier choice.</p>
          {#if buildOptions.length === 0}<p class="hint">Allowed repositories appear here once the list is saved.</p>{/if}
        </div>
        <div class="selrow">
          <label class="lab" for="jira-base">Base branch for every repository without its own</label>
          <input id="jira-base" class="fld" type="text" value={atl.jira.baseBranch ?? ''} oninput={(e) => { atl.jira.baseBranch = e.currentTarget.value; edited('atlassian') }} placeholder="main" autocomplete="off" spellcheck="false" aria-invalid={baseProblem ? 'true' : undefined} aria-describedby="jira-base-hint{baseProblem ? ' jira-base-err' : ''}" />
          <p id="jira-base-hint" class="hint">Runs start from this branch and open their PRs into it. Empty: the repository's default branch.</p>
          {#if baseProblem}<p id="jira-base-err" class="hint err" data-content>{baseProblem}</p>{/if}
        </div>
        {#each baseRepos as repo, index (repo.toLowerCase())}
          {@const key = repo.toLowerCase()}
          <div class="selrow">
            <label class="lab" for="jira-base-{index}" data-content>Base branch for {plainText(repo)} (optional)</label>
            <input id="jira-base-{index}" class="fld" type="text" value={atl.jira.baseBranches[key] ?? ''} oninput={(e) => { atl.jira.baseBranches[key] = e.currentTarget.value; edited('atlassian') }} placeholder={atl.jira.baseBranch || 'default branch'} autocomplete="off" spellcheck="false" aria-invalid={repoProblems[key] ? 'true' : undefined} aria-describedby={repoProblems[key] ? `jira-base-${index}-err` : undefined} />
            {#if repoProblems[key]}<p id="jira-base-{index}-err" class="hint err" data-content>{repoProblems[key]}</p>{/if}
          </div>
        {/each}
        <div class="selrow">
          <label class="lab" for="jira-review">Move the ticket to review</label>
          <select id="jira-review" class="sel" aria-describedby="jira-review-hint" value={atl.jira.reviewMove} onchange={(e) => setReviewMove(e.currentTarget.value)}>
            {#each REVIEW_MOVES as m (m.value)}<option value={m.value}>{m.label}</option>{/each}
          </select>
          <p id="jira-review-hint" class="hint">When Open work moves the Jira ticket to review: once the first PR is open, once every repository with changes has one, or not at all.</p>
        </div>
        <div class="selrow">
          <label class="lab" for="jira-layout">Sessions across repositories</label>
          <select id="jira-layout" class="sel" aria-describedby="jira-layout-hint" value={atl.jira.sessionLayout} onchange={(e) => setSessionLayout(e.currentTarget.value)}>
            {#each SESSION_LAYOUTS as l (l.value)}<option value={l.value}>{l.label}</option>{/each}
          </select>
          <p id="jira-layout-hint" class="hint">The layout the picker starts on when more than one repository is chosen.</p>
        </div>
      </fieldset>
      {@render saveRow('atlassian', atlDirty && !anyBaseProblem, saveAtlassian)}
    </article>
  </div>
</section>

{#if !setup}
  <section class="surf card" aria-labelledby="sources-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
    <h2 id="sources-h" class="hud cap">Which PRs Reviews lists</h2>
    <p class="sub">Reviews shows every PR any source below finds, and never a PR because of a source that is off.</p>

    <ul class="sources" aria-label="Review sources">
      <li class="src">
        {@render sw(requested, 'Requests me', () => { requested = !requested; edited('reviews') }, 'src-requested')}
        <div>
          <b id="src-requested">Requests me</b>
          <p class="hint">Open PRs in your allowed repositories where GitHub asks for your review.</p>
        </div>
      </li>
      <li class="src">
        <span class="tag" class:on={reads.channel}>{reads.channel ? 'On' : 'Off'}</span>
        <div>
          <b>Posted in a channel</b>
          <p class="hint">{#if reads.channel}On: the channel scan reads {slack.channels.join(', ')}.{:else}Off. It turns on with Slack and at least one channel, in the Slack card above.{/if}</p>
        </div>
      </li>
      <li class="src">
        <span class="tag" class:on={reads.dm}>{reads.dm ? 'On' : 'Off'}</span>
        <div>
          <b>Sent to me in a DM</b>
          <p class="hint">{#if reads.dm}On: the DM scan looks for PRs people sent you.{:else}Off. It turns on with Slack and its DM switch, in the Slack card above.{/if}</p>
        </div>
      </li>
      <li class="src">
        {@render sw(allOpen, 'Every open non-draft PR', () => { allOpen = !allOpen; edited('reviews') }, 'src-all')}
        <div>
          <b id="src-all">Every open non-draft PR</b>
          <p class="hint">Every open, non-draft PR in your allowed repositories that you did not write. Off by default; at most 200 per repository pattern.</p>
        </div>
      </li>
    </ul>

    <p class="sum" aria-live="polite"><span data-content>{lists}</span></p>
    {#if revDirty}<p class="hint">This shows the two switches as they are now. Reviews uses them once you save.</p>{/if}
    {@render saveRow('reviews', revDirty, saveReviews)}
  </section>

  <section class="surf card" aria-labelledby="sessions-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
    <h2 id="sessions-h" class="hud cap">Where sessions open</h2>
    <p class="sub">Where a session conn starts runs (a new session, a build from Open work, a review, a self-review or a resumed History session), and the advisor of each but a resume, which keeps the one it started with.</p>

    <fieldset class="choices">
      <legend class="sr">Where sessions open</legend>
      {#each WHERE_CHOICES as choice (choice.value)}
        <label class="choice" class:on={where === choice.value}>
          <input type="radio" name="sessions-where" value={choice.value} checked={where === choice.value} onchange={() => { where = choice.value; edited('sessions') }} />
          <span>
            <b>{choice.label}</b>
            <span class="hint">{choice.help}</span>
          </span>
        </label>
      {/each}
    </fieldset>


    <div class="row">
      {@render sw(resume, 'Resume sessions when conn starts', () => { resume = !resume; edited('sessions') }, 'sessions-resume')}
      <span id="sessions-resume">Resume the sessions that were open when conn quit, when it starts again</span>
    </div>

    <div class="selrow">
      <label class="lab" for="sessions-advisor">Advisor model</label>
      <select id="sessions-advisor" class="sel" aria-describedby="sessions-advisor-hint" value={sessionsAdvisor} onchange={(e) => { sessionsAdvisor = advisorOf(e.currentTarget.value); edited('sessions') }}>
        {#each ADVISOR_CHOICES as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
      </select>
      <p id="sessions-advisor-hint" class="hint">The advisor a session conn starts runs, unless you pick another when you start it. Claude Code’s setting is your own <code>advisorModel</code>. A session keeps its advisor when it is resumed.</p>
    </div>

    {@render saveRow('sessions', sessionsDirty, saveSessions)}
  </section>
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
    margin: 0;
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
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
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
  .hint,
  .info {
    max-width: 78ch;
    color: var(--mu);
    font-size: 12px;
    line-height: 1.55;
    overflow-wrap: anywhere;
  }
  .info b {
    color: var(--tx);
    font-weight: 600;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(260px, 100%), 1fr));
    gap: 12px;
    min-width: 0;
  }
  .mod {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
    box-sizing: border-box;
    padding: 12px;
    background: color-mix(in oklch, var(--sunk) 55%, transparent);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
  }
  .mhead {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .mset {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-width: 0;
    margin: 0;
    padding: 10px 0 0;
    border: 0;
    border-top: 1px var(--border-style) var(--line);
  }
  /* a module that is off keeps its settings in view, greyed, and says why */
  .mod.off .mset,
  .mod.off .info {
    opacity: 0.55;
  }
  .off-note {
    color: var(--mu);
    font-size: 12px;
    font-weight: 600;
  }
  .stat {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
  }
  .st {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    min-width: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--mu);
    overflow-wrap: anywhere;
  }
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--mu);
  }
  .st.ok {
    color: color-mix(in oklab, var(--ok) 60%, var(--bright));
  }
  .st.ok .dot {
    background: var(--ok);
  }
  .st.bad {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  .st.bad .dot {
    background: var(--bad);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 12px;
    color: var(--tx);
  }
  .selrow {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .lab {
    color: var(--mu);
    font-size: 12px;
  }
  .fld {
    min-height: 44px;
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
    padding: 0 10px;
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
  .sel {
    min-height: 44px;
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
    padding: 0 10px;
    color: var(--tx);
    background: var(--sunk);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-family: var(--font-mono), monospace;
    font-size: 13px;
  }
  .sw {
    position: relative;
    flex: none;
    width: 44px;
    min-height: 44px;
    padding: 0;
    background: transparent;
    border: 0;
    cursor: pointer;
  }
  .sw::before {
    content: '';
    position: absolute;
    left: 0;
    top: 50%;
    width: 40px;
    height: 22px;
    margin-top: -11px;
    box-sizing: border-box;
    background: var(--line2);
    border: 1px var(--border-style) var(--line2);
    border-radius: 999px;
  }
  .sw.on::before {
    background: color-mix(in oklch, var(--ok) 70%, transparent);
    border-color: var(--ok);
  }
  .knob {
    position: absolute;
    left: 4px;
    top: 50%;
    width: 14px;
    height: 14px;
    margin-top: -7px;
    background: var(--mu);
    border-radius: 50%;
  }
  .sw.on .knob {
    left: 22px;
    background: var(--bright);
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
  .save {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 14px;
    margin-top: auto;
    padding-top: 4px;
  }
  .hint.err {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  .ok,
  .err {
    font-size: 12px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .ok {
    color: color-mix(in oklab, var(--ok) 60%, var(--bright));
  }
  .err {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
  }
  .sources {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .src {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 8px 0;
    border-top: 1px var(--border-style) var(--line);
  }
  .src:first-child {
    border-top: 0;
  }
  .src b {
    color: var(--bright);
    font-size: 13px;
    font-weight: 600;
  }
  .tag {
    flex: none;
    box-sizing: border-box;
    width: 44px;
    padding: 3px 0;
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
  .choices {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }
  .choice {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    min-height: 44px;
    box-sizing: border-box;
    padding: 8px 10px;
    border: 1px var(--border-style) var(--line);
    border-radius: var(--radius);
    cursor: pointer;
  }
  .choice.on {
    border-color: var(--ac);
    background: color-mix(in oklch, var(--ac) 7%, transparent);
  }
  .choice input {
    flex: none;
    margin: 3px 0 0;
    accent-color: var(--ac);
  }
  .choice span {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .choice b {
    color: var(--bright);
    font-size: 13px;
    font-weight: 600;
  }
  .sum {
    padding: 9px 12px;
    color: var(--tx);
    border-left: 3px var(--border-style) var(--ac);
    background: color-mix(in oklch, var(--ac) 7%, transparent);
    font-size: 13px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  @media (max-width: 600px) {
    .card {
      padding: 14px;
    }
  }
  @media (prefers-reduced-motion: no-preference) {
    .btn,
    .fld,
    .sel {
      transition: border-color 0.16s, background 0.16s, color 0.16s;
    }
    .knob {
      transition: left 0.16s;
    }
  }
</style>
