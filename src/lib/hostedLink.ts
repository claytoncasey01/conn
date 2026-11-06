// A hosted session that runs but that no Claude session has registered under: the session list matches a hosted program to
// claude's registry by the exact process id Conn started, so a CONN_CLAUDE that starts claude as a child (a wrapper that does
// not exec it) is never linked. Its row says so instead of looking broken.

/** What the row says. */
export const NOT_LINKED = 'Not linked to a Claude session yet'

/** Why, for the row's title. */
export const NOT_LINKED_WHY =
  'Conn started this program, but no Claude session has registered under its process yet. If CONN_CLAUDE is a wrapper that starts claude as a child instead of running it in its place (exec), it never will: set CONN_CLAUDE to claude itself.'

/** A hosted row that runs with no Claude session behind it. */
export const notLinked = (s: { hosted?: boolean; sessionId: string; exitCode?: number }): boolean =>
  s.hosted === true && s.sessionId === '' && s.exitCode === undefined
