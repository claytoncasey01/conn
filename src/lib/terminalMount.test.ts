// The panel's whole life with a real xterm.js (its open() is the one thing stubbed: it needs a DOM) and fakes for the
// element, the socket, timers and frames: what a click, a wheel, a paste, a link and a key may do, and that teardown and
// reconnect leave nothing behind.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Terminal, type ITerminalOptions } from '@xterm/xterm'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { FakeFrames, FakeHost, FakeSocket, FakeTimers } from './terminalFakes.test.ts'
import { LEAVE_KEY, mount, REAL_EVENTS_ONLY, type Fit, type Host, type PanelView, type Term } from './terminalMount.ts'

type Made = {
  term: Terminal
  options: ITerminalOptions
  disposed: boolean
  wheel: ((event: WheelEvent) => boolean) | null
  key: ((event: KeyboardEvent) => boolean) | null
}

function rig(origin = 'http://127.0.0.1:7777') {
  const host = new FakeHost()
  const sockets: FakeSocket[] = []
  const made: Made[] = []
  const views: PanelView[] = []
  const opened: string[] = []
  const timers = new FakeTimers()
  const frames = new FakeFrames()
  let left = 0
  let watching = 0
  const fit: Fit = { activate: () => {}, dispose: () => {}, proposeDimensions: () => ({ cols: 100, rows: 30 }) }
  const panel = mount({
    host: host as unknown as Host,
    makeTerminal: options => {
      const term = new Terminal(options)
      const record: Made = { term, options, disposed: false, wheel: null, key: null }
      // open() draws into a DOM; paste() clears the textarea it reads from. Neither exists here
      term.open = () => {}
      ;(term as unknown as { _core: { textarea: object } })._core.textarea = { value: '', blur: () => {}, focus: () => {} }
      const dispose = term.dispose.bind(term)
      term.dispose = () => ((record.disposed = true), dispose())
      const wheel = term.attachCustomWheelEventHandler.bind(term)
      term.attachCustomWheelEventHandler = handler => ((record.wheel = handler), wheel(handler))
      const key = term.attachCustomKeyEventHandler.bind(term)
      term.attachCustomKeyEventHandler = handler => ((record.key = handler), key(handler))
      made.push(record)
      return term as Term
    },
    makeFit: () => fit,
    makeUnicode: () => new Unicode11Addon(),
    openSocket: () => {
      const socket = new FakeSocket()
      sockets.push(socket)
      return socket
    },
    watchSize: changed => {
      watching++
      host.resized = changed
      return { disconnect: () => void watching-- }
    },
    later: timers.later,
    frame: frames.frame,
    openLink: url => void opened.push(url),
    ownOrigin: origin,
    leave: () => void left++,
    show: view => void views.push(view),
    look: { theme: {}, font: 'monospace', size: 13 },
  })
  const socket = () => sockets[sockets.length - 1]
  const term = () => made[made.length - 1]
  const last = () => views[views.length - 1]
  /** The program's terminal is open, replayed with `replay`, and live. */
  const live = async (replay = '') => {
    socket().open()
    if (replay !== '') socket().bytes(replay)
    socket().text('{"t":"replay-end"}')
    await new Promise<void>(done => term().term.write('', done))
  }
  return { host, panel, sockets, socket, made, term, views, last, opened, timers, frames, live, left: () => left, watching: () => watching }
}

/** What the person's input produces: xterm.js's onData, as a key or the mouse would fire it. */
const userTypes = (term: Terminal, data: string) => term.input(data, true)

// ---- teardown and reconnect ----

test('stopping closes the socket, disposes the terminal, stops watching the size and removes every listener', () => {
  const { host, panel, socket, term, watching } = rig()
  assert.ok(host.listeners.length > 0)
  panel.stop()
  assert.deepEqual(socket().closed, [{ code: 1000 }])
  assert.equal(term().disposed, true)
  assert.equal(watching(), 0)
  assert.deepEqual(host.listeners, [])
})

test('a resize waiting for its debounce does not run after the panel stops', () => {
  const { host, panel, timers, socket } = rig()
  socket().open()
  host.resized?.()
  panel.stop()
  assert.equal(timers.waiting.size, 0)
})

for (const code of [1000, 1001, 1003, 1006, 1008, 1009, 1011, 1013, 4000]) {
  test(`a close with ${code} is never followed by a reconnect the person did not ask for`, async () => {
    const { sockets, socket, made, timers, frames } = rig()
    socket().open()
    socket().end(code, code === 1008 ? "input over the session's rate" : '')
    for (let round = 0; round < 5; round++) {
      await Promise.resolve()
      await new Promise(done => setTimeout(done, 0))
      timers.run()
      frames.run()
    }
    assert.equal(sockets.length, 1)
    assert.equal(made.length, 1)
  })
}

