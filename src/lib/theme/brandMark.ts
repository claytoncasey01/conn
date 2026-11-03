// The Conn mark: a squared C of rounded bars with a pill in its opening. One drawing for the header tile (Brand.svelte), the gallery
// preview and public/favicon.svg (a test holds the three together). It paints in currentColor, so the tile's `--onac` colors it; the
// `glyph-brand: conn` token shows it (`--look-brand-mark`), and the other glyphs hide it.

/** The shapes on a 32 grid, with no wrapper: the favicon draws them inside its own tile */
export const BRAND_MARK_SHAPES =
  '<path d="M25.5 7.5H13A5.5 5.5 0 0 0 7.5 13V19A5.5 5.5 0 0 0 13 24.5H25.5" fill="none" stroke="currentColor" stroke-width="3.6" stroke-linecap="round" stroke-linejoin="round"/>' +
  '<rect x="22.5" y="13.6" width="6.4" height="4.8" rx="2.4" fill="currentColor"/>'

/** The mark as an inline SVG; `.g-brand-mark` (lookCss) sizes it and decides whether it shows */
export const BRAND_MARK = `<svg class="g-brand-mark" viewBox="0 0 32 32" aria-hidden="true" focusable="false">${BRAND_MARK_SHAPES}</svg>`
