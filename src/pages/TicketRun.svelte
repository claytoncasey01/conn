<script lang="ts">
  import { flip } from 'svelte/animate'
  import { live, answerRun, openPr, cancelRun, retryRun, fixChecks, moveJira, cleanupRun, resumeRun } from '../lib/live.svelte.ts'
  import { href } from '../lib/router.svelte.ts'
  import { bootIn, bootOut, rowIn, rowOut } from '../lib/fx.ts'
  import { dur, researchModel } from '../lib/format.ts'
  import Brand from '../lib/Brand.svelte'
  import Nav from '../lib/Nav.svelte'
  import ConfirmAction from '../lib/ConfirmAction.svelte'
  import { canOpenAll, legAfterMerge, legCanClean, legChecksFailing, legHosted, legKeptForOthers, legNeedsPr, legPrWarning, legResumable, legSessionLive, legsToOpen, legWorker, prProgress, runMerged, workSummary } from '../lib/runLegs.ts'
  import type { PrStatus, Run, RunAgent, RunLog, RunRepo, RunState, SourceName } from '../lib/types.ts'
  import { hideTerminal, loadTerminal, terminalPanel, toggleTerminal } from '../lib/terminalPanel.svelte.ts'

  let { key }: { key: string } = $props()

  const born = Date.now()
  const skip = () => Date.now() - born < 800

  const run = $derived(live.snap?.runs.find(r => r.key === key))
  /** The run's first repository: the one its session works in. */
  const lead = $derived(run?.repos[0])
  /** A run of one repository: the page shows it as it always has. With more, each repository is a row of its own. */
  const single = $derived(!!run && run.repos.length === 1)
  const sessions = $derived(live.snap?.sessions ?? [])

  const ACTIVE: RunState[] = ['starting', 'researching', 'asking', 'building']
  const active = $derived(!!run && ACTIVE.includes(run.state))
  const dead = $derived(run?.state === 'failed' || run?.state === 'interrupted' || run?.state === 'cancelled')
  const sessionId = $derived(lead?.session?.sessionId ?? null)
  /** A session is running for this run, found the way the server finds it (by name or folder, any repository's): a leg's session stops following a run once its PR is open. */
  const sessionLive = $derived(!!run && (live.snap?.sessions ?? []).some(x => x.name === run.key || run.repos.some(leg => x.cwd === leg.worktree)))
  /** The lead slot's session when Conn hosts it (one repository, or the shared layout's one session): it has a terminal panel here */
  const hosted = $derived(run && lead && (single || run.layout === 'shared') ? legHosted(run, lead, live.snap?.sessions ?? []) : null)
  /** While the first session is still opening it is not in the registry yet; offering RESUME then would start a second one. */
  const resumable = $derived(!!run && !!lead && (run.state === 'pr_open' || (run.state === 'building' && !!lead.claudeSessionId)) && !lead.cleaned && !sessionLive)

  // ---- Jira: the status the ticket is in now. conn does not know the workflow's names: a run moves a to-do ticket into an in-progress status, and the PR into its review status when there is one ----
  const jira = $derived(run ? [{ label: run.jiraStatus.toUpperCase(), after: 'moves to an in-progress status when the run starts, and to its review status when the PR opens', st: 'now' }] : [])
  const JST: Record<string, { border: string; bg: string; fg: string }> = {
    now: { border: 'color-mix(in oklch, var(--ok) 60%, transparent)', bg: 'color-mix(in oklch, var(--ok) 12%, transparent)', fg: 'color-mix(in oklab, var(--ok) 35%, var(--bright))' },
    past: { border: 'color-mix(in oklch, var(--edge) 20%, transparent)', bg: 'transparent', fg: 'color-mix(in oklab, var(--mu) 75%, var(--panel))' },
    next: { border: 'color-mix(in oklch, var(--edge) 20%, transparent)', bg: 'transparent', fg: 'var(--mu)' },
  }

  // ---- the open pull request ----
  const pr = $derived(lead?.pr?.status ?? null)
  const prSummary = (p: PrStatus) =>
    p.state === 'merged' || p.state === 'closed'
      ? p.state === 'merged' ? 'merged' : 'closed without merging'
      : p.checks.state === 'none' ? 'no checks' : p.checks.state === 'pending' ? `${p.checks.pending} of ${p.checks.total} checks running` : `checks ${p.checks.state}`
  const CHECK_LOOK: Record<PrStatus['checks']['state'], { color: string; label: string }> = {
    none: { color: 'var(--mu)', label: 'NO CHECKS' },
    pending: { color: 'var(--warn)', label: 'CHECKS RUNNING' },
    passing: { color: 'var(--ok)', label: 'CHECKS PASSING' },
    failing: { color: 'var(--bad)', label: 'CHECKS FAILING' },
  }
  /** The status to move the ticket to after the merge is typed: the names are the workflow's, not conn's. */
  let jiraTarget = $state<string | null>(null)
  const jiraValue = $derived((jiraTarget ?? '').trim())
  const prDone = $derived(pr?.state === 'merged' || pr?.state === 'closed')
  /** Every PR merged or closed, at least one merged: the ticket may move on. */
  const mergedAll = $derived(!!run && runMerged(run))
  /** A leg's row is drawn in its PR's colour: merged, red, open, or none yet. */
  const legRim = (leg: RunRepo) =>
    leg.pr?.status?.state === 'merged'
      ? 'color-mix(in oklch, var(--role-advisor) 45%, transparent)'
      : leg.pr?.status?.checks.state === 'failing'
        ? 'color-mix(in oklch, var(--bad) 45%, transparent)'
        : leg.pr
          ? 'color-mix(in oklch, var(--ok) 30%, transparent)'
          : 'color-mix(in oklch, var(--edge) 16%, transparent)'

  // ---- stages ----
  const K = {
    done: { color: 'var(--ok)', bg: 'linear-gradient(160deg, color-mix(in oklch, var(--ok) 10%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%)', border: 'color-mix(in oklch, var(--ok) 28%, transparent)', bar: '100%' },
    research: { color: 'var(--info)', bg: 'linear-gradient(160deg, color-mix(in oklch, var(--info) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%)', border: 'color-mix(in oklch, var(--info) 40%, transparent)', bar: '55%' },
    ask: { color: 'var(--warn)', bg: 'linear-gradient(160deg, color-mix(in oklch, var(--warn) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%)', border: 'color-mix(in oklch, var(--warn) 40%, transparent)', bar: '55%' },
    build: { color: 'var(--role-lead)', bg: 'linear-gradient(160deg, color-mix(in oklch, var(--role-lead) 14%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%)', border: 'color-mix(in oklch, var(--role-lead) 40%, transparent)', bar: '55%' },
    todo: { color: 'color-mix(in oklab, var(--mu) 75%, var(--panel))', bg: 'color-mix(in oklch, var(--panel) 60%, transparent)', border: 'color-mix(in oklch, var(--edge) 16%, transparent)', bar: '0%' },
    dead: { color: 'var(--bad)', bg: 'linear-gradient(160deg, color-mix(in oklch, var(--bad) 10%, transparent), color-mix(in oklch, var(--panel) 80%, transparent) 60%)', border: 'color-mix(in oklch, var(--bad) 40%, transparent)', bar: '55%' },
  }
  const STAGES = ['research', 'ask', 'build', 'pr'] as const

  const open = $derived(run ? run.questions.filter(q => q.answer === null).length : 0)
  const agentsDone = $derived(run ? run.agents.filter(a => a.state === 'done').length : 0)

  const stages = $derived.by(() => {
    if (!run || !lead) return []
    const at = STAGES.indexOf(run.stage)
    const deadLabel = run.state.toUpperCase()
    const mk = (i: number, name: string, kind: keyof typeof K, state: string, note: string) => ({ i, name, state, note, ...K[kind] })
    const ses = lead.session
    const out = []

    out.push(
      at > 0
        ? mk(0, '1 RESEARCH', 'done', 'DONE', `${run.agents.length} agents · ${agentsDone} finished`)
        : dead
          ? mk(0, '1 RESEARCH', 'dead', deadLabel, run.error ?? 'stopped before research finished')
          : mk(0, '1 RESEARCH', 'research', 'RUNNING', `${agentsDone} of ${run.agents.length} agents finished`)
    )
    const answered = `${run.answered.length} answered by sources, `
    out.push(
      run.state === 'asking'
        ? mk(1, '2 ASK', 'ask', 'WAITING ON YOU', `${answered}${open} left for you`)
        : at > 1 || (at === 1 && !dead)
          ? mk(1, '2 ASK', 'done', 'DONE', `${answered}${run.questions.length} by you`)
          : at === 1
            ? mk(1, '2 ASK', 'dead', deadLabel, `${answered}${open} left for you`)
            : mk(1, '2 ASK', 'todo', 'QUEUED', 'only what the sources could not answer')
    )
    const work = run.repos.some(leg => leg.session) ? workSummary(run) : ''
    out.push(
      at > 2
        ? mk(2, '3 BUILD', 'done', 'DONE', work || 'session finished')
        : at === 2
          ? dead
            ? mk(2, '3 BUILD', 'dead', deadLabel, work || (run.error ?? ''))
            : mk(2, '3 BUILD', 'build', 'RUNNING', ses ? `session ${run.key} is ${ses.status === 'waiting' ? 'waiting for you' : ses.status === 'busy' ? 'busy' : 'idle'} · ${work}` : 'opening the session')
          : mk(2, '3 BUILD', 'todo', 'QUEUED', 'starts the moment you answer')
    )
    const prs = prProgress(run)
    out.push(
      run.state === 'pr_open' && !single
        ? mk(3, '4 PR', 'done', mergedAll ? 'MERGED' : 'OPEN', `${prs.opened} of ${prs.wanted} PRs opened${prs.merged ? ` · ${prs.merged} merged` : ''}`)
        : run.state === 'pr_open'
        ? mk(3, '4 PR', 'done', pr ? pr.state.toUpperCase() : 'OPEN', lead.pr ? `PR #${lead.pr.number}${pr ? ` · ${prSummary(pr)}` : ''}` : 'PR open')
        : at === 3 && dead
          ? mk(3, '4 PR', 'dead', deadLabel, run.error ?? '')
          : mk(3, '4 PR', 'todo', 'QUEUED', 'Jira moves to its review status when it opens')
    )
    return out
  })

  // ---- questions ----
  let choice = $state<Record<string, string>>({})
  let typed = $state<Record<string, string>>({})
  const valueOf = (id: string): string => typed[id]?.trim() || choice[id] || ''
  const pending = $derived(run?.state === 'asking' ? run.questions : [])
  const left = $derived(pending.filter(q => !valueOf(q.id)).length)
  const ready = $derived(pending.length > 0 && left === 0)

  let sending = $state(false)
  let submitted = $state(false)
  let answerError = $state('')
  // Leaving the ask stage drops the picks too: a retry asks new questions under the same ids (q1, q2 ...).
  $effect(() => {
    if (run?.state !== 'asking') {
      submitted = false
      choice = {}
      typed = {}
    }
  })

  const prWarning = $derived(run && lead ? legPrWarning(run, lead) : '')

  async function send() {
    if (!run || !ready) return
    sending = true
    answerError = ''
    const answers: Record<string, string> = {}
    for (const q of pending) answers[q.id] = valueOf(q.id)
    const r = await answerRun(run.key, answers)
    sending = false
    if (r.ok) submitted = true
    else answerError = r.error ?? 'failed'
  }

  const SRC: Record<SourceName, { color: string; bg: string; border: string }> = {
    JIRA: { color: 'color-mix(in oklab, var(--edge) 55%, var(--bright))', bg: 'color-mix(in oklch, var(--edge) 10%, transparent)', border: 'color-mix(in oklch, var(--edge) 40%, transparent)' },
    CONFLUENCE: { color: 'color-mix(in oklab, var(--ac) 60%, var(--bright))', bg: 'color-mix(in oklch, var(--ac) 10%, transparent)', border: 'color-mix(in oklch, var(--ac) 40%, transparent)' },
    SLACK: { color: 'color-mix(in oklab, var(--ok) 50%, var(--bright))', bg: 'color-mix(in oklch, var(--ok) 10%, transparent)', border: 'color-mix(in oklch, var(--ok) 40%, transparent)' },
    CODE: { color: 'color-mix(in oklab, var(--k-subtask) 55%, var(--bright))', bg: 'color-mix(in oklch, var(--k-subtask) 10%, transparent)', border: 'color-mix(in oklch, var(--k-subtask) 40%, transparent)' },
  }

  // ---- agents and log ----
  const AG: Record<RunAgent['state'], { color: string; glow: string; label: string; border: string; bg: string }> = {
    queued: { color: 'color-mix(in oklab, var(--mu) 75%, var(--panel))', glow: 'none', label: 'QUEUED', border: 'color-mix(in oklch, var(--edge) 16%, transparent)', bg: 'color-mix(in oklch, var(--panel) 70%, transparent)' },
    running: { color: 'var(--info)', glow: '0 0 10px color-mix(in oklch, var(--info) calc(70% * var(--glow)), transparent)', label: 'RUNNING', border: 'color-mix(in oklch, var(--info) 16%, transparent)', bg: 'color-mix(in oklch, var(--panel) 70%, transparent)' },
    done: { color: 'var(--ok)', glow: 'none', label: 'DONE', border: 'color-mix(in oklch, var(--edge) 16%, transparent)', bg: 'color-mix(in oklch, var(--panel) 70%, transparent)' },
    error: { color: 'var(--bad)', glow: 'none', label: 'ERROR', border: 'color-mix(in oklch, var(--bad) 40%, transparent)', bg: 'color-mix(in oklch, var(--bad) 7%, transparent)' },
  }
  const LOG: Record<RunLog['kind'], string> = {
    status: 'var(--ac)',
    branch: 'var(--ac)',
    agent: 'var(--role-agent)',
    source: 'var(--ok)',
    ask: 'var(--warn)',
    build: 'var(--role-lead)',
    pr: 'var(--ok)',
    error: 'var(--bad)',
  }
  const LOG_MAX = 40
  const log = $derived(run ? run.log.map((l, i) => ({ ...l, i })).slice(-LOG_MAX) : [])
  const rel = (at: number) => {
    const s = Math.max(0, Math.floor((live.now - at) / 1000))
    return s < 5 ? 'now' : s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h`
  }

  /** The command last copied, for a moment: each session not opened has its own. */
  let copiedCommand = $state<string | null>(null)
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      copiedCommand = text
      setTimeout(() => (copiedCommand = null), 1500)
    } catch {
      copiedCommand = null
    }
  }

  const cbtn =
    'font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 8px 16px; min-height: 44px; box-sizing: border-box; border-radius: var(--radius); cursor: pointer; background: transparent;'
  // a surface: its rim, fill and radius are data the theme draws (lookCss.ts)
  const card = 'display: flex; flex-direction: column; gap: 8px; padding: 14px 18px; --rim: color-mix(in oklch, var(--edge) 16%, transparent); --r: calc(var(--radius) * 1.5); --wash: color-mix(in oklch, var(--panel) 70%, transparent)'
</script>

<svelte:head><title>{key}</title></svelte:head>

{#snippet manual(cmd: string)}
  {@const copied = copiedCommand === cmd}
  <div in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 8px; padding: 12px 16px; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--edge) 6%, transparent)">
    <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Session not opened · run this in a terminal</div>
    <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px">
      <code style="flex: 1 1 320px; min-width: 0; font-size: 12px; line-height: 1.5; color: var(--tx); word-break: break-all; user-select: all">{cmd}</code>
      <button type="button" onclick={() => copy(cmd)} style="{cbtn} color: {copied ? 'color-mix(in oklab, var(--ok) 50%, var(--bright))' : 'var(--tx)'}; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent)" class="cap">{copied ? 'Copied' : 'Copy'}</button>
    </div>
  </div>
{/snippet}

{#snippet moveTicket(r: Run)}
  <label for="jira-target" style="font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Move Jira to</label>
  <input id="jira-target" type="text" value={jiraTarget ?? ''} oninput={e => (jiraTarget = e.currentTarget.value)} placeholder="status name" maxlength="40" style="flex: 0 1 220px; min-height: 44px; box-sizing: border-box; padding: 0 12px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); background: color-mix(in oklch, var(--panel) 90%, transparent); color: color-mix(in oklab, var(--tx) 50%, var(--bright)); font: inherit; font-size: 13px" />
  {#if jiraValue}
    <ConfirmAction label="MOVE TICKET" confirmText="Moves {r.key} from {r.jiraStatus} to “{jiraValue}”." confirmLabel="MOVE" tone="cyan" run={() => moveJira(key, jiraValue)} />
  {/if}
{/snippet}

<div class="page" style="position: relative; min-height: 100vh; color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font-size: 13px; box-sizing: border-box; display: flex; flex-direction: column; --orb-1: var(--ac); --orb-1-a: 16%; --orb-2: var(--edge); --orb-2-a: 13%; --orb-3: var(--warn); --orb-3-a: 7%">

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

    {#if !live.snap}
      <div role="status" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 40vh; text-align: center">
        <span class="led blink" style="width: 10px; height: 10px; background: var(--ac); box-shadow: 0 0 12px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></span>
        <div class="hud cap" style="font-size: 13px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{live.connected ? 'Loading' : 'Connecting…'}</div>
      </div>
    {:else if !live.snap.openWork}
      <div role="status" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; min-height: 40vh; text-align: center">
        <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">Atlassian is off. Enable it in Settings.</div>
        <div style="font-size: 12px; color: var(--mu)">Open work reads your Jira sprint, so it needs the Atlassian module on and at least one Jira project key.</div>
        <a href={href.settings()} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class="cap">Open Settings <span class="g-go" aria-hidden="true"></span></span></a>
      </div>
    {:else if !run || !lead}
      <div role="status" style="display: flex; flex-direction: column; align-items: center; gap: 12px; padding: 56px 20px; border: 1px dashed color-mix(in oklch, var(--edge) 25%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 60%, transparent); text-align: center">
        <div class="hud" style="font-weight: 700; font-size: 22px; letter-spacing: calc(var(--tracking) * 0.5714); color: color-mix(in oklab, var(--tx) 50%, var(--bright))">No run for <span data-content>{key}</span></div>
        <div style="font-size: 12px; color: var(--mu)">It was never started here, or the server has no record of it.</div>
        <a href={href.work()} style="color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-decoration: none; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 6px 0"><span class="cap"><span class="g-go-back" aria-hidden="true"></span> Open work</span></a>
      </div>
    {:else}

    <!-- run header -->
    <section aria-label="Run" class="surf brk" in:bootIn={{ skip: skip() }} out:bootOut style="--c: var(--ac); display: flex; flex-direction: column; gap: 18px; padding: 20px 24px; --rim: color-mix(in oklch, var(--ac) 34%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--ac) 9%, transparent), color-mix(in oklch, var(--panel) 94%, transparent) 55%)">
      <div class="stripe" style="position: absolute; left: 0; top: 0; width: 100%; height: 2px; background: linear-gradient(90deg, var(--ac), transparent); box-shadow: 0 0 12px color-mix(in oklch, var(--ac) calc(100% * var(--glow)), transparent)"></div>
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px 18px">
        <a href={href.work()} style="color: var(--mu); text-decoration: none; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); padding: 6px 0"><span class="cap"><span class="g-go-back" aria-hidden="true"></span> Open work</span></a>
        <span data-content class="hud" style="font-weight: 700; font-size: 26px; letter-spacing: calc(var(--tracking) * 0.3571); color: color-mix(in oklab, var(--ac) 60%, var(--bright)); text-shadow: 0 0 14px color-mix(in oklch, var(--ac) calc(50% * var(--glow)), transparent)">{run.key}</span>
        <span data-content class="hud" style="flex: 1 1 320px; font-weight: 600; font-size: 20px; color: var(--bright)">{run.title}</span>
        {#if single && lead.pr}
          <a href={lead.pr.url} target="_blank" rel="noopener noreferrer" style="display: flex; align-items: center; gap: 8px; min-height: 44px; box-sizing: border-box; padding: 0 14px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--ok) 45%, transparent); background: color-mix(in oklch, var(--ok) 8%, transparent); color: color-mix(in oklab, var(--ok) 50%, var(--bright)); text-decoration: none; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class="led" style="width: 8px; height: 8px; background: var(--ok); box-shadow: 0 0 10px color-mix(in oklch, var(--ok) calc(100% * var(--glow)), transparent)"></span>PR #{lead.pr.number} <span class="g-go" aria-hidden="true"></span></a>
        {/if}
        {#if sessionId && (single || run.layout === 'shared')}
          <a href={href.overview(sessionId)} style="display: flex; align-items: center; gap: 8px; min-height: 44px; box-sizing: border-box; padding: 0 14px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--role-lead) 40%, transparent); background: color-mix(in oklch, var(--role-lead) 8%, transparent); color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); text-decoration: none; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571)"><span class="led" style="width: 8px; height: 8px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></span>SESSION {run.key} <span class="g-go" aria-hidden="true"></span></a>
        {/if}
        {#if hosted}
          <button type="button" class="cap" onclick={() => toggleTerminal(hosted.id)} aria-pressed={terminalPanel.open === hosted.id} aria-label="Terminal of {run.key}" style="display: flex; align-items: center; min-height: 44px; box-sizing: border-box; padding: 0 14px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); background: {terminalPanel.open === hosted.id ? 'color-mix(in oklch, var(--edge) 14%, transparent)' : 'transparent'}; color: color-mix(in oklab, var(--edge) 55%, var(--bright)); font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer">Terminal</button>
        {/if}
      </div>
      {#if hosted && terminalPanel.open === hosted.id}
        {#key hosted.id}
          {#await loadTerminal() then { default: TerminalPanel }}
            <TerminalPanel sessionId={hosted.id} name={hosted.name} onclose={hideTerminal} />
          {/await}
        {/key}
      {/if}
      <div style="display: flex; flex-wrap: wrap; gap: 10px 24px; align-items: center; font-size: 12px; color: var(--mu)">
        {#if single}
          <span style="word-break: break-all"><span style="color: var(--ac)">⎇ </span>{lead.branch} <span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel))">←</span> origin/{lead.base} <span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel))">· PR into</span> {lead.prBase}</span>
        {:else}
          <span>{run.repos.length} repositories · {run.layout === 'split' ? 'a session in each' : 'one session across them'}</span>
        {/if}
        <span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel))">·</span>
        <span>research agents on {researchModel(run.model)}</span>
        <span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel))">·</span>
        <span>started {dur(live.now - run.startedAt)} ago</span>
      </div>
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 14px">
        <div style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.1429); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" class="cap">Jira</div>
        {#each jira as j, i (j.label)}
          {@const c = JST[j.st]}
          <div in:bootIn={{ index: i, skip: skip() }} style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px">
            <span style="padding: 5px 11px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {c.border}; background: {c.bg}; color: {c.fg}; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{j.label}</span>
            {#if j.after}<span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel)); font-size: 11px">{j.after}</span>{/if}
          </div>
        {/each}
      </div>

      {#if run.error}
        <div role="alert" in:bootIn={{ skip: skip() }} out:bootOut style="padding: 12px 16px; border: 1px var(--border-style) color-mix(in oklch, var(--bad) 40%, transparent); border-radius: var(--radius); background: color-mix(in oklch, var(--bad) 10%, transparent); color: color-mix(in oklab, var(--bad) 55%, var(--bright)); font-size: 12px; line-height: 1.5; word-break: break-word">{run.error}</div>
      {/if}

      {#if single && lead.manualCommand}
        {@render manual(lead.manualCommand)}
      {/if}

      {#if single && resumable}
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px">
          <span style="font-size: 12px; color: var(--mu)">No session for {run.key} is running.</span>
          <ConfirmAction label="RESUME SESSION" confirmText="Opens a new session in {lead.worktree}, where Settings say sessions open, and continues {lead.claudeSessionId ? 'the same conversation' : 'the last conversation in that folder'}." confirmLabel="RESUME" tone="cyan" run={() => resumeRun(key)} />
        </div>
      {/if}

      {#if active || run.state === 'failed' || run.state === 'interrupted'}
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px 24px">
          {#if single && run.state === 'building' && lead.session && lead.ahead > 0}
            <ConfirmAction label="OPEN PR" confirmText="Pushes {lead.branch} and opens a PR into {lead.prBase}. Jira moves to its review status, if it has one.{prWarning}" confirmLabel="OPEN PR" tone="green" run={() => openPr(key)} />
          {/if}
          {#if run.state === 'failed' || run.state === 'interrupted'}
            <ConfirmAction label="RETRY" confirmText="Retry {run.key}?" confirmLabel="RETRY" tone="cyan" run={() => retryRun(key)} />
          {/if}
          {#if active}
            <ConfirmAction label="CANCEL RUN" confirmText={run.state === 'building' ? 'Stops tracking this run. The branch and worktree stay, and the session keeps running until you close it.' : 'Stops the research agents. The branch and worktree stay.'} confirmLabel="CANCEL RUN" tone="red" run={() => cancelRun(key)} />
          {/if}
        </div>
      {/if}

      {#if !single && mergedAll}
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px 18px">
          {@render moveTicket(run)}
        </div>
      {/if}
    </section>

    <!-- one row per repository, when the run builds in more than one -->
    {#if !single}
      <section aria-label="Repositories" style="display: flex; flex-direction: column; gap: 10px">
        <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Repositories <span class="g-sep" aria-hidden="true"></span> {run.repos.length}</div>
        {#each run.repos as leg, i (leg.repo.toLowerCase())}
          {@const ps = leg.pr?.status ?? null}
          {@const done = ps?.state === 'merged' || ps?.state === 'closed'}
          {@const cl = ps ? CHECK_LOOK[ps.checks.state] : null}
          {@const worker = legWorker(run, leg)}
          {@const workerLive = legSessionLive(run, worker, sessions)}
          {@const legHostedHere = run.layout === 'split' ? legHosted(run, leg, live.snap?.sessions ?? []) : null}
          <div class="surf" in:bootIn={{ index: i, skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 10px; padding: 14px 18px; --rim: {legRim(leg)}; --r: calc(var(--radius) * 1.5); --wash: color-mix(in oklch, var(--panel) 70%, transparent)">
            <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px">
              <span data-content style="font-size: 14px; color: var(--bright); overflow-wrap: anywhere">{leg.repo}</span>
              {#if i === 0}<span style="font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: color-mix(in oklab, var(--ac) 60%, var(--bright))" class="cap">lead</span>{/if}
              {#if leg.pr}
                <a href={leg.pr.url} target="_blank" rel="noopener noreferrer" style="color: color-mix(in oklab, var(--ok) 50%, var(--bright)); text-decoration: none; font-size: 13px">PR #{leg.pr.number} ↗</a>
                {#if ps}
                  <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{ps.state.toUpperCase()}</span>
                  {#if !done && cl}
                    <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {cl.color}; color: {cl.color}; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{cl.label}{ps.checks.state === 'pending' ? ` · ${ps.checks.pending} OF ${ps.checks.total}` : ''}</span>
                    {#if ps.review}<span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{ps.review.replace(/_/g, ' ')}</span>{/if}
                  {/if}
                {:else}
                  <span style="font-size: 12px; color: var(--mu)">waiting for the first check of GitHub…</span>
                {/if}
              {/if}
              {#if run.layout === 'split' && leg.session?.sessionId}
                <a href={href.overview(leg.session.sessionId)} style="color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); text-decoration: none; font-size: 12px"><span class="cap">Session <span class="g-go" aria-hidden="true"></span></span></a>
              {/if}
              {#if legHostedHere}
                <button type="button" class="cap" onclick={() => toggleTerminal(legHostedHere.id)} aria-pressed={terminalPanel.open === legHostedHere.id} aria-label="Terminal of {run.key} in {leg.repo}" style="display: flex; align-items: center; min-height: 44px; box-sizing: border-box; padding: 0 14px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 40%, transparent); background: {terminalPanel.open === legHostedHere.id ? 'color-mix(in oklch, var(--edge) 14%, transparent)' : 'transparent'}; color: color-mix(in oklab, var(--edge) 55%, var(--bright)); font: inherit; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer">Terminal</button>
              {/if}
            </div>
            {#if legHostedHere && terminalPanel.open === legHostedHere.id}
              {#key legHostedHere.id}
                {#await loadTerminal() then { default: TerminalPanel }}
                  <TerminalPanel sessionId={legHostedHere.id} name={`${run.key} in ${leg.repo}`} onclose={hideTerminal} />
                {/await}
              {/key}
            {/if}
            <div style="display: flex; flex-wrap: wrap; gap: 6px 18px; font-size: 12px; color: var(--mu)">
              <span style="word-break: break-all"><span style="color: var(--ac)">⎇ </span>{leg.branch} <span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel))">←</span> origin/{leg.base} <span style="color: color-mix(in oklab, var(--mu) 75%, var(--panel))">· PR into</span> {leg.prBase}</span>
              <span>{leg.ahead} commit{leg.ahead === 1 ? '' : 's'} · {leg.dirty} changed file{leg.dirty === 1 ? '' : 's'}</span>
            </div>
            {#if legChecksFailing(run, leg)}
              <div style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); word-break: break-word">Failing: {ps?.checks.failed.join(', ') || 'see GitHub'}</div>
            {/if}
            {#if leg.manualCommand}
              {@render manual(leg.manualCommand)}
            {/if}
            {#if legAfterMerge(run, leg)}
              <div style="font-size: 12px; line-height: 1.5; color: var(--warn)">These commits have no PR. The run counts as merged without them; open one here if they belong in it.</div>
            {/if}
            {#if leg.cleaned}
              <span style="font-size: 12px; color: var(--ok)">Worktree removed · branch {leg.branch} stays</span>
            {:else if legKeptForOthers(run, leg) && done}
              <span style="font-size: 12px; color: var(--mu)">The worktree stays while the other PRs are open: the run's session works here for them.</span>
            {/if}
            <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px">
              {#if legNeedsPr(run, leg)}
                <ConfirmAction label="OPEN PR" confirmText="Pushes {leg.branch} in {leg.repo} and opens a PR into {leg.prBase}. Jira moves to its review status as Settings says, if it has one.{legPrWarning(run, leg)}" confirmLabel="OPEN PR" tone="green" run={() => openPr(key, leg.repo)} />
              {/if}
              {#if legChecksFailing(run, leg)}
                <ConfirmAction label="FIX FAILING CHECKS" confirmText="Tells the session working in {worker.worktree} which checks of {leg.repo} failed and to read them with gh, fix the cause and push." confirmLabel="SEND" tone="cyan" run={() => fixChecks(key, leg.repo)} />
                {#if !workerLive}
                  <span style="font-size: 12px; color: var(--mu); overflow-wrap: anywhere">No running session found in {worker.worktree}: resume it first.</span>
                {/if}
              {/if}
              {#if legResumable(run, leg, legSessionLive(run, leg, sessions))}
                <span style="font-size: 12px; color: var(--mu); overflow-wrap: anywhere">No session is running in {leg.worktree}.</span>
                <ConfirmAction label="RESUME SESSION" confirmText="Opens a new session in {leg.worktree}, where Settings say sessions open, and continues {leg.claudeSessionId ? 'the same conversation' : 'the last conversation in that folder'}." confirmLabel="RESUME" tone="cyan" run={() => resumeRun(key, leg.repo)} />
              {/if}
              {#if legCanClean(run, leg)}
                <ConfirmAction label="REMOVE WORKTREE" confirmText="Removes {leg.worktree}. Refused if a session is still running there or files are uncommitted. The branch stays." confirmLabel="REMOVE" tone="red" run={() => cleanupRun(key, leg.repo)} />
              {/if}
            </div>
          </div>
        {/each}
        {#if canOpenAll(run)}
          <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px">
            <ConfirmAction label="OPEN ALL PRS" confirmText="Pushes {lead.branch} in each of the {legsToOpen(run).length} repositories with commits and no PR, and opens their PRs one after another. A repository refused does not stop the others." confirmLabel="OPEN ALL PRS" tone="green" run={() => openPr(key)} />
            <span style="font-size: 12px; color: var(--mu)">Or open each from its row.</span>
          </div>
        {/if}
      </section>
    {/if}

    <!-- the open pull request -->
    {#if single && run.state === 'pr_open' && lead.pr}
      {@const cl = pr ? CHECK_LOOK[pr.checks.state] : null}
      <section aria-label="Pull request" in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 12px; padding: 16px 20px; border: 1px var(--border-style) {pr?.state === 'merged' ? 'color-mix(in oklch, var(--role-advisor) 45%, transparent)' : pr?.checks.state === 'failing' ? 'color-mix(in oklch, var(--bad) 45%, transparent)' : 'color-mix(in oklch, var(--ok) 30%, transparent)'}; border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 70%, transparent)">
        <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 14px">
          <span class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Pull request //</span>
          <a href={lead.pr.url} target="_blank" rel="noopener noreferrer" style="color: color-mix(in oklab, var(--ok) 50%, var(--bright)); text-decoration: none; font-size: 13px">PR #{lead.pr.number} ↗</a>
          {#if pr}
            <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{pr.state.toUpperCase()}</span>
            {#if !prDone && cl}
              <span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {cl.color}; color: {cl.color}; font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{cl.label}{pr.checks.state === 'pending' ? ` · ${pr.checks.pending} OF ${pr.checks.total}` : ''}</span>
              {#if pr.review}<span style="padding: 4px 9px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 30%, transparent); color: var(--tx); font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143)">{pr.review.replace(/_/g, ' ')}</span>{/if}
              {#if pr.mergeable}<span style="font-size: 11px; letter-spacing: calc(var(--tracking) * 0.7143); color: var(--mu)">MERGE {pr.mergeable}</span>{/if}
            {/if}
            <span style="margin-left: auto; font-size: 11px; color: color-mix(in oklab, var(--mu) 75%, var(--panel))">checked {dur(live.now - pr.at)} ago</span>
          {:else}
            <span style="font-size: 12px; color: var(--mu)">waiting for the first check of GitHub…</span>
          {/if}
        </div>
        {#if pr && !prDone && pr.checks.state === 'failing'}
          <div style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); word-break: break-word">Failing: {pr.checks.failed.join(', ') || 'see GitHub'}</div>
          <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px">
            <ConfirmAction label="FIX FAILING CHECKS" confirmText="Tells the session {run.key} which checks failed and to read them with gh, fix the cause and push." confirmLabel="SEND" tone="cyan" run={() => fixChecks(key)} />
            {#if !sessionLive}
              <span style="font-size: 12px; color: var(--mu)">No running session found for {run.key}: resume it first.</span>
            {/if}
          </div>
        {/if}
        {#if prDone}
          <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px 18px">
            {#if pr?.state === 'merged'}
              {@render moveTicket(run)}
            {/if}
            {#if lead.cleaned}
              <span style="font-size: 12px; color: var(--ok)">Worktree removed · branch {lead.branch} stays</span>
            {:else}
              <ConfirmAction label="REMOVE WORKTREE" confirmText="Removes {lead.worktree}. Refused if the session is still running or files are uncommitted. The branch stays." confirmLabel="REMOVE" tone="red" run={() => cleanupRun(key)} />
            {/if}
          </div>
        {/if}
      </section>
    {/if}

    <!-- pipeline -->
    <section aria-label="Pipeline" class="tiles" style="--cols: 4">
      {#each stages as s (s.name)}
        <div in:bootIn={{ index: s.i, skip: skip() }} class="surf" style="position: relative; overflow: hidden; --wash: {s.bg}; --rim: {s.border}; --r: calc(var(--radius) * 1.5); padding: 14px 16px">
          <div style="position: absolute; left: 0; top: 0; width: {s.bar}; height: 2px; background: {s.color}; box-shadow: 0 0 10px color-mix(in oklch, {s.color} calc(100% * var(--glow)), transparent)"></div>
          <div style="display: flex; align-items: center; justify-content: space-between; gap: 8px">
            <span class="hud" style="font-weight: 700; font-size: 16px; letter-spacing: calc(var(--tracking) * 0.7143); color: {s.color}">{s.name}</span>
            <span style="font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571); color: {s.color}; text-align: right">{s.state}</span>
          </div>
          <div style="font-size: 11px; line-height: 1.5; color: var(--mu); margin-top: 6px; word-break: break-word">
            {#if s.i === 3 && single && lead.pr}<a href={lead.pr.url} target="_blank" rel="noopener noreferrer" style="color: color-mix(in oklab, var(--ok) 50%, var(--bright)); text-decoration: none"><span>{s.note} <span class="g-go" aria-hidden="true"></span></span></a>{:else}{s.note}{/if}
          </div>
        </div>
      {/each}
    </section>

    <div style="display: flex; flex-wrap: wrap; gap: 20px; align-items: flex-start">

      <!-- questions -->
      <section aria-label="Questions" style="flex: 999 1 600px; min-width: 0; display: flex; flex-direction: column; gap: 16px">

        {#if pending.length > 0}
          <div in:bootIn={{ skip: skip() }} out:bootOut style="display: flex; flex-direction: column; gap: 14px">
            <div class="hud" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: var(--warn)"><span class="cap">Needs your answer</span> <span class="g-sep" aria-hidden="true"></span> {left} OF {pending.length} LEFT</div>
            {#each pending as q, i (q.id)}
              {@const done = !!valueOf(q.id)}
              <div class="surf" in:bootIn={{ index: i, skip: skip() }} out:bootOut animate:flip={{ duration: 380 }} style="display: flex; flex-direction: column; gap: 12px; padding: 18px 20px; --rim: {done ? 'color-mix(in oklch, var(--ok) 40%, transparent)' : 'color-mix(in oklch, var(--warn) 38%, transparent)'}; --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--warn) 7%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 60%)">
                <div style="display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px 12px">
                  <span class="hud" style="font-weight: 700; font-size: 13px; color: var(--warn); letter-spacing: calc(var(--tracking) * 0.7143)">Q{i + 1}</span>
                  <span style="flex: 1 1 300px; font-size: 14px; line-height: 1.5; color: var(--bright)">{q.text}</span>
                </div>
                <div style="font-size: 11px; line-height: 1.5; color: var(--mu)">{q.why}</div>
                <div role="group" aria-label="Answer" style="display: flex; flex-wrap: wrap; gap: 8px">
                  {#each q.choices as c (c.id)}
                    {@const on = choice[q.id] === c.id && !typed[q.id]?.trim()}
                    <button data-content class="opt" onclick={() => (choice[q.id] = c.id)} aria-pressed={on} style="padding: 10px 16px; min-height: 44px; text-align: left; cursor: pointer; border-radius: var(--radius); border: 1px var(--border-style) {on ? 'color-mix(in oklch, var(--warn) 75%, transparent)' : 'color-mix(in oklch, var(--edge) 22%, transparent)'}; background: {on ? 'color-mix(in oklch, var(--warn) 13%, transparent)' : 'transparent'}; color: {on ? 'color-mix(in oklab, var(--warn) 40%, var(--bright))' : 'var(--mu)'}; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)">{on ? '● ' : '○ '}{c.label}</button>
                  {/each}
                </div>
                <div style="display: flex; align-items: center; gap: 10px">
                  <label style="flex: 0 0 auto; font-size: 10px; letter-spacing: var(--tracking); color: color-mix(in oklab, var(--mu) 85%, var(--panel))" for="own-{q.id}" class="cap">Or type</label>
                  <input id="own-{q.id}" type="text" placeholder="your own answer" bind:value={typed[q.id]} style="flex: 1; min-width: 0; min-height: 44px; box-sizing: border-box; padding: 0 12px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); background: color-mix(in oklch, var(--panel) 90%, transparent); color: color-mix(in oklab, var(--tx) 60%, var(--bright)); font: inherit; font-size: 12px" />
                </div>
              </div>
            {/each}

            <button type="button" class="go" onclick={send} disabled={!ready || sending || submitted} style="display: flex; align-items: center; justify-content: center; gap: 12px; min-height: 52px; border-radius: var(--radius); cursor: {ready && !sending && !submitted ? 'pointer' : 'default'}; border: 1px var(--border-style) {ready ? 'transparent' : 'color-mix(in oklch, var(--edge) 22%, transparent)'}; background: {ready ? 'linear-gradient(135deg, var(--ac), var(--k-subtask))' : 'transparent'}; color: {ready ? 'var(--onac)' : 'color-mix(in oklab, var(--mu) 75%, var(--panel))'}; font-weight: 700; font-size: 14px; letter-spacing: calc(var(--tracking) * 1.1429); box-shadow: {ready ? '0 0 26px color-mix(in oklch, var(--ac) calc(38% * var(--glow)), transparent)' : 'none'}; opacity: {sending || submitted ? 0.6 : 1}">{sending ? 'SENDING ANSWERS…' : submitted ? 'ANSWERS SENT · WAITING ON THE SERVER' : ready ? '▶ SEND ANSWERS AND START THE BUILD' : `ANSWER ${left} MORE TO CONTINUE`}</button>
            {#if answerError}
              <div role="alert" style="font-size: 12px; line-height: 1.5; color: color-mix(in oklab, var(--bad) 55%, var(--bright)); word-break: break-word">{answerError}</div>
            {/if}
          </div>
        {/if}

        {#if run.state === 'building' && run.questions.length > 0}
          <div class="surf brk" in:bootIn={{ skip: skip() }} out:bootOut style="--c: var(--role-lead); display: flex; flex-direction: column; gap: 10px; padding: 20px 22px; --rim: color-mix(in oklch, var(--role-lead) 40%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--role-lead) 10%, transparent), color-mix(in oklch, var(--panel) 94%, transparent) 60%)">
            <div style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--role-lead) 65%, var(--bright)); font-size: 12px; letter-spacing: var(--tracking)"><span class="led blink" style="width: 9px; height: 9px; background: var(--role-lead); box-shadow: 0 0 10px color-mix(in oklch, var(--role-lead) calc(100% * var(--glow)), transparent)"></span>ANSWERS SENT · BUILD RUNNING</div>
            <div style="font-size: 13px; line-height: 1.6; color: var(--tx)">The session <span style="color: color-mix(in oklab, var(--role-lead) 65%, var(--bright))">{run.key}</span> has the research brief and your {run.questions.length} answer{run.questions.length === 1 ? '' : 's'}. Follow it in Sessions, or stay here for the run log.</div>
          </div>
        {/if}

        <!-- auto answered -->
        <div style="display: flex; flex-direction: column; gap: 10px">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Answered from sources <span class="g-sep" aria-hidden="true"></span> {run.answered.length}</div>
          {#each run.answered as a, i (i)}
            {@const c = SRC[a.src]}
            <div in:bootIn={{ index: i, skip: skip() }} out:bootOut animate:flip={{ duration: 380 }} class="surf" style={card}>
              <div style="font-size: 13px; line-height: 1.5; color: var(--tx)">{a.q}</div>
              <div style="font-size: 12px; line-height: 1.55; color: color-mix(in oklab, var(--mu) 70%, var(--tx))"><span style="color: var(--ok)">→ </span>{a.a}</div>
              <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.8571)">
                <span style="padding: 3px 8px; border-radius: calc(var(--radius) * 0.75); border: 1px var(--border-style) {c.border}; background: {c.bg}; color: {c.color}">{a.src}</span>
                <span style="color: color-mix(in oklab, var(--mu) 85%, var(--panel)); min-width: 0; word-break: break-word">{a.where}</span>
                <span style="margin-left: auto; color: color-mix(in oklab, var(--mu) 85%, var(--panel))">CONFIDENCE {a.conf}</span>
              </div>
            </div>
          {:else}
            <div style="font-size: 12px; color: color-mix(in oklab, var(--mu) 75%, var(--panel))">Nothing answered from the sources yet.</div>
          {/each}
        </div>
      </section>

      <!-- right column -->
      <aside aria-label="Agents and log" style="flex: 1 1 440px; max-width: 520px; min-width: 0; display: flex; flex-direction: column; gap: 20px">

        <div style="display: flex; flex-direction: column; gap: 10px">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Research agents <span class="g-sep" aria-hidden="true"></span> {run.agents.length}</div>
          {#each run.agents as g, i (g.id)}
            {@const c = AG[g.state]}
            <div in:bootIn={{ index: i, skip: skip() }} out:bootOut animate:flip={{ duration: 380 }} class="surf" style="display: flex; align-items: center; gap: 14px; padding: 12px 14px; --rim: {c.border}; --r: calc(var(--radius) * 1.5); --wash: {c.bg}">
              <div aria-hidden="true" class={g.state === 'running' ? 'blink' : ''} style="flex-shrink: 0; width: 14px; height: 14px; border-radius: calc(var(--radius) * 0.75); background: {c.color}; box-shadow: {c.glow}"></div>
              <div style="flex: 1; min-width: 0">
                <div style="font-size: 13px; color: var(--tx)">{g.name}</div>
                {#if g.finding}<div style="font-size: 11px; color: {g.state === 'error' ? 'color-mix(in oklab, var(--bad) 55%, var(--bright))' : 'var(--mu)'}; margin-top: 2px; word-break: break-word">{g.finding}</div>{/if}
              </div>
              <div style="flex-shrink: 0; text-align: right; font-size: 10px; letter-spacing: calc(var(--tracking) * 0.7143); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">
                <div style="color: {c.color}">{c.label}</div>
                <div style="margin-top: 2px">{researchModel(g.model)}</div>
              </div>
            </div>
          {/each}
        </div>

        <div style="display: flex; flex-direction: column; gap: 10px">
          <div class="hud cap" style="font-size: 11px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">Run log</div>
          <div style="display: flex; flex-direction: column; border: 1px var(--border-style) color-mix(in oklch, var(--edge) 16%, transparent); border-radius: calc(var(--radius) * 1.5); background: color-mix(in oklch, var(--panel) 70%, transparent); overflow: hidden">
            {#each log as l (l.i)}
              <div in:rowIn={{ skip: skip() }} out:rowOut style="display: flex; gap: 12px; padding: 10px 14px; border-bottom: 1px var(--border-style) color-mix(in oklch, var(--edge) 10%, transparent); font-size: 12px; line-height: 1.45">
                <span style="flex: 0 0 44px; color: color-mix(in oklab, var(--mu) 75%, var(--panel))">{rel(l.at)}</span>
                <span class="led" aria-hidden="true" style="flex-shrink: 0; width: 8px; height: 8px; margin-top: 5px; background: {LOG[l.kind]}"></span>
                <span style="flex: 1; min-width: 0; color: var(--tx); word-break: break-word">{l.text}</span>
              </div>
            {:else}
              <div style="padding: 10px 14px; font-size: 12px; color: color-mix(in oklab, var(--mu) 75%, var(--panel))">No events yet.</div>
            {/each}
          </div>
        </div>
      </aside>
    </div>
    {/if}
  </main>
</div>
