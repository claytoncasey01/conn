// The page's terminal answers nothing, types only what the person types, and follows the server's protocol (Task 4):
// these run a real xterm.js Terminal (it parses with no DOM) and a fake WebSocket.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Terminal } from '@xterm/xterm'
import { FakeFrames, FakeSocket } from './terminalFakes.test.ts'
import {
  attach, cleanPaste, closeNotice, fitSize, forwardable, inputFrame, isMotionReport, linkTarget, parseFrame, QUIET_CSI, QUIET_DCS,
  QUIET_OSC, silence, terminalOptions, CELLS_MAX, FRAME_BYTES_MAX, INPUT_BYTES_MAX, PASTE_BYTES_MAX, SIZE_MAX,
  WRITE_PENDING_BYTES_HIGH, WRITE_PENDING_BYTES_LOW, WRITE_PENDING_BYTES_MAX, HOLD_QUIET_MS,
  type View,
} from './terminal.ts'

// ---- answering nothing ----

/** Every query a program can send that xterm.js, or a terminal like it, answers. */
const QUERIES: [string, string][] = [
  ['DA1', '\x1b[c'],
  ['DA1 with 0', '\x1b[0c'],
  ['DA2', '\x1b[>c'],
  ['DA3', '\x1b[=c'],
  ['DSR status', '\x1b[5n'],
  ['CPR', '\x1b[6n'],
  ['DECXCPR', '\x1b[?6n'],
  ['colour scheme', '\x1b[?996n'],
  ['XTVERSION', '\x1b[>q'],
  ['XTVERSION 0', '\x1b[>0q'],
  ['DECRQM', '\x1b[4$p'],
  ['DECRQM private', '\x1b[?1004$p'],
  ['DECRQM synchronised output', '\x1b[?2026$p'],
  ['kitty keyboard flags', '\x1b[?u'],
  ['window size in characters', '\x1b[18t'],
  ['screen size in characters', '\x1b[19t'],
  ['window size in pixels', '\x1b[14t'],
  ['cell size in pixels', '\x1b[16t'],
  ['window state', '\x1b[11t'],
  ['window position', '\x1b[13t'],
  ['icon title report', '\x1b[20t'],
  ['window title report', '\x1b[21t'],
  ['push title', '\x1b[22;0t'],
  ['DECREQTPARM', '\x1b[x'],
  ['DECRQSS SGR', '\x1bP$qm\x1b\\'],
  ['DECRQSS margins', '\x1bP$qr\x1b\\'],
  ['DECRQSS cursor style', '\x1bP$q q\x1b\\'],
  ['XTGETTCAP', '\x1bP+q544e\x1b\\'],
  ['OSC 4 colour query', '\x1b]4;1;?\x07'],
  ['OSC 10 foreground query', '\x1b]10;?\x07'],
  ['OSC 11 background query', '\x1b]11;?\x1b\\'],
  ['OSC 12 cursor query', '\x1b]12;?\x07'],
  ['OSC 17 highlight query', '\x1b]17;?\x07'],
  ['OSC 52 clipboard read', '\x1b]52;c;?\x07'],
  ['OSC 52 clipboard write', '\x1b]52;c;aGVsbG8=\x07'],
  ['ENQ answerback', '\x05'],
  ['DECID', '\x1bZ'],
]

/** Window reports all on: the handlers must silence them even if the options were ever turned on. */
const WINDOW_REPORTS_ON = {
  getWinState: true, getWinPosition: true, getWinSizePixels: true, getScreenSizePixels: true, getCellSizePixels: true,
  getWinSizeChars: true, getScreenSizeChars: true, getIconTitle: true, getWinTitle: true, pushTitle: true, popTitle: true,
}

async function replies(sequence: string, options: object, quiet: boolean): Promise<string[]> {
  const term = new Terminal({ ...terminalOptions(), ...options })
  try {
    if (quiet) silence(term.parser)
    const data: string[] = []
    term.onData(d => data.push(d))
    term.onBinary(d => data.push(d))
    await new Promise<void>(done => term.write(sequence, done))
    return data
  } finally {
    term.dispose()
  }
}

for (const [name, sequence] of QUERIES) {
  test(`the page answers nothing to ${name}`, async () => {
    assert.deepEqual(await replies(sequence, {}, true), [])
  })
  test(`the page answers nothing to ${name}, even with every window report turned on`, async () => {
    assert.deepEqual(await replies(sequence, { windowOptions: WINDOW_REPORTS_ON }, true), [])
  })
}

test('xterm.js would answer these by itself: the silencing is what stops it', async () => {
  for (const [name, sequence] of [['DA1', '\x1b[c'], ['CPR', '\x1b[6n'], ['DECRQSS', '\x1bP$qm\x1b\\'], ['DECRQM', '\x1b[?1004$p']]) {
    assert.notDeepEqual(await replies(sequence, {}, false), [], name)
  }
})

