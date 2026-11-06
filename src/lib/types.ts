// The contract between the server and src/. The server derives all of it from ~/.claude on disk.

export type AgentStatus = 'running' | 'done' | 'stalled'
/** waiting: stopped on a permission prompt, a question or a dialog until the person answers */
export type SessionStatus = 'busy' | 'waiting' | 'idle'

export interface ToolCall {
  id: string
  name: string
  /** One-line summary of the input: the command, path or pattern. */
  summary: string
  /** ms epoch the call was made */
  at: number
  /** ms between the call and its result; null while it is still running */
  ms: number | null
  error: boolean
  /** Short preview of the result text, when there is one */
  result: string
}

export interface Agent {
  id: string
  /** The agent type: Explore, Plan, general-purpose, a custom one, or 'teammate' (a claude of its own, started by the lead) */
  type: string
  description: string
  model: string
  status: AgentStatus
  /** 1 = spawned by the lead, 2+ = spawned by another agent */
  depth: number
  /** Agent id of the spawner; null when the lead spawned it */
  parentId: string | null
  startedAt: number
  /** ms epoch of the last line in its transcript */
  lastAt: number
  /** Set once status is done */
  endedAt: number | null
  calls: number
  errors: number
  /** Direct children, counted so a card can say "+2 nested" */
  nested: number
  lastAction: string
  tools: Record<string, number>
  /** Output tokens, a rough work measure */
  tokensOut: number
  /** Estimated dollars at list prices, from the transcript's token counts (advisor calls the agent made included) */
  costUsd: number
  /** First user message of the agent: its task */
  task: string
  /** The final report, once done */
  result: string
  /** Set while a tool call of the agent has no result yet; absent in entries archived before it was recorded */
  inCall?: boolean
}

export interface LogLine {
  at: number
  who: 'lead' | 'advisor' | 'agent'
  /** Agent type when who is agent */
  label: string
  text: string
}

/** 'build' is a session Open work started (its name is a run key); 'review' is one Reviews started (named pr-<number>); 'selfreview' is one My PRs started (named self-<repo>-<number>); everything else is 'adhoc' */
export type SessionKind = 'build' | 'adhoc' | 'review' | 'selfreview'

export interface SessionSummary {
  id: string
  pid: number
  /** The Claude session uuid: what a teammate agent's id is, while `id` is the pid of a live session */
  sessionId: string
  name: string
  kind: SessionKind
  cwd: string
  project: string
  /** The lowercase `owner/repo` of the GitHub origin of the session's folder; null when the folder is in no repo (or its origin is not GitHub) */
  repo: string | null
  branch: string
  status: SessionStatus
  /** Why it is waiting: 'permission prompt', 'input needed', 'sandbox request', 'dialog open'; null otherwise */
  waitingFor: string | null
  /** Running in a cmux surface the dashboard can type into */
  canReply: boolean
  /** What a waiting session's screen is asking, read from its terminal; null when it is not waiting or the screen shows no ask */
  ask: Ask | null
  startedAt: number
  updatedAt: number
  /** The lead's current model */
  model: string
  /** permission mode: auto, plan, acceptEdits, default, bypassPermissions */
  mode: string
  pr: { number: number; url: string } | null
  /** Shell subprocesses of the CLI, when it can be counted */
  shells: number
  /** Last context size (tokens) and the window it is measured against */
  ctxTokens: number
  ctxWindow: number
  leadCalls: number
  advisorCalls: number
  advisorModel: string
  agentsTotal: number
  agentsRunning: number
  /** Last thing the lead did, one line */
  activity: string
  /** Estimated dollars for the lead, its agents and their advisor calls, at list prices */
  costUsd: number
  /** Some responses came from a model with no known price and are left out of costUsd */
  costPartial: boolean
}

/** One call to the advisor. The answer is encrypted in the transcript, so only its shape is known. */
export interface Consult {
  id: string
  at: number
  /** null while the advisor is still answering */
  endedAt: number | null
  /** 'lead' or the id of the agent that asked */
  by: string
  /** Readable name of who asked: LEAD, or the agent's name */
  byLabel: string
  /** The asker's last thought or action before the call: the closest thing to the question */
  before: string
  /** The asker's first action after the answer arrived */
  after: string
}

