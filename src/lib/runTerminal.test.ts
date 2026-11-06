// A run's terminal panel shows its own hosted session and no other: by id, else by name or folder only when one matches.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { runTerminalSession } from './runTerminal.ts'
import type { SessionSummary } from './types.ts'

const session = (id: string, fields: Partial<SessionSummary> = {}) => ({ id, name: 'other', cwd: '/elsewhere', hosted: true, ...fields }) as SessionSummary

const run = { key: 'PROJ-1', worktree: '/w/proj-1', sessionId: 'a'.repeat(32) as string | null }

test('the session with the run\'s id, even with another in the same worktree', () => {
  const sessions = [session('b'.repeat(32), { cwd: run.worktree }), session('a'.repeat(32), { cwd: run.worktree })]
  assert.equal(runTerminalSession(sessions, run)?.id, 'a'.repeat(32))
})

test('two hosted sessions in the run\'s worktree and none with its id: neither is shown', () => {
  const sessions = [session('b'.repeat(32), { cwd: run.worktree }), session('c'.repeat(32), { cwd: run.worktree })]
  assert.equal(runTerminalSession(sessions, { ...run, sessionId: null }), null)
  assert.equal(runTerminalSession(sessions, run), null)
})

test('the one hosted session with the run\'s name or worktree, when the run has no id yet', () => {
  assert.equal(runTerminalSession([session('b'.repeat(32), { name: run.key })], { ...run, sessionId: null })?.id, 'b'.repeat(32))
  assert.equal(runTerminalSession([session('c'.repeat(32), { cwd: run.worktree })], { ...run, sessionId: null })?.id, 'c'.repeat(32))
})

test('a session that is not hosted is never the run\'s terminal', () => {
  assert.equal(runTerminalSession([session('a'.repeat(32), { hosted: false })], run), null)
  assert.equal(runTerminalSession([session('4242', { hosted: undefined, name: run.key })], { ...run, sessionId: null }), null)
})

test('a hosted session of another run is not this one\'s', () => {
  assert.equal(runTerminalSession([session('d'.repeat(32))], run), null)
})
