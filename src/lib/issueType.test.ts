import assert from 'node:assert/strict'
import { test } from 'node:test'
import { typeChipStyle } from './issueType.ts'

test('typeChipStyle colours bug red, epic purple, task light blue, story green and sub-task teal, by their theme tokens', () => {
  assert.match(typeChipStyle('Bug'), /color: var\(--k-bug\)/)
  assert.match(typeChipStyle('Epic'), /color: var\(--k-epic\)/)
  assert.match(typeChipStyle('Task'), /color: var\(--k-task\)/)
  assert.match(typeChipStyle('Story'), /color: var\(--k-story\)/)
  assert.match(typeChipStyle('Sub-task'), /color: var\(--k-subtask\)/)
  assert.match(typeChipStyle('Subtask'), /color: var\(--k-subtask\)/)
})

test('typeChipStyle ignores case and spaces, and gives a type it does not know the neutral chip', () => {
  assert.equal(typeChipStyle('  BUG '), typeChipStyle('bug'))
  assert.match(typeChipStyle('Spike'), /color: var\(--mu\)/)
  assert.doesNotMatch(typeChipStyle('Spike'), /background/)
  assert.match(typeChipStyle(''), /color: var\(--mu\)/)
})

test('typeChipStyle builds the tint from the token: half for the border, 12% for the fill, in the theme border style', () => {
  assert.equal(
    typeChipStyle('bug'),
    'border: 1px var(--border-style) color-mix(in oklch, var(--k-bug) 50%, transparent); background: color-mix(in oklch, var(--k-bug) 12%, transparent); color: var(--k-bug)',
  )
  assert.equal(typeChipStyle('spike'), 'border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: var(--mu)')
})

test('typeChipStyle holds no hex or rgb color', () => {
  for (const t of ['bug', 'epic', 'task', 'story', 'sub-task', 'subtask', 'spike']) assert.doesNotMatch(typeChipStyle(t), /#[0-9a-f]{3,8}\b|\brgba?\(/i)
})
