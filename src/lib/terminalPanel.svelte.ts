// Which hosted session's terminal panel is open: at most one at a time, on whichever page shows that session. OPEN
// TERMINAL on a question the page cannot answer opens it, as does the TERMINAL button on a hosted session.

export const terminalPanel = $state({ open: null as string | null })

/** Opens the panel of the hosted session `id`, closing any other. */
export function showTerminal(id: string): void {
  terminalPanel.open = id
}

/** Opens the panel of `id`, or closes it when it is the one open. */
export function toggleTerminal(id: string): void {
  terminalPanel.open = terminalPanel.open === id ? null : id
}

/** Closes the panel; the session keeps running. */
export function hideTerminal(): void {
  terminalPanel.open = null
}
