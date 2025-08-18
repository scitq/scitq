<script lang="ts"> 
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronRight, ChevronDown, Play } from 'lucide-svelte';
  import { onMount } from 'svelte';
  
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

</script>

{#if workflowsTemp && workflowsTemp.length > 0}
  <!-- Workflow templates list container -->
  <div class="wfTemp-list">
    {#each workflowsTemp as t (t.workflowTemplateId)}
      <!-- Individual workflow template item -->
      <div class="wf-item" data-testid={`wfTemplate-${t.workflowTemplateId}`}>

        <!-- Workflow template information section -->
        <div class="wf-info">
          <!-- Template ID -->
          <span class="wf-id">#{t.workflowTemplateId}</span>
          <!-- Template name -->
          <span class="wf-id">{t.name}</span>
          <!-- Progress bar placeholder -->
          <div class="wf-progress-bar"></div>
          <!-- Template version -->
          <span class="wf-id">v{t.version}</span>
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
        </div>
      </div> 

    {/each}
  </div>
{:else}
  <!-- Empty state message when no templates exist -->
  <p class="workerCompo-empty-state">No Workflow Template found.</p>
{/if}