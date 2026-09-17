<script lang="ts">
  import { preventDefault } from 'svelte/legacy';

  import { onMount, onDestroy, tick } from 'svelte';
  import { wsClient } from '../lib/wsClient';
  import { Plus, Check, Loader2 } from 'lucide-svelte';
  import { getTemplates, UploadTemplates, runTemp, updateTemplateHidden, getTemplateRun } from '../lib/api';
  import WfTemplateList from '../components/WfTemplateList.svelte';
  import '../styles/wfTemplate.css';
  import type { UploadTemplateResponse } from '../lib/types';
  import { Template } from '../../gen/taskqueue';

  let showRunSuccessModal = $state(false);
  let showRunErrorModal = $state(false);
  let successMessage = $state('');
  // Populated on a successful run so the success modal can offer a
  // direct "Open" link to the newly-created workflow, not just a
  // generic "Go to workflows" jump.
  let lastRunWorkflowId: number | null = $state(null);
  let lastRunWorkflowName: string = $state('');
  // True while a RunTemplate gRPC call is in flight. Used to visibly
  // disable the Run button and show a spinner so the user knows their
  // click was registered — the server-side template script can take
  // several seconds to compile for large workflows, during which a
  // silent button looks broken.
  let isRunningTemplate = $state(false);

  // Array of workflow templates
  let workflowsTemp = $state([]);
  // Current sorting method ('template' or 'name'). Default 'name' so the
  // listing is alphabetical out of the box; the dropdown lets the operator
  // switch to 'template' (id desc) when they want chronological / newest-
  // upload-first order.
  let sortBy: 'template' | 'name' = $state('name');
  // Currently selected file for upload
  let selectedFile: File | null = $state(null);
  // Reference to file input element
  let fileInput: HTMLInputElement = $state();
  // Content of selected file as Uint8Array
  let fileContent: Uint8Array | null = null;
  // Response from template upload API
  let uploadResponse: UploadTemplateResponse = {};
  // Whether to show error modal
  let showErrorModal = $state(false);
  // Error message to display
  let errorMessage = $state('');
  // Whether to show parameter modal
  let showParamModal = $state(false);
  // Currently selected template for parameter input
  let selectedTemplate: Template | null = $state(null);
  // User-provided parameter values
  let userParams: Record<string, any> = $state({});
  // --- Advanced run options (collapsed by default) ---
  // runMode: 'new' (default), 'continue' (extend the last matching run), or
  // 'extend' (extend a specific workflow id). See specs/workflow_extend.md.
  let runMode: 'new' | 'continue' | 'extend' = $state('new');
  let runExtendWorkflowId: number | null = $state(null);
  let runRetryFailedOnly = $state(false);
  // Skip the recruiters: workflow is created and tasks submitted but no
  // workers auto-deploy. Useful when permanent workers are already set
  // up for the step, or when the operator wants to inspect the workflow
  // before any cost is incurred. Orthogonal to runMode — works with new
  // / continue / extend.
  let runNoRecruiters = $state(false);
  // Parameter validation errors
  let paramErrors: Record<string, string> = {};
  // Whether to show parameter errors
  let showParamErrors = $state(false);
  // Tracks which parameter help texts are visible
  let showHelp: Record<string, boolean> = $state({});
  // Function to unsubscribe from WebSocket
  let unsubscribeWS: (() => void) | null = null;

  /**
   * Handles incoming WebSocket messages for template updates
   * @param {Object} message - WebSocket message
   * @param {string} message.type - Message type
   * @param {Object} message.payload - Message payload containing template data
   */
  function handleMessage(message) {
    // New envelope: { type: 'template', action: 'uploaded'|'created'|'updated'|'deleted', payload: {...} }
    if (message?.type === 'template') {
      const action = message.action;
      const p = message.payload || {};

      if (action === 'uploaded' || action === 'created') {
        const id = p.workflowTemplateId ?? p.ID ?? p.id;
        if (id != null && !workflowsTemp.some(t => (t.workflowTemplateId ?? t.ID) === id)) {
          workflowsTemp = [...workflowsTemp, p];
        }
        return;
      }

      if (action === 'updated') {
        const id = p.workflowTemplateId ?? p.ID ?? p.id;
        if (id != null) {
          workflowsTemp = workflowsTemp.map(t =>
            (t.workflowTemplateId ?? t.ID) === id ? { ...t, ...p } : t
          );
        }
        return;
      }

      if (action === 'deleted') {
        const id = p.workflowTemplateId ?? p.ID ?? p.id;
        if (id != null) {
          workflowsTemp = workflowsTemp.filter(t => (t.workflowTemplateId ?? t.ID) !== id);
        }
        return;
      }
    }
  }

  // Whether the listing should include templates the operator has hidden
  // (via the per-row hide button or `scitq template update --hide`). The
  // toggle is bound to a checkbox in the template page header.
  let showHidden = $state(false);

  /** Reload templates from the server honouring the showHidden toggle. */
  async function reloadTemplates() {
    workflowsTemp = await getTemplates(undefined, undefined, undefined, false, showHidden);
    handleSortBy();
  }

  // Initialize component - load templates and subscribe to WebSocket
  onMount(async () => {
    await reloadTemplates();
    unsubscribeWS = wsClient.subscribeWithTopics({ template: [] }, handleMessage);
    // If we arrived here via #/workflowsTemplate?from_run=<id> (from
    // the TemplateRunModal's "Launch new workflow from this" button),
    // fetch that run and open the param modal pre-filled with its
    // values. See TemplateRunModal.launchFromThis.
    await maybeHandleFromRunURL();
  });

  /**
   * Consume the `from_run=<id>` query fragment on the hash, if
   * present, and open the parameter modal pre-filled with that run's
   * paramValuesJson. Best-effort: any failure lands as a small note
   * in errorMessage rather than a crash, since the user asked for a
   * shortcut, not a critical operation.
   */
  async function maybeHandleFromRunURL() {
    const hash = window.location.hash;
    const qIdx = hash.indexOf('?');
    if (qIdx < 0) return;
    const params = new URLSearchParams(hash.slice(qIdx + 1));
    const fromRunRaw = params.get('from_run');
    if (!fromRunRaw) return;
    const fromRunId = parseInt(fromRunRaw, 10);
    if (!Number.isFinite(fromRunId) || fromRunId <= 0) return;
    // Strip the query so a browser refresh doesn't re-open the modal
    // and so the URL bar is clean while the user tweaks values.
    const pathOnly = hash.slice(0, qIdx);
    history.replaceState(null, '', location.pathname + location.search + pathOnly);

    let templateRun: any;
    try {
      templateRun = await getTemplateRun(fromRunId);
    } catch (e) {
      console.error('from_run: failed to fetch template run', e);
      return;
    }
    if (!templateRun || !templateRun.workflowTemplateId) {
      console.warn('from_run: template run not found or is an ad-hoc script run', fromRunId);
      return;
    }
    // Look up the referenced template — try the already-loaded set
    // first, fall back to a targeted fetch (covers the hidden case
    // where the template isn't in the default listing).
    let template = workflowsTemp.find(t => t.workflowTemplateId === templateRun.workflowTemplateId);
    if (!template) {
      const fetched = await getTemplates(templateRun.workflowTemplateId, undefined, undefined, false, true);
      template = fetched?.[0];
    }
    if (!template) {
      // Template was deleted. Surface via the run-error modal —
      // that's what the operator will already be primed to read.
      errorMessage = `Template ${templateRun.templateName ?? ''}@${templateRun.templateVersion ?? ''} referenced by run #${fromRunId} is no longer available.`;
      showRunErrorModal = true;
      return;
    }
    // Parse the run's captured values into a plain object.
    let prefill: Record<string, any> = {};
    try {
      prefill = JSON.parse(templateRun.paramValuesJson || '{}');
    } catch (e) {
      console.warn('from_run: paramValuesJson unparseable', e);
    }
    openParamModal(template, {
      values: prefill,
      note: `Pre-filled from run #${fromRunId}${templateRun.createdAt ? ' (' + templateRun.createdAt.slice(0, 10) + ')' : ''}. Change any value before launching.`,
    });
  }

  // Small info banner shown at the top of the param modal when it
  // was opened via the from_run shortcut. Cleared on next open.
  let paramModalNote: string = $state('');
  // Keys present in the source run but absent from the target
  // template's schema (added when the template was later edited).
  // Displayed as a warning so the operator knows some values were
  // dropped and won't be part of the new run.
  let paramModalDropped: string[] = $state([]);
  // Every uploaded version of the currently-open template, populated
  // when the param modal opens. Drives the version <select> in the
  // modal header so the operator can run against a different version
  // of the same template without leaving the launcher. Sorted
  // newest-uploaded first (workflow_template_id descending).
  let paramModalVersions: Template[] = $state([]);

  /** Hide or unhide a single template by id, then reload. */
  async function toggleHidden(templateId: number, hidden: boolean) {
    try {
      await updateTemplateHidden(templateId, hidden);
      await reloadTemplates();
    } catch (e: any) {
      errorMessage = e?.message || 'Failed to update template visibility';
      showErrorModal = true;
    }
  }

  // Cleanup - unsubscribe from WebSocket when component is destroyed
  onDestroy(() => {
    if (unsubscribeWS) {
      unsubscribeWS();
      unsubscribeWS = null;
    }
  });

  /**
   * Sorts workflow templates based on current sort criteria
   */
  function handleSortBy() {
      if (!workflowsTemp) return [];

      workflowsTemp  = [...workflowsTemp].sort((a, b) => {
          switch (sortBy) {
              case 'template':
                  return (b.workflowTemplateId ?? 0) - (a.workflowTemplateId ?? 0);
              case 'name':
                  // Case-insensitive locale compare so "Foo" and "foo"
                  // group together regardless of upload casing.
                  return (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' });
              default:
                  return 0;
          }
      });
  }

  /**
   * Triggers file input click to select a file
   */
  function handleAdd() {
    fileInput.click();
  }

  /**
   * Handles file selection change event
   * @param {Event} event - File input change event
   */
  function handleFileChange(event: Event) {
    const files = (event.target as HTMLInputElement).files;
    if (files?.length) {
      selectedFile = files[0];
    }
  }

  /**
   * Reads file content as ArrayBuffer
   * @param {File} file - File to read
   * @returns {Promise<ArrayBuffer>} Promise resolving with file content
   */
  function readFileAsArrayBuffer(file: File): Promise<ArrayBuffer> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        if (reader.result instanceof ArrayBuffer) {
          resolve(reader.result);
        } else {
          reject(new Error("Unexpected file read result type."));
        }
      };
      reader.onerror = () => reject(reader.error);
      reader.readAsArrayBuffer(file);
    });
  }

  /**
   * Validates and uploads the selected template file
   * @param {boolean} [force=false] - Whether to force upload despite warnings
   * @async
   */
  async function handleValidate(force = false) {
    if (!selectedFile) return;

    try {
      fileContent = new Uint8Array(await readFileAsArrayBuffer(selectedFile));
      uploadResponse = await UploadTemplates(fileContent, force);

      if (!uploadResponse.success) {
        errorMessage = uploadResponse.message || "Unknown error occurred during upload.";
        showErrorModal = true;
      } else {
        resetFileSelection();
      }
    } catch (error) {
      console.error("Error during file upload:", error);
      errorMessage = error.message || "Unknown error occurred.";
      showErrorModal = true;
    }
  }

  /**
   * Toggles help text visibility for a parameter
   * @param {string} paramName - Parameter name to toggle help for
   */
  function toggleHelp(paramName: string) {
    showHelp = {...showHelp, [paramName]: !showHelp[paramName]};
  }

  /**
   * True when this param's requires: declaration is satisfied by
   * the current user inputs. When a param declares
   * `requires: { <other>: <value> }`, the UI greys it out until every
   * such (other, value) pair matches — the same rule the server uses
   * to reject a run where the coupled fields disagree. A missing
   * `requires` clause means the param is always active. The `when:`
   * key inside requires is a server-side trigger override and doesn't
   * take part in the UI relevance check.
   */
  function isParamActive(param: any, values: Record<string, any>): boolean {
    const req = param?.requires;
    if (!req || typeof req !== 'object') return true;
    for (const key of Object.keys(req)) {
      if (key === 'when') continue;
      const want = req[key];
      const actual = values[key];
      if (typeof want === 'boolean') {
        // Booleans: honour "false"/"true"/etc. string coercions so a
        // JSON-serialised param default still compares correctly.
        const falsy = new Set(['', '0', 'false', 'False', 'no', 'No', 'none', 'None', 'null']);
        const actualBool = actual === true || (typeof actual === 'string' && !falsy.has(actual)) || (typeof actual === 'number' && actual !== 0);
        if (actualBool !== want) return false;
      } else {
        if (String(actual ?? '') !== String(want)) return false;
      }
    }
    return true;
  }

  /**
   * Opens parameter modal and initializes parameter states.
   *
   * @param template — Template to run.
   * @param prefill  — Optional { values, note } bundle. `values` is a
   *   name→value map (typically a previous run's paramValuesJson);
   *   after defaults are applied, keys that also appear in the
   *   template's current schema get overwritten with the prefill
   *   value. Keys present in prefill but missing from the current
   *   schema (template was edited since the source run) are surfaced
   *   as `paramModalDropped` so the user sees what was left behind.
   *   `note` renders as a small info banner at the top of the modal.
   */
  function openParamModal(template: Template, prefill?: { values: Record<string, any>; note?: string }) {
    selectedTemplate = template;
    paramErrors = {};
    showParamErrors = false;
    showHelp = {};
    paramModalNote = prefill?.note ?? '';
    paramModalDropped = [];
    try {
      const parsedParams = JSON.parse(template.paramJson || '[]');

      if (!Array.isArray(parsedParams)) {
        throw new Error('paramJson should be an array');
      }

      userParams = {};
      const knownKeys = new Set<string>();
      parsedParams.forEach(param => {
        if (param.name) {
          knownKeys.add(param.name);
          // Prefill wins over default. Boolean-typed params need a
          // real boolean for the checkbox binding; strings come out
          // of paramValuesJson as-is, so a `bool` field ends up
          // string "true"/"false" — coerce so the checkbox reflects
          // the correct state.
          let initial: any = param.default ?? '';
          if (prefill && Object.prototype.hasOwnProperty.call(prefill.values, param.name)) {
            initial = prefill.values[param.name];
            if (param.type === 'bool' && typeof initial === 'string') {
              initial = initial === 'true' || initial === 'True' || initial === '1';
            }
          }
          userParams[param.name] = initial;
          showHelp[param.name] = false;
          // Required-and-empty check: after prefill, most required
          // fields will be populated, but flag the ones that
          // genuinely aren't.
          if (param.required && (userParams[param.name] === '' || userParams[param.name] == null)) {
            paramErrors[param.name] = 'This field is required';
          }
        }
      });
      if (prefill) {
        for (const k of Object.keys(prefill.values)) {
          if (!knownKeys.has(k)) paramModalDropped.push(k);
        }
      }

    } catch (error) {
      console.error("Error parsing paramJson:", error);
      userParams = {};
    }

    // Reset advanced run options each time the modal opens.
    runMode = 'new';
    runExtendWorkflowId = null;
    runRetryFailedOnly = false;

    showParamModal = true;

    // Fire-and-forget: fetch every version of this template so the
    // header <select> can offer them. The dropdown is disabled until
    // the fetch returns; done this way (rather than blocking the
    // modal open) so a slow server never delays the operator seeing
    // the form. Only reload if we haven't already loaded them for
    // this template name — the reload during a version switch reuses
    // the existing list.
    if (
      paramModalVersions.length === 0 ||
      paramModalVersions[0]?.name !== template.name
    ) {
      paramModalVersions = [];
      loadParamModalVersions(template.name);
    }
  }

  /**
   * Populate `paramModalVersions` with every uploaded version of the
   * named template. `allVersions=true, showHidden=true` because the
   * operator may reasonably want to (a) run an older version they
   * pinned earlier, or (b) reproduce a run against a version that's
   * been hidden since.
   */
  async function loadParamModalVersions(name: string) {
    if (!name) return;
    try {
      const versions = await getTemplates(undefined, name, undefined, true, true);
      // Sort newest-uploaded first. workflow_template_id is monotonic
      // in upload order, which is the ordering an operator expects
      // ("what did I just push?").
      versions.sort((a, b) => (b.workflowTemplateId ?? 0) - (a.workflowTemplateId ?? 0));
      paramModalVersions = versions;
    } catch (e) {
      console.error('failed to load template versions', e);
    }
  }

  /**
   * Switch the currently-open param modal to a different version of
   * the same template. Carries the operator's current userParams
   * forward as prefill so any editing / from_run inheritance
   * survives — fields present in the new version get the current
   * value, dropped fields land in the paramModalDropped banner,
   * added fields get their defaults. The version <select>'s
   * onchange calls this.
   */
  function switchTemplateVersion(newTemplateId: number) {
    if (!selectedTemplate) return;
    if (newTemplateId === selectedTemplate.workflowTemplateId) return;
    const target = paramModalVersions.find(v => v.workflowTemplateId === newTemplateId);
    if (!target) return;
    // Reopen the modal with the same values as prefill; keep any
    // provenance note the operator was already shown.
    const carried = { ...userParams };
    const existingNote = paramModalNote;
    openParamModal(target, {
      values: carried,
      note: existingNote || `Switched to ${target.name}@${target.version}. Values preserved from the previous version.`,
    });
  }

  /**
   * Validates parameters and runs the selected template
   * @async
   */
  async function handleRunTemplate() {
    // Guard against double-clicks during the gRPC round-trip.
    if (isRunningTemplate) return;
    showParamErrors = false;
    paramErrors = {};

    try {
      const parsedParams = JSON.parse(selectedTemplate?.paramJson || '[]');
      let hasErrors = false;

      parsedParams.forEach(param => {
        // Inactive (greyed-out) params: strip their value before
        // submitting so a stale entry doesn't trigger the server's
        // `requires:` validator, and skip the "required" check since
        // the field isn't editable right now.
        if (!isParamActive(param, userParams)) {
          userParams[param.name] = param.default ?? '';
          return;
        }
        if (param.required && (!userParams[param.name] || userParams[param.name].trim() === '')) {
          paramErrors[param.name] = 'This field is required';
          hasErrors = true;
        }
      });

      if (hasErrors) {
        showParamErrors = true;
        return;
      }

      if (!selectedTemplate) return;

      // Mark in-flight only AFTER validation passed — we don't want
      // the button to spin while the user is fixing a required-field
      // error in the same modal.
      isRunningTemplate = true;

      // Advanced run options (extend / continue / retry-failed-only / no-recruiters).
      const runOpts: { extendWorkflowId?: number; continueLast?: boolean; retryFailedOnly?: boolean; noRecruiters?: boolean } = {};
      if (runMode === 'continue') {
        runOpts.continueLast = true;
      } else if (runMode === 'extend') {
        if (runExtendWorkflowId == null || runExtendWorkflowId <= 0) {
          errorMessage = 'Enter a workflow id to extend, or pick another run mode.';
          showRunErrorModal = true;
          return;
        }
        runOpts.extendWorkflowId = runExtendWorkflowId;
      }
      if (runMode !== 'new' && runRetryFailedOnly) {
        runOpts.retryFailedOnly = true;
      }
      if (runNoRecruiters) {
        runOpts.noRecruiters = true;
      }

      // Only include paramJson if userParams is non-empty
      const hasParams = Object.keys(userParams).length > 0;
      const res = hasParams
        ? await runTemp(selectedTemplate.workflowTemplateId, JSON.stringify(userParams), runOpts)
        : await runTemp(selectedTemplate.workflowTemplateId, '{}', runOpts);

      if (res.status !== 'S') {
        const msg = res.errorMessage || 'Template run failed (unknown error)';
        errorMessage = msg;
        // KEEP showParamModal open — when the user closes the error
        // modal they land back on the params form with their inputs
        // intact, so fixing a typo and re-launching is one click.
        // Only successful runs auto-close the params form (below).
        showRunErrorModal = true;
        return;
      }

      // ✅ Success case
      // Prefer the concrete "workflow <name> (#<id>)" line — Florian
      // asked for the workflow identity after the run, not just a
      // generic success banner. Fall back to the plain banner when
      // an older server didn't populate workflow_name / workflow_id
      // (extends may also leave workflow_id 0 in some paths).
      const wfName = (res as any).workflowName;
      const wfId = (res as any).workflowId;
      if (wfId) {
        successMessage = `✅ Workflow ${wfName ? `"${wfName}" ` : ''}(#${wfId}) created.`;
      } else {
        successMessage = '✅ Template run created successfully!';
      }
      lastRunWorkflowId = wfId ?? null;
      lastRunWorkflowName = wfName ?? '';
      if (res.errorMessage) {
        successMessage += `\n⚠️ ${res.errorMessage}`;
      }
      showParamModal = false;
      showRunSuccessModal = true;
      await tick();
      document.querySelector('.wfTemp-modal-backdrop')?.focus();

    } catch (error) {
      console.error("Failed to run template:", error);
      errorMessage = error.message || "Unknown error occurred.";
      // Same rationale as above: keep the params modal open behind
      // the error so the user's inputs survive the failure ack and
      // they can adjust + retry without re-typing 8 param values.
      showRunErrorModal = true;
    } finally {
      isRunningTemplate = false;
    }
  }

  /**
   * Resets file selection state
   */
  function resetFileSelection() {
    selectedFile = null;
    fileInput.value = '';
    fileContent = null;
    uploadResponse = {};
    showErrorModal = false;
    errorMessage = '';
  }

  /**
   * Forces file upload despite warnings
   */
  function handleForceUpload() {
    showErrorModal = false;
    handleValidate(true);
  }
