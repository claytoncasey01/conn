import assert from 'node:assert/strict'
import { test } from 'node:test'
import { settleStarted, startedShows, type StartedAt } from './myPrsStarted.ts'

const at = { status: 'idle' as const, round: 0 }
/** What the row does after the press, step by step, with the press's own round settled as the row would settle it */
function walk(start: StartedAt, rows: StartedAt[]): boolean[] {
  let a = start
  return rows.map(r => {
    a = settleStarted(a, r)
    return startedShows(a, r)
  })
}

test('no press, no note', () => {
  assert.equal(startedShows(null, { status: 'idle', round: 0 }), false)
})

test('the note stays while the row is as it was, the round the press itself started included (its record can arrive before or after the answer)', () => {
  assert.deepEqual(walk(at, [{ status: 'idle', round: 0 }, { status: 'idle', round: 1 }, { status: 'idle', round: 1 }]), [true, true, true])
})

test('a RE-REVIEW from findings: the new round reads idle at first, and the note stays until the session shows up', () => {
  const press = { status: 'findings' as const, round: 1 }
  assert.deepEqual(walk(press, [{ status: 'findings', round: 1 }, { status: 'idle', round: 2 }, { status: 'idle', round: 2 }, { status: 'running', round: 2 }]), [true, true, true, false])
  // the new round seen before the answer came back: settled at once
  assert.deepEqual(walk(press, [{ status: 'idle', round: 2 }, { status: 'running', round: 2 }]), [true, false])
})

test('the note goes once the session shows up, the status moves on after the press\'s round showed up, or a later round was recorded', () => {
  assert.equal(startedShows(at, { status: 'running', round: 1 }), false)
  assert.deepEqual(walk(at, [{ status: 'idle', round: 1 }, { status: 'waiting', round: 1 }]), [true, false])
  assert.deepEqual(walk(at, [{ status: 'idle', round: 1 }, { status: 'idle', round: 2 }]), [true, false])
  assert.equal(startedShows(at, { status: 'idle', round: 2 }), false)
  // before its own round shows up, a status change also ends it
  assert.equal(startedShows(at, { status: 'fork', round: 0 }), false)
})

test('settleStarted takes the status once, the first time the press\'s own round is on the row', () => {
  const a = settleStarted({ status: 'findings', round: 1 }, { status: 'findings', round: 1 })
  assert.equal(a.after, undefined)
  const b = settleStarted(a, { status: 'idle', round: 2 })
  assert.equal(b.after, 'idle')
  assert.equal(settleStarted(b, { status: 'waiting', round: 2 }), b)
})
