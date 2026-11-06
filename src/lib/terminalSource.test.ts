// The terminal's wiring that the tests of terminal.ts cannot see, pinned as text as the other source tests are, plus the
// socket's opening against a stand-in WebSocket: what the panel loads, how it attaches, and where the secret goes.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Glob } from 'bun'
import { termSocket } from './api.ts'
import { KEY_PROTOCOL_PREFIX, TERMINAL_PROTOCOL } from './terminal.ts'

const root = new URL('../../', import.meta.url).pathname
const read = (rel: string) => readFileSync(root + rel, 'utf8')
const sources = [...new Glob('{src,public}/**/*.{ts,js,svelte}').scanSync(root)].filter(f => !/\.test\.ts$/.test(f)).sort()
const panel = read('src/lib/Terminal.svelte')
const logic = read('src/lib/terminal.ts')

test('the only terminal packages are xterm.js and its fit and unicode 11 addons, each pinned to an exact version', () => {
  const pkg = JSON.parse(read('package.json')) as { dependencies?: Record<string, string>; devDependencies?: Record<string, string> }
  const all = { ...pkg.dependencies, ...pkg.devDependencies }
  const xterm = Object.keys(all).filter(name => name.startsWith('@xterm/') || name.includes('xterm')).sort()
  assert.deepEqual(xterm, ['@xterm/addon-fit', '@xterm/addon-unicode11', '@xterm/xterm'])
  for (const name of xterm) assert.match(all[name], /^\d+\.\d+\.\d+$/, `${name} is pinned`)
})

test('no source loads a clipboard, image, web-links or other xterm.js addon, or touches the clipboard', () => {
  for (const file of sources) {
    const text = read(file)
    for (const m of text.matchAll(/from '(@xterm\/[^']+)'/g)) {
      assert.ok(['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-unicode11', '@xterm/xterm/css/xterm.css'].includes(m[1]), `${file}: ${m[1]}`)
    }
    if (file === 'src/lib/Terminal.svelte' || file === 'src/lib/terminal.ts') assert.doesNotMatch(text, /navigator\.clipboard|ClipboardAddon|ImageAddon|execCommand/, file)
  }
})

test('OSC 52, the clipboard, is among the commands taken whole', () => {
  assert.match(logic, /export const QUIET_OSC[^=]*= \[\n[^\]]*\b52\b/)
})

test('the panel silences the queries before it opens the terminal or attaches', () => {
  const silenced = panel.indexOf('silence(t.parser)')
  assert.ok(silenced > 0)
  assert.ok(silenced < panel.indexOf('t.open(into)'))
  assert.ok(silenced < panel.indexOf('connect()\n'))
})

