<script lang="ts">
  
  interface Props {
    /**
   * Generic alert banner. Takes a list of alerts and renders one
   * coloured banner per alert. Designed to handle multiple emergency
   * types (auth errors today, future: prolonged stuck-D worker piles,
   * provider outages, ...). Each alert has a class that maps to a
   * colour palette and an icon, plus a title and optional body.
   *
   * Born from the 2026-05-05 incident: an Azure SP credential expired
   * silently and the operator lost hours figuring out why recruitment
   * was failing — the signal needs to be one glance away on every
   * page that loads the dashboard.
   */
    alerts?: Array<{
    class: 'auth' | 'capacity' | 'quota' | 'warning' | 'info';
    title: string;
    body?: string;
  }>;
  }

  let { alerts = [] }: Props = $props();
</script>

{#if alerts.length > 0}
  <div class="alert-banner-stack">
    {#each alerts as alert}
      <div
        class="alert-banner alert-banner-{alert.class}"
        role="alert"
        data-testid={`alert-banner-${alert.class}`}
      >
        <div class="alert-banner-title">{alert.title}</div>
        {#if alert.body}
          <div class="alert-banner-body">{alert.body}</div>
        {/if}
      </div>
    {/each}
  </div>
{/if}

<style>
  .alert-banner-stack {
    display: flex;
    flex-direction: column;
    gap: 0.5em;
    margin: 0.5em 1em;
  }
  .alert-banner {
    padding: 0.75em 1em;
    border-radius: 6px;
    border-left: 4px solid;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
  }
  .alert-banner-title {
    font-weight: 600;
    font-size: 1rem;
    line-height: 1.3;
  }
  .alert-banner-body {
    margin-top: 0.25em;
    font-size: 0.9rem;
    line-height: 1.4;
  }

  .alert-banner-auth {
    background: #fef2f2;
    color: #7f1d1d;
    border-left-color: #dc2626;
  }
  .alert-banner-capacity,
  .alert-banner-quota,
  .alert-banner-warning {
    background: #fffbeb;
    color: #78350f;
    border-left-color: #f59e0b;
  }
  .alert-banner-info {
    background: #ecfeff;
    color: #155e75;
    border-left-color: #0891b2;
  }

  /* Dark-theme variants — softer backgrounds to avoid blowing out
     the contrast against the dashboard's dark surface. */
  :global([data-theme="dark"]) .alert-banner-auth {
    background: #2a1010;
    color: #fecaca;
  }
  :global([data-theme="dark"]) .alert-banner-capacity,
  :global([data-theme="dark"]) .alert-banner-quota,
  :global([data-theme="dark"]) .alert-banner-warning {
    background: #2a1f0a;
    color: #fde68a;
  }
  :global([data-theme="dark"]) .alert-banner-info {
    background: #0a2227;
    color: #a5f3fc;
  }
</style>