</script>

<!-- ----------- MAIN CONTAINER ---------- -->
<div class="wfTemp-container" data-testid="wfTemp-page">
  <!-- Header section with sorting and file actions -->
  <div class="wfTemp-header">
    <!-- Sort form -->
    <form class="wfTemp-sort-form" onsubmit={preventDefault(() => handleSortBy())}>
      <div class="wfTemp-sort-group">
        <label for="sortBy">Sort by</label>
        <select id="sortBy" bind:value={sortBy} onchange={() => handleSortBy()}>
          <option value="template">Template</option>
          <option value="name">Name</option>
        </select>
      </div>
    </form>

    <!-- Hidden file input -->
    <input 
      type="file"
      aria-label="File upload"
      bind:this={fileInput}
      onchange={handleFileChange}
      style="display: none;"
    />

    <!-- File actions section -->
    <div class="wfTemp-file-action-group">
      <!-- File info display -->
      <div class="wfTemp-file-info">
        <input 
          type="text" 
          class="wfTemp-file-display" 
          readonly 
          value={selectedFile?.name || 'No file selected'}
          title={selectedFile?.name || 'No file selected'}
        />

        {#if selectedFile}
          <button class="wfTemp-clear-file" onclick={resetFileSelection} title="Clear selected file">
            &times;
          </button>
        {/if}
      </div>
      
      <!-- Action buttons -->
      <div class="wfTemp-button-group">
        <button class="wfTemp-action-button wfTemp-add-button" onclick={handleAdd}>
          <Plus size={20} title="Add Workflow" />
        </button>
        <button 
          class="wfTemp-action-button wfTemp-validate-button" 
          onclick={() => handleValidate(false)}
          disabled={!selectedFile}
        >
          <Check size={20} title="Validate" />
        </button>
      </div>
    </div>
  </div>

  <!-- Visibility toggle: include hidden templates in the listing. Mirrors
       `scitq template list --show-hidden` on the CLI. Templates marked
       hidden are excluded by default to keep the page focused on what's
       currently runnable. -->
  <label class="wfTemp-show-hidden">
    <input type="checkbox" bind:checked={showHidden} onchange={reloadTemplates} />
    Show hidden templates
  </label>

  <!-- Template list component -->
  <WfTemplateList {workflowsTemp} openParamModal={openParamModal} {toggleHidden}/>
</div>

<!-- ----------- ERROR MODAL ---------- -->
{#if showErrorModal}
  <div class="wfTemp-modal-backdrop">
    <div class="wfTemp-modal">
      <h2>Upload Error</h2>
      <p>{errorMessage}</p>
      <div class="wfTemp-modal-actions">
        <button onclick={handleForceUpload}>Force Upload</button>
        <button onclick={resetFileSelection}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- ----------- SUCCESS MODAL ---------- -->
{#if showRunSuccessModal}
  <div
    class="wfTemp-modal-backdrop"
    role="dialog"
    aria-modal="true"
    tabindex="0"
    onkeydown={(e) => {
      if (e.key === 'Escape') showRunSuccessModal = false;
    }}
  >
    <div class="wfTemp-modal">
      <h2>Workflow Created</h2>
      <p>{successMessage}</p>
      <div class="wfTemp-modal-actions">
        {#if lastRunWorkflowId}
          <button class="button-primary" onclick={() => { showRunSuccessModal = false; window.location.hash = `#/workflows?open=${lastRunWorkflowId}`; }}>
            Open workflow{lastRunWorkflowName ? ` "${lastRunWorkflowName}"` : ''}
          </button>
        {/if}
        <button class="button-primary" onclick={() => { showRunSuccessModal = false; window.location.hash = '#/workflows'; }}>
          Go to workflows
        </button>
        <button class="button-secondary" onclick={() => (showRunSuccessModal = false)}>
          Close
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- ----------- RUN ERROR MODAL ---------- -->
<!-- Rendered on top of the params modal via `wfTemp-modal-above`
     (z-index bumped) so the user can dismiss the error and land
     back on the params form with their inputs intact. -->
{#if showRunErrorModal}
  <div class="wfTemp-modal-backdrop wfTemp-modal-above" role="dialog" aria-modal="true" tabindex="0"
    onkeydown={(e) => { if (e.key === 'Escape') showRunErrorModal = false; }}>
    <div class="wfTemp-modal wfTemp-error-modal">
      <h2 style="color: #ff5555;">Template Error: Workflow Not Created</h2>
      <pre class="wfTemp-error-trace">{errorMessage}</pre>
      <div class="wfTemp-modal-actions">
        <button class="button-primary"
                onclick={() => { navigator.clipboard?.writeText(errorMessage ?? ''); }}
                title="Copy the full error to the clipboard">Copy</button>
        <button class="button-primary" onclick={() => showRunErrorModal = false}>Close</button>
      </div>
    </div>
  </div>
{/if}

<!-- ----------- PARAMETER MODAL ---------- -->
{#if showParamModal}
  <div class="wfTemp-modal-backdrop">
    <div class="wfTemp-modal">
      <div class="wfTemp-modal-content">
        <div class="wfTemp-modal-title">
          <h2>Run "{selectedTemplate?.name}"</h2>
          <!-- Version picker. Populated by loadParamModalVersions on
               modal open; disabled until at least the current version
               appears in the list (so the operator can't select
               something before the fetch settles). Switching versions
               reloads the form with the new schema and carries over
               userParams by name. -->
          <label class="wfTemp-version-picker">
            <span>Version</span>
            <select
              disabled={paramModalVersions.length === 0}
              value={selectedTemplate?.workflowTemplateId ?? ''}
              onchange={(e) => switchTemplateVersion(parseInt((e.currentTarget as HTMLSelectElement).value, 10))}
            >
              {#if paramModalVersions.length === 0 && selectedTemplate}
                <option value={selectedTemplate.workflowTemplateId}>{selectedTemplate.version}</option>
              {/if}
              {#each paramModalVersions as v (v.workflowTemplateId)}
                <option value={v.workflowTemplateId}>{v.version}{v.hidden ? ' (hidden)' : ''}</option>
              {/each}
            </select>
          </label>
        </div>

        {#if paramModalNote}
          <!-- Provenance banner when the modal was opened via the
               "Launch new workflow from this" shortcut. Makes it
               obvious the fields aren't blank defaults. -->
          <div class="wfTemp-prefill-note">{paramModalNote}</div>
        {/if}
        {#if paramModalDropped.length > 0}
          <!-- Schema drift: the source run captured values for
               parameters that are no longer part of the template's
               current schema (template edited between the source run
               and now). Surface them so the operator knows what was
               left behind rather than silently losing information. -->
          <div class="wfTemp-prefill-drop">
            Some values from the source run don't map to the current template schema and were dropped:
            <code>{paramModalDropped.join(', ')}</code>
          </div>
        {/if}

        {#if showParamErrors}
          <div class="wfTemp-error-message">
            Please fill in all required fields
          </div>
        {/if}
        
        <!-- Parameter input fields.
             `active` is the per-param dependency check; when a
             param's `requires:` clause isn't satisfied by the current
             values, we visually dim the group and lock every input in
             it. The submit handler mirrors this by stripping the
             value on inactive params so a stale entry doesn't trip
             the server-side `requires:` validator. -->
        {#each JSON.parse(selectedTemplate?.paramJson || '[]') as param (param.name)}
          {@const active = isParamActive(param, userParams)}
          <div class="wfTemp-form-group" class:inactive={!active}>
            <label
              for={param.name}
              class:required={param.required}
              class:error={active && showParamErrors && param.required && !userParams[param.name]}
            >
              {param.name}
            </label>

            <div class="wfTemp-input-container">
              {#if param.choices}
                <!-- Dropdown for choice parameters -->
                <div class="wfTemp-select-wrapper">
                  <select
                    id={param.name}
                    bind:value={userParams[param.name]}
                    disabled={!active}
                    class:error={active && showParamErrors && param.required && !userParams[param.name]}
                  >
                    {#if !param.required}
                      <option value="">-- Select --</option>
                    {/if}
                    {#each param.choices as choice}
                      <option value={choice}>{choice}</option>
                    {/each}
                  </select>
                </div>

              {:else if param.type === 'bool'}
                <!-- Checkbox for boolean parameters -->
                <label class="wfTemp-checkbox-label">
                  <input
                    type="checkbox"
                    id={param.name}
                    bind:checked={userParams[param.name]}
                    disabled={!active}
                    class:error={active && showParamErrors && param.required && !userParams[param.name]}
                  />
                </label>

              {:else if param.type === 'int'}
                <!-- Number input for integer parameters -->
                <input
                  type="number"
                  id={param.name}
                  bind:value={userParams[param.name]}
                  disabled={!active}
                  class:error={active && showParamErrors && param.required && !userParams[param.name]}
                  placeholder="Enter number"
                />

              {:else if param.type === 'float'}
                <!-- Number input for float parameters. step="any" lets
                     the browser accept decimals and scientific notation
                     (e.g. 1e-4) — without it the spinner snaps to
                     integers and rejects exponent input. -->
                <input
                  type="number"
                  step="any"
                  id={param.name}
                  bind:value={userParams[param.name]}
                  disabled={!active}
                  class:error={active && showParamErrors && param.required && !userParams[param.name]}
                  placeholder="Enter decimal"
                />

              {:else if param.type === 'text'}
                <!-- text: long / multi-line string. Operator can upload
                     a local file (read client-side via FileReader and
                     embedded as content) OR paste directly into the
                     textarea. The runner sees a multi-line string
                     regardless of how it got there — the "file" path
                     is just CLI/UI tooling, not a schema concern. -->
                <div class="wfTemp-text">
                  <input
                    type="file"
                    disabled={!active}
                    onchange={(e) => {
                      const f = e.target.files && e.target.files[0];
                      if (!f) return;
                      const r = new FileReader();
                      r.onload = () => { userParams[param.name] = r.result; };
                      r.readAsText(f);
                    }}
                  />
                  <textarea
                    id={param.name}
                    rows="6"
                    bind:value={userParams[param.name]}
                    disabled={!active}
                    class:error={active && showParamErrors && param.required && !userParams[param.name]}
                    placeholder={param.help || 'Upload a file or paste content (one item per line)'}
                  ></textarea>
                </div>

              {:else}
                <!-- Text input for other parameters -->
                <input
                  type="text"
                  id={param.name}
                  bind:value={userParams[param.name]}
                  disabled={!active}
                  class:error={active && showParamErrors && param.required && !userParams[param.name]}
                  placeholder={param.help || 'Enter value'}
                />
              {/if}

              {#if param.help}
                <button class="wfTemp-help-button" onclick={() => toggleHelp(param.name)}>
                  ?
                </button>
              {/if}
            </div>

            {#if showHelp[param.name] && param.help}
              <div class="wfTemp-help-text">{param.help}</div>
            {/if}

            {#if active && showParamErrors && param.required && !userParams[param.name]}
              <div class="wfTemp-field-error">This field is required</div>
            {/if}
          </div>
        {/each}
      </div>

      <!-- Advanced run options (collapsed by default). Extend/continue an
           existing workflow instead of creating a new one. -->
      <details class="wfTemp-run-options">
        <summary>Options</summary>
        <div class="wfTemp-run-options-body">
          <label class="wfTemp-run-opt">
            <input type="radio" name="runMode" value="new" bind:group={runMode} />
            New workflow <span class="wfTemp-opt-hint">(default)</span>
          </label>
          <label class="wfTemp-run-opt">
            <input type="radio" name="runMode" value="continue" bind:group={runMode} />
            Continue last run <span class="wfTemp-opt-hint">(extend your most recent run of this template with the same parameters)</span>
          </label>
          <label class="wfTemp-run-opt">
            <input type="radio" name="runMode" value="extend" bind:group={runMode} />
            Extend workflow
            <input
              type="number"
              min="1"
              placeholder="id"
              class="wfTemp-extend-id"
              bind:value={runExtendWorkflowId}
              onfocus={() => (runMode = 'extend')}
            />
          </label>
          <label class="wfTemp-run-opt wfTemp-run-subopt" class:wfTemp-opt-disabled={runMode === 'new'}>
            <input type="checkbox" bind:checked={runRetryFailedOnly} disabled={runMode === 'new'} />
            Retry failed only <span class="wfTemp-opt-hint">(re-run only failed tasks, no cascade)</span>
          </label>
          <label class="wfTemp-run-opt">
            <input type="checkbox" bind:checked={runNoRecruiters} />
            No recruiters <span class="wfTemp-opt-hint">(skip auto-deploy; tasks run only on workers you attach manually)</span>
          </label>
        </div>
      </details>

      <!-- Modal action buttons -->
      <div class="wfTemp-modal-actions">
        <button onclick={handleRunTemplate} disabled={isRunningTemplate} class="wfTemp-run-btn">
          {#if isRunningTemplate}
            <Loader2 size="14" class="wfTemp-spin" />
            Running…
          {:else}
            Run
          {/if}
        </button>
        <button onclick={() => showParamModal = false} disabled={isRunningTemplate}>Cancel</button>
      </div>
    </div>
  </div>
{/if}