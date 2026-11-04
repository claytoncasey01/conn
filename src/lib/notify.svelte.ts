import { attention, diffAlerts, newSeen } from './alerts.ts'
import type { Snapshot } from './types.ts'

const KEY = 'conn:alerts'

/** Browser notifications for a session that needs input or finished. Off until the person turns them on. */
export const alerts = $state({
  supported: typeof Notification !== 'undefined',
  enabled: false,
  /** The browser (or the person) blocked notifications for this site */
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

if (alerts.supported) {
  alerts.blocked = Notification.permission === 'denied'
  alerts.enabled = stored() && Notification.permission === 'granted'
}

/** Must run from a click: browsers only show the permission prompt for a user gesture. */
export async function toggleAlerts() {
  if (!alerts.supported) return
  if (alerts.enabled) {
    alerts.enabled = false
    remember(false)
    return
  }
  const p = Notification.permission === 'granted' ? 'granted' : await Notification.requestPermission()
  alerts.blocked = p === 'denied'
  alerts.enabled = p === 'granted'
  remember(alerts.enabled)
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

/** Called with every pushed snapshot. */
export function onSnapshot(snap: Snapshot) {
  const raised = diffAlerts(seen, snap)
  count = attention(snap)
  watchTitle()
  applyTitle()
  // Someone looking at the dashboard does not need a popup for what is on their screen.
  if (!alerts.enabled || (document.hasFocus() && !document.hidden)) return
  for (const a of raised) {
    try {
      const n = new Notification(a.title, { body: a.body, tag: a.tag })
      n.onclick = () => {
        window.focus()
        location.hash = a.href
        n.close()
      }
    } catch {}
  }
}
