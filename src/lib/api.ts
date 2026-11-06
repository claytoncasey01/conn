// The one way the page asks the server: every /api request goes through api(), which adds this launch's secret
// (apiKey.ts). A source test holds every other module to it, so no request can leave without the secret.
import { header } from './apiKey.ts'

export type ApiInit = Omit<RequestInit, 'headers'> & { headers?: Record<string, string> }

/** fetch(path) with the secret's Authorization header added to the request's own headers */
export function api(path: string, init: ApiInit = {}): Promise<Response> {
  return globalThis.fetch(path, { ...init, headers: { ...init.headers, ...header() } })
}
