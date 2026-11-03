// How a Jira issue type is coloured on a chip. Pure, so it runs under bun test.

/** The theme token of each type, by lower-cased name. Anything else (a custom type) keeps the neutral chip. */
const TYPE_TOKENS: Record<string, string> = {
  bug: 'k-bug',
  epic: 'k-epic',
  task: 'k-task',
  story: 'k-story',
  'sub-task': 'k-subtask',
  subtask: 'k-subtask',
}

/** The border, background and text colours of a type's chip, as a style fragment; the tints are the token over transparent. */
export function typeChipStyle(type: string): string {
  const t = TYPE_TOKENS[String(type ?? '').trim().toLowerCase()]
  return t
    ? `border: 1px var(--border-style) color-mix(in oklch, var(--${t}) 50%, transparent); background: color-mix(in oklch, var(--${t}) 12%, transparent); color: var(--${t})`
    : 'border: 1px var(--border-style) color-mix(in oklch, var(--edge) 22%, transparent); color: var(--mu)'
}
