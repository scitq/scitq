<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { wsClient } from '../lib/wsClient';
  import { Plus, Check } from 'lucide-svelte';
  import { getTemplates, UploadTemplates, runTemp, updateTemplateHidden } from '../lib/api';
  import WfTemplateList from '../components/WfTemplateList.svelte';
  import '../styles/wfTemplate.css';
  import type { UploadTemplateResponse } from '../lib/types';
  import { Template } from '../../gen/taskqueue';

  let showRunSuccessModal = false;
  let showRunErrorModal = false;
  let successMessage = '';

  // Array of workflow templates
  let workflowsTemp = [];
  // Current sorting method ('template' or 'name'). Default 'name' so the
  // listing is alphabetical out of the box; the dropdown lets the operator
  // switch to 'template' (id desc) when they want chronological / newest-
  // upload-first order.
  let sortBy: 'template' | 'name' = 'name';
  // Currently selected file for upload
  let selectedFile: File | null = null;
  // Reference to file input element
  let fileInput: HTMLInputElement;
  // Content of selected file as Uint8Array
  let fileContent: Uint8Array | null = null;
  // Response from template upload API
  let uploadResponse: UploadTemplateResponse = {};
  // Whether to show error modal
  let showErrorModal = false;
  // Error message to display
  let errorMessage = '';
  // Whether to show parameter modal
  let showParamModal = false;
  // Currently selected template for parameter input
  let selectedTemplate: Template | null = null;
  // User-provided parameter values
  let userParams: Record<string, any> = {};
  // Parameter validation errors
  let paramErrors: Record<string, string> = {};
  // Whether to show parameter errors
  let showParamErrors = false;
  // Tracks which parameter help texts are visible
  let showHelp: Record<string, boolean> = {};
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
  let showHidden = false;

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

    showParamModal = true;
  }

  /**
   * Validates parameters and runs the selected template
   * @async
   */
  async function handleRunTemplate() {
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

      // Only include paramJson if userParams is non-empty
      const hasParams = Object.keys(userParams).length > 0;
      const res = hasParams
        ? await runTemp(selectedTemplate.workflowTemplateId, JSON.stringify(userParams))
        : await runTemp(selectedTemplate.workflowTemplateId, '{}');

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
    <form class="wfTemp-sort-form" on:submit|preventDefault={() => handleSortBy()}>
      <div class="wfTemp-sort-group">
        <label for="sortBy">Sort by</label>
        <select id="sortBy" bind:value={sortBy} on:change={() => handleSortBy()}>
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
      on:change={handleFileChange}
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
          <button class="wfTemp-clear-file" on:click={resetFileSelection} title="Clear selected file">
            &times;
          </button>
        {/if}
      </div>
      
      <!-- Action buttons -->
      <div class="wfTemp-button-group">
        <button class="wfTemp-action-button wfTemp-add-button" on:click={handleAdd}>
          <Plus size={20} title="Add Workflow" />
        </button>
        <button 
          class="wfTemp-action-button wfTemp-validate-button" 
          on:click={() => handleValidate(false)}
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
    <input type="checkbox" bind:checked={showHidden} on:change={reloadTemplates} />
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
        <button on:click={handleForceUpload}>Force Upload</button>
        <button on:click={resetFileSelection}>Cancel</button>
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
    on:keydown={(e) => {
      if (e.key === 'Escape') showRunSuccessModal = false;
    }}
  >
    <div class="wfTemp-modal">
      <h2>Workflow Created</h2>
      <p>{successMessage}</p>
      <div class="wfTemp-modal-actions">
        <button class="button-primary" on:click={() => { showRunSuccessModal = false; window.location.hash = '#/workflows'; }}>
          Go to workflows
        </button>
        <button class="button-secondary" on:click={() => (showRunSuccessModal = false)}>
          Close
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- ----------- RUN ERROR MODAL ---------- -->
{#if showRunErrorModal}
  <div class="wfTemp-modal-backdrop" role="dialog" aria-modal="true" tabindex="0"
    on:keydown={(e) => { if (e.key === 'Escape') showRunErrorModal = false; }}>
    <div class="wfTemp-modal wfTemp-error-modal">
      <h2 style="color: #ff5555;">Template Error: Workflow Not Created</h2>
      <p>{errorMessage}</p>
      <div class="wfTemp-modal-actions">
        <button class="button-primary" on:click={() => showRunErrorModal = false}>Close</button>
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
                <button class="wfTemp-help-button" on:click={() => toggleHelp(param.name)}>
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

      <!-- Modal action buttons -->
      <div class="wfTemp-modal-actions">
        <button on:click={handleRunTemplate}>Run</button>
        <button on:click={() => showParamModal = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if}