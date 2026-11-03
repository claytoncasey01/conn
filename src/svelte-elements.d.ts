// svelte/elements has no `csp` for an iframe; the gallery's previews set it (Chrome enforces it on the frame's own document)
import 'svelte/elements'

declare module 'svelte/elements' {
  interface HTMLIframeAttributes {
    csp?: string | undefined | null
  }
}
