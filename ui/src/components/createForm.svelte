<script lang="ts">
  import { onMount } from "svelte";
  import { getFlavors, newWorker, getWorkFlow, getStatus, getSteps } from "../lib/api";
  import "../styles/createForm.css";
  import { WorkerDetails, Workflow } from "../../gen/taskqueue";

  /** 
   * Callback function type triggered when a new worker is created.
   * @callback onWorkerCreated
   * @param {Object} event - Event object containing detail of the new worker.
   * @param {Object} event.detail - Detail object with worker info.
   * @param {WorkerDetails} event.detail.worker - The created worker details.
   */

  /** @type {(event: { detail: { worker: WorkerDetails } }) => void} */
  export let onWorkerCreated = () => {};

  /** @type {string} - Provider name input value */
  let provider = "";

  /** @type {string} - Flavor input value */
  let flavor = "";

  /** @type {string} - Region input value */
  let region = "";

  /** @type {number} - Number of workers to create */
  let number = 1;

  /** @type {number} - Worker concurrency value */
  let concurrency = 1;

  /** @type {number} - Worker prefetch value */
  let prefetch = 1;

  /** @type {string} - Workflow step in format 'Workflow.Step' */
  let wfStep = ""; 

  /** @type {string} - Selected workflow name */
  let wf = "";

  /** @type {string} - Selected step name */
  let step = "";

  /** @type {Array<{ flavorName: string, region: string, provider: string }>} */
  let listFlavor = [];

  /** @type {Workflow[]} - List of workflow objects */
  let listWf = [];

  /** @type {Array<{name: string}>} - List of step objects for selected workflow */
  let listStep: Array<{ name: string }> = [];

  /** @type {boolean} - Indicates whether workflow steps are being loaded */
  let isLoadingSteps = false;

  /** @type {boolean} - Flag to show/hide flavor autocomplete suggestions */
  let showFlavorSuggestions = false;

  /** @type {boolean} - Flag to show/hide region autocomplete suggestions */
  let showRegionSuggestions = false;

  /** @type {boolean} - Flag to show/hide provider autocomplete suggestions */
  let showProviderSuggestions = false;

  /** @type {boolean} - Flag to show/hide workflow autocomplete suggestions */
  let showWfSuggestions = false;

  /** @type {boolean} - Flag to show/hide workflow step suggestions */
  let showStepSuggestions = false;

  /** @type {string[]} - Filtered flavor suggestions based on input */
  let flavorSuggestions = [];

  /** @type {string[]} - Filtered region suggestions based on input */
  let regionSuggestions = [];

  /** @type {string[]} - Filtered provider suggestions based on input */
  let providerSuggestions = [];

  /** @type {string[]} - Filtered workflow name suggestions */
  let wfSuggestions = [];

  /** @type {string[]} - Filtered step name suggestions */
  let stepSuggestions = [];

  /** @type {string|null} - Workflow name currently hovered to fetch steps */
  let hoveredWf: string | null = null;

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
    wf = suggestion;
    showWfSuggestions = false;
  }

  /**
   * Handles hovering over a workflow to load its steps asynchronously.
   * Updates `listStep` and controls loading state.
   * @param {string} wfName - Workflow name hovered by the user
   * @returns {Promise<void>}
   */
  async function handleWfHover(wfName: string): Promise<void> {
    hoveredWf = wfName;
    const selectedWf = listWf.find(w => w.name === wfName);

    if (!selectedWf?.workflowId) {
      console.error("No workflow ID found for", wfName);
      listStep = [];
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
    }
  }

  /**
   * Selects a step from the workflow steps list.
   * Updates the combined wfStep value and hides step suggestions.
   * @param {string} suggestion - Step name selected
   */
  function selectStep(suggestion: string) {
    wfStep = `${hoveredWf}.${suggestion}`;
    step = suggestion;
    showStepSuggestions = false;
    showWfSuggestions = false;
  }

  /**
   * Handles form submission to add a new worker.
   * Creates the worker via backend API, retrieves its status,
   * and emits a `onWorkerCreated` event.
   * Resets form fields afterward.
   * @async
   * @returns {Promise<void>}
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
  <div class="createForm-form-group createForm-autocomplete">
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
      on:input={() => showWfSuggestions = true}
      on:blur={() => setTimeout(() => {
        showWfSuggestions = false;
        showStepSuggestions = false;
        hoveredWf = null;
      }, 150)}
    />

    <div class="suggestions-container">
      {#if showWfSuggestions && wfSuggestions.length > 0}
        <div class="workflow-steps-columns">
          <!-- Workflows -->
          <ul class="createForm-suggestions workflow-list">
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
            <ul 
              class="createForm-suggestions steps-list" 
              style:max-height={listStep.length > 5 ? '150px' : (listStep.length * 30 + 10) + 'px'}
            >
              {#if isLoadingSteps}
                <li class="createForm-suggestion-item">Loading steps...</li>
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
                <li class="createForm-suggestion-item">No steps available</li>
              {/if}
            </ul>
          {/if}
        </div>
      {/if}
    </div>
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