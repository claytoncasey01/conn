// The highlight that follows the pointer (the card-sheen token). One listener for the whole page puts the pointer's place on
// the surface under it as --mx and --my, at most once a frame; lookCss.ts reads them only while Effects are on and the OS
// allows motion, and otherwise the highlight rests at the top left. A surface the pointer leaves goes back to its rest, and a
// theme without the highlight (--look-sheen is none) gets no writes at all.

/** What sheen.ts needs of a surface */
export type SheenSurface = {
  getBoundingClientRect(): { left: number; top: number }
  style: { setProperty(name: string, value: string): void; removeProperty(name: string): void }
}

/** What sheen.ts needs of the page */
export type SheenPage = {
  addEventListener(type: 'pointermove' | 'pointerout', listener: (e: SheenPointer) => void, options: { passive: true }): void
  requestAnimationFrame(callback: () => void): void
  /** Whether the current theme draws the highlight */
  lit(): boolean
}

/** The part of a pointer event sheen.ts reads */
export type SheenPointer = { pointerType: string; clientX: number; clientY: number; surface: SheenSurface | null; leftPage: boolean }

/** The pointer's place inside a surface, as the --mx and --my values lookCss.ts reads */
export function placeOn(rect: { left: number; top: number }, x: number, y: number): { mx: string; my: string } {
  return { mx: `${Math.round(x - rect.left)}px`, my: `${Math.round(y - rect.top)}px` }
}

export function trackSheen(page: SheenPage): void {
  let lit: SheenSurface | null = null
  let queued: SheenPointer | null = null
  const rest = () => {
    if (!lit) return
    lit.style.removeProperty('--mx')
    lit.style.removeProperty('--my')
    lit = null
  }
  const frame = () => {
    const e = queued
    queued = null
    if (!e) return
    const surface = e.leftPage ? null : e.surface
    if (surface !== lit) rest()
    if (!surface || !page.lit()) return
    const at = placeOn(surface.getBoundingClientRect(), e.clientX, e.clientY)
    surface.style.setProperty('--mx', at.mx)
    surface.style.setProperty('--my', at.my)
    lit = surface
  }
  const queue = (e: SheenPointer) => {
    // a touch or a pen has no hover to follow
    if (e.pointerType !== 'mouse') return
    if (!queued) page.requestAnimationFrame(frame)
    queued = e
  }
  page.addEventListener('pointermove', queue, { passive: true })
  // leaving the window: pointerout with nothing under the pointer
  page.addEventListener('pointerout', (e) => (e.leftPage ? queue(e) : undefined), { passive: true })
}

/** The browser's page, for trackSheen */
export function browserSheenPage(doc: Document, win: Window): SheenPage {
  const surfaceOf = (target: EventTarget | null): SheenSurface | null => (target instanceof Element ? target.closest<HTMLElement>('.surf') : null)
  return {
    addEventListener: (type, listener, options) =>
      doc.addEventListener(
        type,
        (e) =>
          listener({
            pointerType: e.pointerType,
            clientX: e.clientX,
            clientY: e.clientY,
            surface: surfaceOf(e.target),
            leftPage: type === 'pointerout' && e.relatedTarget === null,
          }),
        options,
      ),
    requestAnimationFrame: (callback) => void win.requestAnimationFrame(callback),
    lit: () => win.getComputedStyle(doc.documentElement).getPropertyValue('--look-sheen').trim() !== 'none',
  }
}
