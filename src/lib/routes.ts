// The hash routes, kept free of browser globals so they can be unit-tested. router.svelte.ts wires them to location.

/** Hash routes: #/ , #/s/:sessionId , #/s/:sessionId/a/:agentId (agentId 'lead' is the lead), #/s/:sessionId/advisor ,
 *  #/work , #/work/PROJ-123 , #/reviews , #/myprs , #/history , #/themes , #/settings , and an archived entry as #/h/:uuid , #/h/:uuid/a/:agentId , #/h/:uuid/advisor */
export type Route =
  | { page: 'sessions' }
  | { page: 'overview'; sid: string }
  | { page: 'advisor'; sid: string }
  | { page: 'agent'; sid: string; aid: string | null }
  | { page: 'work' }
  | { page: 'run'; key: string }
  | { page: 'reviews' }
  | { page: 'myprs' }
  | { page: 'history' }
  | { page: 'themes' }
  | { page: 'settings' }
  | { page: 'entry'; uuid: string }
  | { page: 'entryAgent'; uuid: string; aid: string | null }
  | { page: 'entryAdvisor'; uuid: string }

/** The ids in an entry route are taken as typed; archivedSource checks them before they reach a request. */
export const parseRoute = (hash: string): Route => {
  const m = hash.replace(/^#/, '').split('/').filter(Boolean)
  if (m[0] === 's' && m[1] && m[2] === 'advisor') return { page: 'advisor', sid: m[1] }
  if (m[0] === 's' && m[1]) return m[2] === 'a' ? { page: 'agent', sid: m[1], aid: m[3] ?? null } : { page: 'overview', sid: m[1] }
  // Any Jira-shaped key: which projects count is Settings', and the server checks it
  if (m[0] === 'work') return m[1] && isTicketKey(m[1]) ? { page: 'run', key: m[1] } : { page: 'work' }
  if (m[0] === 'reviews') return { page: 'reviews' }
  if (m[0] === 'myprs') return { page: 'myprs' }
  if (m[0] === 'history') return { page: 'history' }
  if (m[0] === 'themes') return { page: 'themes' }
  if (m[0] === 'settings') return { page: 'settings' }
  if (m[0] === 'h' && m[1] && m[2] === 'advisor') return { page: 'entryAdvisor', uuid: m[1] }
  if (m[0] === 'h' && m[1]) return m[2] === 'a' ? { page: 'entryAgent', uuid: m[1], aid: m[3] ?? null } : { page: 'entry', uuid: m[1] }
  return { page: 'sessions' }
}

/** The first-launch rule: while the server reports `setup` (no repository list saved yet) every page but the theme gallery and Settings
 *  sends the person to Settings. No snapshot yet, or a server that does not report `setup`, redirects nowhere. */
export const needsSetup = (route: Route, snap: { setup?: boolean } | null | undefined): boolean => snap?.setup === true && route.page !== 'themes' && route.page !== 'settings'

/** A ticket key in the shape Jira gives: a project key, a dash, a number. Whether it is a configured project is the server's call. */
export const isTicketKey = (s: string): boolean => /^[A-Z][A-Z0-9]{1,9}-\d{1,9}$/.test(s)

/** A Claude session id, the key of a History entry. The server checks the same shape. */
export const isHistoryId = (s: string): boolean => /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(s)
/** An agent id as the server accepts it ('lead' included). */
export const isAgentId = (s: string): boolean => /^[\w-]{1,128}$/.test(s)

const enc = encodeURIComponent

export const href = {
  sessions: () => '#/',
  overview: (sid: string) => `#/s/${sid}`,
  advisor: (sid: string) => `#/s/${sid}/advisor`,
  agent: (sid: string, aid?: string | null) => (aid ? `#/s/${sid}/a/${aid}` : `#/s/${sid}/a`),
  work: () => '#/work',
  run: (key: string) => `#/work/${key}`,
  reviews: () => '#/reviews',
  myprs: () => '#/myprs',
  history: () => '#/history',
  themes: () => '#/themes',
  settings: () => '#/settings',
  entry: (uuid: string) => `#/h/${enc(uuid)}`,
  entryAdvisor: (uuid: string) => `#/h/${enc(uuid)}/advisor`,
  entryAgent: (uuid: string, aid?: string | null) => (aid ? `#/h/${enc(uuid)}/a/${enc(aid)}` : `#/h/${enc(uuid)}/a`),
}
