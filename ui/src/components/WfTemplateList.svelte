<script lang="ts"> 
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronRight, ChevronDown, Play } from 'lucide-svelte';
  import { onMount } from 'svelte';
  
  export let workflowsTemp: Template[] = [];

</script>

{#if workflowsTemp && workflowsTemp.length > 0}
  <div class="wf-list">
    {#each workflowsTemp as t (t.workflowTemplateId)}
      <div class="wf-item" data-testid={`wf-${wf.workflowTemplateId}`}>

        <div class="wf-info">
          <span class="wf-id">#{wf.workflowTemplateId}</span>
          <span class="wf-name">{wf.name}</span>
          <div class="wf-progress-bar"></div>
          <span class="wf-name">{wf.version}</span>
          <span class="wf-name">{wf.uploadedAt}</span>
          <span class="wf-name">{wf.uploadedBy}</span>
        </div>

        <div class="wf-actions">
          <button class="btn-action" title="Play"><Play /></button>
          <button class="btn-action" title="Pause"><PauseCircle /></button>
          <button class="btn-action" title="Reset"><RefreshCw /></button>
          <button class="btn-action" title="Break"><CircleX /></button>
          <button class="btn-action" title="Clear"><Eraser /></button>
        </div>
      </div>


    {/each}
  </div>
{:else}
  <p class="workerCompo-empty-state">No Workflow Template found.</p>
{/if}
