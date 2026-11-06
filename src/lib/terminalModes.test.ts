// The host's mode preamble (internal/pty/screenmodes.go), from the cases both sides check
// (testdata/answers/terminal/modes.json): a terminal reset and given the preamble is in the modes it would be in had it
// read the program's output itself. So a viewer who joins late, or reconnects, pastes, clicks and types the arrow keys as
// the program asked, however long ago it asked.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { Terminal } from '@xterm/xterm'
import { silence, terminalOptions } from './terminal.ts'

type Case = { name: string; args: [string]; out: string; failSafe?: boolean }
const cases = (JSON.parse(readFileSync(new URL('../../testdata/answers/terminal/modes.json', import.meta.url), 'utf8')) as { preamble: Case[] }).preamble

type Modes = { cursorKeys: boolean; keypad: boolean; paste: boolean; focus: boolean; mouse: string; encoding: string; alternate: boolean }

async function modesAfter(output: string, resetFirst: boolean): Promise<Modes> {
  const term = new Terminal(terminalOptions())
  try {
    silence(term.parser)
    if (resetFirst) term.reset()
    await new Promise<void>(done => term.write(output, done))
    const mouse = (term as unknown as { _core: { coreMouseService: { activeProtocol: string; activeEncoding: string } } })._core.coreMouseService
    return {
      cursorKeys: term.modes.applicationCursorKeysMode,
      keypad: term.modes.applicationKeypadMode,
      paste: term.modes.bracketedPasteMode,
      focus: term.modes.sendFocusMode,
      mouse: mouse.activeProtocol,
      encoding: mouse.activeEncoding,
      alternate: term.buffer.active.type === 'alternate',
    }
  } finally {
    term.dispose()
  }
}

const OFF: Modes = { cursorKeys: false, keypad: false, paste: false, focus: false, mouse: 'NONE', encoding: 'DEFAULT', alternate: false }

test('there are cases', () => {
  assert.ok(cases.length >= 20)
})

for (const recorded of cases) {
  if (recorded.failSafe) {
    test(`${recorded.name}: the preamble turns every input mode off, the safe side`, async () => {
      const got = await modesAfter(recorded.out, true)
      assert.deepEqual({ ...got, alternate: false }, OFF)
    })
    continue
  }
  test(`${recorded.name}: after a reset and the preamble, the page's modes are the ones the output set`, async () => {
    assert.deepEqual(await modesAfter(recorded.out, true), await modesAfter(recorded.args[0], false))
  })
}

test('a reset alone loses the modes, which is what the preamble is for', async () => {
  const claude = cases.find(c => c.name === "Claude Code's start")
  assert.ok(claude)
  assert.notDeepEqual(await modesAfter('', true), await modesAfter(claude.args[0], false))
})