test('a reconnect is a new terminal: the old one is disposed and nothing queued for it is drawn on the new one', async () => {
  const { panel, socket, made, term } = rig()
  socket().open()
  const old = socket()
  for (let i = 0; i < 2000; i++) old.bytes(`OLD${i}\r\n`)
  panel.reconnect()
  assert.equal(made.length, 2)
  assert.equal(made[0].disposed, true)
  assert.deepEqual(old.closed, [{ code: 1000 }])
  old.bytes('LATE\r\n')
  socket().open()
  socket().bytes('NEW-REPLAY')
  await new Promise<void>(done => term().term.write('', done))
  const screen = term().term.buffer.active
  const lines: string[] = []
  for (let row = 0; row < screen.length; row++) lines.push(screen.getLine(row)?.translateToString(true) ?? '')
  const text = lines.join('\n')
  assert.match(text, /NEW-REPLAY/)
  assert.doesNotMatch(text, /OLD|LATE/)
})

test('the terminal is made with the intended options: contrast held, Alt-click never moves the cursor', () => {
  const { term } = rig()
  assert.equal(term().options.minimumContrastRatio, 4.5)
  assert.equal(term().options.altClickMovesCursor, false)
  assert.equal(term().term.unicode.activeVersion, '11')
})

// ---- the wheel and the mouse ----

test('a wheel over a terminal without the focus never reaches xterm.js; with the focus it does', () => {
  const { host, term } = rig()
  const wheel = term().wheel
  assert.ok(wheel)
  assert.equal(wheel({ isTrusted: true } as WheelEvent), false)
  host.dispatch('focusin')
  assert.equal(wheel({ isTrusted: true } as WheelEvent), true)
  assert.equal(wheel({ isTrusted: false } as WheelEvent), false)
  host.dispatch('focusout')
  assert.equal(wheel({ isTrusted: true } as WheelEvent), false)
})

test('the click that gives the terminal the focus types nothing, up to its release; the next click types', async () => {
  const { host, socket, term, live, frames } = rig()
  // A program that asked for every mouse report
  await live('\x1b[?1003h\x1b[?1006h')
  host.dispatch('pointerdown')
  host.dispatch('focusin')
  userTypes(term().term, '\x1b[<0;5;5M')
  userTypes(term().term, '\x1b[<32;6;5M')
  userTypes(term().term, '\x1b[<0;6;5m')
  assert.equal(socket().typed(), '')
  // Once it is let go, the pointer's motion over the program's screen is the person's again
  userTypes(term().term, '\x1b[<35;8;5M')
  frames.run()
  assert.equal(socket().typed(), '\x1b[<35;8;5M')
  socket().sent.length = 0
  host.dispatch('pointerdown')
  userTypes(term().term, '\x1b[<0;7;5M')
  userTypes(term().term, '\x1b[<0;7;5m')
  assert.equal(socket().typed(), '\x1b[<0;7;5M\x1b[<0;7;5m')
})

test('a focus report is never typed, even when the program asked for them', async () => {
  const { host, socket, term, live } = rig()
  await live('\x1b[?1004h')
  host.dispatch('focusin')
  userTypes(term().term, '\x1b[I')
  userTypes(term().term, '\x1b[O')
  assert.equal(socket().typed(), '')
})

// ---- what only a person can do ----

for (const type of REAL_EVENTS_ONLY) {
  test(`a ${type} the page's own script made never reaches xterm.js; a real one does`, () => {
    const { host } = rig()
    const made = host.dispatch(type, { isTrusted: false })
    assert.equal(made.stopped, true)
    assert.equal(made.prevented, true)
    const real = host.dispatch(type, { isTrusted: true })
    assert.equal(real.stopped, false)
  })
}

test('the key handler takes only real keys', () => {
  const { term } = rig()
  const key = term().key
  assert.ok(key)
  assert.equal(key({ isTrusted: false, type: 'keydown', key: 'a' } as KeyboardEvent), false)
  assert.equal(key({ isTrusted: true, type: 'keydown', key: 'a' } as KeyboardEvent), true)
})

test(`${LEAVE_KEY} leaves the terminal and types nothing`, () => {
  const { term, left } = rig()
  const key = term().key
  assert.ok(key)
  assert.equal(key({ isTrusted: true, type: 'keydown', key: LEAVE_KEY } as KeyboardEvent), false)
  assert.equal(key({ isTrusted: true, type: 'keyup', key: LEAVE_KEY } as KeyboardEvent), false)
  assert.equal(left(), 1)
})

// ---- pastes ----

const clipboard = (text: string) => ({ clipboardData: { getData: (kind: string) => (kind === 'text/plain' ? text : '') } })

test('a pasted end-of-paste marker never reaches the socket: the paste stays one paste', async () => {
  const { host, socket, live } = rig()
  await live('\x1b[?2004h')
  const event = host.dispatch('paste', clipboard('safe\x1b[201~\rrm -rf ~\r'))
  assert.equal(event.prevented, true)
  assert.equal(event.stopped, true, 'xterm.js never sees the raw paste')
  const typed = socket().typed()
  assert.equal(typed, '\x1b[200~safe\rrm -rf ~\r\x1b[201~')
  assert.equal(typed.split('\x1b[201~').length - 1, 1)
})