test('the options alone keep window reports off', async () => {
  assert.deepEqual(await replies('\x1b[18t\x1b[14t\x1b[21t', {}, false), [])
  assert.notDeepEqual(await replies('\x1b[18t', { windowOptions: WINDOW_REPORTS_ON }, false), [], 'the report exists when turned on')
})

test('every window report option is set, and set off', () => {
  const options = terminalOptions()
  assert.deepEqual(Object.keys(options.windowOptions ?? {}).sort(), Object.keys(WINDOW_REPORTS_ON).concat(
    'restoreWin', 'minimizeWin', 'setWinPosition', 'setWinSizePixels', 'raiseWin', 'lowerWin', 'refreshWin', 'setWinSizeChars', 'maximizeWin', 'fullscreenWin',
  ).sort())
  for (const [name, on] of Object.entries(options.windowOptions ?? {})) assert.equal(on, false, name)
})

type Spied = { calls: number }

/** A handler for one identifier registered before the silencers: it is reached only if nothing newer took the sequence. */
function spyOn(term: Terminal, kind: 'csi' | 'dcs' | 'osc', id: unknown): Spied {
  const spied = { calls: 0 }
  const hit = () => {
    spied.calls++
    return true
  }
  if (kind === 'osc') term.parser.registerOscHandler(id as number, hit)
  else if (kind === 'dcs') term.parser.registerDcsHandler(id as { final: string }, hit)
  else term.parser.registerCsiHandler(id as { final: string }, hit)
  return spied
}

/**
 * The options with window reports on: with every one off, xterm.js hands a window operation (CSI t) to no handler at all,
 * so the spies could not see one.
 */
const dispatching = () => ({ ...terminalOptions(), windowOptions: WINDOW_REPORTS_ON })

/** A sequence each silencer row matches. */
const csiSample = (id: { prefix?: string; intermediates?: string; final: string }) => `\x1b[${id.prefix ?? ''}18${id.intermediates ?? ''}${id.final}`
const dcsSample = (id: { intermediates?: string; final: string }) => `\x1bP${id.intermediates ?? ''}${id.final}x\x1b\\`
const oscSample = (ident: number) => `\x1b]${ident};?\x07`

const rows: [string, 'csi' | 'dcs' | 'osc', unknown, string][] = [
  ...QUIET_CSI.map((id): [string, 'csi', unknown, string] => [`CSI ${JSON.stringify(id)}`, 'csi', id, csiSample(id)]),
  ...QUIET_DCS.map((id): [string, 'dcs', unknown, string] => [`DCS ${JSON.stringify(id)}`, 'dcs', id, dcsSample(id)]),
  ...QUIET_OSC.map((ident): [string, 'osc', unknown, string] => [`OSC ${ident}`, 'osc', ident, oscSample(ident)]),
]

for (const [name, kind, id, sample] of rows) {
  test(`${name} is taken by the silencer before any handler registered earlier, the built-ins included`, async () => {
    const term = new Terminal(dispatching())
    try {
      const spied = spyOn(term, kind, id)
      silence(term.parser)
      await new Promise<void>(done => term.write(sample, done))
      assert.equal(spied.calls, 0)
    } finally {
      term.dispose()
    }
  })
}

test('the spies see their sequences without the silencers, so the table rows are real matches', async () => {
  for (const [name, kind, id, sample] of rows) {
    const term = new Terminal(dispatching())
    try {
      const spied = spyOn(term, kind, id)
      await new Promise<void>(done => term.write(sample, done))
      assert.equal(spied.calls, 1, name)
    } finally {
      term.dispose()
    }
  }
})

test('the silencers cover every query a terminal like xterm.js may answer, whether or not this version does', () => {
  const required = [
    { final: 'c' }, { prefix: '>', final: 'c' }, { prefix: '=', final: 'c' }, { final: 'n' }, { prefix: '?', final: 'n' },
    { prefix: '>', final: 'q' }, { intermediates: '$', final: 'p' }, { prefix: '?', intermediates: '$', final: 'p' },
    { prefix: '?', final: 'u' }, { final: 't' }, { final: 'x' },
  ]
  for (const id of required) assert.ok(QUIET_CSI.some(row => JSON.stringify(row) === JSON.stringify(id)), `CSI ${JSON.stringify(id)}`)
  for (const id of [{ intermediates: '$', final: 'q' }, { intermediates: '+', final: 'q' }]) {
    assert.ok(QUIET_DCS.some(row => JSON.stringify(row) === JSON.stringify(id)), `DCS ${JSON.stringify(id)}`)
  }
})