/** Where a finished session's record came from, set only on an archived session. */
export interface Archived {
  /** ms epoch the session ended */
  endedAt: number
  /** live: captured when this server saw it end; backfill: replayed from a transcript found on disk later */
  source: 'live' | 'backfill'
  /** The transcript was still on disk when it was archived */
  transcriptAvailable: boolean
  /** The session id this one resumed, when it was a resume; null otherwise */
  resumedFrom: string | null
}

/** A live session that is a teammate of another: its name is the team's, and the lead's transcript is on disk. */
export interface SessionTeam {
  name: string
  /** The lead's session id */
  leadSessionId: string
  /** The `id` of the lead's row when the lead is a live session too (what a link to it needs); null when it has ended */
  leadId: string | null
}

export interface SessionDetail extends SessionSummary {
  /** The lead as an agent-shaped record (id 'lead'), so the agent page can show it */
  lead: Agent
  consults: Consult[]
  agents: Agent[]
  log: LogLine[]
  tools: Record<string, number>
  errors: number
  /** Present only on an archived session */
  archived?: Archived
  /** Set on a live session that is a teammate of a lead (its cost is already in the lead's); null otherwise, and absent in entries archived before teammates were recognised */
  team?: SessionTeam | null
}

export interface Snapshot {
  at: number
  sessions: SessionDetail[]
  /** Open work runs, newest first */
  runs: Run[]
  /** The archive: sessions it failed to capture (the footer count), and the startup backfill while it runs, else null */
  /** `error` is set when History is off or its database cannot be read; the live views still work then. */
  history: { failed: number; backfill: { total: number; done: number } | null; error: string | null }
  /** The Reviews tab: PRs that ask for a review */
  reviews: ReviewsView
  /** The My PRs tab: the signed-in user's own open PRs (absent until the server supplies it) */
  myPrs?: MyPrsView
  /** No repository allowlist has been saved yet: conn reads nothing until Settings has one, and every page but the themes and Settings sends the person there */
  setup: boolean
  /** Open work is on: the Atlassian module is enabled and lists a Jira project. Off, its nav item, route, API, ticket sessions and the ticket filter are gone. */
  openWork: boolean
}

export interface AgentDetail {
  agent: Agent
  toolCalls: ToolCall[]
}

// ---- Open work: Jira tickets and the runs started from them ----

export type ResearchModel = 'haiku' | 'sonnet'

export interface Ticket {
  /** PROJ-123: a project key from Settings, a dash and a number */
  key: string
  title: string
  /** Bug, Story, Task */
  type: string
  /** The Jira status name, whatever the workflow calls it */
  status: string
  /** The status's category, the part of every Jira workflow that is the same: new (to do), indeterminate (in progress), done; '' when Jira did not say */
  statusCategory: 'new' | 'indeterminate' | 'done' | ''
  /** Highest, High, Medium, Low */
  priority: string
  assignee: string
  /** Assigned to the connected Jira user */
  mine: boolean
  /** First ~500 characters of the description */
  desc: string
  /** Issue links and comments, from the list fetch. Confluence and Slack are searched once a run starts. */
  links: number
  /** Not in the list fetch (comments would make the result too big to return); null until a run reads the ticket */
  comments: number | null
}

export interface WorkList {
  tickets: Ticket[]
  /** ms epoch of the last good fetch; null before the first */
  fetchedAt: number | null
  fetching: boolean
  /** ms epoch the running fetch began */
  fetchStartedAt: number | null
  /** What the running fetch is doing right now, e.g. "Atlassian: searchJiraIssuesUsingJql" */
  step: string | null
  /** Connectors that need signing in before the fetch can work */
  needsAuth: string[]
  /** A failed fetch, shown instead of an empty list */
  error: string | null
  /** The sprint has more tickets than were fetched */
  truncated: boolean
  /** The Jira project keys from Settings that the list is read for */
  projects: string[]
  /** Where a run builds: the repository from Settings and whether a local clone of it was found; `reason` says what to fix when not */
  build: { repo: string | null; base: string | null; ready: boolean; reason: string | null }
}

/** research -> ask -> build -> pr */
export type RunStage = 'research' | 'ask' | 'build' | 'pr'
export type RunState =
  | 'starting' // branch and worktree being made, Jira being moved
  | 'researching'
  | 'asking' // waiting on the person
  | 'building' // the named session is open
  | 'pr_open'
  | 'failed'
  | 'interrupted' // the server restarted mid-stage
  | 'cancelled'

