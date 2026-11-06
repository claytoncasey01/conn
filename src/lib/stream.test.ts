import assert from 'node:assert/strict'
import { test } from 'node:test'
import { FRAME_CHARS_MAX, feed, newParse, RETRY_MS_DEFAULT, RETRY_MS_MAX, RETRY_MS_MIN, startStream } from './stream.ts'

test('a frame is the data lines up to a blank line; the retry line sets the wait', () => {
  const p = newParse()
  const out = feed(p, 'retry: 1500\ndata: {"a":1}\n\n')
  assert.deepEqual(out, { events: ['{"a":1}'], overflow: false })
  assert.equal(p.retryMs, 1500)
})

test('a frame split across chunks, even mid-line and between \\r and \\n, comes out whole once', () => {
  const p = newParse()
  assert.deepEqual(feed(p, 'da').events, [])
  assert.deepEqual(feed(p, 'ta: {"a"').events, [])
  assert.deepEqual(feed(p, ':1}\r').events, [])
  assert.deepEqual(feed(p, '\n\r\ndata: 2\n').events, ['{"a":1}'])
  assert.deepEqual(feed(p, '\n').events, ['2'])
})

test('several data lines join with a newline; comments, other fields and empty frames give nothing', () => {
  const p = newParse()
  assert.deepEqual(feed(p, ': ping\n\nevent: x\nid: 3\n\ndata: a\ndata:b\n\n').events, ['a\nb'])
})

test('a retry that is not a number, or out of bounds, is ignored or clamped', () => {
  const p = newParse()
  feed(p, 'retry: soon\n\n')
  assert.equal(p.retryMs, RETRY_MS_DEFAULT)
  feed(p, 'retry: 1\n\n')
  assert.equal(p.retryMs, RETRY_MS_MIN)
  feed(p, `retry: ${RETRY_MS_MAX * 10}\n\n`)
  assert.equal(p.retryMs, RETRY_MS_MAX)
})

test('a frame larger than the limit is an overflow, not a buffer that grows', () => {
  const p = newParse()
  const out = feed(p, `data: ${'x'.repeat(FRAME_CHARS_MAX)}`)
  assert.equal(out.overflow, true)
  assert.equal(p.buffer.length, 0)
})

// A fake server: each call answers the next of `bodies` (a list of chunks, or a status), and then never again.
function server(bodies: (string[] | number)[]) {
  const calls: { path: string; headers: Record<string, string> }[] = []
  const fetcher = async (path: string, init: { headers?: Record<string, string>; signal?: AbortSignal }) => {
    calls.push({ path, headers: init.headers ?? {} })
    const next = bodies.shift()
    if (next === undefined) return new Promise<Response>(() => {})
    if (typeof next === 'number') return new Response('{"error":"unauthorized"}', { status: next })
    const encoder = new TextEncoder()
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        for (const chunk of next) c.enqueue(encoder.encode(chunk))
        c.close()
      },
    })
    return new Response(body, { status: 200 })
  }
  return { calls, fetcher }
}

test('the stream delivers each frame, says when it is connected, and reconnects after the server waits it asked for', async () => {
  const { calls, fetcher } = server([['retry: 1500\ndata: 1\n\n', 'data: 2\n\n'], 401, ['data: 3\n\n']])
  const data: string[] = []
  const states: boolean[] = []
  const waits: number[] = []
  let refused = 0
  const stop = startStream('/api/events', { onData: (d) => data.push(d), onState: (c) => states.push(c), onRefused: () => refused++ }, {
    fetch: fetcher as never,
    wait: async (ms) => void waits.push(ms),
  })
  for (let i = 0; i < 50 && data.length < 3; i++) await new Promise((r) => setTimeout(r, 1))
  stop()
  assert.deepEqual(data, ['1', '2', '3'])
  assert.deepEqual(waits.slice(0, 2), [1500, 1500])
  assert.ok(calls.length >= 3)
  assert.ok(calls.every((c) => c.path === '/api/events'))
  assert.deepEqual(states.slice(0, 4), [true, false, false, true])
  assert.equal(refused, 1, 'the 401 is told apart from a dropped connection')
})
