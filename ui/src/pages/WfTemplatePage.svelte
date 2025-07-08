<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Check } from 'lucide-svelte';
  import { getTemplates, UploadTemplates, runTemp } from '../lib/api';
  import WfTemplateList from '../components/WfTemplateList.svelte';
  import '../styles/wfTemplate.css';
  import type { UploadTemplateResponse } from '../lib/types'; // adjust the import if needed
  import { Template } from '../../gen/taskqueue';

  let workflowsTemp = [];
  let sortBy: 'name' | 'version' = 'name';
  let selectedFile: File | null = null;
  let fileInput: HTMLInputElement;
  let fileContent: Uint8Array | null = null;
  let uploadResponse: UploadTemplateResponse = {};
  let showErrorModal = false;
  let errorMessage = '';
  let showParamModal = false;
  let selectedTemplate: Template | null = null;
  let userParams: Record<string, any> = {};

  onMount(async () => {
    workflowsTemp = await getTemplates();
  });

  function handleSortBy() {}

  function handleAdd() {
    fileInput.click();
  }

  function handleFileChange(event: Event) {
    const files = (event.target as HTMLInputElement).files;
    if (files?.length) {
      selectedFile = files[0];
    }
  }

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

        // Build a new Template object
        const temp: Template = {
          workflowTemplateId: uploadResponse.workflowTemplateId ?? 0,
          name: uploadResponse.name ?? '',
          version: uploadResponse.version ?? '',
          description: uploadResponse.description ?? '',
          paramJson: uploadResponse.paramJson ?? '',
          uploadedAt: new Date().toISOString()
        };

        // Add to the local list
        workflowsTemp = [...workflowsTemp, temp];
        openParamModal(temp);

      }

    } catch (error) {
      console.error("Error during file upload:", error);
      errorMessage = error.message || "Unknown error occurred.";
      showErrorModal = true;
    }
  }

  function openParamModal(template: Template) {
    selectedTemplate = template;

    try {
      const parsedParams = JSON.parse(template.paramJson || '{}');
      userParams = { ...parsedParams }; // Initialize userParams with the default params
    } catch (error) {
      console.error("Invalid paramJson", error);
      userParams = {};
    }

    showParamModal = true;
  }


  async function handleRunTemplate() {
    try {
      if (!selectedTemplate) return;

      await runTemp(selectedTemplate.workflowTemplateId, userParams);
      console.log("Template run successfully");

    } catch (error) {
      console.error("Failed to run template:", error);
    } finally {
      showParamModal = false;
    }
  }



  function resetFileSelection() {
    selectedFile = null;
    fileInput.value = '';
    fileContent = null;
    uploadResponse = {};
    showErrorModal = false;
    errorMessage = '';
  }

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
          <option value="name">Name</option>
          <option value="version">Version</option>
        </select>
      </div>
    </form>

    <input 
      type="file" 
      accept=".json,.xml,.txt,.py" 
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

  <WfTemplateList {workflowsTemp} />
</div>

<!-- ----------- ERROR MODAL ---------- -->
{#if showErrorModal}
  <div class="modal-backdrop">
    <div class="modal">
      <h2>Upload Error</h2>
      <p>{errorMessage}</p>
      <div class="modal-actions">
        <button on:click={resetFileSelection}>Cancel</button>
        <button on:click={handleForceUpload}>Force Upload</button>
      </div>
    </div>
  </div>
{/if}

<!-- {#if showParamModal}
  <div class="modal-backdrop">
    <div class="modal">
      <h2>Run "{selectedTemplate?.name}"</h2>

      {#each Object.keys(userParams) as param}
        <div class="form-group">
          <label>{param}</label>
          <input 
            type="text" 
            bind:value={userParams[param]}
            placeholder="Enter value"
          />
        </div>
      {/each}


      <div class="modal-actions">
        <button on:click={() => showParamModal = false}>Cancel</button>
        <button on:click={handleRunTemplate}>Run</button>
      </div>
    </div>
  </div>
{/if} -->


<!-- ----------- BASIC MODAL STYLE ---------- -->
<style>
  .modal-backdrop {
    position: fixed;
    top: 0; left: 0; right: 0; bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    justify-content: center;
    align-items: center;
  }

  .modal {
    background: white;
    padding: 1.5rem;
    border-radius: 8px;
    min-width: 300px;
  }

  .modal-actions {
    margin-top: 1rem;
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
  }
</style>
