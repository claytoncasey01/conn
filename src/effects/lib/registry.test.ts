import assert from 'node:assert/strict'
import { test } from 'node:test'
import { buildRegistry } from './build.ts'

const mount = () => () => {}

test('only modules that export a string name and a mount function are kept, keyed by name', () => {
  const r = buildRegistry({
    './rain.ts': { name: 'rain', mount },
    './helper.ts': { frame: 1 },
    './half.ts': { name: 'half' },
    './nameless.ts': { mount },
    './badname.ts': { name: 3, mount },
    './badmount.ts': { name: 'bm', mount: 'x' },
    './null.ts': null,
  })
  assert.deepEqual(Object.keys(r), ['rain'])
  assert.equal(r.rain.mount, mount)
})

test('no modules is an empty registry', () => {
  assert.deepEqual(buildRegistry({}), {})
})

test('a module named like an object builtin is a plain key, not a prototype write', () => {
  const r = buildRegistry({ './x.ts': { name: '__proto__', mount } })
  assert.equal(Object.getPrototypeOf(r), Object.prototype)
  assert.deepEqual(Object.keys(r), ['__proto__'])
})

test('a module that names layer above registers above; no layer, or a layer that is not one, is behind', () => {
  const r = buildRegistry({
    './tube.ts': { name: 'tube', mount, layer: 'above' },
    './rain.ts': { name: 'rain', mount },
    './odd.ts': { name: 'odd', mount, layer: 'sideways' },
    './num.ts': { name: 'num', mount, layer: 3 },
  })
  assert.deepEqual(r.tube, { mount, layer: 'above' })
  assert.deepEqual(r.rain, { mount, layer: 'behind' })
  assert.deepEqual(r.odd, { mount, layer: 'behind' })
  assert.deepEqual(r.num, { mount, layer: 'behind' })
})
