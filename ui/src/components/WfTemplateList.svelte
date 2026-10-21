<script lang="ts">
  import { run, stopPropagation, createBubbler } from 'svelte/legacy';

  const bubble = createBubbler();
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronDown, ChevronRight, Play, Eye, EyeOff, FolderOpen, Folder, Move } from 'lucide-svelte';
  import { onDestroy } from 'svelte';
  import { getTemplates } from '../lib/api';
  import type { Template } from '../../gen/taskqueue';

  interface Props {
    /**
   * Array of workflow templates to display
   * @type {Template[]}
   */
    workflowsTemp?: Template[];
    /**
   * Callback function to open parameter modal
   * @type {(template: Template) => void}
   */
    openParamModal: (template: Template) => void;
    /**
   * Optional callback to hide/unhide a template. When omitted, the
   * eye-toggle button is not rendered (page didn't wire it up).
   * @type {(templateId: number, hidden: boolean) => void}
   */
    toggleHidden?: ((templateId: number, hidden: boolean) => void) | undefined;
    /**
   * Optional callback to move a template (every version sharing its
   * name) to another folder path. When omitted, the Move button is
   * not rendered. Signature: (templateId, currentFolder) — the parent
   * owns the prompt / modal and the API call so this component stays
   * presentational.
   * @type {(templateId: number, currentFolder: string) => void}
   */
    moveTemplate?: ((templateId: number, currentFolder: string) => void) | undefined;
  }

  let { workflowsTemp = [], openParamModal, toggleHidden = undefined, moveTemplate = undefined }: Props = $props();

  // Group templates by folder path. Order of folders is alphabetical,
  // with the root ("") pinned first so unfiled templates stay visible.
  let groupedByFolder = $derived.by(() => {
    const map = new Map<string, Template[]>();
    for (const t of workflowsTemp) {
      const f = t.folder ?? '';
      if (!map.has(f)) map.set(f, []);
      map.get(f)!.push(t);
    }
    const paths = Array.from(map.keys()).sort((a, b) => {
      if (a === '') return -1;
      if (b === '') return 1;
      return a.localeCompare(b);
    });
    return paths.map(p => ({ path: p, templates: map.get(p)! }));
  });

  // Per-folder collapse state. Expanded by default (new entries render
  // open on first paint). Keyed by the folder path exactly as it appears
  // in `groupedByFolder` — including the empty string for root.
  let collapsed: Record<string, boolean> = $state({});

  function toggleFolder(path: string) {
    collapsed = { ...collapsed, [path]: !collapsed[path] };
  }

  // Human label for the folder bar. Root templates are shown under
  // "/ (root)" so operators can see at a glance which templates haven't
  // been filed yet. Nested paths show the full breadcrumb; the UI relies
  // on a small font so even deep paths like `bio/qc/fastp` fit a row.
  function folderLabel(path: string): string {
    return path === '' ? '/ (root)' : path;
  }

  // Per-row dropdown state. Only one dropdown is open at a time; the row's
  // workflowTemplateId acts as the discriminator. The version list itself
  // is fetched on demand and cached by template name (operators rarely
  // flip through versions, so on-demand keeps the initial list payload
  // small).
  let openTemplateId: number | null = $state(null);
  type VersionRow = Template;
  let versionsByName: Record<string, VersionRow[] | 'loading' | 'error'> = $state({});

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
  run(() => {
    if (workflowsTemp) openTemplateId = null;
  });

  onDestroy(() => {
    openTemplateId = null;
  });
</script>

{#if workflowsTemp && workflowsTemp.length > 0}
  <!-- Workflow templates list container, grouped by folder path. Each
       folder has a collapsible header; templates render in their folder
       section. Root templates (folder == "") render under "/ (root)" so
       they stay visible even when nested folders exist. -->
  <div class="wfTemp-list" use:closeOnOutsideClick>
    {#each groupedByFolder as grp (grp.path)}
      <div class="wf-folder-group" data-testid={`wfFolder-${grp.path || 'root'}`}>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <button class="wf-folder-header" type="button"
                onclick={() => toggleFolder(grp.path)}
                aria-expanded={!collapsed[grp.path]}>
          {#if collapsed[grp.path]}
            <ChevronRight size={14} />
            <Folder size={14} />
          {:else}
            <ChevronDown size={14} />
            <FolderOpen size={14} />
          {/if}
          <span class="wf-folder-name">{folderLabel(grp.path)}</span>
          <span class="wf-folder-count">({grp.templates.length})</span>
        </button>
        {#if !collapsed[grp.path]}
    {#each grp.templates as t (t.workflowTemplateId)}
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
                onclick={stopPropagation(() => toggleDropdown(t))}
              >
                <span class="wf-id">v{t.version}</span>
                <ChevronDown size={14} />
                <span class="wf-version-count">({t.versionCount})</span>
              </button>

              {#if openTemplateId === (t.workflowTemplateId ?? 0)}
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- on:click|stopPropagation is containment only: keeps an
                     in-menu click from bubbling to the outer "open / close"
                     toggle. Real menu items below have their own buttons /
                     <a>s with keyboard support. -->
                <ul class="wf-version-menu" role="listbox" onclick={stopPropagation(bubble('click'))}>
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
                                onclick={() => { openTemplateId = null; openParamModal(v); }}>
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
          <button class="btn-action" title="Play" onclick={() => openParamModal(t)}><Play /></button>
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
              <button class="btn-action" title="Unhide" onclick={() => toggleHidden(t.workflowTemplateId ?? 0, false)}>
                <EyeOff />
              </button>
            {:else}
              <button class="btn-action" title="Hide" onclick={() => toggleHidden(t.workflowTemplateId ?? 0, true)}>
                <Eye />
              </button>
            {/if}
          {/if}
          {#if moveTemplate}
            <button class="btn-action" title="Move to folder"
                    onclick={() => moveTemplate(t.workflowTemplateId ?? 0, t.folder ?? '')}>
              <Move />
            </button>
          {/if}
        </div>

      </div>

    {/each}
        {/if}
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

  /* Folder grouping: a lightweight breadcrumb header above each cluster
     of templates. The header is a button so keyboard users can
     collapse / expand each folder; the chevron and folder icon together
     signal "this toggles". The count is a quiet hint so operators can
     tell at a glance how many templates live under each folder without
     expanding. */
  .wf-folder-group {
    display: flex;
    flex-direction: column;
    margin-bottom: 10px;
  }
  .wf-folder-header {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px;
    background: var(--bg-secondary, #f0f1f3);
    border: 1px solid var(--border-color, #ddd);
    border-radius: 6px;
    cursor: pointer;
    font-weight: 600;
    color: inherit;
    width: fit-content;
    margin-bottom: 4px;
  }
  .wf-folder-header:hover {
    background: var(--bg-tertiary, #e7e8ec);
  }
  .wf-folder-name {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.95em;
  }
  .wf-folder-count {
    font-size: 0.85em;
    opacity: 0.65;
    font-weight: 400;
  }
</style>