export type SourceName = 'JIRA' | 'CONFLUENCE' | 'SLACK' | 'CODE'

export interface RunAgent {
  id: 'jira' | 'confluence' | 'slack' | 'code'
  /** Jira reader, Confluence reader, Slack reader, Code mapper */
  name: string
  model: string
  state: 'queued' | 'running' | 'done' | 'error'
  /** One line on what it found, or the error */
  finding: string
  startedAt: number | null
  endedAt: number | null
}

/** A question the sources answered. */
export interface RunAnswered {
  q: string
  a: string
  src: SourceName
  /** Page, thread or comment it came from */
  where: string
  conf: 'HIGH' | 'MEDIUM' | 'LOW'
}

/** A question the sources could not answer. */
export interface RunQuestion {
  id: string
  text: string
  /** What was searched and why nothing settled it */
  why: string
  choices: { id: string; label: string }[]
  /** The chosen choice id, or the person's own words; null until answered */
  answer: string | null
}

export interface RunLog {
  at: number
  kind: 'status' | 'branch' | 'agent' | 'source' | 'ask' | 'build' | 'pr' | 'error'
  text: string
}

/** The build session, once it exists. Read from the session registry. */
export interface RunSession {
  /** Same as the ticket key */
  name: string
  /** Matches SessionSummary.id once the session is up; null before */
  sessionId: string | null
  pid: number | null
  status: SessionStatus | null
}

/** One repository a run builds in: its branch, worktree, session and PR. */
export interface RunRepo {
  /** The repository, owner/name; "" for a run from before Settings */
  repo: string
  /** The branch the leg started from: the base branch in Settings, else the clone's default branch */
  base: string
  /** The PR target: the same branch */
  prBase: string
  branch: string
  worktree: string
  session: RunSession | null
  /** Claude's own id for the build session (not the pid), kept so it can be resumed after it exits */
  claudeSessionId: string | null
  /** A command to paste into a terminal when the session could not be opened for the person */
  manualCommand: string | null
  /** Commits on the branch past its base */
  ahead: number
  /** Changed files not yet committed */
  dirty: number
  pr: { url: string; number: number; status?: PrStatus } | null
  /** The worktree was removed after the PR merged or closed */
  cleaned: boolean | null
}

/** How the build sessions are laid out over a run's repositories. */
export type RunLayout = 'shared' | 'split'

/** What GitHub says about a run's open PR, polled while the run is pr_open. */
export interface PrStatus {
  state: 'open' | 'draft' | 'merged' | 'closed'
  checks: {
    state: 'none' | 'pending' | 'passing' | 'failing'
    total: number
    pending: number
    /** Names of the failing checks */
    failed: string[]
  }
  /** APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or '' */
  review: string
  /** GitHub's mergeStateStatus: CLEAN, BLOCKED, BEHIND, DIRTY, UNSTABLE ... */
  mergeable: string
  /** ms epoch of the poll */
  at: number
}

export interface Run {
  key: string
  title: string
  model: ResearchModel
  state: RunState
  stage: RunStage
  startedAt: number
  updatedAt: number
  /** The ticket's Jira status as this run last set or saw it */
  jiraStatus: string
  agents: RunAgent[]
  answered: RunAnswered[]
  questions: RunQuestion[]
  log: RunLog[]
  /** Why the run failed or was interrupted */
  error: string | null
  /** The repositories the run builds in, in the order they were chosen: at least one */
  repos: RunRepo[]
  layout: RunLayout
}

// ---- History: the archive of finished sessions ----

export type SummaryState = 'none' | 'queued' | 'running' | 'ready' | 'failed'

/** One line of the History list: everything the list shows, nothing that needs the stored detail. */
export interface HistoryRow {
  id: string
  name: string
  kind: SessionKind
  project: string
  /** The lowercase `owner/repo` the session's folder belonged to when it was captured; null when none or not known (rows from before the column, whose folder is gone) */
  repo: string | null
  branch: string
  startedAt: number
  endedAt: number
  model: string
  costUsd: number
  agents: number
  advisorCalls: number
  errors: number
  outcome: string
  prNumber: number | null
  ticketKey: string | null
  source: 'live' | 'backfill'
  /** First line of the At a Glance summary; empty until one is written */
  glance: string
  summaryState: SummaryState
}

