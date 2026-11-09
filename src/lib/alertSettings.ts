import type { Permission } from './nativeNotify.ts'

/** Where the page runs: inside Conn.app, which shows macOS notifications, or in a browser, which shows its own. */
export type Where = 'app' | 'browser'

/**
 * What the Alerts row in Settings offers. `ask` and `on` turn alerts on (asking first when nothing was asked yet), `off` turns them
 * off, `settings` opens System Settings (the app only: macOS shows its prompt once, so a denial is changed there), and `none` is a
 * state the page cannot change.
 */
export type AlertAction = 'ask' | 'on' | 'off' | 'settings' | 'none'

export interface AlertRow {
  /** One or two words for the state tag */
  state: string
  /** The tag reads as good (on) or bad (blocked), or neither */
  tone: 'on' | 'bad' | 'plain'
  /** What the state means and what to do, in a sentence or two */
  text: string
  action: AlertAction
}

/** The Alerts row for a page running `where`, with this permission, alerts on or off, and Notifications there at all. */
export function alertRow(where: Where, supported: boolean, permission: Permission, enabled: boolean): AlertRow {
  if (!supported) return { state: 'Unavailable', tone: 'plain', text: 'This browser cannot show notifications.', action: 'none' }
  if (permission === 'unavailable')
    return {
      state: 'Unavailable',
      tone: 'bad',
      text: 'macOS does not let Conn show notifications from where it runs. Move Conn.app into Applications and open it again.',
      action: 'none',
    }
  if (permission === 'denied')
    return where === 'app'
      ? {
          state: 'Off in macOS',
          tone: 'bad',
          text: 'Notifications for Conn are turned off in System Settings, and macOS asks only once. Turn on Allow notifications for Conn there; this page notices when you come back.',
          action: 'settings',
        }
      : {
          state: 'Blocked',
          tone: 'bad',
          text: "This browser blocks notifications for this page, and asks only once. Allow them in the browser's site settings, then reload the page.",
          action: 'none',
        }
  if (permission === 'default')
    return {
      state: 'Not asked',
      tone: 'plain',
      text: where === 'app' ? 'Turning alerts on asks macOS to let Conn show notifications.' : 'Turning alerts on asks the browser to let this page show notifications.',
      action: 'ask',
    }
  return enabled
    ? { state: 'On', tone: 'on', text: 'A notification pops up when a session needs input or finishes, a run has questions, or a PR needs you, while Conn is not in front.', action: 'off' }
    : { state: 'Off', tone: 'plain', text: 'Notifications are allowed, and alerts are off.', action: 'on' }
}
