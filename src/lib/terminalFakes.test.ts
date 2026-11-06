// Hand-written fakes for the terminal's tests: a WebSocket, animation frames and timers that run when the test says, and
// the element the terminal is drawn in. Named .test.ts so the source scans skip it, as they skip every test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TERMINAL_PROTOCOL, type Socket } from './terminal.ts'

export class FakeSocket implements Socket {
  binaryType: BinaryType = 'blob'
  protocol = ''
  sent: string[] = []
  closed: { code?: number }[] = []
  onopen: Socket['onopen'] = null
  onmessage: Socket['onmessage'] = null
  onclose: Socket['onclose'] = null
  onerror: Socket['onerror'] = null
  send(text: string) {
    this.sent.push(text)
  }
  close(code?: number) {
    this.closed.push({ code })
  }
  private socket = this as unknown as WebSocket
  open(protocol = TERMINAL_PROTOCOL) {
    this.protocol = protocol
    this.onopen?.call(this.socket, new Event('open'))
  }
  message(data: unknown) {
    this.onmessage?.call(this.socket, { data } as MessageEvent)
  }
  text(data: string) {
    this.message(data)
  }
  bytes(data: string) {
    this.message(new TextEncoder().encode(data).buffer)
  }
  end(code: number, reason = '') {
    this.onclose?.call(this.socket, { code, reason } as CloseEvent)
  }
  /** What the input frames typed, joined. */
  typed(): string {
    return this.sent.map(text => JSON.parse(text) as { t: string; d?: string }).filter(f => f.t === 'input').map(f => f.d).join('')
  }
}

/** Animation frames that run when the test calls run. */
export class FakeFrames {
  waiting: (() => void)[] = []
  frame = (run: () => void) => {
    this.waiting.push(run)
  }
  run() {
    for (const each of this.waiting.splice(0)) each()
  }
}

/** Timers that run when the test calls run, each at most once. */
export class FakeTimers {
  waiting = new Map<number, () => void>()
  next = 0
  later = (run: () => void, _ms: number) => {
    const id = this.next++
    this.waiting.set(id, run)
    return () => void this.waiting.delete(id)
  }
  run() {
    const due = [...this.waiting.values()]
    this.waiting.clear()
    for (const each of due) each()
  }
}

type Registered = { type: string; run: (event: Event) => void; capture: boolean }

/** The element the terminal is drawn in: it keeps its listeners, and dispatches events the test makes. */
export class FakeHost {
  listeners: Registered[] = []
  /** What the size watcher calls when the element's size changes. */
  resized?: () => void
  addEventListener(type: string, run: (event: Event) => void, options?: boolean | AddEventListenerOptions) {
    this.listeners.push({ type, run, capture: typeof options === 'object' ? !!options.capture : !!options })
  }
  removeEventListener(type: string, run: (event: Event) => void, options?: boolean | EventListenerOptions) {
    const capture = typeof options === 'object' ? !!options.capture : !!options
    this.listeners = this.listeners.filter(each => !(each.type === type && each.run === run && each.capture === capture))
  }
  /** Dispatches an event to the listeners for its type, as the capture phase at this element does. */
  dispatch(type: string, fields: Record<string, unknown> = {}): FakeEvent {
    const event = new FakeEvent(type, fields)
    for (const each of this.listeners.filter(l => l.type === type)) {
      if (event.stopped) break
      each.run(event as unknown as Event)
    }
    return event
  }
}

export class FakeEvent {
  isTrusted = true
  prevented = false
  stopped = false
  constructor(
    public type: string,
    fields: Record<string, unknown>,
  ) {
    Object.assign(this, fields)
  }
  preventDefault() {
    this.prevented = true
  }
  stopImmediatePropagation() {
    this.stopped = true
  }
  stopPropagation() {
    this.stopped = true
  }
}

test('the fake host removes exactly the listener added', () => {
  const host = new FakeHost()
  const run = () => {}
  host.addEventListener('paste', run, { capture: true })
  host.removeEventListener('paste', run, { capture: true })
  assert.equal(host.listeners.length, 0)
})