export interface HistoryList {
  rows: HistoryRow[]
  /** Rows matching every filter, before limit and offset */
  total: number
  /** Per group (kind, outcome, model, project, repo): the count of each value under every filter except that group's own */
  counts: Record<string, Record<string, number>>
  /** What the filter bar offers: the models, folder names and repos of the rows the page may show */
  facets: { models: string[]; projects: string[]; repos: string[] }
}

export interface HistoryEntry {
  row: HistoryRow
  detail: SessionDetail
  summary: { text: string; model: string; at: number; state: SummaryState; error: string | null }
}

// ---- Reviews: pull requests that ask Casey for a review ----

export type ReviewState = 'new' | 'reviewing' | 'waiting' | 'rereview' | 'done'

/** One line of the Reviews list. */
export interface ReviewRow {
  /** `owner/repo#number`, lowercase: what the row, the REVIEW press and the seen marks are keyed by */
  key: string
  /** `owner/repo` as GitHub reports it */
  repo: string
  /** Why REVIEW cannot start here: there is no local clone of the repo (null when there is one) */
  noClone: string | null
  /** A pattern that covers this repo could not be searched at the last poll: the row is as it was before, and REVIEW waits */
  stale?: boolean
  number: number
  title: string
  author: string
  url: string
  baseRef: string
  state: ReviewState
  /** How the author asked again (state rereview) */
  reAsked: 'github' | 'slack' | null
  /** The head moved past the commit Casey reviewed */
  updated: boolean
  /** Permalink of the post in the review channel */
  slackUrl: string | null
  /** Permalink of the first Slack DM that asked for a review of it */
  dmUrl: string | null
  /** False when only "every open PR" lists it while the channel scan is on: tagged NOT IN A CHANNEL */
  inChannel: boolean
  checks: PrStatus['checks']['state']
  createdAt: number
  updatedAt: number
  ended: 'merged' | 'closed' | null
  session: { name: string; status: SessionStatus } | null
  launches: number
}

/** The Reviews tab's state. It rides the snapshot as `Snapshot.reviews` (there is no GET route for it). */
export interface ReviewsView {
  /** The chosen Slack channels the review posts are read from, joined for display ('' when the channel scan is off) */
  channel: string
  enabled: boolean
  disabledReason: string | null
  me: string | null
  rows: ReviewRow[]
  lastPollAt: number | null
  lastScanAt: number | null
  /** A gh problem */
  error: string | null
  slack: 'ok' | 'needs-auth' | 'error' | 'unused'
  slackError: string | null
  /** Why the DM scan failed at its last run (a signed-out Slack is `slack`, not this) */
  dmError: string | null
  /** REFRESH presses counted by the server: `seq` is the last one pressed, `done` the last one whose poll (and scan) has finished */
  refresh: { seq: number; done: number }
  /** The allowlist patterns whose "every open PR" search had more than it reads (200): the list may be cut off */
  capped?: string[]
  /** The allowlist patterns GitHub could not search at the last poll, and why: their repos' rows are stale */
  failed?: { pattern: string; error: string }[]
  /** reviews.json could not be opened for writing: nothing is saved this run (launches and finds are lost at a restart), and why */
  storeError?: string | null
  /** GitHub's rate limit is used up: Reviews asks again after this time (ms) */
  pausedUntil?: number | null
  /** Which review sources are on, so the page can say what it lists */
  sources?: { requested: boolean; channels: string[]; dms: boolean; allOpen: boolean }
  /** Old review records conn could not place in a repo: kept, never listed, offered on the Settings page */
  legacy?: LegacyReview[]
}

/** A review record from before Reviews read several repos whose repo conn could not work out. */
export interface LegacyReview {
  number: number
  /** The clone folder its newest launch was made from, a hint for the person placing it; null when it was never launched */
  folder: string | null
  /** When it was last touched (first seen, a post, a launch) */
  at: number
}

/** Why a My PRs row is where it is: what the one status line says */
export type MyPrStatus = 'no-clone' | 'fork' | 'draft-pr' | 'running' | 'waiting' | 'findings' | 'ready' | 'draft-failed' | 'idle'

/** An unresolved review thread of a My PRs row, with the reply the session proposed for it (every string already cleaned by the server) */
export interface RowThread {
  id: string
  /** Login of the first comment's author, '' when the account is gone */
  by: string
  path: string | null
  line: number | null
  /** The first comment, shortened */
  text: string
  /** A later commit changed its line (the usual sign the finding was fixed): not an open finding, but still unresolved; absent from an older server */
  outdated?: boolean
  proposal: { text: string; state: 'proposed' | 'replied' | 'posted' | 'skipped' | 'failed'; error: string | null } | null
}

