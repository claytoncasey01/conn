// An ended hosted row: its program has exited but the host still keeps it, for its exit code and scrollback. It says it
// has ended, and its stop button removes it from the list rather than stopping anything.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { endedHosted, endedLabel, removeAsk } from './endedRow.ts'

test('only a hosted row with an exit code has ended', () => {
  const rows = [
    [{ hosted: true, exitCode: 0 }, true],
    [{ hosted: true, exitCode: 143 }, true],
    [{ hosted: true }, false],
    [{ hosted: false }, false],
    [{}, false],
  ] as const
  for (const [row, want] of rows) assert.equal(endedHosted(row), want, JSON.stringify(row))
})

test('it says it ended, with the exit code', () => {
  assert.equal(endedLabel(143), 'Ended · 143')
})

test('its button asks to remove it from the list, not to stop it', () => {
  assert.equal(removeAsk('build-3'), 'Remove build-3 from the list?')
})