test('control characters in a paste are never typed as keys', async () => {
  const { host, socket, live } = rig()
  await live('\x1b[?2004h')
  host.dispatch('paste', clipboard('a\x1b[Ab\x03c'))
  assert.equal(socket().typed(), '\x1b[200~a[Abc\x1b[201~')
})

test('a paste with line breaks the program would take as Enters waits for a confirmation', async () => {
  const { host, panel, socket, last, live } = rig()
  await live()
  host.dispatch('paste', clipboard('line one\nline two\n'))
  assert.equal(socket().typed(), '')
  assert.deepEqual(last().ask, { kind: 'paste', lines: 3 })
  panel.confirm({ isTrusted: false })
  assert.equal(socket().typed(), '', 'a confirmation the script made does nothing')
  panel.confirm({ isTrusted: true })
  assert.equal(socket().typed(), 'line one\rline two\r')
  assert.equal(last().ask, null)
})

test('a cancelled paste types nothing', async () => {
  const { host, panel, socket, live } = rig()
  await live()
  host.dispatch('paste', clipboard('a\nb'))
  panel.cancel()
  panel.confirm({ isTrusted: true })
  assert.equal(socket().typed(), '')
})

test('a paste on one line needs no confirmation, bracketed or not', async () => {
  const { host, socket, live } = rig()
  await live()
  host.dispatch('paste', clipboard('just one line'))
  assert.equal(socket().typed(), 'just one line')
})

test('a paste the page\'s script made types nothing', async () => {
  const { host, socket, live } = rig()
  await live('\x1b[?2004h')
  host.dispatch('paste', { isTrusted: false, ...clipboard('x') })
  assert.equal(socket().typed(), '')
})

test('nothing is pasted before the replay has ended', () => {
  const { host, socket } = rig()
  socket().open()
  host.dispatch('paste', clipboard('x'))
  assert.equal(socket().typed(), '')
})

// ---- links ----

const click = (fields: Partial<MouseEvent> = {}) => ({ isTrusted: true, metaKey: false, ctrlKey: false, ...fields }) as MouseEvent

test('hovering a link shows where it goes, not what the program drew for it', () => {
  const { term, last } = rig()
  // The program draws "https://github.com/conn" with a hyperlink to another site: xterm.js hands the handler the target
  term().options.linkHandler?.hover?.(click(), 'https://evil.example/login', { start: { x: 1, y: 1 }, end: { x: 22, y: 1 } })
  assert.match(last().hover, /evil\.example/)
  assert.doesNotMatch(last().hover, /github/)
  term().options.linkHandler?.leave?.(click(), 'https://evil.example/login', { start: { x: 1, y: 1 }, end: { x: 22, y: 1 } })
  assert.equal(last().hover, '')
})

test('a plain click on a link opens nothing; Cmd or Ctrl and a click ask first, naming the host', () => {
  const { panel, term, last, opened } = rig()
  const activate = term().options.linkHandler?.activate
  assert.ok(activate)
  const range = { start: { x: 1, y: 1 }, end: { x: 5, y: 1 } }
  activate(click(), 'https://evil.example/x', range)
  assert.equal(last().ask, null)
  assert.deepEqual(opened, [])
  activate(click({ metaKey: true }), 'https://evil.example/x', range)
  assert.deepEqual(last().ask, { kind: 'link', url: 'https://evil.example/x', host: 'evil.example' })
  assert.deepEqual(opened, [])
  panel.confirm({ isTrusted: true })
  assert.deepEqual(opened, ['https://evil.example/x'])
})

test('Ctrl and a click asks too', () => {
  const { term, last } = rig()
  term().options.linkHandler?.activate(click({ ctrlKey: true }), 'http://example.com/', { start: { x: 1, y: 1 }, end: { x: 5, y: 1 } })
  assert.equal(last().ask?.kind, 'link')
})

const refusedLinks: [string, string][] = [
  ['javascript:', 'javascript:alert(1)'],
  ['Conn itself', 'http://127.0.0.1:7777/#/settings'],
  ['file:', 'file:///etc/passwd'],
]

for (const [name, uri] of refusedLinks) {
  test(`a link to ${name} is refused even with Cmd and a click`, () => {
    const { panel, term, last, opened } = rig()
    term().options.linkHandler?.activate(click({ metaKey: true }), uri, { start: { x: 1, y: 1 }, end: { x: 5, y: 1 } })
    assert.equal(last().ask, null)
    panel.confirm({ isTrusted: true })
    assert.deepEqual(opened, [])
    assert.match(last().hover, /does not open/)
  })
}

test('a link click the page\'s script made does nothing', () => {
  const { term, last } = rig()
  term().options.linkHandler?.activate({ ...click({ metaKey: true }), isTrusted: false } as MouseEvent, 'https://example.com/', { start: { x: 1, y: 1 }, end: { x: 5, y: 1 } })
  assert.equal(last().ask, null)
})

// ---- size ----

test('a change of size is debounced, then fitted and sent', () => {
  const { host, socket, timers } = rig()
  socket().open()
  socket().sent.length = 0
  host.resized?.()
  host.resized?.()
  assert.equal(timers.waiting.size, 1)
  assert.deepEqual(socket().sent, [])
})