test('the silencers cover the titles, the colours and the clipboard', () => {
  for (const ident of [0, 1, 2, 4, 10, 11, 12, 52, 104, 110, 111, 112]) assert.ok(QUIET_OSC.includes(ident), `OSC ${ident}`)
})

test('hyperlinks (OSC 8) are not silenced: they reach the link policy', async () => {
  const term = new Terminal(terminalOptions())
  try {
    const spied = spyOn(term, 'osc', 8)
    silence(term.parser)
    await new Promise<void>(done => term.write('\x1b]8;;https://example.com/\x07x\x1b]8;;\x07', done))
    assert.equal(spied.calls, 2)
  } finally {
    term.dispose()
  }
})

test('a title the program sets never reaches the page', async () => {
  const term = new Terminal(terminalOptions())
  try {
    silence(term.parser)
    const titles: string[] = []
    term.onTitleChange(t => titles.push(t))
    await new Promise<void>(done => term.write('\x1b]0;evil\x07\x1b]2;evil\x1b\\\x1b]1;evil\x07', done))
    assert.deepEqual(titles, [])
  } finally {
    term.dispose()
  }
})

test('the options: bounded scrollback, no line-ending conversion, link handling through the policy, non-web links refused', () => {
  const options = terminalOptions()
  assert.equal(options.convertEol, false)
  assert.ok(options.scrollback !== undefined && options.scrollback > 0 && options.scrollback <= 10_000)
  assert.equal(options.linkHandler?.allowNonHttpProtocols, false)
  assert.equal(options.allowProposedApi, true, 'the unicode 11 width table is a proposed API')
})

// ---- what is typed ----

const forwarded: [string, string, boolean, boolean][] = [
  ['a key', 'a', false, true],
  ['a key while focused', 'a', true, true],
  ['Enter', '\r', true, true],
  ['an arrow key', '\x1b[A', true, true],
  ['a focus-in report', '\x1b[I', true, false],
  ['a focus-out report', '\x1b[O', false, false],
  ['an SGR mouse report while focused', '\x1b[<0;10;5M', true, true],
  ['an SGR mouse report while not focused', '\x1b[<35;10;5M', false, false],
  ['an SGR mouse release while not focused', '\x1b[<0;10;5m', false, false],
  ['a paste that holds a focus report', 'x\x1b[Iy', true, true],
]

for (const [name, data, focused, want] of forwarded) {
  test(`forwardable: ${name}`, () => {
    assert.equal(forwardable(data, focused), want)
  })
}

const utf8 = (s: string) => new TextEncoder().encode(s).length

test('a keystroke is one input frame', () => {
  assert.equal(inputFrame('a'), '{"t":"input","d":"a"}')
})

const fits: [string, string][] = [
  ['ASCII', 'a'.repeat(INPUT_BYTES_MAX)],
  ['a whole bracketed paste at the cap', `\x1b[200~${'x'.repeat(PASTE_BYTES_MAX)}\x1b[201~`],
  ['four-byte characters', '😀'.repeat(INPUT_BYTES_MAX / 4)],
  ['escapes within the envelope', '\x01'.repeat(42)],
]

for (const [name, text] of fits) {
  test(`one frame the server takes for ${name}`, () => {
    const frame = inputFrame(text)
    assert.ok(frame)
    assert.ok(utf8(frame) <= FRAME_BYTES_MAX)
    assert.equal((JSON.parse(frame) as { d: string }).d, text)
  })
}

const refusedInput: [string, string][] = [
  ['one byte over', 'a'.repeat(INPUT_BYTES_MAX + 1)],
  ['two-byte characters over', 'é'.repeat(INPUT_BYTES_MAX / 2 + 1)],
  ['escaped controls past the frame', '\x01'.repeat(20_000)],
  ['line breaks past the frame', '\r'.repeat(40_000)],
]

for (const [name, text] of refusedInput) {
  test(`input that is not one frame is refused whole: ${name}`, () => {
    assert.equal(inputFrame(text), null)
  })
}

test('the paste cap is under the server\'s 1 MiB input burst', () => {
  assert.ok(PASTE_BYTES_MAX < 1 << 20)
})

test('a lone half of a surrogate pair is sent as U+FFFD: the server refuses text that is not UTF-8', () => {
  assert.equal(inputFrame('a\uD800b'), '{"t":"input","d":"a\uFFFDb"}')
})

test('nothing is sent for empty input', () => {
  assert.equal(inputFrame(''), null)
})

// ---- what the server sends ----

