<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { wsClient } from '../lib/wsClient';
  import { Plus, Check } from 'lucide-svelte';
  import { getTemplates, UploadTemplates, runTemp } from '../lib/api';
  import WfTemplateList from '../components/WfTemplateList.svelte';
  import '../styles/wfTemplate.css';
  import type { UploadTemplateResponse } from '../lib/types';
  import { Template } from '../../gen/taskqueue';

  let workflowsTemp = [];
  let sortBy: 'template' | 'name' = 'template';
  let selectedFile: File | null = null;
  let fileInput: HTMLInputElement;
  let fileContent: Uint8Array | null = null;
  let uploadResponse: UploadTemplateResponse = {};
  let showErrorModal = false;
  let errorMessage = '';
  let showParamModal = false;
  let selectedTemplate: Template | null = null;
  let userParams: Record<string, any> = {};
  let paramErrors: Record<string, string> = {};
  let showParamErrors = false;
  let showHelp: Record<string, boolean> = {};
  let unsubscribeWS: () => void;

  function handleMessage(message) {
    if (message.type === 'template-uploaded') {
      if (!workflowsTemp.some(t => t.workflowTemplateId === message.payload.ID)) {
        workflowsTemp = [...workflowsTemp, message.payload];
      }
      console.log('Template created via WebSocket:', message.payload);
    }
  }

  onMount(async () => {
    workflowsTemp = await getTemplates();
    unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
  });

  onDestroy(() => {
    unsubscribeWS?.();
  });

  /**
   * Sorts the workflow templates based on the selected sort criteria
   */
  function handleSortBy() {
      if (!workflowsTemp) return [];

      workflowsTemp  = [...workflowsTemp].sort((a, b) => {
          switch (sortBy) {
              case 'template':
                  return (b.workflowTemplateId ?? 0) - (a.workflowTemplateId ?? 0);
              case 'name':
                  return (a.name || '').localeCompare(b.name || '');
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
   * @param {Event} event - The file input change event
   */
  function handleFileChange(event: Event) {
    const files = (event.target as HTMLInputElement).files;
    if (files?.length) {
      selectedFile = files[0];
    }
  }

  /**
   * Reads file content as ArrayBuffer
   * @param {File} file - The file to read
   * @returns {Promise<ArrayBuffer>} Promise that resolves with file content
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
   * @param {string} paramName - The parameter name to toggle help for
   */
  function toggleHelp(paramName: string) {
    showHelp = {...showHelp, [paramName]: !showHelp[paramName]};
  }

  /**
   * Opens the parameter modal and initializes parameter states
   * @param {Template} template - The template to run
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

      const paramJson = JSON.stringify(userParams);
      await runTemp(selectedTemplate.workflowTemplateId, paramJson);
      showParamModal = false;

    } catch (error) {
      console.error("Failed to run template:", error);
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

<!-- ----------- MAIN UI ---------- -->
<div class="wfTemp-container" data-testid="wfTemp-page">
  <div class="wfTemp-header">
    <form class="wfTemp-sort-form" on:submit|preventDefault={() => handleSortBy()}>
      <div class="wfTemp-sort-group">
        <label for="sortBy">Sort by</label>
        <select id="sortBy" bind:value={sortBy} on:change={() => handleSortBy()}>
          <option value="template">Template</option>
          <option value="name">Name</option>
        </select>
      </div>
    </form>

    <input 
      type="file"
      aria-label="File upload"
      bind:this={fileInput}
      on:change={handleFileChange}
      style="display: none;"
    />

    <div class="wfTemp-file-action-group">
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

  <WfTemplateList {workflowsTemp} openParamModal={openParamModal}/>
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

<!-- ----------- PARAM MODAL ---------- -->
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
                <label class="wfTemp-checkbox-label">
                  <input
                    type="checkbox"
                    id={param.name}
                    bind:checked={userParams[param.name]}
                    class:error={showParamErrors && param.required && !userParams[param.name]}
                  />
                </label>
              
              {:else if param.type === 'int'}
                <input
                  type="number"
                  id={param.name}
                  bind:value={userParams[param.name]}
                  class:error={showParamErrors && param.required && !userParams[param.name]}
                  placeholder="Enter number"
                />
              
              {:else}
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

      <div class="wfTemp-modal-actions">
        <button on:click={handleRunTemplate}>Run</button>
        <button on:click={() => showParamModal = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if} 