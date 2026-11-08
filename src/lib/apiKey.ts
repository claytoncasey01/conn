// The secret every /api request carries. The window or `conn serve` hands it over in the page address's fragment
// (#k=<secret>), which no request ever sends; the page takes it from there once, keeps it in sessionStorage so a reload
// keeps it (never a cookie, which is not scoped by port, and never a query string), and removes it from the address bar.
// main.ts imports this module before the router reads the fragment as a route.

/** The sessionStorage key the secret is kept under */
export const KEY = 'conn.apiKey'
/** A secret's text: 256 bits as unpadded base64url. Anything else in the fragment is not taken. */
const SECRET = /^[A-Za-z0-9_-]{43}$/
const PREFIX = '#k='

type Place = { hash: string; pathname: string; search: string }
type Hist = { replaceState(data: unknown, unused: string, url?: string): void }
type Store = Pick<Storage, 'getItem' | 'setItem'>

/** sessionStorage, or null where it is missing or refused */
function storage(): Store | null {
  try {
    return globalThis.sessionStorage ?? null
  } catch {
    return null
  }
}

/**
 * Takes a secret from the fragment into the store and removes the fragment from the address bar, without a reload or a
 * history entry. A fragment that is not exactly #k=<secret> is left alone (it is a route). Says whether one was taken.
 */
export function takeFromFragment(place: Place = location, hist: Hist = history, store: Store | null = storage()): boolean {
  if (!place.hash.startsWith(PREFIX)) return false
  const text = place.hash.slice(PREFIX.length)
  // the fragment is cleared even when what it held is not a secret, so nothing that looks like one stays in the address bar
  hist.replaceState(null, '', place.pathname + place.search)
  if (!SECRET.test(text)) return false
  try {
    store?.setItem(KEY, text)
  } catch {
    return false
  }
  return true
}

/**
 * The secret itself, read from the store at every call (a new launch hands over a new one); null without one. Only the
 * terminal's WebSocket takes it bare, as a subprotocol: a browser's WebSocket can set no header.
 */
export function secret(store: Store | null = storage()): string | null {
  let text: string | null = null
  try {
    text = store?.getItem(KEY) ?? null
  } catch {
    return null
  }
  return text !== null && SECRET.test(text) ? text : null
}

/** The Authorization header for an /api request; {} without a secret */
export function header(store: Store | null = storage()): Record<string, string> {
  const text = secret(store)
  return text === null ? {} : { authorization: `Bearer ${text}` }
}

let listening = false

/**
 * Takes the secret now, and again whenever only the fragment changes: an app window whose own server restarted opens the
 * same address with a new secret, which is a same-document navigation (no reload). Registered before the router's
 * hashchange listener, so the router never sees #k=.
 */
export function startTaking(): void {
  if (listening) return
  listening = true
  takeFromFragment()
  addEventListener('hashchange', () => void takeFromFragment())
}