const frames: [string, string, ReturnType<typeof parseFrame>][] = [
  ['replay-end', '{"t":"replay-end"}', { t: 'replay-end' }],
  ['exit', '{"t":"exit","code":3}', { t: 'exit', code: 3 }],
  ['exit 0', '{"t":"exit","code":0}', { t: 'exit', code: 0 }],
  ['paste-closed', '{"t":"paste-closed"}', { t: 'paste-closed' }],
  ['paste-closed with more', '{"t":"paste-closed","x":1}', null],
  ['an unknown frame', '{"t":"title","d":"x"}', null],
  ['exit without a code', '{"t":"exit"}', null],
  ['exit with a text code', '{"t":"exit","code":"3"}', null],
  ['exit with a fraction', '{"t":"exit","code":1.5}', null],
  ['replay-end with more', '{"t":"replay-end","x":1}', null],
  ['not JSON', 'replay-end', null],
  ['an array', '[]', null],
  ['null', 'null', null],
  ['over the size a frame can be', '{"t":"replay-end"}' + ' '.repeat(300), null],
]

for (const [name, text, want] of frames) {
  test(`parseFrame: ${name}`, () => {
    assert.deepEqual(parseFrame(text), want)
  })
}

// ---- closing ----

const closes: [string, number, string, number | null, { reconnect: boolean; match: RegExp }][] = [
  ['the program exited', 1000, 'the program exited', 3, { reconnect: false, match: /exited with code 3/ }],
  ['Conn stopping', 1001, 'conn is stopping', null, { reconnect: true, match: /Conn is stopping/ }],
  ['fell behind', 1013, 'fell behind the program\'s output', null, { reconnect: true, match: /fell behind/ }],
  ['a slow replay', 1013, 'the replay took too long to write', null, { reconnect: true, match: /too long/ }],
  ['over the input rate', 1008, 'input over the session\'s rate', null, { reconnect: true, match: /faster than/ }],
  ['a malformed frame', 1008, 'malformed frame', null, { reconnect: false, match: /refused/ }],
  ['a size out of range', 1008, 'terminal size out of range', null, { reconnect: false, match: /size/ }],
  ['a binary frame', 1003, 'only text frames are taken', null, { reconnect: false, match: /refused/ }],
  ['a frame too big', 1009, '', null, { reconnect: false, match: /refused/ }],
  ['the terminal failed', 1011, 'the terminal failed', null, { reconnect: false, match: /failed/ }],
  ['refused at the handshake', 1006, '', null, { reconnect: true, match: /Could not attach/ }],
  ['an unknown code', 4321, 'anything', null, { reconnect: false, match: /closed/ }],
]

for (const [name, code, reason, exit, want] of closes) {
  test(`closeNotice: ${name}`, () => {
    const notice = closeNotice(code, reason, exit)
    assert.equal(notice.reconnect, want.reconnect)
    assert.match(notice.text, want.match)
  })
}

test('closeNotice never shows the reason the server sent, only its own words', () => {
  const secret = 'S'.repeat(43)
  for (const code of [1000, 1001, 1003, 1006, 1008, 1009, 1011, 1013, 4000]) {
    assert.doesNotMatch(closeNotice(code, `conn.key.${secret}`, null).text, /conn\.key|SSSS/)
  }
})

// ---- size ----

const sizes: [string, { cols: number; rows: number } | undefined, { cols: number; rows: number } | null][] = [
  ['a normal size', { cols: 120, rows: 40 }, { cols: 120, rows: 40 }],
  ['nothing measured (the panel is hidden)', undefined, null],
  ['zero columns', { cols: 0, rows: 40 }, null],
  ['not a number', { cols: Number.NaN, rows: 40 }, null],
  ['too wide', { cols: 5000, rows: 10 }, { cols: SIZE_MAX, rows: 10 }],
  ['too many cells', { cols: 1000, rows: 1000 }, { cols: 1000, rows: Math.floor(CELLS_MAX / 1000) }],
  ['a fraction', { cols: 80.7, rows: 24.2 }, { cols: 80, rows: 24 }],
]

for (const [name, proposed, want] of sizes) {
  test(`fitSize: ${name}`, () => {
    assert.deepEqual(fitSize(proposed), want)
  })
}

// ---- links ----

const ORIGIN = 'http://127.0.0.1:7777'
const links: [string, string, string | null][] = [
  ['an https page', 'https://example.com/a?b=c#d', 'https://example.com/a?b=c#d'],
  ['an http page', 'http://example.com/', 'http://example.com/'],
  ['a local dev server on another port', 'http://127.0.0.1:3000/', 'http://127.0.0.1:3000/'],
  ['a page of Conn itself', 'http://127.0.0.1:7777/#/sessions', null],
  ['Conn\'s API', 'http://127.0.0.1:7777/api/sessions/1/stop', null],
  ['javascript:', 'javascript:alert(1)', null],
  ['JavaScript: in capitals', 'JaVaScRiPt:alert(1)', null],
  ['file:', 'file:///etc/passwd', null],
  ['data:', 'data:text/html,<script>1</script>', null],
  ['vbscript:', 'vbscript:x', null],
  ['a relative path', '/api/sessions', null],
  ['ftp:', 'ftp://example.com/file', null],
  ['a WebSocket', 'ws://example.com/', null],
  ['no host', 'https://', null],
  ['a user and password', 'https://user:pass@example.com/', null],
  ['not a URL', 'not a url', null],
  ['too long', 'https://example.com/' + 'a'.repeat(5000), null],
]

