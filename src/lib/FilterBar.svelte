<script lang="ts">
  import { tick } from 'svelte'
  import { CHIP_BACKING, CHIP_DIM } from './chipStyle.ts'
  import { isActive, visibleChips, type BarGroup, type Selection } from './filters.ts'

  let { groups, selection, ontoggle, onclear, label = 'Filters', active = false }: {
    groups: BarGroup[]
    selection: Selection
    ontoggle: (groupId: string, chipId: string) => void
    onclear: () => void
    /** Accessible name of the toolbar */
    label?: string
    /** Another filter outside the chips is set: CLEAR shows even with no chip selected */
    active?: boolean
  } = $props()

  let root: HTMLDivElement | undefined = $state()
  // Groups with a cap that SHOW ALL has opened
  let expanded = $state<Record<string, boolean>>({})

  /** Move focus to the first chip, e.g. after CLEAR removes the button that had it. */
  export function focusFirst() {
    root?.querySelector<HTMLButtonElement>('button[aria-pressed]')?.focus()
  }

  async function clear() {
    onclear()
    await tick()
    focusFirst()
  }

  // A chip that would show nothing is dimmed through its colours, not opacity, so the focus outline stays fully visible.
  // Under every chip while an effect is mounted behind the page (the style rule below), so the rain never prints through its text
  const BACKING = `color-mix(in oklch, var(--bg) ${CHIP_BACKING * 100}%, transparent)`
  const border = (on: boolean, dim: boolean) => (on ? `color-mix(in oklch, var(--role-lead) calc(${dim ? 0.22 : 0.5} * 100%), transparent)` : `color-mix(in oklch, var(--edge) calc(${dim ? 0.08 : 0.18} * 100%), transparent)`)
  const textColor = (on: boolean, dim: boolean) => (on ? `color-mix(in oklch, color-mix(in oklab, var(--role-lead) 30%, var(--bright)) calc(${dim ? CHIP_DIM : 1} * 100%), transparent)` : `color-mix(in oklch, var(--mu) calc(${dim ? CHIP_DIM : 1} * 100%), transparent)`)
</script>

<div bind:this={root} class="chipbar" role="toolbar" aria-label={label} style="--chip-back: {BACKING}; display: flex; flex-wrap: wrap; align-items: center; gap: 12px 20px">
  {#each groups as g (g.id)}
    {@const v = visibleChips(g, selection[g.id] ?? [], !!expanded[g.id])}
    <div role="group" aria-label={g.label} style="display: flex; flex-wrap: wrap; align-items: center; gap: 8px; max-width: 100%; min-width: 0">
      <span class="hud cap" aria-hidden="true" style="font-size: 10px; letter-spacing: calc(var(--tracking) * 1.4286); color: color-mix(in oklab, var(--mu) 85%, var(--panel))">{g.label}</span>
      {#each v.chips as c (c.id)}
        {@const on = (selection[g.id] ?? []).includes(c.id)}
        {@const dim = c.count === 0}
        <button
          class="tab cap"
          onclick={() => ontoggle(g.id, c.id)}
          aria-pressed={on}
          aria-label={`${c.label}${c.count === undefined ? '' : ` ${c.count}`}`}
          title={c.label.length > 24 ? c.label : undefined}
          style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) {border(on, dim)}; background-color: var(--chip-fill, transparent); background-image: {on ? 'linear-gradient(color-mix(in oklch, var(--role-lead) 10%, transparent), color-mix(in oklch, var(--role-lead) 10%, transparent))' : 'none'}; color: {textColor(on, dim)}; font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap"
        >{c.label}{c.count === undefined ? '' : ` ${c.count}`}</button>
      {/each}
      {#if v.hidden > 0 || (g.cap && expanded[g.id] && g.chips.length > g.cap)}
        <button
          class="tab cap"
          onclick={() => (expanded[g.id] = !expanded[g.id])}
          aria-expanded={!!expanded[g.id]}
          aria-label={expanded[g.id] ? `Show fewer ${g.label} chips` : `Show all ${g.chips.length} ${g.label} chips`}
          style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px dashed color-mix(in oklch, var(--edge) 30%, transparent); background-color: var(--chip-fill, transparent); color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
        >{expanded[g.id] ? 'Show less' : `Show all ${g.chips.length}`}</button>
      {/if}
    </div>
  {/each}
  {#if isActive(selection) || active}
    <button
      class="tab cap"
      onclick={clear}
      aria-label="Clear filters"
      style="padding: 8px 16px; min-height: 44px; border-radius: var(--radius); border: 1px var(--border-style) color-mix(in oklch, var(--edge) 18%, transparent); background-color: var(--chip-fill, transparent); color: var(--mu); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.8571); cursor: pointer"
    >Clear ✕</button>
  {/if}
</div>

<style>
  /* #conn-effects (effects/browser.ts LAYER_ID) is the layer behind the page and holds a canvas only while an effect is mounted in it
     (Matrix rain): only then do the chips get a fill; a theme without one keeps them transparent, as they were */
  :global(html:has(#conn-effects canvas)) .chipbar {
    --chip-fill: var(--chip-back);
  }
</style>
