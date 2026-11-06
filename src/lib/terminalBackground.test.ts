// The page tells the server the background its terminal is drawn on, so a hosted program asking for it (OSC 11) gets the
// theme's, light themes included. Sent once per change, through api(), as #rrggbb; a failed send is tried again next time.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { afterEach, test } from 'node:test'
import { looks } from '../../themes/looks.ts'
import { contrast, parseColor } from './theme/color.ts'
import { backgroundSender } from './terminalBackground.ts'
import { terminalTheme } from './terminalTheme.ts'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

type Sent = { url: string; method?: string; body?: string; headers: Record<string, string> }
function recording(status = 200): Sent[] {
  const sent: Sent[] = []
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    sent.push({ url, method: init?.method, body: init?.body as string, headers: (init?.headers ?? {}) as Record<string, string> })
    return new Response('{"ok":true}', { status })
  }) as unknown as typeof fetch
  return sent
}

test('a light look sends its light terminal background, as the terminal draws it', async () => {
  const light = looks().find((l) => l.name.toLowerCase().includes('light'))
  assert.ok(light, `a light look among ${looks().map((l) => l.name).join(', ')}`)
  const background = terminalTheme(light.tokens).background as string
  const parsed = parseColor(background)
  assert.ok(parsed)
  assert.ok(contrast(parsed, { r: 255, g: 255, b: 255, a: 1 }) < 2, `${background} is light`)

  const sent = recording()
  const send = backgroundSender()
  await send(light.tokens)
  assert.equal(sent.length, 1)
  assert.equal(sent[0].url, '/api/term/background')
  assert.equal(sent[0].method, 'PUT')
  assert.equal(sent[0].headers['x-conn'], '1')
  assert.deepEqual(JSON.parse(sent[0].body ?? ''), { background })
})

test('the same background is sent once, a new one again, and a failed send is tried on the next call', async () => {
  const [first, second] = looks()
  const sent = recording()
  const send = backgroundSender()
  await send(first.tokens)
  await send(first.tokens)
  assert.equal(sent.length, 1)
  await send(second.tokens)
  assert.equal(sent.length, terminalTheme(first.tokens).background === terminalTheme(second.tokens).background ? 1 : 2)

  const refused = recording(500)
  const again = backgroundSender()
  await again(first.tokens)
  await again(first.tokens)
  assert.equal(refused.length, 2)
})

test('a lost connection is not thrown', async () => {
  globalThis.fetch = (async () => {
    throw new Error('connection refused')
  }) as unknown as typeof fetch
  await backgroundSender()(looks()[0].tokens)
})

test('the app sends its terminal background once the theme list is read, and again on each change of look', () => {
  const app = readFileSync(new URL('../App.svelte', import.meta.url), 'utf8')
  assert.match(app, /const sendBackground = backgroundSender\(\)/)
  assert.match(app, /\$effect\(\(\) => \{\n\s+if \(theme\.ready\) void sendBackground\(theme\.resolved\.tokens\)\n\s+\}\)/)
})