for (const [name, text, want] of links) {
  test(`linkTarget: ${name}`, () => {
    assert.equal(linkTarget(text, ORIGIN), want)
  })
}

// ---- the connection, with a fake WebSocket ----

class FakeScreen {
  writes: (string | Uint8Array)[] = []
  resets = 0
  /** The callbacks of writes not yet parsed, oldest first. */
  waiting: (() => void)[] = []
  throws = false
  reset() {
    this.resets++
    this.writes = []
  }
  write(data: Uint8Array, done: () => void) {
    if (this.throws) throw new Error('write data discarded')
    // An empty write only asks to be told when everything before it is drawn
    if (data.length > 0) this.writes.push(data)
    this.waiting.push(done)
  }
  /** Parses every write so far. */
  drain() {
    for (const done of this.waiting.splice(0)) done()
  }
  text() {
    return this.writes.map(w => (typeof w === 'string' ? `STRING:${w}` : new TextDecoder().decode(w))).join('')
  }
}

function rig() {
  const socket = new FakeSocket()
  const screen = new FakeScreen()
  const views: View[] = []
  const frames = new FakeFrames()
  const clock = { now: 0 }
  const link = attach(() => socket, screen, view => views.push(view), { frame: frames.frame, now: () => clock.now })
  const last = () => views[views.length - 1]
  return { socket, screen, views, link, last, frames, clock }
}

/** Open, replayed, and the replay drawn: live. */
function live() {
  const parts = rig()
  parts.socket.open()
  parts.socket.text('{"t":"replay-end"}')
  parts.screen.drain()
  return parts
}

test('the socket takes binary frames as ArrayBuffers', () => {
  const { socket } = rig()
  assert.equal(socket.binaryType, 'arraybuffer')
})

test('attaching clears the screen first, so a reconnect does not print the replay twice', () => {
  const { screen } = rig()
  assert.equal(screen.resets, 1)
})

test('the replay is written as bytes, then replay-end makes the terminal live', () => {
  const { socket, screen, last } = rig()
  assert.equal(last().phase, 'connecting')
  socket.open()
  assert.equal(last().phase, 'replaying')
  socket.bytes('old ')
  socket.bytes('screen')
  socket.text('{"t":"replay-end"}')
  assert.equal(last().phase, 'replaying', 'not live until the replay is drawn')
  screen.drain()
  assert.equal(last().phase, 'live')
  socket.bytes(' live')
  assert.equal(screen.text(), 'old screen live')
  for (const w of screen.writes) assert.ok(w instanceof Uint8Array, 'bytes, never a string')
})

test('nothing is typed before the replay has ended and been drawn, and keys are typed once it has', () => {
  const { socket, screen, link } = rig()
  socket.open()
  socket.bytes('replay')
  link.input('a', true)
  assert.deepEqual(socket.sent, [])
  socket.text('{"t":"replay-end"}')
  link.input('b', true)
  assert.deepEqual(socket.sent, [], 'the replay is still being drawn')
  screen.drain()
  link.input('c', true)
  assert.deepEqual(socket.sent, ['{"t":"input","d":"c"}'])
})

const MiB = 1 << 20
const bytesOf = (size: number) => new Uint8Array(size).buffer

test('the page is behind only over the high mark', () => {
  const { socket, last } = live()
  socket.message(bytesOf(WRITE_PENDING_BYTES_HIGH))
  assert.equal(last().lagging, false, 'at the high mark, not over it')
  socket.message(bytesOf(1))
  assert.equal(last().lagging, true)
})

test('between the marks the page stays behind: no flicker around one threshold', () => {
  const { socket, screen, last } = live()
  socket.message(bytesOf(WRITE_PENDING_BYTES_LOW))
  socket.message(bytesOf(WRITE_PENDING_BYTES_HIGH - WRITE_PENDING_BYTES_LOW + 1))
  assert.equal(last().lagging, true)
  screen.waiting.pop()?.()
  assert.equal(last().lagging, true, 'at the low mark, still behind')
  screen.waiting.pop()?.()
  assert.equal(last().lagging, false)
})

