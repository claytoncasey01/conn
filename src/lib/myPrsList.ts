// What the My PRs page shows where the list goes, from the server's view alone.
import type { MyPrsView } from './types.ts'

/**
 * `setup` when no repository is allowed and `github-off` when the GitHub module is off (nothing is read, so there is nothing to wait for),
 * else `loading` until the first poll has answered, `error` when GitHub has never been read, `empty` for a list that is really empty, else `rows`.
 */
export function listState(view: Pick<MyPrsView, 'loaded' | 'error' | 'rows' | 'off'>): 'setup' | 'github-off' | 'loading' | 'error' | 'empty' | 'rows' {
  if (view.off === 'setup') return 'setup'
  if (view.off === 'github') return 'github-off'
  if (view.rows.length > 0) return 'rows'
  if (view.error) return 'error'
  return view.loaded ? 'empty' : 'loading'
}

/**
 * The error banner's text after "GitHub:": the error, and, when there are rows, that they are from the last good read. Judged on the
 * whole list the server sent, never the filtered one, so a repo filter that hides every row cannot hide that the list is old.
 */
export function errorText(view: Pick<MyPrsView, 'error' | 'rows'>): string | null {
  if (!view.error) return null
  return `${view.error}${view.rows.length ? ' The list below is from the last time it could be read.' : ''}`
}
