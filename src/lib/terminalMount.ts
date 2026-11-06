// The panel's terminal from start to teardown, behind the few things it needs from the page (the DOM element, xterm.js,
// the socket, timers), so each rule here is tested with fakes and a real xterm.js parser (terminalMount.test.ts).
// Terminal.svelte gives it the real ones. The rules, beyond terminal.ts's:
// - Only real events count: an event the page's own script made (isTrusted false) never reaches xterm.js.
// - The wheel and the mouse type only while the terminal has the focus, and the click that gives it the focus types
//   nothing, up to its release: a click on the panel is never a click on what the program drew.
// - A paste is cleaned of paste markers and control characters, and one with a line break the program would take as
//   Enters (no bracketed paste) waits for the person to confirm it.
// - A hyperlink opens only on Cmd or Ctrl and a click, then a confirmation that names its host; hovering shows where it goes.
// - LEAVE_KEY leaves the terminal, so a keyboard is never trapped in it.
// - A reconnect is a new terminal, so nothing queued for the old one is drawn after the replay.
import type { ITerminalAddon, ITerminalOptions, ITheme, Terminal } from '@xterm/xterm'
import { attach, cleanPaste, fitSize, hasLineBreak, inputFrame, isMouseRelease, isMouseReport, linkTarget, PASTE_CLOSED, RESIZE_DEBOUNCE_MS, silence, terminalOptions, TOO_LARGE } from './terminal.ts'
import type { Frame, LinkCallbacks, Link, Size, Socket, View } from './terminal.ts'

/** The part of xterm.js's Terminal the panel uses. */
export type Term = Pick<
  Terminal,
  | 'parser' | 'modes' | 'cols' | 'rows' | 'options' | 'unicode' | 'loadAddon' | 'open' | 'onData' | 'write' | 'reset'
  | 'resize' | 'paste' | 'blur' | 'attachCustomWheelEventHandler' | 'attachCustomKeyEventHandler' | 'dispose'
>
/** The fit addon's part the panel uses. */
export type Fit = ITerminalAddon & { proposeDimensions(): Size | undefined }
/** The element the terminal is drawn in. */
export type Host = Pick<HTMLElement, 'addEventListener' | 'removeEventListener'>

/** The theme's colours and font, which follow the theme on screen. */
export type Look = { theme: ITheme; font: string; size: number }

/** What waits for the person to confirm: a link to open, or a paste whose line breaks would be Enters. */
export type Ask = { kind: 'link'; url: string; host: string } | { kind: 'paste'; lines: number }
/** What the panel shows: the connection's view, what waits for a confirmation, and where the hovered link goes. */
export type PanelView = View & { ask: Ask | null; hover: string }

export type MountDeps = {
  host: Host
  /** The window: a click that brings it back to the front is a focusing click. */
  page: Host
  makeTerminal(options: ITerminalOptions): Term
  makeFit(): Fit
  makeUnicode(): ITerminalAddon
  openSocket(): Socket | null
  /** Calls `changed` whenever the host's size changes; disconnect stops it. */
  watchSize(changed: () => void): { disconnect(): void }
  /** Runs `run` after `ms`; the function returned cancels it. */
  later(run: () => void, ms: number): () => void
  frame: Frame
  openLink(url: string): void
  ownOrigin: string
  /** Moves the keyboard out of the terminal. */
  leave(): void
  show(view: PanelView): void
  look: Look
}

export type Panel = {
  /** Attaches again, on a new terminal. Only ever called from a person's click: nothing here calls it. */
  reconnect(): void
  /** Tears it all down: the socket closed, the terminal disposed, every listener removed. */
  stop(): void
  setLook(look: Look): void
  /** Does what waits for a confirmation, if the click is a person's. */
  confirm(event: { isTrusted: boolean }): void
  /** Lets go of a hold on keys after lag, if the click is a person's. */
  dismiss(event: { isTrusted: boolean }): void
  cancel(): void
}

/** How long a paste waits for its confirmation before it is dropped: the screen it was asked against grows stale. */
export const PASTE_CONFIRM_MS = 30_000

/** The key that leaves the terminal: the usual key for moving between a window's panes. */
export const LEAVE_KEY = 'F6'

/** The events a script could make to type or click: dropped at the host, before xterm.js sees them, unless real. */
export const REAL_EVENTS_ONLY = [
  'keydown', 'keypress', 'keyup', 'beforeinput', 'input', 'compositionstart', 'compositionupdate', 'compositionend',
  'mousedown', 'mouseup', 'mousemove', 'click', 'dblclick', 'auxclick', 'contextmenu', 'wheel', 'pointerdown', 'pointerup',
  'pointermove', 'touchstart', 'touchmove', 'touchend',
] as const