test('once a key is held for lag, every later key is held, even once caught up, until a key after a quiet gap', () => {
  const { socket, screen, link, last, clock } = live()
  socket.message(bytesOf(2 * MiB))
  link.input('a', true)
  assert.match(last().note, /catching up/)
  assert.equal(last().held, true)
  screen.drain()
  assert.equal(last().lagging, false)
  clock.now += 100
  link.input('b', true)
  assert.deepEqual(socket.sent, [], 'still held once caught up')
  clock.now += HOLD_QUIET_MS
  link.input('c', true)
  assert.deepEqual(socket.sent, [], 'the key after the gap only lets go of the hold')
  assert.equal(last().held, false)
  link.input('d', true)
  assert.deepEqual(socket.sent, ['{"t":"input","d":"d"}'])
})

test('a quiet gap while still behind does not let go of the hold', () => {
  const { socket, link, last, clock } = live()
  socket.message(bytesOf(2 * MiB))
  link.input('a', true)
  clock.now += HOLD_QUIET_MS * 2
  link.input('b', true)
  assert.equal(last().held, true)
  assert.deepEqual(socket.sent, [])
})

test('Dismiss lets go of the hold, and clears its note', () => {
  const { socket, screen, link, last } = live()
  socket.message(bytesOf(2 * MiB))
  link.input('a', true)
  screen.drain()
  link.dismiss()
  assert.equal(last().held, false)
  assert.equal(last().note, '')
  link.input('b', true)
  assert.deepEqual(socket.sent, ['{"t":"input","d":"b"}'])
})

test('a typed key never clears the note that says keys were held', () => {
  const { socket, screen, link, last, clock } = live()
  socket.message(bytesOf(2 * MiB))
  link.input('a', true)
  screen.drain()
  clock.now += HOLD_QUIET_MS
  link.input('b', true)
  link.input('c', true)
  assert.match(last().note, /catching up|nothing was typed/i)
})

test('typed while frames arrive and are drawn around the mark: every key goes through, or none after the first held', () => {
  const { socket, screen, link, clock } = live()
  const typed = 'git push --force-with-lease\r'
  // Frames that put the page just over and just under the high mark, alternately
  socket.message(bytesOf(WRITE_PENDING_BYTES_HIGH - 64 * 1024))
  for (const key of typed) {
    socket.message(bytesOf(128 * 1024))
    link.input(key, true)
    clock.now += 50
    screen.waiting.shift()?.()
  }
  const sent = socket.sent.map(text => (JSON.parse(text) as { d: string }).d).join('')
  assert.ok(sent === typed || typed.startsWith(sent), `sent ${JSON.stringify(sent)}: a prefix of what was typed, never a mix`)
  assert.ok(!sent.endsWith('\r') || sent === typed, 'Enter only with everything before it')
})

test('the paste-closed frame says plainly that a paste was cut short', () => {
  const { socket, last } = live()
  socket.text('{"t":"paste-closed"}')
  assert.equal(last().note, 'A paste was cut short and has been closed. Check the screen before typing.')
})

test('the connection says when output is still waiting to be drawn', () => {
  const { socket, screen, link } = live()
  assert.equal(link.drawing(), false)
  socket.bytes('x')
  assert.equal(link.drawing(), true)
  screen.drain()
  assert.equal(link.drawing(), false)
})

test('every frame of output is announced, so a paste waiting for a confirmation can go stale', () => {
  const socket = new FakeSocket()
  const screen = new FakeScreen()
  let heard = 0
  attach(() => socket, screen, () => {}, { frame: new FakeFrames().frame, heard: () => void heard++ })
  socket.open()
  socket.bytes('a')
  socket.bytes('b')
  assert.equal(heard, 2)
})

test('focus reports and unfocused mouse reports are never typed', () => {
  const { socket, link } = live()
  link.input('\x1b[I', true)
  link.input('\x1b[O', false)
  link.input('\x1b[<35;1;1M', false)
  assert.deepEqual(socket.sent, [])
})

test('a paste over the cap types nothing and says so', () => {
  const { socket, link, last } = live()
  link.input('x'.repeat(INPUT_BYTES_MAX + 1), true)
  assert.deepEqual(socket.sent, [])
  assert.match(last().note, /too large/)
})

test('input that would need more than one frame is refused whole, never cut: escaped control bytes count', () => {
  const { socket, link, last } = live()
  // Under the byte cap, but each tab is two bytes as JSON
  link.input('\t'.repeat(PASTE_BYTES_MAX), true)
  assert.deepEqual(socket.sent, [])
  assert.match(last().note, /too large/)
  link.input('x'.repeat(PASTE_BYTES_MAX), true)
  assert.equal(socket.sent.length, 1)
})

test('the paste cap is one input frame less the two paste markers', () => {
  assert.equal(PASTE_BYTES_MAX, INPUT_BYTES_MAX - 12)
})

