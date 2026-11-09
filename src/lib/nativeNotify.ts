import { oneLine, type Alert } from './alerts.ts'

/**
 * The app's bridge to macOS notifications (cmd/conn-app/notify_darwin.m). Inside Conn.app, WebKit answers the Web Notification API
 * with `denied` and never asks macOS, so the page sends its alerts to the app instead. The bridge is there only in the app; a page
 * from a newer or older Conn than the app finds it by its presence and reads any reply it does not know as `unavailable`.
 */

/** The permission in the Web Notification API's words, and `unavailable` where notifications cannot be shown at all. */
export type Permission = 'default' | 'granted' | 'denied' | 'unavailable'

/** The message handler WebKit gives the page: each post is answered with the app's reply, or rejected with its reason. */
export interface Bridge {
  postMessage(message: unknown): Promise<unknown>
}

/** The most the app shows of each field, in characters (desk.CheckNotification): testdata/notification-cases.json holds both sides to them. */
export const NOTIFICATION_LIMITS = { title: 256, body: 1024, tag: 128, href: 512 } as const

/** The bridge on this page, or null in a browser (and in an app too old to have one). */
export function findBridge(page: unknown = globalThis): Bridge | null {
  const handler = (page as { webkit?: { messageHandlers?: { connNotify?: { postMessage?: unknown } } } }).webkit?.messageHandlers?.connNotify
  return typeof handler?.postMessage === 'function' ? (handler as Bridge) : null
}

const PERMISSIONS: readonly Permission[] = ['default', 'granted', 'denied', 'unavailable']

/** The app's or the browser's answer as a Permission: anything else is `unavailable`. */
export const permissionOf = (answer: unknown): Permission => (PERMISSIONS.includes(answer as Permission) ? (answer as Permission) : 'unavailable')

/**
 * An alert cut to what the app shows: title and body to one line within their limits (oneLine adds an ellipsis, so it is given one
 * character less), the tag cut to its limit. The href is a route the page made, which the app checks, so it is sent as it is.
 */
export const fitNotification = (alert: Alert): Alert => ({
  title: oneLine(alert.title, NOTIFICATION_LIMITS.title - 1),
  body: oneLine(alert.body, NOTIFICATION_LIMITS.body - 1),
  tag: alert.tag.slice(0, NOTIFICATION_LIMITS.tag),
  href: alert.href,
})

/** Asks the bridge, and gives `unavailable` when it refuses or fails: there is nothing else the page could do. */
async function ask(bridge: Bridge, message: object): Promise<Permission> {
  try {
    return permissionOf(await bridge.postMessage(message))
  } catch {
    return 'unavailable'
  }
}

/** The permission macOS reports now. */
export const nativeStatus = (bridge: Bridge): Promise<Permission> => ask(bridge, { kind: 'status' })

/** Asks macOS, whose prompt shows only while the permission is `default`, and gives the permission after. */
export const nativeRequest = (bridge: Bridge): Promise<Permission> => ask(bridge, { kind: 'request' })

/** Shows an alert as a macOS notification. A refusal is dropped: the alert is also counted in the tab title. */
export async function nativeShow(bridge: Bridge, alert: Alert): Promise<void> {
  try {
    await bridge.postMessage({ kind: 'show', ...fitNotification(alert) })
  } catch {}
}

/** Opens System Settings at Conn's notifications. */
export async function nativeSettings(bridge: Bridge): Promise<void> {
  try {
    await bridge.postMessage({ kind: 'settings' })
  } catch {}
}
