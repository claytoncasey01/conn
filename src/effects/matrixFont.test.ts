import assert from 'node:assert/strict'
import { existsSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { brotliDecompressSync } from 'node:zlib'
import { GLYPHS } from './rainModel.ts'

const dir = join(import.meta.dirname, 'fonts', 'matrix-code')
const file = (name: string) => join(dir, name)
const text = (name: string) => readFileSync(file(name), 'utf8')

test('the font, its license, its README and its build script are all committed together', () => {
  for (const name of ['ConnMatrixCode.woff2', 'OFL.txt', 'README.md', 'build.py']) {
    assert.ok(existsSync(file(name)), `${name} is missing`)
  }
})

test('the font is a woff2 under 8 KB', () => {
  const bytes = readFileSync(file('ConnMatrixCode.woff2'))
  assert.equal(bytes.subarray(0, 4).toString('latin1'), 'wOF2')
  assert.ok(statSync(file('ConnMatrixCode.woff2')).size < 8 * 1024)
})

test('OFL.txt is the SIL Open Font License with the M+ FONTS Project copyright', () => {
  const ofl = text('OFL.txt')
  assert.ok(ofl.includes('SIL OPEN FONT LICENSE Version 1.1'))
  assert.ok(ofl.includes('M+ FONTS Project'))
})

test('the README gives the source, the instance, the transform, the glyph set and the tooling note', () => {
  const readme = text('README.md')
  for (const needle of [
    'MPLUS1Code[wght].ttf',
    'github.com/google/fonts',
    'ff68678c5bd7e9d9d6ab6d57e4355aabe30f6b8f8bff9bd59baf6a7807dcfd36',
    'wght 300',
    '0.75',
    'mirrored',
    'U+FF66',
    'U+FF9D',
    'digits',
    'Conn Matrix Code',
    'fonttools is a manual tool and not a dependency',
  ]) {
    assert.ok(readme.includes(needle), `README.md does not mention ${needle}`)
  }
})

test('the README states the copyright and license name entries were kept, and OFL.txt sits beside the font', () => {
  const readme = text('README.md')
  assert.match(readme, /copyright/i)
  assert.match(readme, /license/i)
  assert.match(readme, /name id 0/i)
  assert.match(readme, /13, 14/)
  assert.match(readme, /kept/i)
  assert.ok(existsSync(file('OFL.txt')))
})

test('build.py opens with a docstring that names its usage', () => {
  const py = text('build.py')
  assert.ok(py.startsWith('"""'))
  const doc = py.slice(0, py.indexOf('"""', 3))
  assert.match(doc, /usage: python \S+\.py MPLUS1Code\[wght\]\.ttf out\.woff2/)
})

// The shipped file is read the way a browser reads it (WOFF2 spec, section 5): header, table directory, one brotli
// stream, then the tables back to back. node:zlib has brotli, so no dependency is added. Only cmap and maxp are read,
// and neither is ever transformed, so the glyf/loca transform is not decoded.
const TAGS = ['cmap', 'head', 'hhea', 'hmtx', 'maxp', 'name', 'OS/2', 'post', 'cvt ', 'fpgm', 'glyf', 'loca', 'prep']

function base128(b: Buffer, at: number): [number, number] {
  let v = 0
  for (let i = 0; i < 5; i++) {
    const byte = b[at + i]
    assert.ok(byte !== undefined, 'UIntBase128 runs off the end')
    assert.ok(!(i === 0 && byte === 0x80), 'UIntBase128 has a leading zero')
    v = v * 128 + (byte & 0x7f)
    if (!(byte & 0x80)) return [v, at + i + 1]
  }
  throw new Error('UIntBase128 longer than 5 bytes')
}

function woff2(b: Buffer) {
  const header = {
    signature: b.subarray(0, 4).toString('latin1'),
    flavor: b.readUInt32BE(4),
    length: b.readUInt32BE(8),
    numTables: b.readUInt16BE(12),
    reserved: b.readUInt16BE(14),
    totalSfntSize: b.readUInt32BE(16),
    totalCompressedSize: b.readUInt32BE(20),
  }
  let at = 48
  const dir: { tag: string; size: number }[] = []
  for (let i = 0; i < header.numTables; i++) {
    const flags = b[at++]
    const index = flags & 0x3f
    const version = flags >> 6
    let tag: string
    if (index === 63) {
      tag = b.subarray(at, at + 4).toString('latin1')
      at += 4
    } else tag = TAGS[index] ?? `#${index}`
    let size: number
    ;[size, at] = base128(b, at)
    // glyf and loca are transformed at version 0 (3 is the null transform), every other table at any version but 0
    const transformed = tag === 'glyf' || tag === 'loca' ? version !== 3 : version !== 0
    if (transformed) [size, at] = base128(b, at)
    dir.push({ tag, size })
  }
  const stream = brotliDecompressSync(b.subarray(at, at + header.totalCompressedSize))
  const tables = new Map<string, Buffer>()
  let off = 0
  for (const { tag, size } of dir) {
    tables.set(tag, stream.subarray(off, off + size))
    off += size
  }
  return { header, dir, streamLength: stream.length, tables, directoryEnd: at }
}

// every code point a cmap maps, from its format 4 and 12 subtables (the two a subset font has)
function mapped(cmap: Buffer): Set<number> {
  const out = new Set<number>()
  for (let i = 0; i < cmap.readUInt16BE(2); i++) {
    const sub = cmap.subarray(cmap.readUInt32BE(4 + i * 8 + 4))
    const format = sub.readUInt16BE(0)
    if (format === 4) {
      const n = sub.readUInt16BE(6) / 2
      const ends = 14
      const starts = ends + n * 2 + 2
      const deltas = starts + n * 2
      const ranges = deltas + n * 2
      for (let s = 0; s < n; s++) {
        const [end, start] = [sub.readUInt16BE(ends + s * 2), sub.readUInt16BE(starts + s * 2)]
        for (let c = start; c <= end && c !== 0xffff; c++) {
          const offset = sub.readUInt16BE(ranges + s * 2)
          const gid = offset === 0 ? (c + sub.readUInt16BE(deltas + s * 2)) & 0xffff : sub.readUInt16BE(ranges + s * 2 + offset + (c - start) * 2)
          if (gid !== 0) out.add(c)
        }
      }
    } else if (format === 12) {
      for (let g = 0; g < sub.readUInt32BE(12); g++) {
        const [start, end, gid] = [sub.readUInt32BE(16 + g * 12), sub.readUInt32BE(20 + g * 12), sub.readUInt32BE(24 + g * 12)]
        for (let c = start; c <= end; c++) if (gid + (c - start) !== 0) out.add(c)
      }
    } else assert.fail(`cmap subtable format ${format} is not one this test reads`)
  }
  return out
}

test('the woff2 header and table directory are well formed and describe the whole file', () => {
  const bytes = readFileSync(file('ConnMatrixCode.woff2'))
  const { header, dir, streamLength, directoryEnd } = woff2(bytes)
  assert.equal(header.signature, 'wOF2')
  assert.ok([0x00010000, 0x4f54544f].includes(header.flavor), `flavor ${header.flavor.toString(16)} is neither TrueType nor CFF`)
  assert.equal(header.length, bytes.length)
  assert.equal(header.reserved, 0)
  assert.equal(header.numTables, dir.length)
  const tags = dir.map((d) => d.tag)
  for (const need of ['cmap', 'head', 'hhea', 'hmtx', 'maxp', 'glyf', 'loca']) assert.ok(tags.includes(need), `no ${need} table in ${tags}`)
  assert.equal(new Set(tags).size, tags.length, 'a table is listed twice')
  // no metadata or private block: the compressed stream runs to the end of the file (padded to 4)
  assert.ok(bytes.length - (directoryEnd + header.totalCompressedSize) < 4, 'bytes after the compressed stream')
  // the brotli stream holds exactly the tables the directory declares, and the font they rebuild to is larger than that
  assert.equal(streamLength, dir.reduce((n, d) => n + d.size, 0))
  assert.ok(header.totalSfntSize >= streamLength)
})

test('the font carries exactly the glyphs the rain draws, and .notdef', () => {
  const { tables } = woff2(readFileSync(file('ConnMatrixCode.woff2')))
  const maxp = tables.get('maxp')!
  assert.equal(maxp.readUInt16BE(4), GLYPHS.length + 1, 'numGlyphs is the rain glyph set plus .notdef')
  const have = mapped(tables.get('cmap')!)
  const drawn = new Set(GLYPHS.flatMap((g) => [...g].map((c) => c.codePointAt(0)!)))
  assert.deepEqual(
    [...drawn].filter((c) => !have.has(c)).map((c) => c.toString(16)),
    [],
    'a character the rain draws has no glyph',
  )
  assert.deepEqual(
    [...have].filter((c) => !drawn.has(c)).map((c) => c.toString(16)),
    [],
    'the font maps a character the rain never draws',
  )
})

test('the woff2 reader rejects a damaged file, so the checks above are not vacuous', () => {
  const bytes = Buffer.from(readFileSync(file('ConnMatrixCode.woff2')))
  const cut = bytes.subarray(0, bytes.length - 40)
  assert.notEqual(cut.readUInt32BE(8), cut.length)
  const flipped = Buffer.from(bytes)
  flipped[bytes.length - 20] ^= 0xff
  assert.throws(() => woff2(flipped))
})
