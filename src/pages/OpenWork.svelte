<script lang="ts">
  import { flip } from 'svelte/animate'
  import { live, work, fetchWork, refreshWork, startRun } from '../lib/live.svelte.ts'
  import { href } from '../lib/router.svelte.ts'
  import { bootIn, bootOut, rowIn, rowOut } from '../lib/fx.ts'
  import { typeChipStyle } from '../lib/issueType.ts'
  import { branchName, dur } from '../lib/format.ts'
  import Brand from '../lib/Brand.svelte'
  import Nav from '../lib/Nav.svelte'
  import { loadChoice, preselect, saveChoice, startBody, toggleRepo } from '../lib/repoChoice.ts'
  import AdvisorPick from '../lib/AdvisorPick.svelte'
  import type { AdvisorPick as Pick } from '../lib/advisor.ts'
  import { runPrStatus } from '../lib/runLegs.ts'
  import type { ResearchModel, Run, RunLayout, RunState, Ticket, WorkBuild } from '../lib/types.ts'

  const born = Date.now()
  const skip = () => Date.now() - born < 800

  // Open work off (or no snapshot yet): nothing is asked of the server, whose Open work API is not there then
  const enabled = $derived(!!live.snap?.openWork)
  $effect(() => {
    if (!enabled) return
    fetchWork()
    const id = setInterval(fetchWork, 60_000)
    return () => clearInterval(id)
  })

  const list = $derived(work.list)

  // The first fetch takes a while; pick the result up as soon as it lands instead of at the next minute.
  $effect(() => {
    if (!list?.fetching) return
    const id = setInterval(fetchWork, 1500)
    return () => clearInterval(id)
  })
  const tickets = $derived(list?.tickets ?? [])
  const runs = $derived(live.snap?.runs ?? [])
  const runOf = (key: string): Run | undefined => runs.find(r => r.key === key)

  const ACTIVE: RunState[] = ['starting', 'researching', 'asking', 'building']
  const isActive = (r: Run | undefined) => !!r && ACTIVE.includes(r.state)

  type Owner = 'all' | 'mine' | 'unassigned'
  let owner = $state<Owner>('all')
  /** Statuses switched on; none on means every status. */
  let statuses = $state<string[]>([])
  const match = (t: Ticket): boolean => {
    if (owner === 'mine' && !t.mine) return false
    if (owner === 'unassigned' && t.assignee) return false
    return statuses.length === 0 || statuses.includes(t.status)
  }
  const toggleStatus = (s: string) => (statuses = statuses.includes(s) ? statuses.filter(x => x !== s) : [...statuses, s])
  const shown = $derived(tickets.filter(match))

  const sum = $derived({
    open: tickets.length,
    mine: tickets.filter(t => t.mine).length,
    runs: runs.filter(r => ACTIVE.includes(r.state)).length,
    asks: runs.filter(r => r.state === 'asking').reduce((n, r) => n + r.questions.filter(q => q.answer === null).length, 0),
    review: runs.filter(r => r.state === 'pr_open').length,
  })

  const owners: { id: Owner; label: string }[] = [
    { id: 'all', label: 'ALL' },
    { id: 'mine', label: 'MINE' },
    { id: 'unassigned', label: 'UNASSIGNED' },
  ]
  /** Whatever statuses the sprint holds, in the order Jira's categories run (to do, in progress, done), then as first seen. */
  const CATEGORY_ORDER = ['new', 'indeterminate', 'done', '']
  const statusChips = $derived.by(() => {
    const first = new Map<string, Ticket['statusCategory']>()
    for (const t of tickets) if (!first.has(t.status)) first.set(t.status, t.statusCategory)
    const names = [...first.keys()].sort((a, b) => CATEGORY_ORDER.indexOf(first.get(a)!) - CATEGORY_ORDER.indexOf(first.get(b)!))
    return names.map(name => ({ name, count: tickets.filter(t => t.status === name && (owner === 'all' || (owner === 'mine' ? t.mine : !t.assignee))).length }))
  })

  const GREY = { color: 'color-mix(in oklab, var(--mu) 75%, var(--panel))', glow: 'none' }
  const RS: Record<RunState, { label: string; color: string; glow: string }> = {
    starting: { label: 'STARTING', color: 'var(--info)', glow: '0 0 10px color-mix(in oklch, var(--info) calc(100% * var(--glow)), transparent)' },
    researching: { label: 'RESEARCHING', color: 'var(--info)', glow: '0 0 10px color-mix(in oklch, var(--info) calc(100% * var(--glow)), transparent)' },
    asking: { label: 'NEEDS ANSWERS', color: 'var(--warn)', glow: '0 0 10px color-mix(in oklch, var(--warn) calc(100% * var(--glow)), transparent)' },
    building: { label: 'IMPLEMENTING', color: 'var(--role-lead)', glow: '0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)' },
    pr_open: { label: 'PR OPEN', color: 'var(--ok)', glow: '0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)' },
    failed: { label: 'FAILED', color: 'var(--bad)', glow: '0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)' },
    interrupted: { label: 'INTERRUPTED', color: 'color-mix(in oklab, var(--stall) 75%, var(--bright))', glow: '0 0 10px color-mix(in oklch, color-mix(in oklab, var(--stall) 75%, var(--bright)) calc(100% * var(--glow)), transparent)' },
    cancelled: { label: 'NOT STARTED', ...GREY },
  }
  /** A run with an open PR shows what GitHub says about it, not just "PR OPEN". */
  const prSlot = (r: Run) => {
    const p = runPrStatus(r)
    if (!p) return RS.pr_open
    if (p.state === 'merged') return { label: 'PR MERGED', color: 'var(--role-advisor)', glow: '0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)' }
    if (p.state === 'closed') return { label: 'PR CLOSED', ...GREY }
    if (p.checks.state === 'failing') return { label: 'CHECKS FAILING', color: 'var(--bad)', glow: '0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)' }
    if (p.checks.state === 'pending') return { label: 'CHECKS RUNNING', color: 'var(--warn)', glow: '0 0 10px color-mix(in oklch, var(--warn) calc(100% * var(--glow)), transparent)' }
    if (p.checks.state === 'passing') return { label: p.review === 'APPROVED' ? 'APPROVED' : 'CHECKS PASSING', color: 'var(--ok)', glow: '0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)' }
    return RS.pr_open
  }
  const slot = (t: Ticket, r: Run | undefined) => (r ? (r.state === 'pr_open' ? prSlot(r) : RS[r.state]) : { label: t.status.toUpperCase(), ...GREY })

  const PRI: Record<string, { color: string; border: string }> = {
    high: { color: 'color-mix(in oklab, var(--bad) 70%, var(--bright))', border: 'color-mix(in oklch, var(--bad) 45%, transparent)' },
    medium: { color: 'var(--warn)', border: 'color-mix(in oklch, var(--warn) 40%, transparent)' },
    low: { color: 'var(--mu)', border: 'color-mix(in oklch, var(--edge) 26%, transparent)' },
  }
  const priStyle = (p: string) => PRI[p.toLowerCase().replace(/^(highest)$/, 'high').replace(/^lowest$/, 'low')] ?? PRI.low

  let selKey = $state<string | null>(null)
  const sel = $derived(tickets.find(t => t.key === selKey) ?? shown[0] ?? tickets[0])
  const selRun = $derived(sel ? runOf(sel.key) : undefined)
  const fresh = $derived(!selRun || selRun.state === 'cancelled')
  /** The selected run's PRs: the first one linked, how many there are, and the status the pane reads (one leg's own, or the one most in need of the person). */
  const selPr = $derived(selRun?.repos.find(leg => leg.pr)?.pr ?? null)
  const selPrs = $derived(selRun?.repos.filter(leg => leg.pr).length ?? 0)
  const selStatus = $derived(selRun ? runPrStatus(selRun) : null)

  const noteFor = (r: Run): string => {
    if (r.state === 'asking') {
      const n = r.questions.filter(q => q.answer === null).length
      return `${n} question${n === 1 ? '' : 's'} waiting on you`
    }
    if (r.state === 'failed' || r.state === 'interrupted') return r.error ?? ''
    return dur(live.now - r.startedAt)
  }

  let model = $state<ResearchModel>('haiku')
  // The advisor of this run's build sessions: Settings' unless picked here, and not remembered, not even from one ticket to the next
  let advisor = $state<Pick>('')
  const MODELS: { id: ResearchModel; label: string; hint: string }[] = [
    { id: 'haiku', label: 'HAIKU', hint: 'fast, cheap sweep' },
    { id: 'sonnet', label: 'SONNET 5.5', hint: 'slower, deeper reading' },
  ]
  const pipe = $derived([
    { name: '1 RESEARCH', note: 'Jira, Confluence and Slack read first', color: 'var(--info)' },
    { name: '2 ASK', note: 'only what the sources could not answer', color: 'var(--warn)' },
    { name: '3 BUILD', note: `session named ${sel?.key ?? ''}`, color: 'var(--role-lead)' },
    { name: '4 PR', note: 'Jira moves to its review status, if it has one', color: 'var(--ok)' },
  ])

  /** Where a run may build: each allowed repository, ready or with what to fix */
  const NO_BUILD: WorkBuild = { repos: [], default: null, layout: 'shared' }
  const build = $derived(list?.build ?? NO_BUILD)

  // The repositories START builds in, in the order they were ticked (the first is the lead), and the layout of their sessions.
  // The picker starts on what this browser chose last, else the default repository, once the list names repositories; a choice is
  // remembered as it is made.
  let chosen = $state<string[]>([])
  let layout = $state<RunLayout>('shared')
  // Until something is preselected or the person picks, each new list is tried again: one read
  // before a clone was ready, or before the default was listed, does not fix the panel on nothing.
  let picked = false
  $effect(() => {
    if (picked || build.repos.length === 0) return
    const remembered = loadChoice()
    const initial = preselect(build, remembered.repos)
    layout = remembered.layout ?? build.layout
    if (initial.length === 0) return
    picked = true
    chosen = initial
  })
  /** The chosen repositories that a run can start in now: one that stopped being ready is not sent */
  const chosenReady = $derived(chosen.filter(r => build.repos.some(b => b.repo === r && b.ready)))
  const pickRepo = (repo: string) => {
    picked = true
    chosen = toggleRepo(chosen, repo)
    saveChoice({ repos: chosen, layout })
  }
  const pickLayout = (next: RunLayout) => {
    picked = true
    layout = next
    saveChoice({ repos: chosen, layout })
  }
  const LAYOUTS: { id: RunLayout; label: string; hint: string }[] = [
    { id: 'shared', label: 'ONE SESSION', hint: 'one Claude works in every repository' },
    { id: 'split', label: 'ONE PER REPOSITORY', hint: 'a session in each, side by side' },
  ]

  type Reason = 'assignee' | 'status'
  let step = $state<'idle' | 'confirm' | 'working'>('idle')
  let reasons = $state<Reason[]>([])
  let startError = $state('')

  // Another ticket is selected: the half-made decision, its advisor pick and its error belong to the old one.
  const selId = $derived(sel?.key)
  $effect(() => {
    selId
    step = 'idle'
    advisor = ''
    startError = ''
  })

  const reasonText = (t: Ticket, r: Reason) =>
    r === 'assignee' ? `${t.key} ${t.assignee ? `is assigned to ${t.assignee}` : 'is unassigned'}` : `${t.key} is ${t.status}, not in a To Do status`
  const confirmText = (t: Ticket) => reasons.map(r => reasonText(t, r)).join('. ') + '. Start it anyway?'

  async function start(t: Ticket, force: boolean) {
    if (!force) {
      const need: Reason[] = []
      if (!t.mine) need.push('assignee')
      if (t.statusCategory !== 'new') need.push('status')
      if (need.length) {
        reasons = need
        step = 'confirm'
        return
      }
    }
    step = 'working'
    startError = ''
    const r = await startRun(t.key, startBody(model, force, chosenReady, layout, advisor))
    if (r.ok) {
      step = 'idle'
      location.hash = href.run(t.key)
    } else if (r.needsConfirm && !force) {
      reasons = [r.needsConfirm]
      step = 'confirm'
    } else {
      startError = r.error ?? 'failed'
      step = 'idle'
    }
  }

  let refreshing = $state(false)
  let refreshError = $state('')
  async function refresh() {
    refreshing = true
    refreshError = ''
    const r = await refreshWork()
    if (!r.ok) refreshError = r.error ?? 'refresh failed'
    refreshing = false
  }
  const busy = $derived(refreshing || !!list?.fetching)

  const tile = 'position: relative; overflow: hidden; --r: calc(var(--radius) * 1.5); padding: 14px 16px'
  const cbtn =
    'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); cursor: pointer; background: transparent;'
