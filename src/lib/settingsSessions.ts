// The "Where sessions open" card's pure parts. The server is the judge of the section (settings.json's `sessions`); the page reads
// what it sends, keeps a draft, and sends the whole section on save. No setting names a program: "My terminal" is cmux. No browser
// globals.

export type Where = 'inside' | 'terminal' | 'command'

/** The `sessions` section as `settings.json` holds it. */
export interface SessionsSettings {
  where: Where
  resumeOnLaunch: boolean
}

/** The three choices, each with the one line of help the card shows under it. */
export const WHERE_CHOICES: readonly { value: Where; label: string; help: string }[] = [
  { value: 'inside', label: 'Inside Conn', help: 'Each session runs in a terminal inside Conn, on the Sessions and Open work pages. No terminal app is needed.' },
  { value: 'terminal', label: 'My terminal', help: 'Each session opens in a new cmux workspace.' },
  { value: 'command', label: 'Show me the command', help: 'Nothing starts by itself: conn shows the command to paste into sh, bash, zsh or fish (not csh or tcsh).' },
]

const WHERES: readonly unknown[] = WHERE_CHOICES.map((c) => c.value)

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)

/** The section as the server sent it, each field of the right type or its default; any other key is not read. */
export function readSessions(v: unknown): SessionsSettings {
  const o = isObj(v) ? v : {}
  return {
    where: WHERES.includes(o.where) ? (o.where as Where) : 'inside',
    resumeOnLaunch: typeof o.resumeOnLaunch === 'boolean' ? o.resumeOnLaunch : true,
  }
}

/** The body a save sends: the whole section. */
export const sessionsSection = (s: SessionsSettings): SessionsSettings => ({ where: s.where, resumeOnLaunch: s.resumeOnLaunch })

export const sameSessions = (a: SessionsSettings, b: SessionsSettings): boolean => a.where === b.where && a.resumeOnLaunch === b.resumeOnLaunch

/** The line a save leaves: where sessions now open, and whether they resume. */
export function wroteSessions(s: SessionsSettings): string {
  const resume = s.resumeOnLaunch ? 'resume' : 'do not resume'
  if (s.where === 'command') return `Saved: conn shows the command to paste, and sessions ${resume} when conn starts`
  return `Saved: sessions open ${s.where === 'inside' ? 'inside Conn' : 'in cmux'}, and ${resume} when conn starts`
}
