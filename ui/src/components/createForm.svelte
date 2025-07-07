<script lang="ts">
  import { onMount } from "svelte";
  import { getFlavors, newWorker, getWorkFlow, getStatus, getSteps } from "../lib/api";
  import "../styles/createForm.css";
  import { JobId, WorkerDetails, Workflow, Job } from "../../gen/taskqueue";

  /**
   * Callback when new worker (and associated job) is created
   * @param worker Created worker details
   * @param job Associated job record
   */
  export let onWorkerCreated = (event: { detail: { 
    worker: WorkerDetails, 
    job: Partial<Job>  
  } }) => {};

  // Form state
  let provider = "";
  let flavor = "";
  let region = "";
  let number = 1;
  let concurrency = 1;
  let prefetch = 1;
  let wfStep = "";
  let wf = "";
  let step = "";

  // Data stores
  let listFlavor: Array<{ flavorName: string, region: string, provider: string }> = [];
  let listWf: Workflow[] = [];
  let listStep: Array<{ name: string }> = [];

  // UI state
  let isLoadingSteps = false;
  let showFlavorSuggestions = false;
  let showRegionSuggestions = false;
  let showProviderSuggestions = false;
  let showWfSuggestions = false;
  let showStepSuggestions = false;
  let hoveredWf: string | null = null;

  // Filtered suggestions
  let flavorSuggestions: string[] = [];
  let regionSuggestions: string[] = [];
  let providerSuggestions: string[] = [];
  let wfSuggestions: string[] = [];
  let stepSuggestions: string[] = [];

  /**
   * Lifecycle hook that runs on component mount.
   * Fetches initial data for flavors and workflows.
   * @async
   */
  onMount(async () => {
    listFlavor = await getFlavors();
    listWf = await getWorkFlow();
  });

  /** Updates flavor suggestions based on input and available data. */
  $: flavorSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.flavorName)
        .filter(name => name?.toLowerCase().includes(flavor.toLowerCase()))
    )
  );

  /** Updates region suggestions based on input and available data. */
  $: regionSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.region)
        .filter(name => name?.toLowerCase().includes(region.toLowerCase()))
    )
  );

  /** Updates provider suggestions based on input and available data. */
  $: providerSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.provider)
        .filter(name => name?.toLowerCase().includes(provider.toLowerCase()))
    )
  );

  /** Updates workflow name suggestions based on input and available workflows. */
  $: wfSuggestions = Array.from(
    new Set(
      listWf
        .map(wf => wf.name)
        .filter(name => name?.toLowerCase().includes(wf.toLowerCase()))
    )
  );

  /** Updates step suggestions from the fetched listStep, filtered by input. */
  $: stepSuggestions = listStep?.map(s => s.name)?.filter(name =>
    name?.toLowerCase().includes(step.toLowerCase())
  ) || [];

  /**
   * Selects a flavor from the suggestions.
   * @param {string} suggestion - Selected flavor name
   */
  function selectFlavor(suggestion: string) {
    flavor = suggestion;
    showFlavorSuggestions = false;
  }

  /**
   * Selects a region from the suggestions.
   * @param {string} suggestion - Selected region name
   */
  function selectRegion(suggestion: string) {
    region = suggestion;
    showRegionSuggestions = false;
  }

  /**
   * Selects a provider from the suggestions.
   * @param {string} suggestion - Selected provider name
   */
  function selectProvider(suggestion: string) {
    provider = suggestion;
    showProviderSuggestions = false;
  }

  /**
   * Selects a workflow from the suggestions.
   * @param {string} suggestion - Selected workflow name
   */
  function selectWf(suggestion: string) {
    wfStep = suggestion;
    wf = "";
    showWfSuggestions = false;
  }

  /**
   * Handles hovering over a workflow to load its steps asynchronously.
   * Updates `listStep` and controls loading state.
   * @param {string} wfName - Workflow name hovered by the user
   * @returns {Promise<void>}
   */
  let isHandlingHover = false;

  async function handleWfHover(wfName: string): Promise<void> {
      if (isHandlingHover) return;
      isHandlingHover = true;
      
      hoveredWf = wfName;
      const selectedWf = listWf.find(w => w.name === wfName);

      if (!selectedWf?.workflowId) {
          console.error("No workflow ID found for", wfName);
          listStep = [];
          isHandlingHover = false;
          return;
      }

      isLoadingSteps = true;
      try {
          listStep = await getSteps(selectedWf.workflowId) || [];
          showStepSuggestions = listStep.length > 0;
      } catch (error) {
          console.error("Failed to load steps:", error);
          listStep = [];
          showStepSuggestions = false;
      } finally {
          isLoadingSteps = false;
          isHandlingHover = false;
      }
  }

  /**
   * Selects a step from the workflow steps list.
   * Updates the combined wfStep value and hides step suggestions.
   * @param {string} suggestion - Step name selected
   */
function selectStep(suggestion: string) {
    const selectedWf = hoveredWf || wfStep.split('.')[0];
    if (selectedWf) {
        wfStep = `${selectedWf}.${suggestion}`;
        step = suggestion;
        showStepSuggestions = false;
        showWfSuggestions = false;
    } else {
        console.error("No workflow selected");
    }
    hoveredWf = null; // Reset après sélection
}


  /**
   * Create new worker and associated job
   * Resets form on success
   */
  async function handleAddWorker(): Promise<void> {
    const workersDetails = await newWorker(concurrency, prefetch, flavor, region, provider, number, wfStep);
    const workerCreatedDetails = workersDetails[workersDetails.length - 1];
    const statuses = await getStatus([workerCreatedDetails.workerId]);
    const statusObj = statuses[0];
    const workerStatus = statusObj ? statusObj.status : 'unknown';

    onWorkerCreated({
      detail: {
        worker: {
          workerId: workerCreatedDetails.workerId,
          name: workerCreatedDetails.workerName,
          concurrency,
          prefetch,
          status: workerStatus,
          flavor,
          provider,
          region,
        },
        job: {
          JobId: workerCreatedDetails.jobId,
          action: 'C',
          workerId: workerCreatedDetails.workerId,
          modifiedAt : new Date().toLocaleString(),
        }
      }
    });


    // Reset form
    concurrency = 1;
    prefetch = 1;
    flavor = "";
    region = "";
    provider = "";
    number = 1;
    wfStep = "";
  }
