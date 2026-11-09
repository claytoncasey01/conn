// The "Where sessions open" card's pure parts: the section as the server sends it, the body a save sends (held to the server's own
// answers in testdata/answers/settings.json) and the line a save leaves. No setting names a program.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { validateSection } from './serverAnswers.ts'
import { WHERE_CHOICES, readSessions, sameSessions, sessionsSection, wroteSessions } from './settingsSessions.ts'

test('a section the page cannot read is the defaults: inside Conn, resume on launch, Claude Code’s advisor; a terminal command is not read', () => {
  const defaults = { where: 'inside', resumeOnLaunch: true, advisor: 'claude-code' }
  for (const raw of [undefined, null, 5, 'inside', [], {}]) assert.deepEqual(readSessions(raw), defaults)
  assert.deepEqual(readSessions({ where: 'tmux', resumeOnLaunch: 'no', advisor: 'claude-opus-5-5' }), defaults)
  assert.deepEqual(readSessions({ where: 'command', terminalCommand: ['/bin/sh', '-c', '{command}'], resumeOnLaunch: false, advisor: 'fable' }), {
    where: 'command',
    resumeOnLaunch: false,
    advisor: 'fable',
  })
})

test('each choice is one the server takes, with a line of help', () => {
  assert.deepEqual(
    WHERE_CHOICES.map((c) => c.value),
    ['inside', 'terminal', 'command'],
  )
  for (const c of WHERE_CHOICES) {
    assert.ok(c.label.length > 0)
    assert.ok(c.help.length > 0 && !c.help.includes('\n'), c.value)
    assert.equal(validateSection('sessions', { where: c.value }).ok, true, c.value)
  }
  assert.match(WHERE_CHOICES[0].help, /inside Conn/)
})

test('a save sends the whole section, and the server takes it as sent; a terminal command sent by an older page is not kept', () => {
  const good = { where: 'terminal' as const, resumeOnLaunch: false, advisor: 'claude-code' as const }
  assert.deepEqual(sessionsSection(good), good)
  const answer = validateSection('sessions', sessionsSection(good))
  assert.equal(answer.ok, true)
  assert.deepEqual(answer.value, good)
  const old = validateSection('sessions', { where: 'inside', terminalCommand: ['/bin/sh', '-c', '{command}'] })
  assert.deepEqual(old.value, { where: 'inside', resumeOnLaunch: true, advisor: 'claude-code' })
})

test('two drafts are the same when every field is', () => {
  const a = { where: 'inside' as const, resumeOnLaunch: true, advisor: 'claude-code' as const }
  assert.equal(sameSessions(a, { ...a }), true)
  assert.equal(sameSessions(a, { ...a, where: 'command' }), false)
  assert.equal(sameSessions(a, { ...a, resumeOnLaunch: false }), false)
  assert.equal(sameSessions(a, { ...a, advisor: 'fable' }), false)
})

test('a save says where sessions now open, and their advisor', () => {
  const own = 'claude-code' as const
  assert.equal(wroteSessions({ where: 'inside', resumeOnLaunch: true, advisor: own }), 'Saved: sessions open inside Conn, and resume when conn starts. Advisor: Claude Code’s setting')
  assert.equal(wroteSessions({ where: 'inside', resumeOnLaunch: false, advisor: 'fable' }), 'Saved: sessions open inside Conn, and do not resume when conn starts. Advisor: Fable')
  assert.equal(wroteSessions({ where: 'terminal', resumeOnLaunch: false, advisor: own }), 'Saved: sessions open in cmux, and those that ran inside Conn do not resume. Advisor: Claude Code’s setting')
  assert.equal(
    wroteSessions({ where: 'terminal', resumeOnLaunch: true, advisor: 'opus' }),
    'Saved: sessions open in cmux, and those that ran inside Conn resume once sessions open inside Conn again. Advisor: Opus',
  )
  assert.equal(
    wroteSessions({ where: 'command', resumeOnLaunch: true, advisor: 'sonnet' }),
    'Saved: conn shows the command to paste, and those that ran inside Conn resume once sessions open inside Conn again. Advisor: Sonnet',
  )
})

test('each advisor is one the server takes', () => {
  for (const advisor of ['claude-code', 'fable', 'opus', 'sonnet']) assert.equal(validateSection('sessions', { advisor }).ok, true, advisor)
  assert.equal(validateSection('sessions', { advisor: 'claude-opus-5-5' }).ok, false)
})
