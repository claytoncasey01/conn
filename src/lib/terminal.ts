// The page's side of a hosted session's terminal, as data and plain functions; Terminal.svelte is the glue that puts them
// on screen. The protocol is the server's (internal/server/term.go): text JSON frames up, binary output and two text
// frames down. Three rules hold here:
// - The page answers nothing. The host answers a program's terminal queries from a fixed table (internal/pty/answers.go);
//   xterm.js would answer several by itself, typing replies into the program, so `silence` takes every query first.
// - Only what the person types is typed: no focus report ever, and a mouse report only while the terminal has the focus.
//   The host counts both as input, which holds the page's own answers back.
// - Output is untrusted: no clipboard, no window reports, no title, and a hyperlink opens only through `linkTarget`.
import type { IDisposable, IFunctionIdentifier, IParser, ITerminalOptions } from '@xterm/xterm'

/** The subprotocol the server speaks, and the one that carries the secret (never the URL, a cookie or the DOM). */
export const TERMINAL_PROTOCOL = 'conn.term.v1'
export const KEY_PROTOCOL_PREFIX = 'conn.key.'
/** A hosted session's id, as the host issues it: anything else is never put in a URL. */
export const SESSION_ID = /^[0-9a-f]{32}$/

/** The server's maxInputFrameBytes: the text one input frame may type. */
export const INPUT_BYTES_MAX = 64 << 10
/** The server's read limit: an input frame's text plus its JSON envelope. */
export const FRAME_BYTES_MAX = INPUT_BYTES_MAX + 256
/** The bytes the two bracketed-paste markers add around a paste. */
const PASTE_MARKER_BYTES = 12
/**
 * The largest paste typed: one input frame with its markers, so a paste is never split. A paste split over frames could
 * lose its tail (the server drops input a program is not reading), and with it the end marker, leaving the program inside
 * a paste where every later key is pasted text. A larger paste is refused whole.
 */
export const PASTE_BYTES_MAX = INPUT_BYTES_MAX - PASTE_MARKER_BYTES
/** The host's terminal size bounds: each side, and the cells in all. */
export const SIZE_MAX = 1000
export const CELLS_MAX = 1 << 18
/** Lines kept above the screen: the host keeps 4 MiB of output, which a replay redraws into these. */
export const SCROLLBACK_LINES = 5000
/** A resize waits this long for the next: at most about seven a second, under the server's ten. */
export const RESIZE_DEBOUNCE_MS = 150
/** The longest server text frame: `{"t":"exit","code":-2147483648}` is 31 bytes. */
const SERVER_FRAME_CHARS_MAX = 64
/** The longest link a hyperlink may open. */
const LINK_CHARS_MAX = 2048

// ---- answering nothing ----

/**
 * Control sequences a program sends to ask the terminal something: device attributes (primary, secondary, tertiary),
 * status and cursor position reports (and every `CSI ? … n`, the colour scheme among them), the version, mode reports,
 * the kitty keyboard flags, every window operation (reports, moves and title stacks), and DECREQTPARM.
 */
export const QUIET_CSI: readonly IFunctionIdentifier[] = [
  { final: 'c' },
  { prefix: '>', final: 'c' },
  { prefix: '=', final: 'c' },
  { final: 'n' },
  { prefix: '?', final: 'n' },
  { prefix: '>', final: 'q' },
  { intermediates: '$', final: 'p' },
  { prefix: '?', intermediates: '$', final: 'p' },
  { prefix: '?', final: 'u' },
  { final: 't' },
  { final: 'x' },
]

/** Device control strings that ask: a setting's value (DECRQSS) and termcap entries (XTGETTCAP). */
export const QUIET_DCS: readonly IFunctionIdentifier[] = [
  { intermediates: '$', final: 'q' },
  { intermediates: '+', final: 'q' },
]

/**
 * Operating system commands taken whole: the titles (0, 1, 2: never the page's title, never shown raw), the colours and
 * their resets (4, 5, 10 to 19, 104, 105, 110 to 119: a query would be answered, a set would change the theme's
 * colours), and the clipboard (52: never read, never written). OSC 8, a hyperlink, is not here.
 */
