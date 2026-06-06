<script lang="ts">
  import { onMount } from 'svelte';
  import { X } from 'lucide-svelte';
  import { getTemplateRun } from '../lib/api';

  interface Props {
    templateRunId?: number | null;
    onClose: () => void;
  }

  let { templateRunId = null, onClose }: Props = $props();

  let run: taskqueue.TemplateRun | null = $state(null);
  let loading = $state(true);
  let error = $state('');

  onMount(async () => {
    if (templateRunId == null) {
      error = 'No template run id provided';
      loading = false;
      return;
    }
    try {
      run = await getTemplateRun(templateRunId);
      if (!run) error = 'Template run not found';
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  });

  // Pretty-print param_values_json into a sorted key/value table.
  let params = $derived((() => {
    if (!run?.paramValuesJson) return [];
    try {
      const obj = JSON.parse(run.paramValuesJson);
      return Object.keys(obj).sort().map(k => ({ key: k, val: String(obj[k]) }));
    } catch {
      return [{ key: '(raw)', val: run.paramValuesJson }];
    }
  })());

  function onBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget) onClose();
  }
</script>

<div class="modal-backdrop" onclick={onBackdropClick} onkeydown={(e) => e.key === 'Escape' && onClose()} role="dialog" aria-modal="true" tabindex="-1">
  <div class="modal-card">
    <div class="modal-header">
      <h2>Template run details</h2>
      <button class="modal-close" onclick={onClose} aria-label="Close"><X size="18" /></button>
    </div>
    <div class="modal-body">
      {#if loading}
        <p>Loading…</p>
      {:else if error}
        <p class="modal-error">{error}</p>
      {:else if run}
        <dl class="modal-meta">
          {#if run.templateName}
            <dt>Template</dt>
            <dd>{run.templateName}{run.templateVersion ? '@' + run.templateVersion : ''}</dd>
          {/if}
          {#if run.scriptName}
            <dt>Script</dt>
            <dd>{run.scriptName}{run.scriptSha256 ? ' (' + run.scriptSha256.slice(0, 8) + ')' : ''}</dd>
          {/if}
          <dt>Run id</dt>
          <dd>#{run.templateRunId}</dd>
          {#if run.runByUsername}
            <dt>Run by</dt>
            <dd>{run.runByUsername}</dd>
          {/if}
          {#if run.createdAt}
            <dt>Created</dt>
            <dd>{run.createdAt}</dd>
          {/if}
          {#if run.status}
            <dt>Status</dt>
            <dd>{run.status}</dd>
          {/if}
        </dl>
        {#if params.length > 0}
          <h3>Parameters</h3>
          <table class="modal-params">
            <tbody>
              {#each params as p (p.key)}
                <tr><th>{p.key}</th><td>{p.val}</td></tr>
              {/each}
            </tbody>
          </table>
        {/if}
        {#if run.errorMessage}
          <h3>Error</h3>
          <pre class="modal-error">{run.errorMessage}</pre>
        {/if}
      {/if}
    </div>
  </div>
</div>

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .modal-card {
    background: var(--bg-primary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    width: min(640px, 92vw);
    max-height: 85vh;
    overflow: auto;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.25);
  }
  .modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border-color);
  }
  .modal-header h2 {
    margin: 0;
    font-size: 1rem;
  }
  .modal-close {
    background: transparent;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    padding: 4px;
    display: inline-flex;
    align-items: center;
  }
  .modal-body {
    padding: 0.75rem 1rem 1rem;
  }
  .modal-meta {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.25rem 1rem;
    margin: 0 0 0.75rem 0;
  }
  .modal-meta dt {
    font-weight: 600;
    color: var(--text-secondary, var(--text-primary));
  }
  .modal-meta dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .modal-body h3 {
    font-size: 0.95rem;
    margin: 0.75rem 0 0.4rem;
  }
  .modal-params {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.88rem;
  }
  .modal-params th {
    text-align: left;
    padding: 0.25rem 0.5rem;
    background: var(--bg-secondary);
    width: 30%;
    font-weight: 600;
  }
  .modal-params td {
    padding: 0.25rem 0.5rem;
    overflow-wrap: anywhere;
    border-bottom: 1px solid var(--border-color);
  }
  .modal-error {
    color: #c93b3b;
    white-space: pre-wrap;
  }
</style>
