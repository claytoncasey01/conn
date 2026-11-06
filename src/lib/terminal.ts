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

/** The server's inputFrameBytesMax: the text one input frame may type. */
export const INPUT_BYTES_MAX = 64 << 10
/** The server's read limit: an input frame's text plus its JSON envelope. */
export const FRAME_BYTES_MAX = INPUT_BYTES_MAX + 256
/** The largest paste typed, under the server's 1 MiB input burst so a paste never closes the terminal for its rate. */
export const PASTE_BYTES_MAX = 256 << 10
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
/** The bytes of `{"t":"input","d":""}` around an input frame's text. */
const INPUT_ENVELOPE_BYTES = 20

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

/**
 * The terminal's options, every one that bears on what reaches the program or the page set here on purpose:
 * - every window operation off (a report would type text the program chose into its own input);
 * - hyperlinks only through `open`, which applies `linkTarget`, and never a link that is not http or https;
 * - line endings as the program sends them (the terminal on the host already turns LF into CR LF);
 * - a bounded scrollback;
 * - the proposed API on, for the unicode 11 width table (the host's screen model counts emoji two cells wide, as it does).
 */
export function terminalOptions(open: (link: string) => void = () => {}): ITerminalOptions {
  return {
    allowProposedApi: true,
    convertEol: false,
    scrollback: SCROLLBACK_LINES,
    cursorBlink: false,
    disableStdin: false,
    ignoreBracketedPasteMode: false,
    screenReaderMode: false,
    // Colours a program picks for a dark background stay readable on a light theme (the host tells programs it is dark)
    minimumContrastRatio: 4.5,
    linkHandler: { activate: (_event, text) => open(text), allowNonHttpProtocols: false },
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
const SGR_MOUSE = /^\x1b\[<\d+;\d+;\d+[Mm]$/
const X10_MOUSE = /^\x1b\[M[\s\S]{3}$/

/**
 * Whether what xterm.js produced is the person's own input: never a focus report (the host counts it as input while the
 * program has not asked for them), and a mouse report only while the terminal has the focus (with any-motion tracking on,
 * a pointer passing over the panel on its way to a button would otherwise type).
 */
export function forwardable(data: string, focused: boolean): boolean {
  if (FOCUS_REPORTS.has(data)) return false
  if (SGR_MOUSE.test(data)) return focused
  if (X10_MOUSE.test(data)) return focused
  return true
}

const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g

/** The UTF-8 bytes of one code point. */
const utf8Bytes = (point: number): number => (point < 0x80 ? 1 : point < 0x800 ? 2 : point < 0x10000 ? 3 : 4)
/** The bytes one character takes in a JSON string: an escape for the controls, the quote and the backslash. */
const jsonBytes = (char: string, point: number): number => (point < 0x80 ? JSON.stringify(char).length - 2 : utf8Bytes(point))

const inputFrame = (text: string): string => JSON.stringify({ t: 'input', d: text })

/**
 * Input as the frames that type it, each within the server's limits, splitting no character; null for a paste over
 * PASTE_BYTES_MAX, which is refused whole rather than cut. A lone half of a surrogate pair becomes U+FFFD.
 */
export function inputFrames(data: string): string[] | null {
  // A string's UTF-16 length is never more than its UTF-8 bytes, so a longer one is over the cap without counting
  if (data.length > PASTE_BYTES_MAX) return null
  const text = data.replace(LONE_SURROGATE, '\uFFFD')
  const frames: string[] = []
  let chunk = ''
  let chunkBytes = 0
  let chunkJson = 0
  let total = 0
  for (const char of text) {
    const point = char.codePointAt(0) ?? 0
    const bytes = utf8Bytes(point)
    const json = jsonBytes(char, point)
    total += bytes
    if (total > PASTE_BYTES_MAX) return null
    if (chunkBytes + bytes > INPUT_BYTES_MAX || INPUT_ENVELOPE_BYTES + chunkJson + json > FRAME_BYTES_MAX) {
      frames.push(inputFrame(chunk))
      chunk = ''
      chunkBytes = 0
      chunkJson = 0
    }
    chunk += char
    chunkBytes += bytes
    chunkJson += json
  }
  if (chunk !== '') frames.push(inputFrame(chunk))
  return frames
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

export type ServerFrame = { t: 'replay-end' } | { t: 'exit'; code: number }

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
  if (frame.t === 'exit' && keys === 'code,t' && Number.isInteger(frame.code)) return { t: 'exit', code: frame.code as number }
  return null
}

export type Notice = { text: string; reconnect: boolean }

/** The server's close reasons it says more about (Task 4's table); any other reason is never shown. */
const REASONS: Record<string, string> = {
  "input over the session's rate": 'Conn closed the terminal: input came faster than the session takes it.',
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

/** The part of the terminal the connection writes to. */
export type Screen = { reset(): void; write(data: Uint8Array): void }

export type Phase = 'connecting' | 'replaying' | 'live' | 'ended' | 'closed'
/** What the panel shows: where the connection is, the exit code once the program ended, why it closed, and a passing note. */
export type View = { phase: Phase; exitCode: number | null; notice: Notice | null; note: string }

export type Link = {
  /** Types what the person typed, if it is theirs and the terminal is live. */
  input(data: string, focused: boolean): void
  /** Asks the host for a size, once open and only when it changed. */
  resize(size: Size): void
  /** Closes the terminal; nothing more is shown. */
  detach(): void
}

const NORMAL_CLOSE = 1000

/**
 * Attaches to a terminal: clears the screen (so a reconnect does not print the replay twice), opens the socket, writes the
 * replay and then live output as bytes, and reports each change of view. It never reconnects by itself.
 */
export function attach(open: () => Socket | null, screen: Screen, show: (view: View) => void): Link {
  let view: View = { phase: 'connecting', exitCode: null, notice: null, note: '' }
  let wanted: Size | null = null
  let sent: Size | null = null
  let detached = false
  const update = (next: Partial<View>) => {
    view = { ...view, ...next }
    if (!detached) show(view)
  }
  screen.reset()
  show(view)
  const socket = open()
  if (!socket) {
    update({ phase: 'closed', notice: closeNotice(1006, '', null) })
    return { input: () => {}, resize: () => {}, detach: () => {} }
  }
  socket.binaryType = 'arraybuffer'
  const opened = () => view.phase !== 'connecting' && view.phase !== 'closed'
  const sendSize = () => {
    if (!wanted || !opened()) return
    if (sent && sent.cols === wanted.cols && sent.rows === wanted.rows) return
    sent = wanted
    socket.send(JSON.stringify({ t: 'resize', cols: wanted.cols, rows: wanted.rows }))
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
    if (event.data instanceof ArrayBuffer) {
      screen.write(new Uint8Array(event.data))
      return
    }
    if (typeof event.data !== 'string') return
    const frame = parseFrame(event.data)
    if (frame?.t === 'replay-end') {
      if (view.phase === 'replaying') update({ phase: 'live' })
    } else if (frame?.t === 'exit') {
      update({ phase: 'ended', exitCode: frame.code, notice: closeNotice(NORMAL_CLOSE, '', frame.code) })
    }
  }
  socket.onclose = (event: CloseEvent) => {
    if (view.phase === 'ended') return
    update({ phase: 'closed', notice: closeNotice(event.code, event.reason, null) })
  }
  socket.onerror = () => {}
  return {
    input(data, focused) {
      if (view.phase !== 'live') return
      if (!forwardable(data, focused)) return
      const frames = inputFrames(data)
      if (!frames) {
        update({ note: `That paste is too large to type: at most ${PASTE_BYTES_MAX >> 10} KiB at once.` })
        return
      }
      if (view.note !== '') update({ note: '' })
      for (const frame of frames) socket.send(frame)
    },
    resize(size) {
      const fitted = fitSize(size)
      if (!fitted) return
      wanted = fitted
      sendSize()
    },
    detach() {
      detached = true
      socket.close(NORMAL_CLOSE)
    },
  }
}
