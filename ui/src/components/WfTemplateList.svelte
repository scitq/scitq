<script lang="ts"> 
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronRight, ChevronDown, Play } from 'lucide-svelte';
  import { onMount } from 'svelte';
  
  export let workflowsTemp: Template[] = [];
  export let openParamModal: (template: Template) => void; 

</script>

{#if workflowsTemp && workflowsTemp.length > 0}
  <div class="wfTemp-list">
    {#each workflowsTemp as t (t.workflowTemplateId)}
      <div class="wf-item" data-testid={`wfTemplate-${t.workflowTemplateId}`}>

        <div class="wf-info">
          <span class="wf-id">#{t.workflowTemplateId}</span>
          <span class="wf-id">{t.name}</span>
          <div class="wf-progress-bar"></div>
          <span class="wf-id">v{t.version}</span>
          <span class="wf-id">
            {new Date(t.uploadedAt).toLocaleDateString('en-US', {
              month: '2-digit',
              day: '2-digit',
              year: 'numeric'
            })}
          </span>
          <span class="wf-id">{t.uploadedBy || 'Unknown'}</span>
        </div>

        <div class="wf-actions">
          <button class="btn-action" title="Play" on:click={() => openParamModal(t)}><Play /></button>
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
