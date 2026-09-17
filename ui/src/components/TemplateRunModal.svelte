<script lang="ts">
  import { onMount } from 'svelte';
  import { X, Copy, Terminal, Rocket, ChevronDown, ChevronRight } from 'lucide-svelte';
  import { getTemplateRun } from '../lib/api';

  interface Props {
    templateRunId?: number | null;
    onClose: () => void;
  }

  let { templateRunId = null, onClose }: Props = $props();

  let run: taskqueue.TemplateRun | null = $state(null);
  let loading = $state(true);
  let error = $state('');

  // Show-command-line panel is collapsed by default. When open, the
  // whole shell invocation is rendered as a <pre> block with a
  // one-click copy button.
  let showCmdPanel = $state(false);
  let copied = $state(false);

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

  /**
   * Single-quote a shell value so it survives copy-paste into bash /
   * sh / zsh regardless of what's inside. The `'\''` sequence closes
   * the current quoted string, inserts a literal quote, and reopens.
   * Single quotes preserve newlines and every other special character
   * except the single quote itself, so this one rule covers URIs,
   * comma-heavy values, embedded spaces, and multi-line text alike.
   */
  function shquote(v: string): string {
    return "'" + v.replace(/'/g, "'\\''") + "'";
  }

  /**
   * Render the full `scitq template run …` invocation for this run.
   * One --param per line so a value that itself contains commas (like
   * the depth field `1x1,2,3,...`) doesn't confuse the CLI's
   * comma-separated ParamPairs parser. Multi-line and unusual values
   * are safe by single-quoting alone; text-typed params are noted so
   * the operator knows a --values-file might be preferable.
   */
  let commandLine = $derived((() => {
    if (!run?.templateName) return '';
    const lines: string[] = [];
    lines.push('scitq template run \\');
    lines.push(`  --name ${shquote(run.templateName)}${run.templateVersion ? ` --ver ${shquote(run.templateVersion)}` : ''} \\`);
    const notes: string[] = [];
    for (const p of params) {
      if (p.val.includes('\n')) {
        notes.push(`# ${p.key}: value contains newlines — the single-quoted form below is bash-safe, but for very long text prefer 'scitq template run … --values-file /path/to.json'.`);
      }
      lines.push(`  --param ${p.key}=${shquote(p.val)} \\`);
    }
    // Strip trailing backslash on the last line so the copied block
    // is directly executable (an orphan trailing backslash would make
    // the shell wait for another line).
    if (lines.length > 0) {
      const last = lines.pop()!;
      lines.push(last.replace(/ \\$/, ''));
    }
    return (notes.length ? notes.join('\n') + '\n' : '') + lines.join('\n');
  })());

  async function copyCommand() {
    try {
      await navigator.clipboard.writeText(commandLine);
      copied = true;
      setTimeout(() => { copied = false; }, 1500);
    } catch (e) {
      // Fallback for insecure contexts (some intranets on http://).
      // A textarea + execCommand still works.
      const ta = document.createElement('textarea');
      ta.value = commandLine;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
      copied = true;
      setTimeout(() => { copied = false; }, 1500);
    }
  }

  /**
   * Navigate to the template launcher pre-populated with this run's
   * param values. The destination page (WfTemplatePage) reads
   * ?from_run=<id> off the URL and fetches the run fresh — keeps the
   * URL short/shareable and always uses the current server-side
   * paramValuesJson (which is immutable, but fetching is a
   * single-source-of-truth policy). Modal closes so the target page
   * takes focus.
   */
  function launchFromThis() {
    if (!run?.templateRunId) return;
    onClose();
    window.location.hash = `#/workflowsTemplate?from_run=${run.templateRunId}`;
  }

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

        <!-- Re-run helpers. Both are read-only convenience actions —
             the button labels match the exact user-story the operator
             is likely thinking about ("I want a shell command I can
             adapt", "I want to open the launcher with these values
             already filled in and tweak one field").
             Only shown for template-launched runs (script runs have
             no reusable template identity). -->
        {#if run.templateName && (run.workflowTemplateId ?? 0) > 0}
          <div class="modal-actions">
            <button
              class="action-btn"
              onclick={() => showCmdPanel = !showCmdPanel}
              aria-expanded={showCmdPanel}
              title="Reveal a scitq CLI invocation reproducing this run"
            >
              {#if showCmdPanel}<ChevronDown size="14" />{:else}<ChevronRight size="14" />{/if}
              <Terminal size="14" />
              Show command line
            </button>
            <button
              class="action-btn primary"
              onclick={launchFromThis}
              title="Open the template launcher pre-filled with these parameter values"
            >
              <Rocket size="14" />
              Launch new workflow from this
            </button>
          </div>
          {#if showCmdPanel}
            <div class="cmd-panel">
              <div class="cmd-toolbar">
                <button class="copy-btn" onclick={copyCommand} title="Copy to clipboard">
                  <Copy size="12" />
                  {copied ? 'Copied' : 'Copy'}
                </button>
              </div>
              <pre class="cmd-block">{commandLine}</pre>
            </div>
          {/if}
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
    width: min(720px, 92vw);
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

  .modal-actions {
    display: flex;
    gap: 0.5rem;
    margin: 1rem 0 0 0;
    flex-wrap: wrap;
  }
  .action-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.4rem 0.75rem;
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    font-size: 0.85rem;
    cursor: pointer;
    transition: background-color 0.15s ease;
  }
  .action-btn:hover {
    background: var(--bg-primary);
  }
  .action-btn.primary {
    background: var(--primary-color, #1f6feb);
    color: var(--primary-text, #ffffff);
    border-color: var(--primary-color, #1f6feb);
  }
  .action-btn.primary:hover {
    background: var(--primary-hover, #1a5ac0);
  }

  .cmd-panel {
    margin-top: 0.6rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--bg-secondary);
  }
  .cmd-toolbar {
    display: flex;
    justify-content: flex-end;
    padding: 0.25rem 0.5rem;
    border-bottom: 1px solid var(--border-color);
  }
  .copy-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.15rem 0.45rem;
    background: var(--bg-primary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    font-size: 0.75rem;
    cursor: pointer;
  }
  .cmd-block {
    margin: 0;
    padding: 0.6rem 0.75rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 0.78rem;
    line-height: 1.4;
    color: var(--text-primary);
    white-space: pre;
    overflow-x: auto;
  }
</style>