test('the exit frame shows the exit code and stops typing; the close after it changes nothing', () => {
  const { socket, link, last } = rig()
  socket.open()
  socket.text('{"t":"replay-end"}')
  socket.text('{"t":"exit","code":7}')
  assert.equal(last().phase, 'ended')
  assert.equal(last().exitCode, 7)
  link.input('a', true)
  assert.deepEqual(socket.sent, [])
  socket.end(1000, 'the program exited')
  assert.equal(last().phase, 'ended')
  assert.match(last().notice?.text ?? '', /exited with code 7/)
  assert.equal(last().notice?.reconnect, false)
})

test('a close for falling behind offers a reconnect and never reconnects by itself', () => {
  let opened = 0
  const socket = new FakeSocket()
  const views: View[] = []
  attach(() => (opened++, socket), new FakeScreen(), v => views.push(v), { frame: new FakeFrames().frame })
  socket.open()
  socket.end(1013, 'fell behind the program\'s output')
  assert.equal(views[views.length - 1].phase, 'closed')
  assert.equal(views[views.length - 1].notice?.reconnect, true)
  assert.equal(opened, 1)
})

test('a close for the input rate says so in plain words and offers a reconnect, which it never makes by itself', () => {
  const { socket, last } = rig()
  socket.open()
  socket.end(1008, 'input over the session\'s rate')
  assert.equal(last().phase, 'closed')
  assert.equal(last().notice?.reconnect, true)
  assert.match(last().notice?.text ?? '', /faster than the session takes it/)
})

test('a close for a refused frame offers no reconnect: the page would send it again', () => {
  const { socket, last } = rig()
  socket.open()
  socket.end(1008, 'malformed frame')
  assert.equal(last().notice?.reconnect, false)
})

test('a server that does not echo the terminal protocol is closed at once', () => {
  const { socket, last } = rig()
  socket.open('something.else')
  assert.equal(socket.closed.length, 1)
  assert.equal(last().phase, 'closed')
})

test('no socket (no secret, or an id that is not a session\'s) shows a notice and opens nothing', () => {
  const views: View[] = []
  attach(() => null, new FakeScreen(), v => views.push(v), { frame: new FakeFrames().frame })
  assert.equal(views[views.length - 1].phase, 'closed')
  assert.match(views[views.length - 1].notice?.text ?? '', /Could not attach/)
})

test('a size is sent once open, then only when it changes', () => {
  const { socket, link } = rig()
  link.resize({ cols: 100, rows: 30 })
  assert.deepEqual(socket.sent, [])
  socket.open()
  assert.deepEqual(socket.sent, ['{"t":"resize","cols":100,"rows":30}'])
  socket.text('{"t":"replay-end"}')
  link.resize({ cols: 100, rows: 30 })
  link.resize({ cols: 90, rows: 30 })
  assert.deepEqual(socket.sent, ['{"t":"resize","cols":100,"rows":30}', '{"t":"resize","cols":90,"rows":30}'])
})

test('a size out of range is never sent', () => {
  const { socket, link } = rig()
  socket.open()
  link.resize({ cols: 0, rows: 30 })
  link.resize({ cols: 2000, rows: 2000 })
  for (const s of socket.sent) {
    const f = JSON.parse(s) as { cols: number; rows: number }
    assert.ok(f.cols >= 1 && f.cols <= SIZE_MAX && f.rows >= 1 && f.rows <= SIZE_MAX && f.cols * f.rows <= CELLS_MAX, s)
  }
})

test('detaching closes the socket normally, and a later close shows nothing new', () => {
  const { socket, link, views } = rig()
  socket.open()
  link.detach()
  assert.deepEqual(socket.closed, [{ code: 1000 }])
  const count = views.length
  socket.end(1000)
  assert.equal(views.length, count)
})

test('a frame that is not ArrayBuffer or text is ignored', () => {
  const { socket, screen } = rig()
  socket.open()
  socket.message({ not: 'bytes' })
  assert.deepEqual(screen.writes, [])
})

// ---- the option set ----

test('every option that bears on input or output is set, to exactly these values', () => {
  const { linkHandler, windowOptions, ...rest } = terminalOptions()
  assert.deepEqual(rest, {
    allowProposedApi: true,
    altClickMovesCursor: false,
    convertEol: false,
    cursorBlink: false,
    disableStdin: false,
    ignoreBracketedPasteMode: false,
    logLevel: 'off',
    macOptionClickForcesSelection: false,
    macOptionIsMeta: false,
    minimumContrastRatio: 4.5,
    rightClickSelectsWord: false,
    screenReaderMode: false,
    scrollback: 5000,
    scrollOnUserInput: true,
    tabStopWidth: 8,
  })
  assert.equal(linkHandler?.allowNonHttpProtocols, false)
  assert.equal(Object.values(windowOptions ?? {}).filter(Boolean).length, 0)
})

// ---- pastes ----

