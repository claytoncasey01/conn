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
const mounting = read('src/lib/terminalMount.ts')

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
    if (['src/lib/Terminal.svelte', 'src/lib/terminal.ts', 'src/lib/terminalMount.ts'].includes(file)) assert.doesNotMatch(text, /navigator\.clipboard|ClipboardAddon|ImageAddon|execCommand/, file)
  }
})

test('OSC 52, the clipboard, is among the commands taken whole', () => {
  assert.match(logic, /export const QUIET_OSC[^=]*= \[\n[^\]]*\b52\b/)
})



test('nothing in the terminal moves the window, sets the page title, or draws text as HTML', () => {
  for (const text of [panel, logic, mounting]) {
    assert.doesNotMatch(text, /location\.(href|assign|replace)|document\.title|onTitleChange|innerHTML|outerHTML|insertAdjacentHTML|\{@html/)
  }
})

test('no page that shows the panel draws HTML from a string', () => {
  for (const file of ['src/lib/Terminal.svelte', 'src/lib/AskPanel.svelte', 'src/pages/Sessions.svelte', 'src/pages/TicketRun.svelte', 'src/pages/AgentPage.svelte']) {
    assert.doesNotMatch(read(file), /\{@html/, file)
  }
})



test('the terminal logs nothing', () => {
  for (const text of [panel, logic, mounting, read('src/lib/terminalTheme.ts')]) assert.doesNotMatch(text, /console\./)
})


test('OPEN TERMINAL on a hosted session opens its panel and asks the server nothing', () => {
  const ask = read('src/lib/AskPanel.svelte')
  const fn = ask.slice(ask.indexOf('async function terminal()'), ask.indexOf('const replyS'))
  assert.match(fn, /if \(session\.hosted\) \{\n(\s*\/\/.*\n)?\s*showTerminal\(session\.id\)\n\s*return\n\s*\}/)
  assert.ok(fn.indexOf('showTerminal') < fn.indexOf('openTerminal('))
})


test('the panel gives mount the real pieces, and adds nothing to the options it makes', () => {
  assert.match(panel, /makeTerminal: options => new Terminal\(options\),/)
  assert.match(panel, /makeUnicode: \(\) => new Unicode11Addon\(\),/)
  assert.match(panel, /openSocket: \(\) => termSocket\(sessionId\),/)
  assert.match(panel, /openLink: url => void window\.open\(url, '_blank', 'noopener,noreferrer'\),/)
  assert.match(panel, /ownOrigin: location\.origin,/)
  // nothing in the panel turns a window report, a link or a mouse option back on
  assert.doesNotMatch(panel, /windowOptions|linkHandler|allowNonHttpProtocols|altClickMovesCursor|onData|onBinary|attachCustom/)
})

test('a reconnect is a person\'s click on the button, and nothing else calls it', () => {
  assert.match(panel, /\{#if view\.notice\?\.reconnect\}\s*<button type="button" class="btn cap" onclick=\{reconnect\}>Reconnect<\/button>/)
  assert.match(panel, /function reconnect\(event: MouseEvent\) \{\n\s*if \(!event\.isTrusted\) return\n\s*panel\?\.reconnect\(\)/)
  assert.equal(panel.split('.reconnect(').length - 1, 1)
  assert.doesNotMatch(mounting.slice(mounting.indexOf('return {')), /reconnect\(\)\s*\{[^}]*setTimeout/)
})

test('the panel says how to leave the terminal by keyboard', () => {
  assert.match(panel, /\{LEAVE_HINT\} leaves the terminal/)
  assert.match(panel, /leave: \(\) => detachButton\?\.focus\(\),/)
})

test('the pages load the panel with import(), so xterm.js is not in their first chunk', () => {
  for (const page of ['src/pages/Sessions.svelte', 'src/pages/TicketRun.svelte', 'src/pages/AgentPage.svelte']) {
    const text = read(page)
    assert.doesNotMatch(text, /import Terminal from|Terminal\.svelte'/, page)
    assert.match(text, /\{#await loadTerminal\(\) then \{ default: TerminalPanel \}\}/, page)
  }
  for (const file of sources.filter(f => f !== 'src/lib/terminalPanel.svelte.ts')) {
    assert.doesNotMatch(read(file), /from '[^']*Terminal\.svelte'/, file)
  }
  assert.match(read('src/lib/terminalPanel.svelte.ts'), /loading \?\?= import\('\.\/Terminal\.svelte'\)/)
})

test('the Sessions, run and agent pages show the panel of a hosted session, a new panel for each session', () => {
  // The panel attaches once, when it is made: keyed by the session, so another session's never reuses it
  assert.match(read('src/pages/Sessions.svelte'), /\{#key terminalOf\.id\}\s*\{#await loadTerminal\(\) then \{ default: TerminalPanel \}\}\s*<TerminalPanel sessionId=\{terminalOf\.id\}/)
  assert.match(read('src/pages/TicketRun.svelte'), /\{#key hosted\.id\}\s*\{#await loadTerminal\(\) then \{ default: TerminalPanel \}\}\s*<TerminalPanel sessionId=\{hosted\.id\}/)
  assert.match(read('src/pages/AgentPage.svelte'), /\{#if owner\?\.session\.hosted && terminalPanel\.open === owner\.session\.id\}\s*\{#key owner\.session\.id\}\s*\{#await loadTerminal\(\) then \{ default: TerminalPanel \}\}\s*<TerminalPanel sessionId=\{owner\.session\.id\}/)
  // A run of several repositories: the lead's session in the shared layout, and each leg's own in the split one
  assert.match(read('src/pages/TicketRun.svelte'), /legHosted\(run, lead, live\.snap\?\.sessions \?\? \[\]\)/)
  assert.match(read('src/pages/TicketRun.svelte'), /\{#key legHostedHere\.id\}\s*\{#await loadTerminal\(\) then \{ default: TerminalPanel \}\}\s*<TerminalPanel sessionId=\{legHostedHere\.id\}/)
  assert.match(read('src/lib/runLegs.ts'), /runTerminalSession\(sessions, \{ key: run\.repos\.length > 1 \? null : run\.key, worktree: leg\.worktree/)
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

test('Dismiss can act only while the terminal is live, and only for a hold or a closed paste', () => {
  assert.match(panel, /const dismissable = \$derived\(view\.phase === 'live' && \(view\.held \|\| view\.pasteClosed\)\)/)
  assert.match(panel, /disabled=\{!dismissable\}[\s\S]*?onclick=\{event => panel\?\.dismiss\(event\)\}>Dismiss<\/button>/)
})

test('the status line is statusText, which a closed or ended terminal answers before any lag or hold', () => {
  assert.match(panel, /\{statusText\(view\)\}/)
})

test('the confirmation takes the keyboard: focus moves to its first button, Escape cancels, and focus goes back', () => {
  assert.match(panel, /role="alertdialog" aria-modal="true" aria-labelledby="\{askTitleId\}" aria-describedby="\{askTextId\}"/)
  assert.match(panel, /onkeydown=\{askKey\}/)
  assert.match(panel, /function askKey\(event: KeyboardEvent\) \{\n\s*if \(event\.key !== 'Escape'\) return/)
  assert.match(panel, /bind:this=\{askFirst\}/)
  assert.match(panel, /askFirst\?\.focus\(\)/)
  assert.match(panel, /askReturn\?\.focus\(\)/)
})

test('Dismiss stays while it has the focus, disabled once there is nothing to dismiss', () => {
  assert.match(panel, /\{#if dismissable \|\| dismissFocused\}/)
  assert.match(panel, /disabled=\{!dismissable\}/)
  assert.match(panel, /onfocus=\{\(\) => \(dismissFocused = true\)\} onblur=\{\(\) => \(dismissFocused = false\)\}/)
})

test('the panel names both keys that leave the terminal', () => {
  assert.match(panel, /\{LEAVE_HINT\} leaves the terminal/)
})
