<script lang="ts">
  import { preventDefault } from 'svelte/legacy';

  import { onMount, onDestroy, tick } from 'svelte';
  import { wsClient } from '../lib/wsClient';
  import { Plus, Check, Loader2 } from 'lucide-svelte';
  import { getTemplates, UploadTemplates, runTemp, updateTemplateHidden } from '../lib/api';
  import WfTemplateList from '../components/WfTemplateList.svelte';
  import '../styles/wfTemplate.css';
  import type { UploadTemplateResponse } from '../lib/types';
  import { Template } from '../../gen/taskqueue';

  let showRunSuccessModal = $state(false);
  let showRunErrorModal = $state(false);
  let successMessage = $state('');
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
  });

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
   * Opens parameter modal and initializes parameter states
   * @param {Template} template - Template to run
   */
  function openParamModal(template: Template) {
    selectedTemplate = template;
    paramErrors = {};
    showParamErrors = false;
    showHelp = {};
    try {
      const parsedParams = JSON.parse(template.paramJson || '[]');

      if (!Array.isArray(parsedParams)) {
        throw new Error('paramJson should be an array');
      }

      userParams = {};
      parsedParams.forEach(param => {
        if (param.name) {
          userParams[param.name] = param.default ?? '';
          showHelp[param.name] = false;
          if (param.required && !param.default) {
            paramErrors[param.name] = 'This field is required';
          }
        }
      });

    } catch (error) {
      console.error("Error parsing paramJson:", error);
      userParams = {};
    }

    // Reset advanced run options each time the modal opens.
    runMode = 'new';
    runExtendWorkflowId = null;
    runRetryFailedOnly = false;

    showParamModal = true;
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
        showParamModal = false;
        showRunErrorModal = true;
        return;
      }

      // ✅ Success case
      successMessage = '✅ Template run created successfully!';
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
      showParamModal = false;
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
{#if showRunErrorModal}
  <div class="wfTemp-modal-backdrop" role="dialog" aria-modal="true" tabindex="0"
    onkeydown={(e) => { if (e.key === 'Escape') showRunErrorModal = false; }}>
    <div class="wfTemp-modal wfTemp-error-modal">
      <h2 style="color: #ff5555;">Template Error: Workflow Not Created</h2>
      <p>{errorMessage}</p>
      <div class="wfTemp-modal-actions">
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
        <h2>Run "{selectedTemplate?.name}"</h2>
        
        {#if showParamErrors}
          <div class="wfTemp-error-message">
            Please fill in all required fields
          </div>
        {/if}
        
        <!-- Parameter input fields -->
        {#each JSON.parse(selectedTemplate?.paramJson || '[]') as param (param.name)}
          <div class="wfTemp-form-group">
            <label 
              for={param.name}
              class:required={param.required}
              class:error={showParamErrors && param.required && !userParams[param.name]}
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
                    class:error={showParamErrors && param.required && !userParams[param.name]}
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
                    class:error={showParamErrors && param.required && !userParams[param.name]}
                  />
                </label>
              
              {:else if param.type === 'int'}
                <!-- Number input for integer parameters -->
                <input
                  type="number"
                  id={param.name}
                  bind:value={userParams[param.name]}
                  class:error={showParamErrors && param.required && !userParams[param.name]}
                  placeholder="Enter number"
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
                    class:error={showParamErrors && param.required && !userParams[param.name]}
                    placeholder={param.help || 'Upload a file or paste content (one item per line)'}
                  ></textarea>
                </div>

              {:else}
                <!-- Text input for other parameters -->
                <input
                  type="text"
                  id={param.name}
                  bind:value={userParams[param.name]}
                  class:error={showParamErrors && param.required && !userParams[param.name]}
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
            
            {#if showParamErrors && param.required && !userParams[param.name]}
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