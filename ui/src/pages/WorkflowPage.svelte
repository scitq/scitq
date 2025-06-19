<script lang="ts">
  import { onMount } from 'svelte';
  import { getWorkFlow } from '../lib/api';
  import WorkflowList from '../components/WorkflowList.svelte';
  import '../styles/workflow.css';

  /**
   * List of workflows fetched from the API.
   * @type {Array}
   */
  let workflows = [];

  /**
   * Fetches the workflows on component mount and updates the workflows variable.
   */
  onMount(async () => {
    workflows = await getWorkFlow();
  });
</script>

<div class="wf-container" data-testid="wf-page">
  <!-- Filtered task list -->
  <WorkflowList {workflows} />
</div>
