<script lang="ts">
  import { onMount } from "svelte";
  import { getFlavors, newWorker, getWorkFlow, getStatus } from "../lib/api";
  import "../styles/createForm.css";
  import { WorkerDetails } from "../../gen/taskqueue";

  export let onWorkerCreated: (event: { detail: { worker: WorkerDetails } }) => void = () => {};

  // Form field values
  let provider = "";
  let flavor = "";
  let region = "";
  let number = 1;
  let concurrency = 1;
  let prefetch = 1;
  let wfStep = "";

  // Lists retrieved from the backend
  let listFlavor = [];
  let listWf = [];

  // Autocomplete dropdown visibility flags
  let showFlavorSuggestions = false;
  let showRegionSuggestions = false;
  let showProviderSuggestions = false;
  let showWfStepSuggestions = false;

  // Filtered suggestion arrays
  let flavorSuggestions = [];
  let regionSuggestions = [];
  let providerSuggestions = [];
  let wfStepSuggestions = [];

  // Fetch data when the component is mounted
  onMount(async () => {
    listFlavor = await getFlavors();
    listWf = await getWorkFlow();
  });

  // Reactive statements to update suggestion lists based on input
  $: flavorSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.flavorName)
        .filter(name => name?.toLowerCase().includes(flavor.toLowerCase()))
    )
  );

  $: regionSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.region)
        .filter(name => name?.toLowerCase().includes(region.toLowerCase()))
    )
  );

  $: providerSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.provider)
        .filter(name => name?.toLowerCase().includes(provider.toLowerCase()))
    )
  );

  $: wfStepSuggestions = Array.from(
    new Set(
      listWf
        .map(wf => wf.name)
        .filter(name => name?.toLowerCase().includes(wfStep.toLowerCase()))
    )
  );

  // Handle suggestion selection
  function selectFlavor(suggestion: string) {
    flavor = suggestion;
    showFlavorSuggestions = false;
  }

  function selectRegion(suggestion: string) {
    region = suggestion;
    showRegionSuggestions = false;
  }

  function selectProvider(suggestion: string) {
    provider = suggestion;
    showProviderSuggestions = false;
  }

  function selectWfStep(suggestion: string) {
    wfStep = suggestion;
    showWfStepSuggestions = false;
  }

  // Handle form submission to create a new worker
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

    // Reset form fields after submission
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
    <input id="concurrency" class="createForm-input" type="number" bind:value={concurrency} min="0" />
  </div>

  <!-- Prefetch input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="prefetch">Prefetch:</label>
    <input id="prefetch" class="createForm-input" type="number" bind:value={prefetch} min="1" />
  </div>

  <!-- Flavor autocomplete -->
  <div class="createForm-form-group createForm-autocomplete">
    <label class="createForm-label" for="flavor">Flavor:</label>
    <input
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
    <input id="number" class="createForm-input" type="number" bind:value={number} min="0" />
  </div>

  <!-- Submit button -->
  <button class="btn-validate createForm-add-button" on:click={handleAddWorker} aria-label="Add" data-testid="add-worker-button">
    Add
  </button>
</div>
