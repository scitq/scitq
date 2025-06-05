<script lang="ts">
  import { onMount } from "svelte";
  import { getFlavors, newWorker, getWorkFlow, getStatus } from "../lib/api";
  import "../styles/createForm.css";
  import { WorkerDetails } from "../../gen/taskqueue";

  /**
   * Callback function type triggered when a new worker is created.
   * @callback onWorkerCreated
   * @param {Object} event - Event object containing detail of the new worker.
   * @param {Object} event.detail - Detail object with worker info.
   * @param {WorkerDetails} event.detail.worker - The created worker details.
   */

  /**
   * Event callback prop triggered when a new worker is created.
   * @type {(event: { detail: { worker: WorkerDetails } }) => void}
   */
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

  /** @type {string} - Workflow step input value */
  let wfStep = "";

  /** @type {Array} - List of flavor objects fetched from backend */
  let listFlavor = [];

  /** @type {Array} - List of workflow objects fetched from backend */
  let listWf = [];

  /** @type {boolean} - Flag to show/hide flavor autocomplete suggestions */
  let showFlavorSuggestions = false;

  /** @type {boolean} - Flag to show/hide region autocomplete suggestions */
  let showRegionSuggestions = false;

  /** @type {boolean} - Flag to show/hide provider autocomplete suggestions */
  let showProviderSuggestions = false;

  /** @type {boolean} - Flag to show/hide workflow step autocomplete suggestions */
  let showWfStepSuggestions = false;

  /** @type {string[]} - Filtered flavor suggestions based on user input */
  let flavorSuggestions = [];

  /** @type {string[]} - Filtered region suggestions based on user input */
  let regionSuggestions = [];

  /** @type {string[]} - Filtered provider suggestions based on user input */
  let providerSuggestions = [];

  /** @type {string[]} - Filtered workflow step suggestions based on user input */
  let wfStepSuggestions = [];

  /**
   * Lifecycle hook that runs once when component is mounted.
   * Fetches flavor and workflow data from backend API.
   * @async
   * @returns {Promise<void>}
   */
  onMount(async () => {
    listFlavor = await getFlavors();
    listWf = await getWorkFlow();
  });

  /**
   * Reactive statement updating flavorSuggestions array whenever `flavor` or `listFlavor` changes.
   */
  $: flavorSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.flavorName)
        .filter(name => name?.toLowerCase().includes(flavor.toLowerCase()))
    )
  );

  /**
   * Reactive statement updating regionSuggestions array whenever `region` or `listFlavor` changes.
   */
  $: regionSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.region)
        .filter(name => name?.toLowerCase().includes(region.toLowerCase()))
    )
  );

  /**
   * Reactive statement updating providerSuggestions array whenever `provider` or `listFlavor` changes.
   */
  $: providerSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.provider)
        .filter(name => name?.toLowerCase().includes(provider.toLowerCase()))
    )
  );

  /**
   * Reactive statement updating wfStepSuggestions array whenever `wfStep` or `listWf` changes.
   */
  $: wfStepSuggestions = Array.from(
    new Set(
      listWf
        .map(wf => wf.name)
        .filter(name => name?.toLowerCase().includes(wfStep.toLowerCase()))
    )
  );

  /**
   * Handles selection of a flavor suggestion.
   * Updates the flavor input value and hides the suggestion dropdown.
   * @param {string} suggestion - Selected flavor suggestion
   */
  function selectFlavor(suggestion: string) {
    flavor = suggestion;
    showFlavorSuggestions = false;
  }

  /**
   * Handles selection of a region suggestion.
   * Updates the region input value and hides the suggestion dropdown.
   * @param {string} suggestion - Selected region suggestion
   */
  function selectRegion(suggestion: string) {
    region = suggestion;
    showRegionSuggestions = false;
  }

  /**
   * Handles selection of a provider suggestion.
   * Updates the provider input value and hides the suggestion dropdown.
   * @param {string} suggestion - Selected provider suggestion
   */
  function selectProvider(suggestion: string) {
    provider = suggestion;
    showProviderSuggestions = false;
  }

  /**
   * Handles selection of a workflow step suggestion.
   * Updates the workflow step input value and hides the suggestion dropdown.
   * @param {string} suggestion - Selected workflow step suggestion
   */
  function selectWfStep(suggestion: string) {
    wfStep = suggestion;
    showWfStepSuggestions = false;
  }

  /**
   * Handles form submission to create new worker(s).
   * Calls backend API with current form data, retrieves status,
   * triggers onWorkerCreated event with new worker details,
   * and resets form fields to default values.
   * @async
   * @returns {Promise<void>}
   */
  async function handleAddWorker() {
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

    // Reset form fields
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
      on:focus={() => showWfStepSuggestions = true}
      on:input={() => showWfStepSuggestions = true}
      on:blur={() => setTimeout(() => showWfStepSuggestions = false, 150)}
    />
    {#if showWfStepSuggestions && wfStepSuggestions.length > 0}
      <ul class="createForm-suggestions">
        {#each wfStepSuggestions as suggestion}
          <li>
            <button class="createForm-suggestion-item" type="button" on:click={() => selectWfStep(suggestion)}>
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
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
