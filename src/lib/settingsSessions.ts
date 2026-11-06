// The "Where sessions open" card's pure parts. The server is the judge of the section (settings.json's `sessions`); the page reads
// what it sends, keeps a draft, and sends the whole section on save. No browser globals.

export type Where = 'inside' | 'terminal' | 'command'

/** The `sessions` section as `settings.json` holds it. `terminalCommand` empty is cmux. */
export interface SessionsSettings {
  where: Where
  terminalCommand: string[]
  resumeOnLaunch: boolean
}

/** The three choices, each with the one line of help the card shows under it. */
export const WHERE_CHOICES: readonly { value: Where; label: string; help: string }[] = [
  { value: 'inside', label: 'Inside Conn', help: 'Each session runs in a terminal inside conn, on the Sessions and Open work pages. No terminal app is needed.' },
  { value: 'terminal', label: 'My terminal', help: 'Each session opens in a new cmux workspace, or with your own terminal command below.' },
  { value: 'command', label: 'Show me the command', help: 'Nothing starts by itself: conn shows the command to paste into a terminal.' },
]

const WHERES: readonly unknown[] = WHERE_CHOICES.map((c) => c.value)

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)

/** The section as the server sent it, each field of the right type or its default. */
export function readSessions(v: unknown): SessionsSettings {
  const o = isObj(v) ? v : {}
  return {
    where: WHERES.includes(o.where) ? (o.where as Where) : 'inside',
    terminalCommand: Array.isArray(o.terminalCommand) ? o.terminalCommand.filter((x): x is string => typeof x === 'string') : [],
    resumeOnLaunch: typeof o.resumeOnLaunch === 'boolean' ? o.resumeOnLaunch : true,
  }
}

/** The terminal command as typed, one argument a line: blank lines are dropped and nothing else is changed (the server judges it). */
export const commandLines = (text: string): string[] => text.split(/\r?\n/).filter((line) => line.trim() !== '')

/** The terminal command as the box shows it. */
export const commandText = (list: readonly string[]): string => list.join('\n')

/** The body a save sends: the whole section. */
export const sessionsSection = (s: SessionsSettings): SessionsSettings => ({ where: s.where, terminalCommand: [...s.terminalCommand], resumeOnLaunch: s.resumeOnLaunch })

export const sameSessions = (a: SessionsSettings, b: SessionsSettings): boolean =>
  a.where === b.where && a.resumeOnLaunch === b.resumeOnLaunch && a.terminalCommand.length === b.terminalCommand.length && a.terminalCommand.every((x, i) => x === b.terminalCommand[i])

/** The line a save leaves: where sessions now open, and whether they resume. */
export function wroteSessions(s: SessionsSettings): string {
  if (s.where === 'command') return `Saved: conn shows the command to paste, and sessions ${s.resumeOnLaunch ? 'resume' : 'do not resume'} when conn starts`
  const place = s.where === 'inside' ? 'inside Conn' : s.terminalCommand.length ? 'with your terminal command' : 'in cmux'
  return `Saved: sessions open ${place}, and ${s.resumeOnLaunch ? 'resume' : 'do not resume'} when conn starts`
}
