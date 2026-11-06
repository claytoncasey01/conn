// The "Where sessions open" card's pure parts: the section as the server sends it, the terminal command as one argument a line,
// the body a save sends (held to the server's own answers in testdata/answers/settings.json) and the line a save leaves.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { validateSection } from './serverAnswers.ts'
import { WHERE_CHOICES, commandLines, commandText, readSessions, sameSessions, sessionsSection, wroteSessions } from './settingsSessions.ts'

test('a section the page cannot read is the defaults: inside Conn, no command of your own, resume on launch', () => {
  const defaults = { where: 'inside', terminalCommand: [], resumeOnLaunch: true }
  for (const raw of [undefined, null, 5, 'inside', [], {}]) assert.deepEqual(readSessions(raw), defaults)
  assert.deepEqual(readSessions({ where: 'tmux', terminalCommand: 'kitty', resumeOnLaunch: 'no' }), defaults)
  assert.deepEqual(readSessions({ where: 'command', terminalCommand: ['/bin/t', 7, '{command}'], resumeOnLaunch: false }), {
    where: 'command',
    terminalCommand: ['/bin/t', '{command}'],
    resumeOnLaunch: false,
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
})

test('the terminal command is one argument a line: blank lines are dropped, nothing else is changed', () => {
  assert.deepEqual(commandLines(''), [])
  assert.deepEqual(commandLines('/bin/t\n\n  --title={name}\n{command}\n'), ['/bin/t', '  --title={name}', '{command}'])
  assert.deepEqual(commandLines('/bin/t\r\n{command}'), ['/bin/t', '{command}'])
  assert.equal(commandText(['/bin/t', '{command}']), '/bin/t\n{command}')
  assert.deepEqual(commandLines(commandText(['/a b', "it's", '{command}'])), ['/a b', "it's", '{command}'])
})

test('a save sends the whole section, and the server takes a good one as sent and names what is wrong with a bad one', () => {
  const good = { where: 'terminal' as const, terminalCommand: ['/Applications/kitty.app/Contents/MacOS/kitty', '--directory', '{dir}', 'sh', '-c', '{command}'], resumeOnLaunch: false }
  const answer = validateSection('sessions', sessionsSection(good))
  assert.equal(answer.ok, true)
  assert.deepEqual(answer.value, good)

  const bad = validateSection('sessions', sessionsSection({ where: 'terminal', terminalCommand: ['kitty'], resumeOnLaunch: true }))
  assert.equal(bad.ok, false)
  assert.match(bad.error ?? '', /absolute path/)
  assert.match(bad.error ?? '', /no \{command\}/)
})

test('two drafts are the same when every field is', () => {
  const a = { where: 'inside' as const, terminalCommand: ['/bin/t', '{command}'], resumeOnLaunch: true }
  assert.equal(sameSessions(a, { ...a, terminalCommand: ['/bin/t', '{command}'] }), true)
  assert.equal(sameSessions(a, { ...a, where: 'command' }), false)
  assert.equal(sameSessions(a, { ...a, terminalCommand: ['/bin/t'] }), false)
  assert.equal(sameSessions(a, { ...a, resumeOnLaunch: false }), false)
})

test('a save says where sessions now open', () => {
  assert.equal(wroteSessions({ where: 'inside', terminalCommand: [], resumeOnLaunch: true }), 'Saved: sessions open inside Conn, and resume when conn starts')
  assert.equal(wroteSessions({ where: 'terminal', terminalCommand: [], resumeOnLaunch: false }), 'Saved: sessions open in cmux, and do not resume when conn starts')
  assert.equal(wroteSessions({ where: 'terminal', terminalCommand: ['/bin/t', '{command}'], resumeOnLaunch: true }), 'Saved: sessions open with your terminal command, and resume when conn starts')
  assert.equal(wroteSessions({ where: 'command', terminalCommand: [], resumeOnLaunch: true }), 'Saved: conn shows the command to paste, and sessions resume when conn starts')
})