test('the panel takes the options from terminalOptions, which keeps window reports off, with links through the policy', () => {
  assert.match(panel, /new Terminal\(\{ \.\.\.terminalOptions\(openLink\), theme: terminalTheme\(theme\.resolved\.tokens\)/)
  // nothing after the spread turns a window report or a link back on
  assert.doesNotMatch(panel, /windowOptions|linkHandler|allowNonHttpProtocols/)
  const open = panel.slice(panel.indexOf('function openLink('), panel.indexOf('function measure('))
  assert.match(open, /const target = linkTarget\(text, location\.origin\)\n\s*if \(target === null\) return\n\s*window\.open\(target, '_blank', 'noopener,noreferrer'\)/)
})

test('nothing in the terminal moves the window, sets the page title, or draws text as HTML', () => {
  for (const text of [panel, logic]) {
    assert.doesNotMatch(text, /location\.(href|assign|replace)|document\.title|onTitleChange|innerHTML|outerHTML|insertAdjacentHTML|\{@html/)
  }
})

test('no page that shows the panel draws HTML from a string', () => {
  for (const file of ['src/lib/Terminal.svelte', 'src/lib/AskPanel.svelte', 'src/pages/Sessions.svelte', 'src/pages/TicketRun.svelte', 'src/pages/AgentPage.svelte']) {
    assert.doesNotMatch(read(file), /\{@html/, file)
  }
})

test('the unicode 11 width table is the one in use', () => {
  assert.match(panel, /t\.loadAddon\(new Unicode11Addon\(\)\)\n(\s*\/\/.*\n)?\s*t\.unicode\.activeVersion = '11'/)
})

test('what the person types goes through the connection, which types only their own input', () => {
  assert.match(panel, /t\.onData\(data => link\?\.input\(data, focused\)\)/)
  assert.doesNotMatch(panel, /onBinary/)
  assert.match(panel, /into\.addEventListener\('focusin', gained\)/)
  assert.match(panel, /into\.addEventListener\('focusout', lost\)/)
})

test('the terminal logs nothing', () => {
  for (const text of [panel, logic, read('src/lib/terminalTheme.ts')]) assert.doesNotMatch(text, /console\./)
})

test('a reconnect is a button, never a timer', () => {
  assert.match(panel, /\{#if view\.notice\?\.reconnect\}\s*<button type="button" class="btn cap" onclick=\{connect\}>Reconnect<\/button>/)
  assert.doesNotMatch(logic, /setTimeout|setInterval/)
  const timers = [...panel.matchAll(/setTimeout\(([^,]+),/g)].map(m => m[1])
  assert.deepEqual(timers, ['done', 'measure'])
})

test('OPEN TERMINAL on a hosted session opens its panel and asks the server nothing', () => {
  const ask = read('src/lib/AskPanel.svelte')
  const fn = ask.slice(ask.indexOf('async function terminal()'), ask.indexOf('const replyS'))
  assert.match(fn, /if \(session\.hosted\) \{\n(\s*\/\/.*\n)?\s*showTerminal\(session\.id\)\n\s*return\n\s*\}/)
  assert.ok(fn.indexOf('showTerminal') < fn.indexOf('openTerminal('))
})

test('the Sessions, run and agent pages show the panel of a hosted session, a new panel for each session', () => {
  // The panel attaches once, when it is made: keyed by the session, so another session's never reuses it
  assert.match(read('src/pages/Sessions.svelte'), /\{#key terminalOf\.id\}\s*<Terminal sessionId=\{terminalOf\.id\}/)
  assert.match(read('src/pages/TicketRun.svelte'), /\{#key hosted\.id\}\s*<Terminal sessionId=\{hosted\.id\}/)
  assert.match(read('src/pages/AgentPage.svelte'), /\{#if owner\?\.session\.hosted && terminalPanel\.open === owner\.session\.id\}\s*\{#key owner\.session\.id\}\s*<Terminal sessionId=\{owner\.session\.id\}/)
})

// ---- the socket ----

type Opened = { url: string; protocols: string[] }

function opening(run: () => unknown): Opened[] {
  const opened: Opened[] = []
  const real = globalThis.WebSocket
  globalThis.WebSocket = class {
    constructor(url: string, protocols: string[]) {
      opened.push({ url, protocols })
    }
  } as unknown as typeof WebSocket
  try {
    run()
  } finally {
    globalThis.WebSocket = real
  }
  return opened
}

const KEY = 'k'.repeat(43)
const ID = '0123456789abcdef0123456789abcdef'

test('the socket carries the secret as its second subprotocol, never in the URL (Task 4)', () => {
  const opened = opening(() => termSocket(ID, KEY, '127.0.0.1:7777'))
  assert.deepEqual(opened, [{ url: `ws://127.0.0.1:7777/api/term/${ID}`, protocols: [TERMINAL_PROTOCOL, `${KEY_PROTOCOL_PREFIX}${KEY}`] }])
  assert.equal(TERMINAL_PROTOCOL, 'conn.term.v1')
  assert.equal(KEY_PROTOCOL_PREFIX, 'conn.key.')
  assert.ok(!opened[0].url.includes(KEY))
})

const refused: [string, string, string | null][] = [
  ['no secret', ID, null],
  ['an id in capitals', ID.toUpperCase(), KEY],
  ['an id with a path in it', '../sessions/0123456789abcdef0123', KEY],
  ['a pid', '4242', KEY],
  ['an id with more after it', `${ID}?x=1`, KEY],
]

for (const [name, id, key] of refused) {
  test(`no socket is opened for ${name}`, () => {
    let socket: unknown = 'unset'
    const opened = opening(() => (socket = termSocket(id, key, '127.0.0.1:7777')))
    assert.deepEqual(opened, [])
    assert.equal(socket, null)
  })
}
