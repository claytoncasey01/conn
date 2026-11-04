<!-- The brand at the left of every page header: the tile (the theme's `glyph-brand`, the Conn mark by default) and the name. -->
<script lang="ts">
  import { BRAND_MARK } from './theme/brandMark.ts'
</script>

<div style="display: flex; align-items: center; gap: 12px">
  <div style="width: 38px; height: 38px; border-radius: calc(var(--radius) * 2); background: linear-gradient(135deg, var(--role-lead), var(--bad)); color: var(--onac); display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 13px; box-shadow: 0 0 22px color-mix(in oklch, var(--role-lead) calc(55% * var(--glow)), transparent)" aria-hidden="true"><span class="g-brand" aria-hidden="true"></span>{@html BRAND_MARK}</div>
  <div class="hud brand" style="font-weight: 700; font-size: 20px; letter-spacing: var(--tracking); text-transform: uppercase; background: linear-gradient(90deg, var(--bright), color-mix(in oklab, var(--edge) 75%, var(--bright))); -webkit-background-clip: text; background-clip: text; color: transparent">Conn</div>
</div>
