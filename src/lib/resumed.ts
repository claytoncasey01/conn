// The banner a launch shows after it resumed the hosted sessions that ran at the last quit: how many, which, and a Stop all.
import type { ResumedSession, Snapshot } from './types.ts'

/** The resumed sessions the snapshot lists, none when it lists none */
export function resumedOf(snap: Snapshot | null): ResumedSession[] {
  return snap?.resumed?.sessions ?? []
}

/** What the banner says: how many sessions were resumed and which */
export function resumedLine(sessions: ResumedSession[]): string {
  const count = sessions.length === 1 ? '1 session' : `${sessions.length} sessions`
  return `Resumed ${count} that ran when Conn quit: ${sessions.map(s => s.name).join(', ')}.`
}

/** A stop of one session, as stopSession answers */
export type Stop = (id: string, force: boolean) => Promise<{ ok: boolean; error?: string }>

/** Stops every resumed session, each whatever it is doing (the person asked for all of them), one after another, and is what
 * could not be stopped, one line each */
export async function stopAll(sessions: ResumedSession[], stop: Stop): Promise<string[]> {
  const failed: string[] = []
  for (const session of sessions) {
    const r = await stop(session.id, true)
    if (!r.ok) failed.push(`${session.name}: ${r.error ?? 'failed'}`)
  }
  return failed
}

/** Whether the banner shows: some session is listed that was not dismissed */
export function bannerShown(sessions: ResumedSession[], dismissed: ReadonlySet<string>): boolean {
  return sessions.some(s => !dismissed.has(s.id))
}
