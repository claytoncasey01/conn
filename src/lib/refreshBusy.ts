// Whether the Reviews page's REFRESH button must stay disabled. The server counts presses (`seq`) and the ones whose poll and scan have
// finished (`done`), so the answer does not depend on catching a snapshot taken while the work was running.

/**
 * True while any press (this page's or another tab's) is still going (`seq > done`), or while the press this page is waiting for
 * (`waitingFor`, the number the POST answered) has not finished yet. A snapshot older than that press (`seq < waitingFor`) is
 * either the frame before the one that shows it (the POST answer usually beats the next frame by up to 600 ms: `staleOk`, a short
 * grace the page gives) or a server that has restarted and begun counting again, which is not waited for.
 */
export function refreshBusy(r: { seq: number; done: number } | undefined, waitingFor: number | null, staleOk = false): boolean {
  if (!r) return false
  if (r.seq > r.done) return true
  if (waitingFor === null) return false
  if (waitingFor > r.seq) return staleOk
  return r.done < waitingFor
}
