<script lang="ts">
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronRight, ChevronDown, Play, Eye, EyeOff } from 'lucide-svelte';
  import { onMount } from 'svelte';
  import { getTemplates } from '../lib/api';
  import type { Template } from '../../gen/taskqueue';

  /**
   * Array of workflow templates to display
   * @type {Template[]}
   */
  export let workflowsTemp: Template[] = [];

  /**
   * Callback function to open parameter modal
   * @type {(template: Template) => void}
   */
  export let openParamModal: (template: Template) => void;

  /**
   * Optional callback to hide/unhide a template. When omitted, the
   * eye-toggle button is not rendered (page didn't wire it up).
   * @type {(templateId: number, hidden: boolean) => void}
   */
  export let toggleHidden: ((templateId: number, hidden: boolean) => void) | undefined = undefined;

  // Per-row UI state for the "show all versions of this template" dropdown.
  // Keyed by workflow_template_id of the latest-version row that's been
  // expanded. Loading older versions is on-demand and cached after the
  // first fetch — operators rarely flip versions back and forth.
  type VersionRow = Template & { _loaded?: boolean };
  let expanded: Record<number, boolean> = {};
  let versionsByName: Record<string, VersionRow[] | 'loading' | 'error'> = {};

  /**
   * Lazily fetch every version of a given template name and cache the
   * result keyed by name. Hidden templates are NOT included unless the
   * page-level toggle requested them; the dropdown shows what the parent
   * listing would show, just expanded across versions.
   */
  async function ensureVersionsLoaded(name: string) {
    if (versionsByName[name] && versionsByName[name] !== 'error') {
      return;
    }
    versionsByName = { ...versionsByName, [name]: 'loading' };
    try {
      // allVersions=true; showHidden left default so the dropdown matches
      // the page-level state (hidden versions only show when the page
      // toggle is on, which causes the parent to refetch entirely).
      const all = await getTemplates(undefined, name, undefined, true, false);
      // Sort by uploadedAt desc (matches server default) so the dropdown
      // reads newest→oldest.
      all.sort((a, b) => (b.uploadedAt || '').localeCompare(a.uploadedAt || ''));
      versionsByName = { ...versionsByName, [name]: all };
    } catch (e) {
      versionsByName = { ...versionsByName, [name]: 'error' };
    }
  }

  function toggleExpand(t: Template) {
    const id = t.workflowTemplateId ?? 0;
    expanded = { ...expanded, [id]: !expanded[id] };
    if (expanded[id]) {
      ensureVersionsLoaded(t.name);
    }
  }
</script>

