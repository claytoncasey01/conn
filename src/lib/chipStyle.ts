// How the filter bar draws a chip that would show nothing, and what every chip rests on. Numbers only, so a test can hold them
// to a contrast floor in every look (themes/chips.test.ts).

/** Text of a count-0 chip is its colour at this share: quieter than a live chip, still 3:1 on the page in every theme */
export const CHIP_DIM = 0.8

/** The page's bg laid under every chip at this share: the Matrix rain (behind the page) cannot print through the text */
export const CHIP_BACKING = 0.9
