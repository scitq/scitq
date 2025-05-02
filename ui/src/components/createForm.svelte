<script lang="ts">
  import { onMount } from "svelte";
  import { getFlavors, newWorker, getWorkFlow } from "../lib/api";
  import "../styles/createForm.css";
  import { createEventDispatcher } from 'svelte';

  const dispatch = createEventDispatcher();

  let provider = "";
  let flavor = "";
  let region = "";
  let number = 1;
  let concurrency = 1;
  let prefetch = 1;
  let wfStep = "";

  let listFlavor = [];
  let listWf = [];

  // Autocomplete visibility flags
  let showFlavorSuggestions = false;
  let showRegionSuggestions = false;
  let showProviderSuggestions = false;
  let showWfStepSuggestions = false;

  let flavorSuggestions = [];
  let regionSuggestions = [];
  let providerSuggestions = [];
  let wfStepSuggestions = [];

  // Fetch available flavors and workflows on mount
  onMount(async () => {
    listFlavor = await getFlavors();
    listWf = await getWorkFlow(); // récupère tous les workflows
  });

  // Dynamic flavor suggestions
  $: flavorSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.flavorName)
        .filter(name => name?.toLowerCase().includes(flavor.toLowerCase()))
    )
  );

  // Dynamic region suggestions
  $: regionSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.region)
        .filter(name => name?.toLowerCase().includes(region.toLowerCase()))
    )
  );

  // Dynamic provider suggestions
  $: providerSuggestions = Array.from(
    new Set(
      listFlavor
        .map(f => f.provider)
        .filter(name => name?.toLowerCase().includes(provider.toLowerCase()))
    )
  );

  // Dynamic workflow step suggestions
  $: wfStepSuggestions = Array.from(
    new Set(
      listWf
        .map(wf => wf.name)
        .filter(name => name?.toLowerCase().includes(wfStep.toLowerCase()))
    )
  );

  // Select a flavor from suggestions
  function selectFlavor(suggestion: string) {
    flavor = suggestion;
    showFlavorSuggestions = false;
  }

  // Select a region from suggestions
  function selectRegion(suggestion: string) {
    region = suggestion;
    showRegionSuggestions = false;
  }

  // Select a provider from suggestions
  function selectProvider(suggestion: string) {
    provider = suggestion;
    showProviderSuggestions = false;
  }

  // Select a workflow step from suggestions
  function selectWfStep(suggestion: string) {
    wfStep = suggestion;
    showWfStepSuggestions = false;
  }
</script>

<div class="form-container">
  <h2>Create Worker</h2>

  <!-- CONCURRENCY -->
  <div class="form-group">
    <label for="concurrency">Concurrency :</label>
    <input id="concurrency" type="number" bind:value={concurrency} min="0" />
  </div>

  <!-- PREFETCH -->
  <div class="form-group">
    <label for="prefetch">Prefetch :</label>
    <input id="prefetch" type="number" bind:value={prefetch} min="0" />
  </div>

  <!-- FLAVOR -->
  <div class="form-group autocomplete">
    <label for="flavor">Flavor :</label>
    <input
      id="flavor"
      type="text"
      bind:value={flavor}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showFlavorSuggestions = true}
      on:input={() => showFlavorSuggestions = true}
      on:blur={() => setTimeout(() => showFlavorSuggestions = false, 150)}
    />
    {#if showFlavorSuggestions && flavorSuggestions.length > 0}
      <ul class="suggestions">
        {#each flavorSuggestions as suggestion}
          <li>
            <button
              class="suggestion-item"
              type="button"
              on:click={() => selectFlavor(suggestion)}
            >
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- REGION -->
  <div class="form-group autocomplete">
    <label for="region">Region :</label>
    <input
      id="region"
      type="text"
      bind:value={region}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showRegionSuggestions = true}
      on:input={() => showRegionSuggestions = true}
      on:blur={() => setTimeout(() => showRegionSuggestions = false, 150)}
    />
    {#if showRegionSuggestions && regionSuggestions.length > 0}
      <ul class="suggestions">
        {#each regionSuggestions as suggestion}
          <li>
            <button
              class="suggestion-item"
              type="button"
              on:click={() => selectRegion(suggestion)}
            >
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- PROVIDER -->
  <div class="form-group autocomplete">
    <label for="provider">Provider :</label>
    <input
      id="provider"
      type="text"
      bind:value={provider}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showProviderSuggestions = true}
      on:input={() => showProviderSuggestions = true}
      on:blur={() => setTimeout(() => showProviderSuggestions = false, 150)}
    />
    {#if showProviderSuggestions && providerSuggestions.length > 0}
      <ul class="suggestions">
        {#each providerSuggestions as suggestion}
          <li>
            <button
              class="suggestion-item"
              type="button"
              on:click={() => selectProvider(suggestion)}
            >
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- STEP -->
  <div class="form-group autocomplete">
    <label for="step">Step (Workflow.step) : </label>
    <input
      id="step"
      type="text"
      bind:value={wfStep}
      autocomplete="off"
      placeholder="Type to search..."
      on:focus={() => showWfStepSuggestions = true}
      on:input={() => showWfStepSuggestions = true}
      on:blur={() => setTimeout(() => showWfStepSuggestions = false, 150)}
    />
    {#if showWfStepSuggestions && wfStepSuggestions.length > 0}
      <ul class="suggestions">
        {#each wfStepSuggestions as suggestion}
          <li>
            <button
              class="suggestion-item"
              type="button"
              on:click={() => selectWfStep(suggestion)}
            >
              {suggestion}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
  <!--VERIFIER QUE CA SOIT BON MAIS SI ON CREER DIRECT D'ICI, DIRE QUE SI CA N4EXISTE PAS ALORS ENREGISTTRER LE NOM ET A LA FIN IL FAUDRA APPELLER CREATEWORFLOW ET FAIRE UNE POP UP QUI DEMANDE LE NOMRBE DE WORKER A METTRE ETC A VOIR-->

  <!-- NUMBER -->
  <div class="form-group">
    <label for="number">Number :</label>
    <input id="number" type="number" bind:value={number} min="0" />
  </div>

  <!-- ADD -->
  <button
    class="add-button"
    on:click={async () => {
      await newWorker(concurrency, prefetch, flavor, region, provider, number, wfStep);
      dispatch('workerAdded'); // Notify external components that a worker was added
    }}
    aria-label="Add"
    data-testid="add-worker-button"
  >
    Add
  </button>
</div>
