// The one way the page asks the server: every /api request goes through api(), which adds this launch's secret
// (apiKey.ts). A source test holds every other module to it, so no request can leave without the secret.
import { header, secret } from './apiKey.ts'
import { KEY_PROTOCOL_PREFIX, SESSION_ID, TERMINAL_PROTOCOL } from './terminal.ts'

export type ApiInit = Omit<RequestInit, 'headers'> & { headers?: Record<string, string> }

/** The paths api() sends to: the server's own routes, never another origin (`//host`, `https:`) or a page file */
const API_PATH = /^\/api\//

/**
 * fetch(path) with the secret's Authorization header added to the request's own headers. A path that is not one of the
 * server's /api routes is refused before anything is sent, so the secret can never go anywhere else.
 */
export function api(path: string, init: ApiInit = {}): Promise<Response> {
  if (!API_PATH.test(path)) return Promise.reject(new Error(`not an /api route: ${path.slice(0, 80)}`))
  return globalThis.fetch(path, { ...init, headers: { ...init.headers, ...header() } })
}

/**
 * Opens a hosted session's terminal (internal/server/term.go). The secret travels as the second subprotocol, the one place
 * a browser's WebSocket lets the page put it: never in the URL, a cookie or the DOM. null, opening nothing, without a
 * secret or for an id that is not one the host issues.
 */
export function termSocket(id: string, key: string | null = secret(), host: string = location.host): WebSocket | null {
  if (key === null) return null
  if (!SESSION_ID.test(id)) return null
  return new WebSocket(`ws://${host}/api/term/${id}`, [TERMINAL_PROTOCOL, KEY_PROTOCOL_PREFIX + key])
}
