<script lang="ts">
  import { onMount } from 'svelte';
  import Plus from 'lucide-svelte/icons/plus';
  import Check from 'lucide-svelte/icons/check';
  import WfTemplateList from '../components/WfTemplateList.svelte';
  import '../styles/wfTemplate.css';

  let workflowsTemp = [];
  let sortBy: 'name' | 'version' = 'name';
  let selectedFile: File | null = null;
  let fileInput: HTMLInputElement;
  let selectedFileContent: ArrayBuffer | null = null;

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

  function handleValidate() {
    if (!selectedFile) return;

    const reader = new FileReader();

    reader.onload = (event) => {
      selectedFileContent = event.target?.result as ArrayBuffer;

      // Log binary content to the console
      console.log('Binary content (ArrayBuffer):', selectedFileContent);
      console.log('Content as bytes:', new Uint8Array(selectedFileContent));

      const text = new TextDecoder().decode(selectedFileContent);
      console.log('Text content:\n', text);

      resetFileSelection();
    };

    reader.onerror = (event) => {
      console.error('Read error:', event);
    };

    reader.readAsArrayBuffer(selectedFile);
  }

  function resetFileSelection() {
    selectedFile = null;
    fileInput.value = '';
  }
</script>

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
      accept=".json,.xml,.txt" 
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
          on:click={handleValidate}
          disabled={!selectedFile}
        >
          <Check size={20} title="Validate" />
        </button>
      </div>
    </div>
  </div>

  <WfTemplateList {workflowsTemp} />
</div>