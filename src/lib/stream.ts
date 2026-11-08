// The live-update stream over fetch: what EventSource did, with the secret's header, which EventSource cannot send. The
// server's text/event-stream is read as it arrives (data lines up to a blank line make one frame, `retry:` sets the wait
// before a reconnect), and a stream that ends, fails or is refused is opened again after that wait, as EventSource would.
// Every buffer is bounded: a frame over FRAME_CHARS_MAX drops the connection rather than grow the page's memory.
import { api } from './api.ts'

/** The most characters one frame may hold, data and the line being read together. A snapshot is far smaller. */
export const FRAME_CHARS_MAX = 16 << 20
/** The wait before a reconnect until the server says otherwise; the server asks for this too */
export const RETRY_MS_DEFAULT = 1500
/** The bounds on the wait the server may ask for: never a hot loop, never so long the page looks dead */
export const RETRY_MS_MIN = 500
export const RETRY_MS_MAX = 30_000

/** What has been read of one connection and not yet made into a frame, and the wait the server asked for */
export type Parse = { buffer: string; data: string[]; dataChars: number; retryMs: number; skipLF: boolean }

export const newParse = (): Parse => ({ buffer: '', data: [], dataChars: 0, retryMs: RETRY_MS_DEFAULT, skipLF: false })

/** Starts the next connection's reading: what was half read is dropped, the wait the server asked for is kept */
function restart(p: Parse): void {
  p.buffer = ''
  p.data = []
  p.dataChars = 0
  p.skipLF = false
}

/** Reads the next piece of the stream into p, and gives the frames it completed. overflow: a frame passed the limit, and p was emptied. */
export function feed(p: Parse, text: string): { events: string[]; overflow: boolean } {
  // a \r that ended the last piece already ended its line: a \n right after it is the same line end
  p.buffer += p.skipLF && text.startsWith('\n') ? text.slice(1) : text
  p.skipLF = false
  const events: string[] = []
  for (;;) {
    const end = p.buffer.search(/[\r\n]/)
    if (end < 0) break
    const line = p.buffer.slice(0, end)
    let next = end + 1
    if (p.buffer[end] === '\r') {
      if (end + 1 === p.buffer.length) p.skipLF = true
      else if (p.buffer[end + 1] === '\n') next = end + 2
    }
    p.buffer = p.buffer.slice(next)
    const event = line === '' ? dispatch(p) : field(p, line)
    if (event !== null) events.push(event)
  }
  if (p.buffer.length + p.dataChars > FRAME_CHARS_MAX) {
    restart(p)
    return { events, overflow: true }
  }
  return { events, overflow: false }
}

/** A blank line: the data read so far is one frame, or nothing when there is none */
function dispatch(p: Parse): string | null {
  if (p.data.length === 0) return null
  const event = p.data.join('\n')
  p.data = []
  p.dataChars = 0
  return event
}

/** One field line: data is kept for the frame, retry sets the wait, a comment or any other field is ignored */
function field(p: Parse, line: string): null {
  if (line.startsWith(':')) return null
  const colon = line.indexOf(':')
  const name = colon < 0 ? line : line.slice(0, colon)
  let value = colon < 0 ? '' : line.slice(colon + 1)
  if (value.startsWith(' ')) value = value.slice(1)
  if (name === 'data') {
    p.data.push(value)
    p.dataChars += value.length + 1
  } else if (name === 'retry' && /^[0-9]{1,9}$/.test(value)) {
    p.retryMs = Math.min(RETRY_MS_MAX, Math.max(RETRY_MS_MIN, Number(value)))
  }
  return null
}

/** onRefused: the server answered 401, so this page has no secret, or an old one: only a new address can give it one */
export type Handlers = { onData: (data: string) => void; onState: (connected: boolean) => void; onRefused?: () => void }
type Fetch = (path: string, init: { headers: Record<string, string>; cache: RequestCache; signal: AbortSignal }) => Promise<Response>
export type Deps = { fetch: Fetch; wait: (ms: number) => Promise<void> }

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

/** Opens the stream at path through api() and keeps it open until the returned stop is called */
export function startStream(path: string, handlers: Handlers, deps: Deps = { fetch: api, wait: sleep }): () => void {
  const p = newParse()
  let stopped = false
  let controller: AbortController | null = null
  void (async () => {
    while (!stopped) {
      controller = new AbortController()
      restart(p)
      await connect(path, p, handlers, deps.fetch, controller)
      handlers.onState(false)
      if (stopped) break
      await deps.wait(p.retryMs)
    }
  })()
  return () => {
    stopped = true
    controller?.abort()
  }
}

/** One connection: read until the stream ends, fails, is refused, overflows or is aborted */
async function connect(path: string, p: Parse, handlers: Handlers, fetcher: Fetch, controller: AbortController): Promise<void> {
  try {
    const r = await fetcher(path, { headers: { accept: 'text/event-stream' }, cache: 'no-store', signal: controller.signal })
    if (!r.ok || !r.body) {
      if (r.status === 401) handlers.onRefused?.()
      await r.body?.cancel().catch(() => {})
      return
    }
    handlers.onState(true)
    const reader = r.body.pipeThrough(new TextDecoderStream()).getReader()
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      const out = feed(p, value)
      for (const event of out.events) handlers.onData(event)
      if (out.overflow) {
        controller.abort()
        return
      }
    }
  } catch {
    // a dropped connection or an abort: the caller waits and connects again
  }
}