</script>


<div class="createForm-form-container">
  <h2 class="createForm-title">Create Worker</h2>

  <!-- Concurrency input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="concurrency">Concurrency:</label>
    <input data-testid="concurrency-createWorker" id="concurrency" class="createForm-input" type="number" bind:value={concurrency} min="0" />
  </div>

  <!-- Prefetch input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="prefetch">Prefetch:</label>
    <input data-testid="prefetch-createWorker" id="prefetch" class="createForm-input" type="number" bind:value={prefetch} min="1" />
  </div>

  <!-- Flavor autocomplete -->
  <div class="createForm-form-group createForm-autocomplete">
    <label class="createForm-label" for="flavor">Flavor:</label>
    <input
      data-testid="flavor-createWorker"
      id="flavor"
      class="createForm-input"
      type="text"
      bind:value={flavor}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showFlavorSuggestions = true}
      on:input={() => showFlavorSuggestions = true}
      on:blur={() => setTimeout(() => showFlavorSuggestions = false, 150)}
    />
    {#if showFlavorSuggestions && flavorSuggestions.length > 0}
      <ul class="createForm-suggestions">
        {#each flavorSuggestions as suggestion}
          <li>
            <button class="createForm-suggestion-item" type="button" on:click={() => selectFlavor(suggestion)}>
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- Region autocomplete -->
  <div class="createForm-form-group createForm-autocomplete">
    <label class="createForm-label" for="region">Region:</label>
    <input
      data-testid="region-createWorker"
      id="region"
      class="createForm-input"
      type="text"
      bind:value={region}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showRegionSuggestions = true}
      on:input={() => showRegionSuggestions = true}
      on:blur={() => setTimeout(() => showRegionSuggestions = false, 150)}
    />
    {#if showRegionSuggestions && regionSuggestions.length > 0}
      <ul class="createForm-suggestions">
        {#each regionSuggestions as suggestion}
          <li>
            <button class="createForm-suggestion-item" type="button" on:click={() => selectRegion(suggestion)}>
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- Provider autocomplete -->
  <div class="createForm-form-group createForm-autocomplete">
    <label class="createForm-label" for="provider">Provider:</label>
    <input
      data-testid="provider-createWorker"
      id="provider"
      class="createForm-input"
      type="text"
      bind:value={provider}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showProviderSuggestions = true}
      on:input={() => showProviderSuggestions = true}
      on:blur={() => setTimeout(() => showProviderSuggestions = false, 150)}
    />
    {#if showProviderSuggestions && providerSuggestions.length > 0}
      <ul class="createForm-suggestions">
        {#each providerSuggestions as suggestion}
          <li>
            <button class="createForm-suggestion-item" type="button" on:click={() => selectProvider(suggestion)}>
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- Workflow step autocomplete -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="step">Step (Workflow.step):</label>
    <input
      data-testid="wfStep-createWorker"
      id="step"
      class="createForm-input"
      type="text"
      bind:value={wfStep}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showWfSuggestions = true}
      on:input={(e) => {
        wf = e.target.value.split('.')[0];
        showWfSuggestions = true;
      }}
      on:blur={() => setTimeout(() => {
        showWfSuggestions = false;
        showStepSuggestions = false;
        hoveredWf = null;
      }, 200)}
    />

    {#if showWfSuggestions && wfSuggestions.length > 0}
      <div class="suggestions-container">
        <div class="workflow-steps-columns">
          <!-- Workflows -->
          <ul class="workflow-list">
            {#each wfSuggestions as suggestionWf}
              <li>
                <button 
                  class="createForm-suggestion-item {hoveredWf === suggestionWf ? 'active' : ''}" 
                  type="button" 
                  on:click={() => selectWf(suggestionWf)}
                  on:mouseenter={() => handleWfHover(suggestionWf)}
                >
                  {suggestionWf}
                </button>
              </li>
            {/each}
          </ul>

          <!-- Steps -->
          {#if hoveredWf}
            <ul class="steps-list">
              {#if isLoadingSteps}
                <li class="loading-message">Loading steps...</li>
              {:else if listStep.length > 0}
                {#each listStep as stepObj (stepObj.name)}
                  <li>
                    <button 
                      class="createForm-suggestion-item" 
                      type="button" 
                      on:click={() => selectStep(stepObj.name)}
                    >
                      {stepObj.name}
                    </button>
                  </li>
                {/each}
              {:else}
                <li class="no-steps-message">No steps available</li>
              {/if}
            </ul>
          {/if}
        </div>
      </div>
    {/if}
  </div>

  <!-- Number of workers to create -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="number">Number:</label>
    <input data-testid="number-createWorker" id="number" class="createForm-input" type="number" bind:value={number} min="0" />
  </div>

  <!-- Submit button -->
  <button class="btn-validate createForm-add-button" on:click={handleAddWorker} aria-label="Add" data-testid="add-worker-button">
    Add
  </button>
</div>