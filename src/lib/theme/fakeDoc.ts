// A document with just the parts apply.ts touches, for tests: head is an ordered list so insertion order can be asserted.
import type { Doc, El } from './apply.ts'

export type FakeDoc = Doc & { attrs: Record<string, string>; order(): string[]; el(id: string): El | undefined; hrefWrites(el: El): number }

export function fakeDoc(): FakeDoc {
  const children: El[] = []
  const writes = new WeakMap<El, number>()
  const make = (): El => {
    let href: string | undefined
    const e: El = {
      id: '',
      textContent: '',
      get href() {
        return href
      },
      set href(v) {
        href = v
        writes.set(e, (writes.get(e) ?? 0) + 1)
      },
      getAttribute: (name) => (name === 'href' ? (href ?? null) : null),
      remove() {
        const i = children.indexOf(e)
        if (i >= 0) children.splice(i, 1)
      },
    }
    return e
  }
  const attrs: Record<string, string> = {}
  return {
    attrs,
    documentElement: { setAttribute: (k, v) => void (attrs[k] = String(v)) },
    head: {
      get lastElementChild() {
        return children.at(-1) ?? null
      },
      appendChild: (e) => void children.push(e),
      insertBefore: (e, ref) => {
        const i = ref ? children.indexOf(ref) : -1
        if (i < 0) children.push(e)
        else children.splice(i, 0, e)
      },
    },
    getElementById: (id) => children.find((c) => c.id === id) ?? null,
    createElement: () => make(),
    order: () => children.map((c) => c.id),
    el: (id) => children.find((c) => c.id === id),
    hrefWrites: (e) => writes.get(e) ?? 0,
  }
}