const cleaned: [string, string, string][] = [
  ['plain text', 'hello world', 'hello world'],
  ['line breaks and tabs kept', 'a\tb\r\nc\n', 'a\tb\r\nc\n'],
  ['an end-of-paste marker', 'x\x1b[201~\rrm -rf ~\r', 'x\rrm -rf ~\r'],
  ['a start marker', '\x1b[200~x', 'x'],
  ['a lone ESC', 'a\x1bb', 'ab'],
  ['an arrow key', '\x1b[A', '[A'],
  ['C0 controls', 'a\x03\x04\x1a\x00b', 'ab'],
  ['DEL and C1', 'a\x7f\x9b31mb', 'a31mb'],
  ['emoji and accents', 'é😀', 'é😀'],
]

for (const [name, text, want] of cleaned) {
  test(`cleanPaste: ${name}`, () => {
    assert.equal(cleanPaste(text), want)
  })
}


// ---- pointer motion ----

const motions: [string, string, boolean][] = [
  ['any-motion report', '\x1b[<35;10;5M', true],
  ['drag report', '\x1b[<32;10;5M', true],
  ['a press', '\x1b[<0;10;5M', false],
  ['a release', '\x1b[<0;10;5m', false],
  ['a wheel', '\x1b[<64;10;5M', false],
  ['a key', 'a', false],
]

for (const [name, data, want] of motions) {
  test(`isMotionReport: ${name}`, () => {
    assert.equal(isMotionReport(data), want)
  })
}

test('pointer motion is sent once a frame, the newest, and before anything typed after it', () => {
  const { socket, link, frames } = live()
  for (let x = 1; x <= 50; x++) link.input(`\x1b[<35;${x};5M`, true)
  assert.deepEqual(socket.sent, [])
  assert.equal(frames.waiting.length, 1)
  frames.run()
  assert.equal(socket.typed(), '\x1b[<35;50;5M')
  link.input('\x1b[<35;51;5M', true)
  link.input('\x1b[<0;51;5M', true)
  assert.equal(socket.typed(), '\x1b[<35;50;5M\x1b[<35;51;5M\x1b[<0;51;5M')
  frames.run()
  assert.equal(socket.typed(), '\x1b[<35;50;5M\x1b[<35;51;5M\x1b[<0;51;5M')
})

test('a motion waiting for its frame is not sent once the terminal is left', () => {
  const { socket, link, frames } = live()
  link.input('\x1b[<35;1;5M', true)
  link.detach()
  frames.run()
  assert.deepEqual(socket.sent, [])
})

// ---- flow control ----

test('output arriving faster than it is drawn closes the terminal at the high mark, offering a reconnect', () => {
  const { socket, screen, last } = live()
  const chunk = new Uint8Array(1 << 20).buffer
  for (let i = 0; i < 16; i++) socket.message(chunk.slice(0))
  assert.equal(last().phase, 'live')
  assert.equal(last().lagging, true)
  socket.message(chunk.slice(0))
  assert.equal(last().phase, 'closed')
  assert.deepEqual(socket.closed, [{ code: 1000 }])
  assert.match(last().notice?.text ?? '', /fell behind/)
  assert.equal(last().notice?.reconnect, true)
  assert.equal(screen.writes.length, 16, 'nothing is handed over past the mark')
  socket.message(chunk.slice(0))
  assert.equal(screen.writes.length, 16, 'nothing is drawn once closed')
  socket.end(1000)
  assert.match(last().notice?.text ?? '', /fell behind/, 'the close the page made keeps its reason')
})

test('output drawn as fast as it arrives never closes the terminal, and the lag clears at the low mark', () => {
  const { socket, screen, last } = live()
  const chunk = new Uint8Array(1 << 20).buffer
  for (let i = 0; i < 64; i++) {
    socket.message(chunk.slice(0))
    screen.drain()
  }
  assert.equal(last().phase, 'live')
  for (let i = 0; i < 3; i++) socket.message(chunk.slice(0))
  assert.equal(last().lagging, true)
  screen.drain()
  assert.equal(last().lagging, false)
  assert.ok(WRITE_PENDING_BYTES_LOW < WRITE_PENDING_BYTES_HIGH)
  assert.ok(WRITE_PENDING_BYTES_HIGH < WRITE_PENDING_BYTES_MAX)
})

test('a screen that throws on a write closes the terminal as fallen behind, not the page', () => {
  const { socket, screen, last } = live()
  screen.throws = true
  socket.bytes('x')
  assert.equal(last().phase, 'closed')
  assert.equal(last().notice?.reconnect, true)
})

test('output from a connection the page has left is never drawn', () => {
  const { socket, screen, link } = live()
  socket.bytes('before')
  link.detach()
  socket.bytes('after')
  assert.equal(screen.text(), 'before')
})