</script>

<svelte:head><title>Open work</title></svelte:head>

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column; --orb-1: var(--ac); --orb-1-a: 16%; --orb-2: var(--edge); --orb-2-a: 13%; --orb-3: var(--role-lead); --orb-3-a: 7%">

  <div class="page-scan" aria-hidden="true" style="position: absolute; inset: 0; z-index: 5; pointer-events: none"></div>

  <header class="page-head" style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 14px 28px; padding: 14px 32px; background: color-mix(in oklch, var(--panel) 72%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
    <div class="glint" style="position: absolute; left: 0; right: 0; bottom: -1px; height: 1px"></div>
    <Brand />
    <Nav active="work" />
    {#if live.connected}
      <div style="display: flex; align-items: center; gap: 10px; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>LINK ESTABLISHED · {location.host}</div>
    {:else}
      <div role="status" style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)"><span class="led blink" style="width: 8px; height: 8px; background: var(--bad); box-shadow: 0 0 10px color-mix(in oklch, var(--bad) calc(100% * var(--glow)), transparent)"></span>LINK LOST · {location.host}</div>
    {/if}
  </header>

  {#if !live.connected && live.snap}
    <div role="alert" style="position: relative; padding: 9px 32px; background: color-mix(in oklch, var(--bad) 10%, transparent); border-bottom: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.5714)">SERVER DISCONNECTED · showing the last snapshot, reconnecting…</div>
  {/if}

  <main style="flex: 1; display: flex; flex-direction: column; gap: 20px; padding: 24px 32px 40px; width: 100%; max-width: 1480px; box-sizing: border-box; margin: 0 auto">

    {#if live.snap && !enabled}
      <div role="status" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 40vh; text-align: center">
        <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">Atlassian is off. Enable it in Settings.</div>
        <div style="font-size: 12px; color: var(--mu)">Open work reads your Jira sprint, so it needs the Atlassian module on and at least one Jira project key.</div>
        <a href={href.settings()} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class="cap">Open Settings <span class="g-go" aria-hidden="true"></span></span></a>
      </div>
    {:else if !list}
      <div role="status" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 40vh; text-align: center">
        <span class="led blink" style="width: 10px; height: 10px; background: var(--ac); box-shadow: 0 0 12px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></span>
        <div class="hud cap" style="font-size: 13px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Loading the sprint…</div>
      </div>
    {:else}

    {#if list.error}
      <div role="alert" in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px; padding: 12px 18px; border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--bad) 10%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; line-height: 1.5">
        <span style="flex: 1 1 300px; min-width: 0; word-break: break-word"><span style="letter-spacing: calc(var(--tracking) * 0.8571)" class="cap">Could not read the sprint</span> · {list.error}</span>
        <button type="button" onclick={refresh} disabled={busy} style="{cbtn} color: color-mix(in oklab, var(--bad) 55%, var(--bright)); border: 1px var(--border-style) color-mix(in oklch, var(--bad) 50%, transparent); opacity: {busy ? 0.5 : 1}" class="cap">{busy ? 'Retrying…' : 'Retry'}</button>
      </div>
    {/if}

    <!-- sprint summary -->
    <section aria-label="Sprint summary" class="tiles" style="--cols: 4">
      <div class="surf" style="{tile}; --wash: linear-gradient(160deg, color-mix(in oklch, var(--ac) 13%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--ac) 30%, transparent)">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--ac); box-shadow: 0 0 10px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--ac) 40%, var(--mu))" class="cap">Open in sprint</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--ac) calc(50% * var(--glow)), transparent)">{sum.open}</div>
        <div style="font-size: 11px; color: color-mix(in oklab, var(--ac) 10%, var(--mu)); margin-top: 2px">{list?.projects.join(' ')} · {sum.mine} mine</div>
      </div>
      <div class="surf" style="{tile}; --wash: linear-gradient(160deg, color-mix(in oklch, var(--edge) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--edge) 28%, transparent)">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--edge); box-shadow: 0 0 10px color-mix(in oklch, var(--edge) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: var(--mu)" class="cap">Runs active</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--edge) 55%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--edge) calc(60% * var(--glow)), transparent)">{sum.runs}</div>
        <div style="font-size: 11px; color: var(--mu); margin-top: 2px">researching or implementing</div>
      </div>
      <div class="surf" style="{tile}; --wash: linear-gradient(160deg, color-mix(in oklch, var(--warn) 12%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--warn) 30%, transparent)">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--warn); box-shadow: 0 0 10px color-mix(in oklch, var(--warn) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--warn) 50%, var(--mu))" class="cap">Needs your answer</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--warn) 70%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--warn) calc(50% * var(--glow)), transparent)">{sum.asks}</div>
        <div style="font-size: 11px; color: color-mix(in oklab, var(--warn) 25%, var(--mu)); margin-top: 2px">questions the agents could not resolve</div>
      </div>
      <div class="surf" style="{tile}; --wash: linear-gradient(160deg, color-mix(in oklch, var(--ok) 12%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%); --rim: color-mix(in oklch, var(--ok) 28%, transparent)">
        <div class="stripe" style="position: absolute; left: 0; top: 0; width: 38%; height: 2px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></div>
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--ok) 55%, var(--mu))" class="cap">In code review</div>
        <div class="hud" style="font-weight: 700; font-size: 28px; margin-top: 4px; color: color-mix(in oklab, var(--ok) 50%, var(--bright)); text-shadow: 0 0 16px color-mix(in oklch, var(--ok) calc(50% * var(--glow)), transparent)">{sum.review}</div>
        <div style="font-size: 11px; color: color-mix(in oklab, var(--ok) 85%, var(--bg)); margin-top: 2px">PR open, waiting on people</div>
      </div>
    </section>

    <div style="display: flex; flex-wrap: wrap; gap: 20px; align-items: flex-start">

      <!-- ticket list -->
      <section aria-label="Open tickets" style="flex: 999 1 600px; min-width: 0; display: flex; flex-direction: column; gap: 14px">
        <div style="display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Current sprint <span class="g-sep" aria-hidden="true"></span> {shown.length} shown</div>
          <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px">
            <div role="group" aria-label="Filter by owner" style="display: flex; flex-wrap: wrap; gap: 8px">
              {#each owners as f (f.id)}
                {@const active = owner === f.id}
                <button
                  class="tabc"
                  onclick={() => (owner = f.id)}
                  aria-pressed={active}
                  style="padding: 8px 14px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) {active ? 'color-mix(in oklch, var(--ac) 55%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; background: {active ? 'color-mix(in oklch, var(--ac) 10%, transparent)' : 'transparent'}; color: {active ? 'color-mix(in oklab, var(--ac) 30%, var(--bright))' : 'var(--mu)'}; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
                >{f.label}</button>
              {/each}
            </div>
            <button
              type="button"
              class="tabc"
              onclick={refresh}
              disabled={busy}
              aria-label="Refresh the sprint from Jira"
              style="display: flex; align-items: center; gap: 10px; padding: 8px 14px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background: transparent; color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: {busy ? 'default' : 'pointer'}"
            ><span class={busy ? 'blink' : ''} aria-hidden="true">↻</span>{busy ? 'REFRESHING…' : 'REFRESH'}<span style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.2857); color: color-mix(in oklab, var(--mu) 75%, var(--panel))">{list.fetchedAt ? `updated ${dur(live.now - list.fetchedAt)} ago` : 'not updated yet'}</span></button>
          </div>
        </div>

        <div role="group" aria-label="Filter by status" style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">
          <span style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 75%, var(--panel)); margin-right: 4px" class="cap">Status</span>
          {#each statusChips as c (c.name)}
            {@const active = statuses.includes(c.name)}
            <button
              class="tabc"
              onclick={() => toggleStatus(c.name)}
              aria-pressed={active}
              style="padding: 6px 12px; min-height: 36px; border-radius: var(--radius); border: 1px var(--border-style) {active ? 'color-mix(in oklch, var(--ac) 55%, transparent)' : 'color-mix(in oklch, var(--edge) 18%, transparent)'}; background: {active ? 'color-mix(in oklch, var(--ac) 10%, transparent)' : 'transparent'}; color: {active ? 'color-mix(in oklab, var(--ac) 30%, var(--bright))' : 'var(--mu)'}; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); cursor: pointer"
            >{c.name.toUpperCase()} <span style="color: {active ? 'color-mix(in oklab, var(--ac) 60%, var(--bright))' : 'color-mix(in oklab, var(--mu) 75%, var(--panel))'}">{c.count}</span></button>
          {/each}
          {#if statuses.length}
            <button class="tabc cap" onclick={() => (statuses = [])} style="padding: 6px 10px; min-height: 36px; border: none; background: transparent; color: color-mix(in oklab, var(--ac) 10%, var(--mu)); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); cursor: pointer">Clear</button>
          {/if}
        </div>

        {#if refreshError}
          <div role="alert" style="font-size: 11px; color: color-mix(in oklab, var(--bad) 55%, var(--bright))">{refreshError}</div>
        {/if}
        {#if list.truncated}
          <div role="note" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.4286); color: color-mix(in oklab, var(--warn) 25%, var(--mu))">Showing the first {tickets.length} of the sprint</div>
        {/if}

        {#if list.needsAuth.length}
          <div role="alert" style="display: flex; flex-direction: column; gap: 6px; padding: 14px 18px; border-radius: calc(var(--radius) * 1.5); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 45%, transparent); background: color-mix(in oklch, var(--role-lead) 7%, transparent)">
            <div class="hud" style="font-weight: 700; font-size: 14px; letter-spacing: calc(var(--tracking) * 0.7143); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))"><span data-content>{list.needsAuth.join(' AND ')}</span> NEEDS SIGNING IN</div>
            <div style="font-size: 12px; line-height: 1.6; color: color-mix(in oklab, var(--role-lead) 40%, var(--mu))">Connect it at <a href="https://claude.ai/settings/connectors" target="_blank" rel="noopener noreferrer" style="color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">claude.ai/settings/connectors</a>, or run <span style="color: color-mix(in oklab, var(--tx) 50%, var(--bright))">/mcp</span> in Claude Code. Then press REFRESH.</div>
          </div>
        {/if}
        {#if list.fetching}
          <div role="status" aria-live="polite" style="display: flex; flex-direction: column; gap: 8px; padding: 16px 20px; border-radius: calc(var(--radius) * 1.5); border: 1px var(--border-style) color-mix(in oklch, var(--ac) 30%, transparent); background: color-mix(in oklch, var(--ac) 5%, transparent)">
            <div style="display: flex; align-items: center; gap: 10px; font-size: 11px; letter-spacing: var(--tracking); color: var(--mu)"><span class="led blink" style="width: 8px; height: 8px; background: var(--ac); box-shadow: 0 0 10px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></span>FETCHING THE SPRINT FROM JIRA{#if list.fetchStartedAt} · {dur(live.now - list.fetchStartedAt)}{/if}</div>
            <div style="font-size: 13px; color: var(--tx); overflow-wrap: anywhere">{list.step ?? 'Starting Claude'}</div>
            <div style="font-size: 11px; color: color-mix(in oklab, var(--mu) 75%, var(--panel))">A headless Claude reads Jira through your connector. The first fetch can take a minute or two.</div>
          </div>
        {/if}

        {#if shown.length === 0}
          {#if !list.error && !list.fetching && !list.needsAuth.length}
            <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 10px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
              {#if tickets.length === 0}
                <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No open tickets</div>
                <div style="font-size: 12px; color: var(--mu)">Nothing in the current sprint is open.</div>
              {:else}
                <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No tickets match</div>
                <div style="font-size: 12px; color: var(--mu)">Try another filter.</div>
              {/if}
            </div>
          {/if}
        {:else}
          <div style="display: flex; flex-direction: column; gap: 8px">
            {#each shown as t (t.key)}
              {@const on = sel?.key === t.key}
              {@const r = runOf(t.key)}
              {@const st = slot(t, r)}
              <button class="row" in:rowIn={{ skip: skip() }} out:rowOut animate:flip={{ duration: 380 }} onclick={() => (selKey = t.key)} aria-pressed={on} style="position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; width: 100%; box-sizing: border-box; padding: 14px 18px 14px 22px; text-align: left; cursor: pointer; color: inherit; font-size: 13px; border: 1px var(--border-style) {on ? 'color-mix(in oklch, var(--ac) 60%, transparent)' : 'color-mix(in oklch, var(--edge) 16%, transparent)'}; border-radius: calc(var(--radius) * 1.5); background: {on ? 'linear-gradient(90deg, color-mix(in oklch, var(--ac) 12%, transparent), color-mix(in oklch, var(--panel) 90%, transparent) 70%)' : 'color-mix(in oklch, var(--panel) 70%, transparent)'}">
                <span style="position: absolute; left: 0; top: 8px; bottom: 8px; width: 3px; border-radius: calc(var(--radius) * 0.5); background: {on ? 'var(--ac)' : r ? st.color : 'transparent'}; box-shadow: {on ? '0 0 10px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)' : 'none'}"></span>
                <span data-content class="hud" style="flex: 0 0 76px; font-weight: 700; font-size: 15px; letter-spacing: calc(var(--tracking) * 0.4286); color: {on ? 'color-mix(in oklab, var(--ac) 60%, var(--bright))' : 'color-mix(in oklab, var(--edge) 45%, var(--bright))'}">{t.key}</span>
                <span data-content style="flex: 1 1 260px; min-width: 0; color: var(--tx); line-height: 1.4">{t.title}</span>
                <span style="flex: 0 0 auto; padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); {typeChipStyle(t.type)}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)">{t.type.toUpperCase()}</span>
                <span style="flex: 0 0 168px; display: flex; align-items: center; justify-content: flex-end; gap: 8px; color: {st.color}; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)"><span class="led" style="width: 8px; height: 8px; background: {st.color}; box-shadow: {st.glow}"></span>{st.label}</span>
              </button>
            {/each}
          </div>
        {/if}
      </section>

      <!-- ticket panel -->
      {#if sel}
        {@const pri = priStyle(sel.priority)}
        <aside aria-label="Selected ticket" class="surf brk" in:bootIn={{ skip: skip() }} out:bootOut style="--c: var(--ac); flex: 1 1 440px; max-width: 520px; min-width: 0; box-sizing: border-box; display: flex; flex-direction: column; gap: 18px; padding: 22px 24px; --rim: color-mix(in oklch, var(--ac) 34%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--ac) 9%, transparent), color-mix(in oklch, var(--panel) 94%, transparent) 55%); backdrop-filter: blur(6px)">
          <div class="stripe" style="position: absolute; left: 0; top: 0; width: 100%; height: 2px; background: linear-gradient(90deg, var(--ac), transparent); box-shadow: 0 0 12px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></div>

          <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px">
            <span data-content class="hud" style="font-weight: 700; font-size: 24px; letter-spacing: calc(var(--tracking) * 0.3571); color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--ac) calc(50% * var(--glow)), transparent)">{sel.key}</span>
            <span style="padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); {typeChipStyle(sel.type)}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)">{sel.type.toUpperCase()}</span>
            <span style="padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {pri.border}; color: {pri.color}; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)">{sel.priority.toUpperCase()}</span>
            <span style="margin-left: auto; color: var(--mu); font-size: 12px">Jira: {sel.status}</span>
          </div>
          <div data-content class="hud" style="font-weight: 600; font-size: 20px; line-height: 1.3; color: var(--bright)">{sel.title}</div>
          <div style="font-size: 12px; line-height: 1.6; color: color-mix(in oklab, var(--mu) 70%, var(--tx))">{sel.desc || 'The description is read when the run starts.'}</div>

          <!-- context the agents will pull -->
          <div style="display: flex; flex-direction: column; gap: 8px">
            <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Context found</div>
            <div style="display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px">
              <div style="padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--edge) 6%, transparent)">
                <div class="hud" style="font-weight: 700; font-size: 18px; color: color-mix(in oklab, var(--edge) 45%, var(--bright))">{sel.links}</div>
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--mu) 85%, var(--panel)); margin-top: 2px" class="cap">Jira Links</div>
              </div>
              <div style="padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--ac) 26%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--ac) 6%, transparent)">
                <div class="hud" style="font-weight: 700; font-size: 18px; color: color-mix(in oklab, var(--ac) 60%, var(--bright))">{sel.comments ?? '–'}</div>
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--ac) 40%, var(--mu)); margin-top: 2px" class="cap">Comments</div>
              </div>
              <div style="padding: 10px 12px; border: 1px var(--border-style) color-mix(in oklch, var(--ok) 26%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--ok) 6%, transparent)">
                <div class="hud" style="font-weight: 700; font-size: 18px; color: color-mix(in oklab, var(--ok) 50%, var(--bright))">on start</div>
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--ok) 55%, var(--mu)); margin-top: 2px" class="cap">Confluence + Slack</div>
              </div>
            </div>
          </div>

          {#if fresh}
            <!-- start configuration -->
            <div in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 14px; padding-top: 16px; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
              {#if selRun}
                <div style="font-size: 11px; line-height: 1.5; color: var(--mu)">The last run was cancelled. <a href={href.run(sel.key)} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none; letter-spacing: calc(var(--tracking) * 0.7143)"><span class="cap">View run <span class="g-go" aria-hidden="true"></span></span></a></div>
              {/if}
              <div style="display: flex; flex-direction: column; gap: 8px">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Branch</div>
                <div style="font-size: 12px; color: var(--mu); line-height: 1.5; overflow-wrap: anywhere"><span style="color: var(--ac)">⎇ </span>{branchName(sel.key, sel.title)}</div>
              </div>

              <fieldset style="display: flex; flex-direction: column; gap: 6px; margin: 0; padding: 0; border: 0; min-width: 0">
                <legend style="padding: 0; margin-bottom: 8px; font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Repositories</legend>
                {#each build.repos as r (r.repo.toLowerCase())}
                  {@const on = chosenReady.includes(r.repo)}
                  {@const lead = chosenReady.length > 1 && chosenReady[0] === r.repo}
                  <label style="display: flex; align-items: flex-start; gap: 10px; padding: 8px 12px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); cursor: {r.ready ? 'pointer' : 'default'}; border: 1px var(--border-style) {on ? 'color-mix(in oklch, var(--edge) 65%, transparent)' : 'color-mix(in oklch, var(--edge) 20%, transparent)'}; background: {on ? 'color-mix(in oklch, var(--edge) 13%, transparent)' : 'transparent'}; opacity: {r.ready ? 1 : 0.7}">
                    <input type="checkbox" checked={on} disabled={!r.ready} onchange={() => pickRepo(r.repo)} style="margin: 2px 0 0; accent-color: var(--ac)" />
                    <span style="display: flex; flex-direction: column; gap: 2px; min-width: 0">
                      <span data-content style="font-size: 12px; color: {on ? 'var(--tx)' : 'var(--mu)'}; overflow-wrap: anywhere">{r.repo}{#if lead}<span style="margin-left: 8px; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--ac) 60%, var(--bright))" class="cap">lead</span>{/if}</span>
                      {#if r.ready}
                        <span style="font-size: 11px; color: color-mix(in oklab, var(--mu) 85%, var(--panel)); overflow-wrap: anywhere">from origin/{r.base ?? 'the default branch'} · PR into {r.base ?? 'the default branch'}</span>
                      {:else}
                        <span data-content style="font-size: 11px; line-height: 1.5; color: var(--warn); overflow-wrap: anywhere">{r.reason ?? 'There is no clone to build in.'}</span>
                      {/if}
                    </span>
                  </label>
                {/each}
                {#if build.repos.length === 0}
                  <div role="status" style="font-size: 12px; line-height: 1.5; color: var(--warn)">No repository to build in yet: allow one and set its clone in Settings.</div>
                {/if}
                {#if build.repos.length === 0 || build.repos.some(r => !r.ready)}
                  <a href={href.settings()} style="font-size: 12px; color: color-mix(in oklab, var(--ac) 60%, var(--bright))">Open Settings</a>
                {/if}
              </fieldset>

              {#if chosenReady.length > 1}
                <div style="display: flex; flex-direction: column; gap: 8px">
                  <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Sessions</div>
                  <div role="group" aria-label="Sessions across repositories" style="display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px">
                    {#each LAYOUTS as l (l.id)}
                      {@const on = layout === l.id}
                      <button type="button" onclick={() => pickLayout(l.id)} aria-pressed={on} style="display: flex; flex-direction: column; gap: 3px; align-items: flex-start; padding: 10px 12px; min-height: 52px; text-align: left; cursor: pointer; border-radius: var(--radius); border: 1px var(--border-style) {on ? 'color-mix(in oklch, var(--edge) 65%, transparent)' : 'color-mix(in oklch, var(--edge) 20%, transparent)'}; background: {on ? 'color-mix(in oklch, var(--edge) 13%, transparent)' : 'transparent'}; color: {on ? 'var(--tx)' : 'var(--mu)'}">
                        <span style="font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); font-weight: 600">{l.label}</span>
                        <span style="font-size: 10px; color: {on ? 'color-mix(in oklab, var(--edge) 75%, var(--bright))' : 'color-mix(in oklab, var(--mu) 75%, var(--panel))'}">{l.hint}</span>
                      </button>
                    {/each}
                  </div>
                </div>
              {/if}

              <div style="display: flex; flex-direction: column; gap: 8px">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Research agents</div>
                <div role="group" aria-label="Research model" style="display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px">
                  {#each MODELS as m (m.id)}
                    {@const on = model === m.id}
                    <button onclick={() => (model = m.id)} aria-pressed={on} style="display: flex; flex-direction: column; gap: 3px; align-items: flex-start; padding: 10px 12px; min-height: 52px; text-align: left; cursor: pointer; border-radius: var(--radius); border: 1px var(--border-style) {on ? 'color-mix(in oklch, var(--edge) 65%, transparent)' : 'color-mix(in oklch, var(--edge) 20%, transparent)'}; background: {on ? 'color-mix(in oklch, var(--edge) 13%, transparent)' : 'transparent'}; color: {on ? 'var(--tx)' : 'var(--mu)'}">
                      <span style="font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); font-weight: 600">{m.label}</span>
                      <span style="font-size: 10px; color: {on ? 'color-mix(in oklab, var(--edge) 75%, var(--bright))' : 'color-mix(in oklab, var(--mu) 75%, var(--panel))'}">{m.hint}</span>
                    </button>
                  {/each}
                </div>
              </div>

              <div style="display: flex; flex-direction: column; gap: 8px">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Build advisor</div>
                <AdvisorPick bind:value={advisor} label="Advisor for the build sessions of {sel.key}" prefix="" disabled={step === 'working'} style="width: 100%" />
              </div>

              <!-- pipeline preview -->
              <div style="display: flex; flex-direction: column; gap: 8px">
                <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Pipeline</div>
                <div style="display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 6px">
                  {#each pipe as p (p.name)}
                    <div style="display: flex; flex-direction: column; gap: 6px">
                      <div style="height: 3px; border-radius: calc(var(--radius) * 0.5); background: {p.color}; box-shadow: 0 0 8px color-mix(in oklch, {p.color} calc(100% * var(--glow)), transparent)"></div>
                      <div style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); color: {p.color}">{p.name}</div>
                      <div style="font-size: 10px; line-height: 1.4; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{p.note}</div>
                    </div>
                  {/each}
                </div>
              </div>

              {#if step === 'confirm'}
                <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px">
                  <span role="alert" style="flex: 1 1 100%; font-size: 12px; line-height: 1.5; letter-spacing: calc(var(--tracking) * 0.2857); color: var(--warn)">{confirmText(sel)}</span>
                  <button type="button" onclick={() => start(sel, true)} style="{cbtn} color: var(--onac); background: var(--ac); border: 1px var(--border-style) var(--ac); font-weight: 700" class="cap">Start anyway</button>
                  <button type="button" onclick={() => (step = 'idle')} style="{cbtn} color: var(--tx); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">Cancel</button>
                </div>
              {:else}
                <button type="button" class="go" onclick={() => start(sel, false)} disabled={step === 'working' || chosenReady.length === 0} style="display: flex; align-items: center; justify-content: center; gap: 12px; min-height: 52px; box-sizing: border-box; width: 100%; border: 0; border-radius: var(--radius); cursor: {step === 'working' || chosenReady.length === 0 ? 'default' : 'pointer'}; background: linear-gradient(135deg, var(--ac), var(--k-subtask)); color: var(--onac); font-weight: 700; font-size: 14px; letter-spacing: calc(var(--tracking) * 1.2857); box-shadow: 0 0 26px color-mix(in oklch, var(--ac) calc(38% * var(--glow)), transparent); opacity: {step === 'working' ? 0.6 : chosenReady.length > 0 ? 1 : 0.4}">{#if step === 'working'}<span data-content>STARTING {sel.key}…</span>{:else if chosenReady.length === 0}<span>Choose a repository</span>{:else}<span aria-hidden="true">▶</span><span data-content>START {sel.key}</span>{/if}</button>
              {/if}
              {#if startError}
                <div role="alert" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); word-break: break-word">{startError}</div>
              {/if}
              <div style="font-size: 11px; line-height: 1.5; color: color-mix(in oklab, var(--mu) 85%, var(--panel)); text-align: center">{#if chosenReady.length > 1}Moves {sel.key} to an In Progress status, creates the branch in each of the {chosenReady.length} repositories and opens {layout === 'split' ? 'a session in each' : 'one session across them'} named {sel.key}.{:else}Moves {sel.key} to an In Progress status, creates the branch and opens a session named {sel.key}.{/if}</div>
            </div>
          {:else if selRun?.state === 'pr_open'}
            {@const ps = prSlot(selRun)}
            <div in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 10px; padding-top: 16px; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
              <div style="display: flex; align-items: center; gap: 10px; color: {ps.color}; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class="led" style="width: 9px; height: 9px; background: {ps.color}; box-shadow: {ps.glow}"></span>{ps.label} ·
                {#if selPr}<a href={selPr.url} target="_blank" rel="noopener noreferrer" style="color: color-mix(in oklab, var(--ok) 50%, var(--bright)); text-decoration: none"><span>#{selPr.number} <span class="g-go" aria-hidden="true"></span></span></a>{/if}{#if selPrs > 1}<span>{selPrs} PRs</span>{/if}</div>
              <div style="font-size: 11px; line-height: 1.5; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Handed off. Jira is {selRun.jiraStatus}{selStatus && selStatus.state !== 'merged' && selStatus.state !== 'closed' && selStatus.review ? ` · review ${selStatus.review.replace(/_/g, ' ').toLowerCase()}` : ''}. Nothing to start here.</div>
              <a href={href.run(sel.key)} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class="cap">View run <span class="g-go" aria-hidden="true"></span></span></a>
            </div>
          {:else if selRun}
            {@const st = RS[selRun.state]}
            <div in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 14px; padding-top: 16px; border-top: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent)">
              <div style="display: flex; align-items: center; gap: 10px; color: {st.color}; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class={['led', isActive(selRun) ? 'blink' : '']} style="flex-shrink: 0; width: 9px; height: 9px; background: {st.color}; box-shadow: 0 0 10px color-mix(in oklch, {st.color} calc(100% * var(--glow)), transparent)"></span><span style="min-width: 0; word-break: break-word">{st.label}{noteFor(selRun) ? ` · ${noteFor(selRun)}` : ''}</span></div>
              <a class="go cap" href={href.run(sel.key)} style="display: flex; align-items: center; justify-content: center; gap: 12px; min-height: 52px; box-sizing: border-box; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--ac) 55%, transparent); background: color-mix(in oklch, var(--ac) 10%, transparent); color: color-mix(in oklab, var(--ac) 30%, var(--bright)); text-decoration: none; font-weight: 700; font-size: 14px; letter-spacing: calc(var(--tracking) * 1.2857)">Open run <span class="g-go" aria-hidden="true"></span></a>
            </div>
          {/if}
        </aside>
      {/if}
    </div>
    {/if}
  </main>
</div>
