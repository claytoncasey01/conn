<script lang="ts">
  // Alerts: whether macOS (in Conn.app) or the browser lets this page show notifications, and the one thing to do about it. Neither
  // asks twice: after a no, the app opens System Settings and a browser points to its site settings. Both are read again whenever
  // the window comes back to the front (notify.svelte.ts), so a change made there shows here without a reload.
  import { alertRow } from '../lib/alertSettings.ts'
  import { alerts, openNotificationSettings, toggleAlerts } from '../lib/notify.svelte.ts'

  const row = $derived(alertRow(alerts.where, alerts.supported, alerts.permission, alerts.enabled))
  let busy = $state(false)

  async function press(act: () => Promise<void>) {
    busy = true
    await act()
    busy = false
  }
</script>

<section class="surf card" aria-labelledby="alerts-h" style="--rim: color-mix(in oklch, var(--edge) 18%, transparent); --r: calc(var(--radius) * 1.5); --wash: linear-gradient(170deg, color-mix(in oklch, var(--edge) 6%, transparent), color-mix(in oklch, var(--panel) 92%, transparent) 55%)">
  <h2 id="alerts-h" class="hud cap">Alerts</h2>
  <div class="row">
    <span class="tag" class:on={row.tone === 'on'} class:bad={row.tone === 'bad'}>{row.state}</span>
    <p class="hint what">{row.text}</p>
    {#if row.action === 'ask' || row.action === 'on'}
      <button type="button" class="btn primary small cap" onclick={() => press(toggleAlerts)} disabled={busy} aria-busy={busy}>Turn on alerts</button>
    {:else if row.action === 'off'}
      <button type="button" class="btn small cap" onclick={() => press(toggleAlerts)} disabled={busy} aria-busy={busy}>Turn off</button>
    {:else if row.action === 'settings'}
      <button type="button" class="btn small cap" onclick={() => press(openNotificationSettings)} disabled={busy} aria-busy={busy}>Open System Settings</button>
    {/if}
  </div>
</section>

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
    overflow-wrap: anywhere;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
    padding: 10px 12px;
    background: color-mix(in oklch, var(--sunk) 55%, transparent);
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
  }
  .what {
    flex: 1;
    min-width: 0;
  }
  .tag {
    flex: none;
    box-sizing: border-box;
    min-width: 78px;
    padding: 3px 8px;
    color: var(--mu);
    border: 1px var(--border-style) var(--line2);
    border-radius: 999px;
    font-size: 11px;
    text-align: center;
  }
  .tag.on {
    color: color-mix(in oklab, var(--ok) 60%, var(--bright));
    border-color: var(--ok);
  }
  .tag.bad {
    color: color-mix(in oklab, var(--bad) 55%, var(--bright));
    border-color: var(--bad);
  }
  .btn {
    flex: none;
    min-height: 36px;
    padding: 0 12px;
    color: var(--tx);
    background: transparent;
    border: 1px var(--border-style) var(--line2);
    border-radius: var(--radius);
    font-size: 12px;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) {
    background: var(--hover);
  }
  .btn.primary {
    color: var(--onac);
    background: var(--ac);
    border-color: var(--ac);
    font-weight: 600;
  }
  .btn.primary:hover:not(:disabled) {
    background: var(--ac);
    filter: brightness(1.1);
  }
  .btn:disabled {
    cursor: default;
    opacity: 0.5;
  }
  .btn[aria-busy='true'] {
    cursor: wait;
  }

  @media (max-width: 600px) {
    .row {
      flex-wrap: wrap;
    }
  }
</style>
