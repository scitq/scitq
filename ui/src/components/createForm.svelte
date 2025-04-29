<script lang="ts">
  import { onMount } from "svelte";
  import { getFlavors, newWorker } from "../lib/api";
  import "../styles/createForm.css";

  let provider = "";
  let flavor = "";
  let region = "";
  let number = 1;
  let concurrency = 1;
  let prefetch = 1;
  let task = "";

  let listFlavor = [];

  // Autocomplete visibility flags
  let showFlavorSuggestions = false;
  let showRegionSuggestions = false;
  let showProviderSuggestions = false;

  let flavorSuggestions = [];
  let regionSuggestions = [];
  let providerSuggestions = [];

  // Fetch available flavors on mount
  onMount(async () => {
    listFlavor = await getFlavors();
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

  <!-- TASK -->
  <div class="form-group">
    <label for="task">Task :</label>
    <input id="task" type="text" bind:value={task} />
  </div>

  <!-- NUMBER -->
  <div class="form-group">
    <label for="number">Number :</label>
    <input id="number" type="number" bind:value={number} min="0" />
  </div>

  <!-- ADD -->
  <button
    class="add-button"
    on:click={() => newWorker(concurrency, prefetch, flavor, region, provider, number)}
    aria-label="Add"
    data-testid="add-worker-button"
  >
    Add
  </button>
</div>
