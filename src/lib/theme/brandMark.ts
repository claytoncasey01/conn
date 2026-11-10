// The Conn mark: a squared C of rounded bars with a pill in its opening. One drawing for the header tile (Brand.svelte), the gallery
// preview and public/favicon.svg (a test holds the three together), with two more of the same C and pill a theme can draw it as: a
// neon tube, and pixels on a 4-unit grid. Each is a group of the one SVG, and the `glyph-brand` token shows one (`--look-brand-*`
// in css.ts). Everything paints in currentColor, so the tile's ink colors it; the text glyphs hide the SVG.

/** The shapes on a 32 grid, with no wrapper: the favicon draws them inside its own tile */
export const BRAND_MARK_SHAPES =
  '<path d="M25.5 7.5H13A5.5 5.5 0 0 0 7.5 13V19A5.5 5.5 0 0 0 13 24.5H25.5" fill="none" stroke="currentColor" stroke-width="3.6" stroke-linecap="round" stroke-linejoin="round"/>' +
  '<rect x="22.5" y="13.6" width="6.4" height="4.8" rx="2.4" fill="currentColor"/>'

const C_PATH = 'M25.5 7.5H13A5.5 5.5 0 0 0 7.5 13V19A5.5 5.5 0 0 0 13 24.5H25.5'

/** The mark as a bent glass tube: the C as a thinner line with a hot core (`.bm-core`, lighter in lookCss), the pill an outline */
export const BRAND_TUBE_SHAPES =
  `<path d="${C_PATH}" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>` +
  '<rect x="22.5" y="13.8" width="6.4" height="4.4" rx="2.2" fill="none" stroke="currentColor" stroke-width="2"/>' +
  `<path class="bm-core" d="${C_PATH}" fill="none" stroke="currentColor" stroke-width=".9" stroke-linecap="round" stroke-linejoin="round"/>`

/** The mark in pixels: the C's bars one 4-unit cell thick with its corners left out, and the pill two cells wide in its opening */
export const BRAND_PIXEL_SHAPES = '<path d="M10 6h16v4H10zM6 10h4v12H6zM10 22h16v4H10zM24 14h6v4h-6z" fill="currentColor"/>'

/** The mark as an inline SVG; `.g-brand-mark` (lookCss) sizes it and decides whether it shows, and `.bm-*` which drawing */
export const BRAND_MARK =
  '<svg class="g-brand-mark" viewBox="0 0 32 32" aria-hidden="true" focusable="false">' +
  `<g class="bm-solid">${BRAND_MARK_SHAPES}</g><g class="bm-tube">${BRAND_TUBE_SHAPES}</g><g class="bm-pixel">${BRAND_PIXEL_SHAPES}</g></svg>`
