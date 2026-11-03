// A Google Fonts stylesheet URL for the allow-listed names only: a theme's `fonts` list is data from a
// file, so nothing outside the list ever reaches the URL. Weights are per font, since asking for one
// a family lacks makes Google refuse the whole request.
import { ALLOWED_FONTS } from './tokens.ts'

const WEIGHTS: Record<string, string> = {
  'Chakra Petch': '400;500;600;700',
  'JetBrains Mono': '400;500;700',
  'IBM Plex Sans': '400;500;600;700',
  'IBM Plex Mono': '400;500;600;700',
  'Share Tech Mono': '400',
}

export function fontsHref(names: string[]): string | null {
  const use = [...new Set(names)].filter((n) => ALLOWED_FONTS.includes(n))
  if (use.length === 0) return null
  const families = use.map((n) => `family=${n.replaceAll(' ', '+')}:wght@${WEIGHTS[n] ?? '400'}`)
  return `https://fonts.googleapis.com/css2?${families.join('&')}&display=swap`
}
