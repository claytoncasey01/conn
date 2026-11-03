<script lang="ts">
  /** Stands where a live page shows its link status: this page is a History entry, frozen at endedAt. */
  let { endedAt }: { endedAt: number } = $props()

  const MONTHS = ['JAN', 'FEB', 'MAR', 'APR', 'MAY', 'JUN', 'JUL', 'AUG', 'SEP', 'OCT', 'NOV', 'DEC']
  const pad = (x: number) => String(x).padStart(2, '0')
  const when = $derived.by(() => {
    if (!endedAt) return ''
    const d = new Date(endedAt)
    return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  })
</script>

<div style="display: flex; align-items: center; gap: 10px; color: color-mix(in oklab, var(--role-advisor) 35%, var(--tx)); font-size: 12px; letter-spacing: calc(var(--tracking) * 0.4286)">
  <span class="led" style="width: 8px; height: 8px; background: var(--role-advisor); box-shadow: 0 0 10px color-mix(in oklch, var(--role-advisor) calc(100% * var(--glow)), transparent)"></span>ARCHIVED{when ? ' · ENDED ' + when : ''}
</div>
