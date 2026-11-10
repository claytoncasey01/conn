// Settings > App icon: the Dock icon of Conn.app, which follows the theme or is pinned to one look. Only inside the app,
// whose connIcon handler (cmd/conn-app/icon_darwin.m) takes the icon this page draws; a browser has no Dock icon to change.
// The choice is kept in this page's storage, like the theme it follows. The page sends the icon once each time it opens,
// and after that only when the icon it means changes. What it last sent is not kept in storage: the icon file is the app's,
// and another page (another port's server) or a deleted file can change it, so each opening puts it right again.
import { ICON_LIMITS, iconChoiceOf, iconChoiceText, iconKey, iconSpecFor, iconSvg, isClassic, type IconChoice, type IconSpec } from './theme/appIcon.ts'
import { theme } from './theme/theme.svelte.ts'
import defaultTheme from '../../themes/default/theme.json'
import type { ThemeManifest } from './theme/types.ts'

const KEY = 'conn:appIcon'

/** The message handler WebKit gives the page: each post is answered with the app's reply, or rejected with its reason */
export interface IconBridge {
  postMessage(message: unknown): Promise<unknown>
}

/** The bridge on this page, or null in a browser (and in an app too old to have one, or run without a bundle identifier) */
export function findIconBridge(page: unknown = globalThis): IconBridge | null {
  const handler = (page as { webkit?: { messageHandlers?: { connIcon?: { postMessage?: unknown } } } }).webkit?.messageHandlers?.connIcon
  return typeof handler?.postMessage === 'function' ? (handler as IconBridge) : null
}

const bridge = findIconBridge()

const read = (key: string): string | null => {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
const write = (key: string, value: string) => {
  try {
    localStorage.setItem(key, value)
  } catch {}
}

export const appIcon = $state({
  /** Inside Conn.app, which can take an icon */
  available: bridge !== null,
  choice: iconChoiceOf(read(KEY)) as IconChoice,
  /** Why the last icon did not reach the app; null once one does */
  error: null as string | null,
})

/** The icon a choice means against the theme list now */
export const specFor = (choice: IconChoice): IconSpec =>
  iconSpecFor(choice, { list: theme.list, theme: theme.choice, fallback: defaultTheme as unknown as ThemeManifest })

/** An SVG as an image address, for the settings' previews and for drawing it */
export const svgUrl = (spec: IconSpec): string => `data:image/svg+xml;charset=utf-8,${encodeURIComponent(iconSvg(spec))}`

/** The icon drawn at the app's size, as base64 PNG without its data: prefix */
async function png(spec: IconSpec): Promise<string> {
  const image = new Image()
  image.src = svgUrl(spec)
  await image.decode()
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = ICON_LIMITS.pixels
  const context = canvas.getContext('2d')
  if (!context) throw new Error('the page cannot draw')
  context.drawImage(image, 0, 0, ICON_LIMITS.pixels, ICON_LIMITS.pixels)
  const encoded = canvas.toDataURL('image/png').slice('data:image/png;base64,'.length)
  if ((encoded.length * 3) / 4 > ICON_LIMITS.maxBytes) throw new Error('the icon is too large')
  return encoded
}

// One send at a time: a theme changed during a send is sent after it, and only the newest icon wanted is.
let sending = false
let wanted: IconSpec | null = null
/** The iconKey of the icon the app last took from this page, null until it takes one */
let sent: string | null = null

async function send(spec: IconSpec) {
  wanted = spec
  if (sending || !bridge) return
  sending = true
  try {
    while (wanted) {
      const next: IconSpec = wanted
      wanted = null
      const key = iconKey(next)
      if (key === sent) {
        // The app already shows this icon, so an error from an icon wanted since is no longer true
        appIcon.error = null
        continue
      }
      try {
        await bridge.postMessage(isClassic(next) ? { kind: 'classic' } : { kind: 'set', png: await png(next) })
        sent = key
        appIcon.error = null
      } catch (e) {
        appIcon.error = `The app icon did not change: ${e instanceof Error ? e.message : 'the app refused it'}`
      }
    }
  } finally {
    sending = false
  }
}

/** Picks the icon: saved in this page. initAppIcon's effect follows the choice and sends it, so it is sent once. */
export function chooseIcon(choice: IconChoice): void {
  appIcon.choice = choice
  write(KEY, iconChoiceText(choice))
}

/**
 * Keeps the app's icon on the choice, sending it when the choice or the theme changes. Inside the app only, and only once the theme list is read: until
 * then the theme is Default's look, which would send the classic icon for a moment.
 */
export function initAppIcon(): void {
  if (!bridge) return
  $effect.root(() => {
    $effect(() => {
      if (!theme.ready) return
      void send(specFor(appIcon.choice))
    })
  })
}
