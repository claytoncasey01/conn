// The background the page's terminal is drawn on, told to the server (PUT /api/term/background), which answers a hosted
// program asking for it (OSC 11) with it, so Claude Code picks the palette that reads on the theme, light themes included.
// The server cannot know the theme: it is the person's choice, kept in this browser, and the system's light or dark. The
// last page to say wins; a program that already asked keeps its answer, so a change reaches the sessions started after it.
import { api } from './api.ts'
import { terminalTheme } from './terminalTheme.ts'
import type { TokenSet } from './theme/types.ts'

/** A sender: each call sends the background for `tokens` unless it is the one last sent. Never throws. */
export function backgroundSender(): (tokens: Readonly<TokenSet>) => Promise<void> {
  let sent: string | null = null
  return async (tokens) => {
    const background = terminalTheme(tokens).background
    if (!background || background === sent) return
    sent = background
    try {
      const r = await api('/api/term/background', {
        method: 'PUT',
        headers: { 'x-conn': '1', 'content-type': 'application/json' },
        body: JSON.stringify({ background }),
      })
      if (!r.ok) sent = null
    } catch {
      // the next theme change, or the next load, says it again
      sent = null
    }
  }
}
