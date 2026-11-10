<script lang="ts">
  // App icon: the Dock icon of Conn.app, shown only inside the app (appIcon.svelte.ts). Match theme follows the theme, its
  // variant and accent; Classic is the bundle's icon; each other choice pins one theme's look. Each is shown as the icon it
  // would make, drawn by the same code that draws the one sent. Finder and Launchpad keep the bundle's icon.
  import { appIcon, chooseIcon, specFor, svgUrl } from '../lib/appIcon.svelte.ts'
  import { iconChoiceText, pinnable, type IconChoice } from '../lib/theme/appIcon.ts'
  import { theme } from '../lib/theme/theme.svelte.ts'

  const options = $derived<{ choice: IconChoice; label: string }[]>([
    { choice: 'match', label: 'Match theme' },
    { choice: 'classic', label: 'Classic' },
    ...pinnable(theme.list).map((t) => ({ choice: { theme: t.id }, label: t.name })),
  ])
  const chosen = $derived(iconChoiceText(appIcon.choice))
</script>

{#if appIcon.available}
  <section class="surf card" aria-labelledby="app-icon-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
    <h2 id="app-icon-h" class="hud cap">App icon</h2>
    <p class="hint">Conn's icon in the Dock, while it runs and while it is closed. Finder and Launchpad keep the classic one.</p>
    <div class="icons" role="radiogroup" aria-labelledby="app-icon-h">
      {#each options as o (iconChoiceText(o.choice))}
        {@const value = iconChoiceText(o.choice)}
        <label class="icon" class:on={chosen === value}>
          <input type="radio" name="app-icon" {value} checked={chosen === value} onchange={() => chooseIcon(o.choice)} />
          <img src={svgUrl(specFor(o.choice))} alt="" width="56" height="56" />
          <span class="name">{o.label}</span>
        </label>
      {/each}
    </div>
    {#if appIcon.error}<p class="err" role="alert">{appIcon.error}</p>{/if}
  </section>
{/if}

<style>
  /* Tokens only, so the block reads in every theme */
  h2 {
    margin: 0 0 4px;
    font-size: 15px;
    font-weight: 600;
    color: var(--bright);
  }
  p {
    margin: 0;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 10px;
    box-sizing: border-box;
    min-width: 0;
    padding: 16px 18px;
  }
  .hint {
    max-width: 78ch;
    color: var(--mu);
    font-size: 12px;
    line-height: 1.55;
  }
  .err {
    color: var(--bad);
    font-size: 12px;
  }
  .icons {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
  }
  .icon {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    width: 92px;
    padding: 10px 6px 8px;
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    cursor: pointer;
  }
  .icon:hover {
    background: var(--hover);
  }
  .icon.on {
    border-color: var(--ac);
    background: var(--ac-soft);
  }
  .icon:has(input:focus-visible) {
    outline: 2px solid var(--ac);
    outline-offset: 2px;
  }
  /* The radio stays in the page for the keyboard and screen readers; the tile shows which is picked */
  input {
    position: absolute;
    opacity: 0;
    pointer-events: none;
  }
  .name {
    color: var(--tx);
    font-size: 11px;
    text-align: center;
    overflow-wrap: anywhere;
  }
</style>
