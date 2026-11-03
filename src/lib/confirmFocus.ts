// Where keyboard focus goes when ConfirmAction swaps its buttons. Pure, so the choices are tested without a DOM.
export type Tone = 'red' | 'green' | 'cyan'

/**
 * The control the prompt hands focus to. The confirm button, except for a destructive (red) action: Enter on the button that
 * asked could repeat onto the confirm button that replaced it and run the action unseen, so there Cancel takes focus,
 * as it does in the Theme menu's own prompt.
 */
export const firstFocus = (tone: Tone): 'confirm' | 'cancel' => (tone === 'red' ? 'cancel' : 'confirm')

/**
 * Focus has nowhere to be: nothing holds it, the page body does (a focused button was removed), or it is still on a control of
 * this component that is gone. Focus anywhere else was put there by the person or the page and is left alone.
 */
export const adrift = (active: Element | null, body: Element | null, root: { contains(n: Node): boolean } | null | undefined): boolean =>
  !active || active === body || (!!root && root.contains(active))
