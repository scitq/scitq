<script lang="ts">
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronDown, Play, Eye, EyeOff } from 'lucide-svelte';
  import { onDestroy } from 'svelte';
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

  // Per-row dropdown state. Only one dropdown is open at a time; the row's
  // workflowTemplateId acts as the discriminator. The version list itself
  // is fetched on demand and cached by template name (operators rarely
  // flip through versions, so on-demand keeps the initial list payload
  // small).
  let openTemplateId: number | null = null;
  type VersionRow = Template;
  let versionsByName: Record<string, VersionRow[] | 'loading' | 'error'> = {};

  async function ensureVersionsLoaded(name: string) {
    if (versionsByName[name] && versionsByName[name] !== 'error') return;
    versionsByName = { ...versionsByName, [name]: 'loading' };
    try {
      const all = await getTemplates(undefined, name, undefined, true, false);
      // Newest first — matches the server's default order and reads as
      // "current version at top, history below".
      all.sort((a, b) => (b.uploadedAt || '').localeCompare(a.uploadedAt || ''));
      versionsByName = { ...versionsByName, [name]: all };
    } catch {
      versionsByName = { ...versionsByName, [name]: 'error' };
    }
  }

  function toggleDropdown(t: Template) {
    const id = t.workflowTemplateId ?? 0;
    if (openTemplateId === id) {
      openTemplateId = null;
      return;
    }
    openTemplateId = id;
    ensureVersionsLoaded(t.name);
  }

  // Close any open dropdown on outside click. We attach a single document
  // listener while a dropdown is open; the action's element stops the
  // bubbled click on its own anchors so toggling stays sticky inside the
  // dropdown.
  function closeOnOutsideClick(node: HTMLElement) {
    function onDocClick(ev: MouseEvent) {
      if (openTemplateId === null) return;
      if (!node.contains(ev.target as Node)) {
        openTemplateId = null;
      }
    }
    document.addEventListener('click', onDocClick);
    return {
      destroy() {
        document.removeEventListener('click', onDocClick);
      },
    };
  }

  // Close the dropdown when the underlying list changes (e.g. parent page
  // refetches after a hide/unhide). Avoids a stale openTemplateId pointing
  // at a row that's no longer rendered.
  $: if (workflowsTemp) openTemplateId = null;

  onDestroy(() => {
    openTemplateId = null;
  });
</script>

{#if workflowsTemp && workflowsTemp.length > 0}
  <!-- Workflow templates list container -->
  <div class="wfTemp-list" use:closeOnOutsideClick>
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

          <!-- Version + (only when there's history) a dropdown trigger.
               versionCount is populated by the server only when collapsing
               to latest-per-name, which is exactly when the dropdown is
               useful. With one version (or unset), no chevron renders. -->
          {#if (t.versionCount ?? 1) > 1}
            <div class="wf-version-dropdown">
              <button
                class="wf-version-trigger"
                type="button"
                title={`Show ${t.versionCount} versions`}
                aria-haspopup="listbox"
                aria-expanded={openTemplateId === (t.workflowTemplateId ?? 0)}
                on:click|stopPropagation={() => toggleDropdown(t)}
              >
                <span class="wf-id">v{t.version}</span>
                <ChevronDown size={14} />
                <span class="wf-version-count">({t.versionCount})</span>
              </button>

              {#if openTemplateId === (t.workflowTemplateId ?? 0)}
                <ul class="wf-version-menu" role="listbox" on:click|stopPropagation>
                  {#if versionsByName[t.name] === 'loading'}
                    <li class="wf-version-status">Loading…</li>
                  {:else if versionsByName[t.name] === 'error'}
                    <li class="wf-version-status">Failed to load.</li>
                  {:else if Array.isArray(versionsByName[t.name])}
                    {#each versionsByName[t.name] as v (v.workflowTemplateId)}
                      <li class="wf-version-item"
                          class:wf-version-current={v.workflowTemplateId === t.workflowTemplateId}
                          role="option"
                          aria-selected={v.workflowTemplateId === t.workflowTemplateId}>
                        <button class="wf-version-link" type="button"
                                title={`Run version ${v.version}`}
                                on:click={() => { openTemplateId = null; openParamModal(v); }}>
                          <span class="wf-id">v{v.version}</span>
                          <span class="wf-version-meta">
                            {new Date(v.uploadedAt).toLocaleDateString('en-US', { month: '2-digit', day: '2-digit', year: 'numeric' })}
                            · {v.uploadedBy || 'Unknown'}
                          </span>
                          {#if v.workflowTemplateId === t.workflowTemplateId}
                            <span class="wf-version-tag">latest</span>
                          {/if}
                        </button>
                      </li>
                    {/each}
                  {/if}
                </ul>
              {/if}
            </div>
          {:else}
            <span class="wf-id">v{t.version}</span>
          {/if}

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

      </div>

    {/each}
  </div>
{:else}
  <!-- Empty state message when no templates exist -->
  <p class="workerCompo-empty-state">No Workflow Template found.</p>
{/if}

<style>
  /* Inline-block so the dropdown can be absolutely positioned relative to
     the trigger (no need for a portal — the menu sits over the row). */
  .wf-version-dropdown {
    position: relative;
    display: inline-block;
  }
  .wf-version-trigger {
    background: none;
    border: 1px solid transparent;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 6px;
    border-radius: 4px;
    color: inherit;
  }
  .wf-version-trigger:hover {
    background: rgba(0, 0, 0, 0.06);
    border-color: var(--border-color, #ddd);
  }
  .wf-version-count {
    font-size: 0.85em;
    opacity: 0.65;
  }
  .wf-version-menu {
    position: absolute;
    top: 100%;
    left: 0;
    margin: 4px 0 0;
    padding: 4px 0;
    list-style: none;
    background: var(--bg-primary, #fff);
    color: var(--text-primary, #213547);
    border: 1px solid var(--border-color, #ddd);
    border-radius: 6px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
    min-width: 220px;
    max-height: 320px;
    overflow-y: auto;
    z-index: 100;
  }
  .wf-version-status {
    padding: 6px 10px;
    font-style: italic;
    opacity: 0.75;
  }
  .wf-version-item .wf-version-link {
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    cursor: pointer;
    padding: 6px 10px;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
    color: inherit;
  }
  .wf-version-item .wf-version-link:hover {
    background: var(--bg-secondary, #f5f5f5);
  }
  .wf-version-meta {
    font-size: 0.85em;
    opacity: 0.7;
  }
  .wf-version-current {
    background: var(--bg-secondary, #f5f5f5);
  }
  .wf-version-tag {
    font-size: 0.7em;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    background: var(--primary-color, #192245);
    color: var(--primary-text, #fff);
    padding: 1px 6px;
    border-radius: 999px;
    margin-top: 2px;
  }
  .wf-hidden-marker { opacity: 0.6; }
  .wf-hidden-tag { font-size: 0.85em; }
</style>
