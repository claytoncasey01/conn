// A hosted session whose program has exited by itself (an Interrupt that made it quit, or an exit typed in its terminal)
// stays a row while the host keeps it, for its exit code and scrollback. It must not look alive: it says it ended, and
// its stop button removes it from the list (POST /stop on an ended hosted session lets the host forget it).

/** A hosted row whose program has ended. */
export const endedHosted = (row: { hosted?: boolean; exitCode?: number }): boolean => row.hosted === true && row.exitCode !== undefined

/** What an ended row says in place of its status. */
export const endedLabel = (exitCode: number): string => `Ended · ${exitCode}`

/** What the stop button of an ended row asks before it removes the row. */
export const removeAsk = (name: string): string => `Remove ${name} from the list?`