export const QUIET_OSC: readonly number[] = [
  0, 1, 2, 4, 5, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 52, 104, 105, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119,
]

const taken = () => true

/**
 * Registers a handler that takes each query and does nothing. A handler added later is tried before those added
 * earlier, and xterm.js adds its own at construction, so these take the sequence before xterm.js's answer can.
 */
export function silence(parser: IParser): IDisposable[] {
  return [
    ...QUIET_CSI.map(id => parser.registerCsiHandler(id, taken)),
    ...QUIET_DCS.map(id => parser.registerDcsHandler(id, taken)),
    ...QUIET_OSC.map(ident => parser.registerOscHandler(ident, taken)),
  ]
}

/** The link policy for a hyperlink in the output: an http or https page of another origin than Conn's, or null. */
export function linkTarget(text: string, ownOrigin: string): string | null {
  if (text.length > LINK_CHARS_MAX) return null
  let url: URL
  try {
    url = new URL(text)
  } catch {
    return null
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
  if (url.hostname === '') return null
  // A user and password in a link is a way to make one host look like another
  if (url.username !== '' || url.password !== '') return null
  // A page of Conn would load in the app window (the desktop app keeps its own pages in it): a link from a program never does
  if (url.origin === ownOrigin) return null
  return url.href
}

/** What the terminal does with a hyperlink in the output: the panel's link policy (terminalMount.ts). */
export type LinkCallbacks = {
  activate(event: MouseEvent, uri: string): void
  hover(event: MouseEvent, uri: string): void
  leave(event: MouseEvent, uri: string): void
}

const NO_LINKS: LinkCallbacks = { activate: () => {}, hover: () => {}, leave: () => {} }

/** The contrast xterm.js holds every character to against its background, whatever colours the program picks. */
export const MINIMUM_CONTRAST = 4.5

/**
 * The terminal's options, each one that bears on what reaches the program or the page set here on purpose, defaults
 * included (AGENTS: library options are set explicitly):
 * - every window operation off (a report would type text the program chose into its own input);
 * - hyperlinks only through `links`, which applies the link policy, and never a link that is not http or https;
 * - no click that types: Alt-click does not move the cursor by typing arrow keys, a right click does not select;
 * - Option is not Meta and does not force a selection, so the keys type what the keyboard says;
 * - line endings as the program sends them (the terminal on the host already turns LF into CR LF);
 * - a bounded scrollback, scrolled to the bottom when the person types;
 * - the proposed API on, for the unicode 11 width table (the host's screen model counts emoji two cells wide, as it does);
 * - xterm.js logs nothing (output is never logged).
 */
export function terminalOptions(links: LinkCallbacks = NO_LINKS): ITerminalOptions {
  return {
    allowProposedApi: true,
    altClickMovesCursor: false,
    convertEol: false,
    cursorBlink: false,
    disableStdin: false,
    ignoreBracketedPasteMode: false,
    logLevel: 'off',
    macOptionClickForcesSelection: false,
    macOptionIsMeta: false,
    // Colours a program picks for a dark background stay readable on a light theme (the host tells programs it is dark)
    minimumContrastRatio: MINIMUM_CONTRAST,
    rightClickSelectsWord: false,
    screenReaderMode: false,
    scrollback: SCROLLBACK_LINES,
    scrollOnUserInput: true,
    tabStopWidth: 8,
    linkHandler: { activate: links.activate, hover: links.hover, leave: links.leave, allowNonHttpProtocols: false },
    windowOptions: {
      restoreWin: false, minimizeWin: false, setWinPosition: false, setWinSizePixels: false, raiseWin: false, lowerWin: false,
      refreshWin: false, setWinSizeChars: false, maximizeWin: false, fullscreenWin: false, getWinState: false,
      getWinPosition: false, getWinSizePixels: false, getScreenSizePixels: false, getCellSizePixels: false,
      getWinSizeChars: false, getScreenSizeChars: false, getIconTitle: false, getWinTitle: false, pushTitle: false,
      popTitle: false,
    },
  }
}

// ---- what is typed ----

const FOCUS_REPORTS = new Set(['\x1b[I', '\x1b[O'])
/** A mouse report as xterm.js types it: SGR (mode 1006) or SGR pixels (1016), press (M) or release (m). */
const SGR_MOUSE = /^\x1b\[<(\d+);\d+;\d+([Mm])$/
/** The button code's bit for motion. */
const MOTION_BIT = 32

/**
 * Whether what xterm.js produced is the person's own input: never a focus report (the host counts it as input while the
 * program has not asked for them), and a mouse report only while the terminal has the focus (with any-motion tracking on,
 * a pointer passing over the panel on its way to a button would otherwise type).
 *
 * Only SGR reports are looked for. A program that asks for the mouse in the default (X10) encoding gets none: xterm.js sends
 * those reports through onBinary, which the panel does not take (the server closes the terminal on a binary frame), so
 * they never reach this function. Claude Code asks for SGR (1006).
 */
export function forwardable(data: string, focused: boolean): boolean {
  if (FOCUS_REPORTS.has(data)) return false
  if (SGR_MOUSE.test(data)) return focused
  return true
}

/** Whether data is one mouse report. */
export const isMouseReport = (data: string): boolean => SGR_MOUSE.test(data)
/** Whether data is a mouse report of a button let go. */
export const isMouseRelease = (data: string): boolean => SGR_MOUSE.exec(data)?.[2] === 'm'
/** Whether data is a mouse report of the pointer moving, which the panel sends at most once a frame. */
export function isMotionReport(data: string): boolean {
  const report = SGR_MOUSE.exec(data)
  if (!report) return false
  return report[2] === 'M' && (Number(report[1]) & MOTION_BIT) !== 0
}

/** The bracketed-paste markers xterm.js puts around a paste while the program asks for them. */
const PASTE_MARKERS = /\x1b\[20[01]~/g
/** Every control character but tab, line feed and carriage return: C0, DEL and C1. */
const PASTE_CONTROLS = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/g

/**
 * Clipboard text as it may be typed: the paste markers taken out (one inside the text would end the paste early and
 * type the rest as keys), then every control character but tab and the line breaks (an ESC alone would start a key).
 */
export const cleanPaste = (text: string): string => text.replace(PASTE_MARKERS, '').replace(PASTE_CONTROLS, '')

/** Whether text holds a line break: pasted while the program does not bracket pastes, each one is an Enter. */
export const hasLineBreak = (text: string): boolean => /[\r\n]/.test(text)

const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g

const encoder = new TextEncoder()
/** The UTF-8 bytes of text. */
const utf8Length = (text: string): number => encoder.encode(text).length

const inputFrameText = (text: string): string => JSON.stringify({ t: 'input', d: text })

/**
 * Input as the one frame that types it, within the server's limits (its text at most INPUT_BYTES_MAX, the frame at most
 * FRAME_BYTES_MAX once escaped as JSON); null when it would need more, as it is refused whole rather than cut. A lone half
 * of a surrogate pair becomes U+FFFD.
 */
export function inputFrame(data: string): string | null {
  // A string's UTF-16 length is never more than its UTF-8 bytes, so a longer one is over the cap without counting
  if (data.length > INPUT_BYTES_MAX) return null
  if (data === '') return null
  const text = data.replace(LONE_SURROGATE, '\uFFFD')
  if (utf8Length(text) > INPUT_BYTES_MAX) return null
  const frame = inputFrameText(text)
  if (utf8Length(frame) > FRAME_BYTES_MAX) return null
  return frame
}

// ---- size ----

export type Size = { cols: number; rows: number }

/** The size to ask the host for, from what the fit addon measured: within the host's bounds, or null when nothing was measured. */
export function fitSize(proposed: Size | undefined): Size | null {
  if (!proposed) return null
  const cols = Math.min(SIZE_MAX, Math.floor(proposed.cols))
  const rows = Math.min(SIZE_MAX, Math.floor(proposed.rows))
  if (!(cols >= 1) || !(rows >= 1)) return null
  return { cols, rows: Math.min(rows, Math.floor(CELLS_MAX / cols)) }
}

// ---- what the server sends ----

export type ServerFrame = { t: 'replay-end' } | { t: 'paste-closed' } | { t: 'exit'; code: number }

/** A text frame from the server, or null for anything that is not exactly one of the two it sends. */
export function parseFrame(text: string): ServerFrame | null {
  if (text.length > SERVER_FRAME_CHARS_MAX) return null
  let value: unknown
  try {
    value = JSON.parse(text)
  } catch {
    return null
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null
  const frame = value as Record<string, unknown>
  const keys = Object.keys(frame).sort().join(',')
  if (frame.t === 'replay-end' && keys === 't') return { t: 'replay-end' }
  if (frame.t === 'paste-closed' && keys === 't') return { t: 'paste-closed' }
  if (frame.t === 'exit' && keys === 'code,t' && Number.isInteger(frame.code)) return { t: 'exit', code: frame.code as number }
  return null
}

export type Notice = { text: string; reconnect: boolean }

/** The server's reason for closing a viewer whose session's input went over its rate. */
const RATE_REASON = "input over the session's rate"

/** The server's close reasons it says more about (Task 4's table); any other reason is never shown. */
const REASONS: Record<string, string> = {
  "input over the session's rate": 'Conn closed the terminal: input came faster than the session takes it. Nothing more was typed.',
  'malformed frame': 'Conn closed the terminal: it refused a frame from this page.',
  'terminal size out of range': 'Conn closed the terminal: it refused the size this page asked for.',
  "fell behind the program's output": 'The terminal fell behind the program\'s output and was closed.',
  'the replay took too long to write': 'The terminal took too long to catch up with the program and was closed.',
}

/**
 * What the panel says when the terminal closes, and whether it offers a Reconnect: only where trying again can help, and
 * only ever as a button. Its words are the page's own: the reason the server sent picks one of them, and is never shown.
 */
export function closeNotice(code: number, reason: string, exitCode: number | null): Notice {
  if (exitCode !== null) return { text: `The program exited with code ${exitCode}.`, reconnect: false }
  const known = Object.hasOwn(REASONS, reason) ? REASONS[reason] : null
  switch (code) {
    case 1001:
      return { text: 'Conn is stopping, so the terminal closed.', reconnect: true }
    case 1013:
      return { text: known ?? 'The terminal fell behind and was closed.', reconnect: true }
    case 1006:
      return { text: 'Could not attach to the terminal: the session may have ended, or has as many viewers as it takes.', reconnect: true }
    case 1008:
      // Too much input is the person's (a fast pointer with any-motion mouse on): trying again is safe. A refused frame or
      // size is this page's own mistake, which a reconnect would repeat
      if (reason === RATE_REASON) return { text: REASONS[RATE_REASON], reconnect: true }
      return { text: known ?? 'Conn closed the terminal: it refused input from this page.', reconnect: false }
    case 1003:
    case 1009:
      return { text: 'Conn closed the terminal: it refused a frame from this page.', reconnect: false }
    case 1011:
      return { text: 'The terminal failed.', reconnect: false }
    default:
      return { text: `The terminal closed (code ${code}).`, reconnect: false }
  }
}

// ---- the connection ----

/** The part of a WebSocket the connection uses. */
export type Socket = Pick<WebSocket, 'binaryType' | 'protocol' | 'send' | 'close' | 'onopen' | 'onmessage' | 'onclose' | 'onerror'>

/** The part of the terminal the connection writes to: `done` runs once xterm.js has parsed the bytes. */
export type Screen = { reset(): void; write(data: Uint8Array, done: () => void): void }

/**
 * Bytes handed to the screen and not yet parsed. Over WRITE_PENDING_BYTES_MAX the page has fallen behind the program: the
 * terminal is closed, as the server closes a viewer that falls behind it, rather than letting xterm.js's own queue grow to
 * the 50 MB at which it throws (it is four whole replays, so a replay alone never reaches it). Over
 * WRITE_PENDING_BYTES_HIGH the page is behind (`lagging`), and stays behind until it is under WRITE_PENDING_BYTES_LOW: with
 * one mark for both, output drawn about as fast as it arrives would flip it on and off between keys.
 */
export const WRITE_PENDING_BYTES_MAX = 16 << 20
export const WRITE_PENDING_BYTES_HIGH = 1 << 20
export const WRITE_PENDING_BYTES_LOW = 256 << 10
/**
 * Once a key is held for lag, keys stay held until the page has caught up and the person presses a key after this long
 * without one (that key only lets go), or presses Dismiss: a word is typed whole or not at all, never with a gap.
 */
export const HOLD_QUIET_MS = 1500

export type Phase = 'connecting' | 'replaying' | 'live' | 'ended' | 'closed'
/**
 * What the connection shows: where it is, the exit code once the program ended, why it closed, a passing note, and
 * whether output is arriving faster than it is drawn.
 */
export type View = {
  phase: Phase
  exitCode: number | null
  notice: Notice | null
  note: string
  lagging: boolean
  /** Keys are being held since one was held for lag: nothing but an interrupt is typed until the hold is let go. */
  held: boolean
  /** The server closed a cut paste in place of a key, and the note saying so is up until Dismiss or a later key. */
  pasteClosed: boolean
}

export type Link = {
  /**
   * Types what the person typed, if it is theirs, the terminal is live and the page is not behind the program; pointer
   * motion at most once a frame.
   */
  input(data: string, focused: boolean): void
  /** Whether output handed to the screen is still waiting to be drawn: the screen's modes may not be the program's yet. */
  drawing(): boolean
  /** Lets go of a hold on keys and clears the paste-closed note: the person's Dismiss. */
  dismiss(): void
  /** Asks the host for a size, once open and only when it changed. */
  resize(size: Size): void
  /** Closes the terminal; nothing more is shown or drawn. */
  detach(): void
}

/** Runs a function at the next animation frame. */
export type Frame = (run: () => void) => void

/** What the connection takes besides its socket and screen: frames for pointer motion, a call for each frame of output, and a monotonic clock in ms. */
export type AttachOptions = { frame: Frame; heard?: () => void; now?: () => number }

const NORMAL_CLOSE = 1000
/** What the panel says of input refused for its size, or held while the page catches up. */
export const TOO_LARGE = `That is too large to type at once: at most ${PASTE_BYTES_MAX >> 10} KiB, sent whole. Nothing was typed.`
const HELD = 'The terminal is catching up with the program, so keys other than Ctrl-C are held. Once it has, press a key to type again, or Dismiss.'
/** The note the page shows once the server has closed a cut paste in place of a key. */
export const PASTE_CLOSED = 'A paste was cut short and has been closed. Check the screen before typing.'
const RELEASED = 'Typing again. The key that ended the hold was not typed.'
/**
 * The interrupt keys, Ctrl-C and Ctrl-\: sent whenever the terminal is open, held or not, and never part of a hold, so
 * the person can stop a program that floods its terminal. In a cooked terminal they are the interrupt and quit signals
 * and do nothing else. In a raw-mode program they are keys like any other: in Emacs C-c is a prefix, so C-c C-c can
 * finish a commit or run a block, and in Claude Code a second Ctrl-C exits. Two presses through a hold reach the program
 * as two keys.
 */
const INTERRUPTS = new Set(['\x03', '\x1c'])

/** The panel's status for a view. */
export function statusText(view: View): string {
  if (view.phase === 'ended') {
    if (view.exitCode !== null) return `Exited · ${view.exitCode}`
    return STATUS.ended
  }
  // A closed terminal takes no keys: neither a lag nor a hold is worth saying
  if (view.phase === 'closed') return STATUS.closed
  if (view.lagging) return 'Catching up…'
  if (view.held) return 'Held: press a key or click Dismiss'
  return STATUS[view.phase]
}

const STATUS: Record<Phase, string> = { connecting: 'Connecting…', replaying: 'Catching up…', live: 'Live', ended: 'Ended', closed: 'Closed' }
const FELL_BEHIND: Notice = { text: 'This page fell behind the program\'s output, so the terminal was closed.', reconnect: true }

/**
 * Attaches to a terminal: clears the screen, opens the socket, writes the replay and then live output as bytes, and
 * reports each change of view and each frame of output (`heard`). It goes live only once the replay's last byte is drawn,
 * so nothing is typed against a screen, or modes, the program has already left. It never reconnects by itself.
 */
export function attach(open: () => Socket | null, screen: Screen, show: (view: View) => void, options: AttachOptions): Link {
  const { frame } = options
  const heard = options.heard ?? (() => {})
  const now = options.now ?? (() => performance.now())
  let view: View = { phase: 'connecting', exitCode: null, notice: null, note: '', lagging: false, held: false, pasteClosed: false }
  // The note is made of these, each that is true joined in a fixed rank: the paste-closed warning, then too large, then
  // the hold's (typing again, or held). So no note ever replaces one that is still true
  let holdNote = ''
  let inputNote = ''
  let pasteClosedAt: number | null = null
  const noteNow = (): string => [pasteClosedAt === null ? '' : PASTE_CLOSED, inputNote, holdNote].filter(Boolean).join(' ')
  const noted = (next: Partial<View> = {}) => update({ ...next, note: noteNow(), pasteClosed: pasteClosedAt !== null })
  // A closed or ended terminal takes no keys and shows no Dismiss: no note of input is left to ask for either
  const ends = (next: Partial<View>) => {
    holdNote = ''
    inputNote = ''
    pasteClosedAt = null
    noted({ ...next, held: false, lagging: false })
  }
  // When the last key was held, and when the page last caught up: the quiet gap runs from the later
  let heldAt = 0
  let caughtUpAt = 0
  let wanted: Size | null = null
  let sent: Size | null = null
  let detached = false
  let pending = 0
  let motion: string | null = null
  const update = (next: Partial<View>) => {
    view = { ...view, ...next }
    if (!detached) show(view)
  }
  screen.reset()
  show(view)
  const socket = open()
  if (!socket) {
    update({ phase: 'closed', notice: closeNotice(1006, '', null) })
    return { input: () => {}, drawing: () => false, dismiss: () => {}, resize: () => {}, detach: () => {} }
  }
  socket.binaryType = 'arraybuffer'
  const opened = () => view.phase !== 'connecting' && view.phase !== 'closed'
  const sendSize = () => {
    if (!wanted || !opened()) return
    if (sent && sent.cols === wanted.cols && sent.rows === wanted.rows) return
    sent = wanted
    socket.send(JSON.stringify({ t: 'resize', cols: wanted.cols, rows: wanted.rows }))
  }
  const fellBehind = () => {
    if (view.phase === 'closed') return
    socket.close(NORMAL_CLOSE)
    ends({ phase: 'closed', notice: FELL_BEHIND })
  }
  const drawn = (size: number) => () => {
    pending -= size
    if (view.lagging && pending < WRITE_PENDING_BYTES_LOW) {
      caughtUpAt = now()
      update({ lagging: false })
    }
  }
  const draw = (data: ArrayBuffer) => {
    const size = data.byteLength
    if (pending + size > WRITE_PENDING_BYTES_MAX) return fellBehind()
    pending += size
    try {
      screen.write(new Uint8Array(data), drawn(size))
    } catch {
      return fellBehind()
    }
    if (!view.lagging && pending > WRITE_PENDING_BYTES_HIGH) update({ lagging: true })
  }
  // An empty write is drawn after everything written before it: the replay's last byte
  const replayDrawn = () => {
    try {
      screen.write(new Uint8Array(0), () => {
        if (view.phase === 'replaying') update({ phase: 'live' })
      })
    } catch {
      fellBehind()
    }
  }
  socket.onopen = () => {
    if (socket.protocol !== TERMINAL_PROTOCOL) {
      socket.close(NORMAL_CLOSE)
      update({ phase: 'closed', notice: closeNotice(1006, '', null) })
      return
    }
    update({ phase: 'replaying' })
    sendSize()
  }
  socket.onmessage = (event: MessageEvent<unknown>) => {
    // Nothing from a connection the page has left or closed is drawn: a reconnect's new screen holds only its own replay
    if (detached || view.phase === 'closed') return
    if (event.data instanceof ArrayBuffer) {
      heard()
      return draw(event.data)
    }
    if (typeof event.data !== 'string') return
    const message = parseFrame(event.data)
    if (message?.t === 'replay-end') {
      if (view.phase === 'replaying') replayDrawn()
    } else if (message?.t === 'paste-closed') {
      pasteClosedAt = now()
      noted()
    } else if (message?.t === 'exit') {
      ends({ phase: 'ended', exitCode: message.code, notice: closeNotice(NORMAL_CLOSE, '', message.code) })
    }
  }
  socket.onclose = (event: CloseEvent) => {
    if (view.phase === 'ended') return
    if (view.phase === 'closed') return
    ends({ phase: 'closed', notice: closeNotice(event.code, event.reason, null) })
  }
  socket.onerror = () => {}
  const typeFrames = (data: string) => {
    const text = inputFrame(data)
    if (text === null) {
      inputNote = TOO_LARGE
      noted()
      return
    }
    socket.send(text)
    // A pointer report is not a key: it clears no note, as it neither starts nor ends a hold
    if (isMouseReport(data)) return
    // A key typed makes old the notes about earlier input. The paste-closed warning is still true after a key (the program
    // still holds the paste), so only a key well after it, or Dismiss, clears it
    holdNote = ''
    inputNote = ''
    if (pasteClosedAt !== null) {
      if (now() - pasteClosedAt >= HOLD_QUIET_MS) pasteClosedAt = null
    }
    if (view.note !== noteNow()) noted()
  }
  // Behind the program, the screen shown is not the program's: a key would answer something not yet drawn. Once one key
  // is held, all are, until the page has caught up and a key comes after a quiet gap (it only lets go) or Dismiss
  const holds = (data: string): boolean => {
    const at = now()
    // Pointer reports are dropped while held or behind, and neither start a hold nor keep it from ending
    if (isMouseReport(data)) return view.held || view.lagging
    if (view.held) {
      const quiet = at - Math.max(heldAt, caughtUpAt) >= HOLD_QUIET_MS
      heldAt = at
      if (view.lagging || !quiet) return true
      holdNote = RELEASED
      noted({ held: false })
      return true
    }
    if (!view.lagging) return false
    heldAt = at
    holdNote = HELD
    noted({ held: true })
    return true
  }
  // An interrupt goes as soon as the socket is open, during a replay too: after falling behind and reconnecting, it is how
  // the person stops the program that flooded the page
  const interrupt = (data: string) => {
    if (view.phase !== 'live') {
      if (view.phase !== 'replaying') return
    }
    // A pointer motion waiting for its frame went first, so it goes first
    sendMotion()
    socket.send(inputFrameText(data))
  }
  // The newest pointer motion waits for the next frame, so a fast pointer sends one report a frame, not one an event
  const sendMotion = () => {
    const held = motion
    motion = null
    if (held === null) return
    if (detached || view.phase !== 'live') return
    typeFrames(held)
  }
  return {
    input(data, focused) {
      if (INTERRUPTS.has(data)) return interrupt(data)
      if (view.phase !== 'live') return
      if (!forwardable(data, focused)) return
      if (holds(data)) return
      if (isMotionReport(data)) {
        if (motion === null) frame(sendMotion)
        motion = data
        return
      }
      // Anything else goes after the motion before it, in order
      sendMotion()
      typeFrames(data)
    },
    drawing: () => pending > 0,
    dismiss() {
      holdNote = ''
      pasteClosedAt = null
      noted({ held: false })
    },
    resize(size) {
      const fitted = fitSize(size)
      if (!fitted) return
      wanted = fitted
      sendSize()
    },
    detach() {
      detached = true
      motion = null
      socket.close(NORMAL_CLOSE)
    },
  }
}