{#if workflowsTemp && workflowsTemp.length > 0}
  <!-- Workflow templates list container -->
  <div class="wfTemp-list">
    {#each workflowsTemp as t (t.workflowTemplateId)}
      <!-- Individual workflow template item -->
      <div class="wf-item" data-testid={`wfTemplate-${t.workflowTemplateId}`}
           class:wf-hidden-marker={t.hidden}>

        <!-- Workflow template information section -->
        <div class="wf-info">
          <!-- Template ID -->
          <span class="wf-id">#{t.workflowTemplateId}</span>
          <!-- Template name -->
          <span class="wf-id">{t.name}</span>
          <!-- Progress bar placeholder -->
          <div class="wf-progress-bar"></div>

          <!-- Version + dropdown trigger. Clicking the chevron expands a
               nested list of every uploaded version for this template
               name, fetched on demand. -->
          <button
            class="wf-version-toggle"
            type="button"
            title={expanded[t.workflowTemplateId ?? 0] ? 'Hide other versions' : 'Show all versions'}
            on:click={() => toggleExpand(t)}
          >
            {#if expanded[t.workflowTemplateId ?? 0]}
              <ChevronDown size={14} />
            {:else}
              <ChevronRight size={14} />
            {/if}
            <span class="wf-id">v{t.version}</span>
          </button>

          <!-- Upload date -->
          <span class="wf-id">
            {new Date(t.uploadedAt).toLocaleDateString('en-US', {
              month: '2-digit',
              day: '2-digit',
              year: 'numeric'
            })}
          </span>
          <!-- Uploaded by -->
          <span class="wf-id">{t.uploadedBy || 'Unknown'}</span>
          {#if t.hidden}
            <span class="wf-id wf-hidden-tag" title="Hidden">🙈</span>
          {/if}
        </div>

        <!-- Workflow template action buttons -->
        <div class="wf-actions">
          <!-- Play button to open parameters modal -->
          <button class="btn-action" title="Play" on:click={() => openParamModal(t)}><Play /></button>
          <!-- Pause button -->
          <button class="btn-action" title="Pause"><PauseCircle /></button>
          <!-- Reset button -->
          <button class="btn-action" title="Reset"><RefreshCw /></button>
          <!-- Break button -->
          <button class="btn-action" title="Break"><CircleX /></button>
          <!-- Clear button -->
          <button class="btn-action" title="Clear"><Eraser /></button>
          <!-- Hide / unhide toggle. Only rendered when the page passed a
               toggleHidden callback; eye = currently visible (click to
               hide), crossed-eye = currently hidden (click to unhide). -->
          {#if toggleHidden}
            {#if t.hidden}
              <button class="btn-action" title="Unhide" on:click={() => toggleHidden(t.workflowTemplateId ?? 0, false)}>
                <EyeOff />
              </button>
            {:else}
              <button class="btn-action" title="Hide" on:click={() => toggleHidden(t.workflowTemplateId ?? 0, true)}>
                <Eye />
              </button>
            {/if}
          {/if}
        </div>

        {#if expanded[t.workflowTemplateId ?? 0]}
          <!-- Versions dropdown panel -->
          <div class="wf-versions">
            {#if versionsByName[t.name] === 'loading'}
              <span>Loading versions…</span>
            {:else if versionsByName[t.name] === 'error'}
              <span>Failed to load versions.</span>
            {:else if Array.isArray(versionsByName[t.name])}
              {#each versionsByName[t.name] as v (v.workflowTemplateId)}
                <div class="wf-version-row" class:wf-current-version={v.workflowTemplateId === t.workflowTemplateId}>
                  <span class="wf-id">#{v.workflowTemplateId}</span>
                  <span class="wf-id">v{v.version}</span>
                  <span class="wf-id">
                    {new Date(v.uploadedAt).toLocaleDateString('en-US', {
                      month: '2-digit',
                      day: '2-digit',
                      year: 'numeric'
                    })}
                  </span>
                  <span class="wf-id">{v.uploadedBy || 'Unknown'}</span>
                  <button class="btn-action" title="Run this version" on:click={() => openParamModal(v)}><Play /></button>
                </div>
              {/each}
            {/if}
          </div>
        {/if}

      </div>

    {/each}
  </div>
{:else}
  <!-- Empty state message when no templates exist -->
  <p class="workerCompo-empty-state">No Workflow Template found.</p>
{/if}

<style>
  .wf-version-toggle {
    background: none;
    border: none;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0 4px;
    color: inherit;
  }
  .wf-version-toggle:hover { background: rgba(0, 0, 0, 0.05); }
  .wf-versions {
    margin-top: 6px;
    padding: 6px 12px;
    background: var(--bg-secondary, #f5f5f5);
    border-left: 2px solid var(--border-color, #ddd);
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .wf-version-row {
    display: flex;
    gap: 12px;
    align-items: center;
  }
  .wf-current-version {
    font-weight: 600;
  }
  .wf-hidden-marker {
    opacity: 0.6;
  }
  .wf-hidden-tag {
    font-size: 0.85em;
  }
  .wfTemp-show-hidden { /* selector also used in the page */
    display: inline-flex; align-items: center; gap: 6px; margin: 6px 0;
  }
</style>