/** One line of the My PRs list. */
export interface MyPrRow {
  repo: string
  number: number
  title: string
  url: string
  draft: boolean
  createdAt: number
  lastCommentAt: number
  /** Open threads started by a bot in config.findingBots */
  openBot: number
  /** Open threads started by anyone else */
  openHuman: number
  /** Unresolved threads whose line a later commit changed; shown, not counted as open */
  outdated: number
  /** The thread list hit its page size, so the counts may be short */
  partial: boolean
  /** The unresolved threads, the open ones then the outdated ones, each oldest first, at most ROW_THREADS_MAX, with the proposed replies */
  threads: RowThread[]
  /** Self-Review rounds started so far */
  round: number
  status: MyPrStatus
  canStart: boolean
  /** Why the button is disabled, null when it is not */
  reason: string | null
  label: 'Self-review' | 'Re-review'
  /** The Slack draft saved for the ready head (set once there is one); not `draft`, which is the PR's own flag */
  slackDraft?: MyPrDraft | null
  /** True when no draft is made now (Slack is off or has no channel): a clean row then says so instead of "drafting" */
  draftOff?: true
  /** Why the last Greptile ask for this PR failed, as one sentence to show as it is (it says whether conn tries again), null when nothing is wrong */
  error?: string | null
  /** When the failed Slack draft for this head is tried again (while a dashboard is open and the PR stays clean); null when it is not: saved, its attempts used, or not clean */
  draftRetryAt?: number | null
  /** Entries of the newest round's replies file that were not taken (a thread that is not an unresolved one of this PR, a repeat, a bad or too long text, past the 50 of a read); 0 when none */
  ignoredReplies?: number
}

export interface MyPrDraft {
  sha: string
  text: string
  state: 'saved' | 'failed'
  error: string | null
  at: number
  /** Attempts made for this sha */
  tries: number
  /** The channel it was saved in; absent for a draft saved before the channel was kept */
  channel?: string
}

/** The My PRs tab's state. */
export interface MyPrsView {
  at: number
  /** The signed-in GitHub login */
  me: string | null
  /** A gh problem */
  error: string | null
  /** Some PRs may not be listed: the search hit its limit, or GitHub could not read some of them */
  capped: boolean
  /** REFRESH presses counted by the server: `seq` is the last one pressed, `done` the last one whose poll has finished */
  refresh: { seq: number; done: number }
  /** A poll has succeeded since the server started: `rows` is the real list, not the empty one sent before the first poll */
  loaded: boolean
  /** Attempts the server makes at a head's Slack draft (`DRAFT_MAX_TRIES`): a failed draft is final once `tries` reaches it */
  draftMaxTries: number
  /** The Slack channel a draft is saved in now (the draft channel setting, else the first channel); null when none is made: Slack is off or has no channel */
  channel: string | null
  /** Why nothing is listed: `setup` (no repository is allowed), `github` (the GitHub module is off); null when My PRs reads GitHub. Absent from an older server */
  off?: 'setup' | 'github' | null
  /** Something is off about the list that is not an error: a search that failed (the other repos are listed) or the rate limit used up; null when all is well. Absent from an older server */
  warning?: string | null
  /** The GraphQL cost of the last poll (points, summed over its searches), what GitHub said is left, and when it resets (ms); null before a poll has reported it */
  rate?: { cost: number; remaining: number; resetAt: number } | null
  /** Polling waits until then (ms) because the rate limit is used up; null when it does not */
  pausedUntil?: number | null
  rows: MyPrRow[]
}

/** What a session's screen is asking: a multiple-choice `question`, a tool `permission`, the empty input box (`text`), or a dialog that is not understood (`unknown`) */
export type AskKind = 'question' | 'permission' | 'text' | 'unknown'
/** One choice. `key` is the digit printed beside it, the key that picks it. */
export interface AskOption { key: string; label: string; role: 'pick' | 'allow' | 'deny' }
/** What an agent is waiting on. `id` names this exact prompt (a click is only honored while the screen still has it); `since` is when the Store first saw it, 0 from the parser. */
export interface Ask { kind: AskKind; id: string; title: string; text: string; options: AskOption[]; since: number }