const UNICODE_VERSION = '11'
const LINK_REFUSED = 'This link does not open from the terminal: only web pages outside Conn do.'
const PASTE_STALE = 'The screen changed, so the paste was not sent. Paste again to be asked again.'
const PASTE_EXPIRED = 'The paste waited too long for a confirmation, so it was not sent.'
const PASTE_NOT_LIVE = 'The terminal is not live, so the paste was not sent.'
/** The markers xterm.js would put around a paste: a paste is checked as one frame with them, bracketed or not. */
const PASTE_START = '\x1b[200~'
const PASTE_END = '\x1b[201~'

type Listener = { type: string; run: (event: Event) => void }

/** The terminal of one hosted session, in deps.host. */
export function mount(deps: MountDeps): Panel {
  let term: Term | null = null
  let fit: Fit | null = null
  let link: Link | null = null
  let look = deps.look
  let base: View = { phase: 'connecting', exitCode: null, notice: null, note: '', lagging: false, held: false, pasteClosed: false }
  let ask: Ask | null = null
  let held = ''
  let hover = ''
  let note = ''
  let cancelHeld = () => {}
  let stopped = false
  // Whether the terminal has the keyboard; whether the window has been left since; and whether a click that gave the
  // terminal (or the window) the focus is still held down
  let focused = false
  let windowLeft = false
  let focusingClick = false
  let cancelMeasure = () => {}

  const render = () => {
    if (!stopped) deps.show({ ...base, note: shownNote(), ask, hover })
  }

  /**
   * The note shown, in the same rank as the connection's: its paste-closed warning first, joined with the panel's own note
   * (a paste refused or dropped, which answers what the person just did) ahead of the connection's other notes.
   */
  const shownNote = (): string => {
    if (note === '') return base.note
    if (!base.pasteClosed) return note
    // The connection's note starts with the warning whenever it is up
    const rest = base.note.slice(PASTE_CLOSED.length).trim()
    return [PASTE_CLOSED, note, rest].filter(Boolean).join(' ')
  }

  /** The connection's view. A new note of its own replaces the panel's, which never hides it. */
  const shown = (next: View) => {
    if (next.note !== base.note) {
      if (next.note !== '') note = ''
    }
    base = next
    render()
  }

  const links: LinkCallbacks = {
    hover(_event, uri) {
      const target = linkTarget(uri, deps.ownOrigin)
      hover = target === null ? LINK_REFUSED : `Link to ${new URL(target).host}: hold Cmd or Ctrl and click to open ${target}`
      render()
    },
    leave() {
      hover = ''
      render()
    },
    activate(event, uri) {
      if (!event.isTrusted) return
      const target = linkTarget(uri, deps.ownOrigin)
      if (target === null) {
        hover = LINK_REFUSED
        return render()
      }
      if (!event.metaKey && !event.ctrlKey) return links.hover(event, uri)
      ask = { kind: 'link', url: target, host: new URL(target).host }
      render()
    },
  }

  /** Fits the screen to the panel and asks the host for that size; nothing when the panel is not drawn. */
  const measure = () => {
    if (!term || !fit) return
    const size = fitSize(fit.proposeDimensions())
    if (!size) return
    if (size.cols !== term.cols || size.rows !== term.rows) term.resize(size.cols, size.rows)
    link?.resize(size)
  }

  const typed = (data: string) => {
    // The click that gave the terminal the focus is the person's click on the panel, not on the program's screen
    if (focusingClick && isMouseReport(data)) {
      if (isMouseRelease(data)) focusingClick = false
      return
    }
    // The panel's own note (a stale paste, one too large) has been read once the person types on
    if (note !== '') {
      note = ''
      render()
    }
    link?.input(data, focused)
  }

  const keyed = (event: KeyboardEvent): boolean => {
    if (!event.isTrusted) return false
    if (event.key !== LEAVE_KEY) return true
    if (event.type === 'keydown') {
      term?.blur()
      deps.leave()
    }
    return false
  }

  const open = () => {
    const made = deps.makeTerminal({ ...terminalOptions(links), theme: look.theme, fontFamily: look.font, fontSize: look.size })
    // Before anything is written: the queries are taken by these, not by xterm.js's own answers
    silence(made.parser)
    made.loadAddon(deps.makeUnicode())
    // The host's screen model counts emoji two cells wide, as unicode 11 does
    made.unicode.activeVersion = UNICODE_VERSION
    const fitter = deps.makeFit()
    made.loadAddon(fitter)
    // A wheel over a terminal without the focus scrolls the page: it never types arrow keys into the program
    made.attachCustomWheelEventHandler(event => event.isTrusted && focused)
    made.attachCustomKeyEventHandler(keyed)
    made.open(deps.host as HTMLElement)
    // The output's own title is never used; only the session's name, from the session list, labels the panel
    made.onData(typed)
    term = made
    fit = fitter
    const screen = { reset: () => made.reset(), write: (data: Uint8Array, done: () => void) => made.write(data, done) }
    // Any output drops a paste waiting for its confirmation: it was asked about a screen that may have changed. A program
    // that writes steadily (a spinner) therefore denies such pastes. That is by design: it can only deny, never change
    // what a later confirmation types, which is the person's own cleaned text
    link = attach(deps.openSocket, screen, shown, { frame: deps.frame, heard: () => dropHeld(PASTE_STALE) })
    measure()
  }

  /** Drops a paste waiting for its confirmation, saying why. */
  function dropHeld(why: string) {
    if (ask?.kind !== 'paste') return
    cancelHeld()
    ask = null
    held = ''
    note = why
    render()
  }

  const close = () => {
    cancelHeld()
    note = ''
    link?.detach()
    term?.dispose()
    link = null
    term = null
    fit = null
    ask = null
    held = ''
    hover = ''
  }

  const paste = (text: string) => {
    if (!term) return
    if (!link) return
    if (base.phase !== 'live') return
    // While keys are held, so is a paste: it would be the key that ends the hold, and be thrown away
    if (base.held) {
      note = PASTE_NOT_LIVE
      return render()
    }
    const clean = cleanPaste(text)
    if (clean === '') return
    // One frame, markers and escapes counted, or nothing: a paste split over frames could lose its end marker
    if (inputFrame(PASTE_START + clean.replace(/\r?\n/g, '\r') + PASTE_END) === null) {
      note = TOO_LARGE
      return render()
    }
    note = ''
    // While output is still being drawn the screen's modes may not be the program's: a line break is always asked about
    const asks = !term.modes.bracketedPasteMode || link.drawing() || base.lagging
    if (hasLineBreak(clean) && asks) {
      cancelHeld()
      held = clean
      ask = { kind: 'paste', lines: clean.split(/\r\n|\r|\n/).length }
      cancelHeld = deps.later(() => dropHeld(PASTE_EXPIRED), PASTE_CONFIRM_MS)
      return render()
    }
    render()
    term.paste(clean)
  }

  const listeners: Listener[] = [
    ...REAL_EVENTS_ONLY.map((type): Listener => ({
      type,
      run: event => {
        if (event.isTrusted) return
        event.preventDefault()
        event.stopImmediatePropagation()
      },
    })),
    {
      type: 'pointerdown',
      run: event => {
        if (!event.isTrusted) return
        // The textarea keeps the focus while the window is away, so the click that brings the window back finds it focused
        focusingClick = !focused || windowLeft
        windowLeft = false
      },
    },
    {
      type: 'paste',
      run: event => {
        // xterm.js never sees a paste: the cleaned text is handed to it, or held for a confirmation
        event.preventDefault()
        event.stopImmediatePropagation()
        if (event.isTrusted) paste((event as ClipboardEvent).clipboardData?.getData('text/plain') ?? '')
      },
    },
    { type: 'drop', run: event => event.preventDefault() },
    { type: 'focusin', run: () => (focused = true) },
    { type: 'focusout', run: () => (focused = false) },
  ]
  for (const each of listeners) deps.host.addEventListener(each.type, each.run, { capture: true })
  const left = () => (windowLeft = true)
  deps.page.addEventListener('blur', left)
  const watcher = deps.watchSize(() => {
    cancelMeasure()
    cancelMeasure = deps.later(measure, RESIZE_DEBOUNCE_MS)
  })
  open()

  return {
    reconnect() {
      if (stopped) return
      close()
      open()
    },
    stop() {
      if (stopped) return
      stopped = true
      cancelMeasure()
      watcher.disconnect()
      for (const each of listeners) deps.host.removeEventListener(each.type, each.run, { capture: true })
      deps.page.removeEventListener('blur', left)
      close()
    },
    setLook(next) {
      look = next
      if (!term) return
      term.options.theme = next.theme
      if (term.options.fontFamily === next.font) return
      term.options.fontFamily = next.font
      measure()
    },
    confirm(event) {
      if (!event.isTrusted) return
      const waiting = ask
      const text = held
      cancelHeld()
      ask = null
      held = ''
      if (waiting?.kind === 'link') deps.openLink(waiting.url)
      if (waiting?.kind === 'paste') {
        // Typed only into the live terminal it was asked about
        if (base.phase === 'live' && !base.lagging && !base.held) term?.paste(text)
        else note = PASTE_NOT_LIVE
      }
      render()
    },
    dismiss(event) {
      if (!event.isTrusted) return
      link?.dismiss()
    },
    cancel() {
      cancelHeld()
      ask = null
      held = ''
      render()
    },
  }
}
