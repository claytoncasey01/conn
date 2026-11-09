import { attention, diffAlerts, newSeen, type Alert } from './alerts.ts'
import type { Where } from './alertSettings.ts'
import { findBridge, nativeRequest, nativeSettings, nativeShow, nativeStatus, permissionOf, type Permission } from './nativeNotify.ts'
import type { Snapshot } from './types.ts'

const KEY = 'conn:alerts'

/** The app's bridge to macOS notifications, or null in a browser, which shows the Web Notification API's own. */
const bridge = findBridge()

/** Notifications for a session that needs input or finished. Off until the person turns them on. */
export const alerts = $state({
  /** Inside Conn.app or in a browser */
  where: (bridge ? 'app' : 'browser') as Where,
  supported: bridge !== null || typeof Notification !== 'undefined',
  /** What macOS or the browser lets this page do */
  permission: 'default' as Permission,
  enabled: false,
  /** Notifications cannot be turned on from the page: denied in macOS or the browser, or unavailable */
  blocked: false,
})

const stored = () => {
  try {
    return localStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}
const remember = (on: boolean) => {
  try {
    localStorage.setItem(KEY, on ? '1' : '0')
  } catch {}
}

/** Sets the permission, and alerts on only while they were left on and are still allowed. */
function settle(permission: Permission) {
  alerts.permission = permission
  alerts.blocked = permission === 'denied' || permission === 'unavailable'
  alerts.enabled = stored() && permission === 'granted'
}

/** Reads the permission again: it changes outside the page (System Settings, the browser's site settings), so on every return to the window. */
export async function readPermission() {
  if (bridge) settle(await nativeStatus(bridge))
  else if (alerts.supported) settle(permissionOf(Notification.permission))
}

void readPermission()
window.addEventListener('focus', () => void readPermission())

// A click on a macOS notification: the app shows the window and hands over the route the alert was made with.
window.addEventListener('conn-notification-click', (event) => {
  const to = (event as CustomEvent<unknown>).detail
  if (typeof to === 'string' && to.startsWith('#/')) location.hash = to
})

/** Must run from a click: browsers only show the permission prompt for a user gesture. */
export async function toggleAlerts() {
  if (!alerts.supported) return
  if (alerts.enabled) {
    alerts.enabled = false
    remember(false)
    return
  }
  const asked = alerts.permission === 'granted' ? 'granted' : bridge ? await nativeRequest(bridge) : permissionOf(await Notification.requestPermission())
  remember(asked === 'granted')
  settle(asked)
}

/** Opens System Settings at Conn's notifications: in the app, where macOS's own prompt shows only once. */
export async function openNotificationSettings() {
  if (bridge) await nativeSettings(bridge)
}

const seen = newSeen()
let count = 0

/** `(2) All sessions`: the page sets its own title, so the count is re-applied whenever either changes. */
function applyTitle() {
  const base = document.title.replace(/^\(\d+\) /, '')
  const want = count ? `(${count}) ${base}` : base
  if (document.title !== want) document.title = want
}

let watching = false
function watchTitle() {
  if (watching) return
  watching = true
  const el = document.querySelector('title')
  if (el) new MutationObserver(applyTitle).observe(el, { childList: true, characterData: true, subtree: true })
}

/** Pops one alert up: through the app inside Conn.app, as a Web Notification in a browser. */
function popUp(a: Alert) {
  if (bridge) {
    void nativeShow(bridge, a)
    return
  }
  try {
    const n = new Notification(a.title, { body: a.body, tag: a.tag })
    n.onclick = () => {
      window.focus()
      location.hash = a.href
      n.close()
    }
  } catch {}
}

/** Called with every pushed snapshot. */
export function onSnapshot(snap: Snapshot) {
  const raised = diffAlerts(seen, snap)
  count = attention(snap)
  watchTitle()
  applyTitle()
  // Someone looking at the dashboard does not need a popup for what is on their screen.
  if (!alerts.enabled || (document.hasFocus() && !document.hidden)) return
  for (const a of raised) popUp(a)
}
