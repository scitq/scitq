<script lang="ts">
  import { onMount } from 'svelte';
  import { X, Trash } from 'lucide-svelte';
  import {
    listRecruiters,
    createRecruiter,
    updateRecruiter,
    deleteRecruiter,
    type RecruiterFields,
  } from '../lib/api';

  // ---------------------------------------------------------------------------
  // Recruiter management modal — mirrors `scitq recruiter update/create` 1:1.
  //
  // A step's recruiters are keyed by (step_id, rank); a step can carry several
  // with different ranks (the recruiter loop tries them in rank order). The
  // dropdown at the top lets the operator pick which rank to edit, or create
  // a new one at rank N+1 where N is the highest existing rank.
  //
  // Field set mirrors the CLI's recruiter update flags exactly. Optional
  // fields are sent only when filled — empty input ≡ "don't change" on
  // update; "don't set" on create. Server enforces the same validation
  // as the CLI (concurrency OR per-task resources; prefetch OR
  // prefetch_percent; rounds > 0).
  // ---------------------------------------------------------------------------

  interface Props {
    stepId: number;
    stepName?: string;
    workflowMaximumWorkers?: number | null;
    onClose: () => void;
  }

  let { stepId, stepName = '', workflowMaximumWorkers = null, onClose }: Props = $props();

  // The "+ New" sentinel for the dropdown. Stored as a non-numeric string so
  // it round-trips through <select> without colliding with a real rank value.
  const NEW_RANK_SENTINEL = '__new__';

  let recruiters: taskqueue.Recruiter[] = $state([]);
  let selectedRank: number | typeof NEW_RANK_SENTINEL = $state(NEW_RANK_SENTINEL);
  let loading = $state(true);
  let errorMessage = $state('');
  let saving = $state(false);

  // Form state — independent from `recruiters`, edited freely. Strings (vs
  // numbers) so empty input is distinguishable from 0 — important for the
  // "absent ⇒ don't change" semantic on update.
  let form: Record<keyof RecruiterFields, string> = $state(blankForm());

  function blankForm(): Record<keyof RecruiterFields, string> {
    return {
      protofilter: '',
      concurrency: '',
      prefetch: '',
      prefetchPercent: '',
      cpuPerTask: '',
      memoryPerTask: '',
      diskPerTask: '',
      concurrencyMin: '',
      concurrencyMax: '',
      maxWorkers: '',
      // Match the CLI's create defaults so the form previews what would
      // actually land if the user just clicks Save.
      rounds: '10',
      timeout: '300',
    };
  }

  function recruiterToForm(r: taskqueue.Recruiter): Record<keyof RecruiterFields, string> {
    return {
      protofilter: r.protofilter ?? '',
      concurrency: r.concurrency != null ? String(r.concurrency) : '',
      prefetch: r.prefetch != null ? String(r.prefetch) : '',
      prefetchPercent: r.prefetchPercent != null ? String(r.prefetchPercent) : '',
      cpuPerTask: r.cpuPerTask != null ? String(r.cpuPerTask) : '',
      memoryPerTask: r.memoryPerTask != null ? String(r.memoryPerTask) : '',
      diskPerTask: r.diskPerTask != null ? String(r.diskPerTask) : '',
      concurrencyMin: r.concurrencyMin != null ? String(r.concurrencyMin) : '',
      concurrencyMax: r.concurrencyMax != null ? String(r.concurrencyMax) : '',
      maxWorkers: r.maxWorkers != null ? String(r.maxWorkers) : '',
      rounds: r.rounds != null ? String(r.rounds) : '10',
      timeout: r.timeout != null ? String(r.timeout) : '300',
    };
  }

  // Convert the string form to a RecruiterFields payload. Empty strings
  // become `undefined` (= field absent from the request); numeric fields
  // get parsed.
  function formToFields(): RecruiterFields {
    const numOrUndef = (s: string): number | undefined => {
      if (s === '' || s == null) return undefined;
      const n = Number(s);
      return Number.isFinite(n) ? n : undefined;
    };
    return {
      protofilter: form.protofilter || undefined,
      concurrency: numOrUndef(form.concurrency),
      prefetch: numOrUndef(form.prefetch),
      prefetchPercent: numOrUndef(form.prefetchPercent),
      cpuPerTask: numOrUndef(form.cpuPerTask),
      memoryPerTask: numOrUndef(form.memoryPerTask),
      diskPerTask: numOrUndef(form.diskPerTask),
      concurrencyMin: numOrUndef(form.concurrencyMin),
      concurrencyMax: numOrUndef(form.concurrencyMax),
      maxWorkers: numOrUndef(form.maxWorkers),
      rounds: numOrUndef(form.rounds),
      timeout: numOrUndef(form.timeout),
    };
  }

  async function reload() {
    loading = true;
    errorMessage = '';
    try {
      recruiters = await listRecruiters(stepId);
      if (recruiters.length === 0) {
        // No recruiters yet — straight into create mode at rank 1.
        selectedRank = NEW_RANK_SENTINEL;
        form = blankForm();
      } else {
        selectedRank = recruiters[0].rank;
        form = recruiterToForm(recruiters[0]);
      }
    } catch (e) {
      errorMessage = String(e);
    } finally {
      loading = false;
    }
  }

  onMount(reload);

  // Switching selection: reload form values. (A): when "+ New" is picked,
  // form goes to defaults — not a copy of the previously-selected rank.
  // That's the explicit decision: predictable blank slate.
  function onSelectChange() {
    if (selectedRank === NEW_RANK_SENTINEL) {
      form = blankForm();
    } else {
      const r = recruiters.find(r => r.rank === selectedRank);
      if (r) form = recruiterToForm(r);
    }
  }

  let nextRank = $derived(
    recruiters.length === 0 ? 1 : Math.max(...recruiters.map(r => r.rank)) + 1
  );

  let isCreating = $derived(selectedRank === NEW_RANK_SENTINEL);

  // Display flag for the "will auto-bump workflow max" hint. Only meaningful
  // when the user is editing max_workers AND a workflow cap is known.
  let willBumpWorkflowMax = $derived((() => {
    const mw = Number(form.maxWorkers);
    if (!Number.isFinite(mw) || mw <= 0) return false;
    if (workflowMaximumWorkers == null) return false;
    return mw > workflowMaximumWorkers;
  })());

  async function onSave() {
    saving = true;
    errorMessage = '';
    try {
      const fields = formToFields();
      if (isCreating) {
        await createRecruiter(stepId, nextRank, fields);
      } else {
        await updateRecruiter(stepId, selectedRank as number, fields);
      }
      // Refresh to pick up server-side normalisation (e.g. default rounds/
      // timeout on create) and to expose the newly-created rank in the
      // dropdown.
      await reload();
      // If we just created, pick that one so the operator sees what they
      // just saved.
      if (isCreating) {
        const created = recruiters.find(r => r.rank === nextRank - 1)
                     ?? recruiters[recruiters.length - 1];
        if (created) {
          selectedRank = created.rank;
          form = recruiterToForm(created);
        }
      }
    } catch (e: any) {
      errorMessage = e?.message ?? String(e);
    } finally {
      saving = false;
    }
  }

  async function onDelete() {
    if (isCreating) return;
    const rank = selectedRank as number;
    if (!confirm(`Delete recruiter at rank ${rank}? Tasks already assigned by it keep running; only future assignments stop.`)) {
      return;
    }
    saving = true;
    errorMessage = '';
    try {
      await deleteRecruiter(stepId, rank);
      await reload();
    } catch (e: any) {
      errorMessage = e?.message ?? String(e);
    } finally {
      saving = false;
    }
  }

  function onBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget) onClose();
  }
</script>

<div class="recruiter-modal-backdrop"
     role="dialog" aria-modal="true" tabindex="-1"
     onclick={onBackdropClick}
     onkeydown={(e) => e.key === 'Escape' && onClose()}>
  <div class="recruiter-modal-card">
    <div class="recruiter-modal-header">
      <h2>Recruiter — {stepName || `step ${stepId}`}</h2>
      <button class="recruiter-modal-close" onclick={onClose} aria-label="Close">
        <X size="18" />
      </button>
    </div>

    <div class="recruiter-modal-body">
      {#if loading}
        <p>Loading…</p>
      {:else}
        <div class="recruiter-row">
          <label class="recruiter-label" for="recruiter-rank-select">Recruiter:</label>
          <select id="recruiter-rank-select"
                  bind:value={selectedRank} onchange={onSelectChange} disabled={saving}>
            {#each recruiters as r (r.rank)}
              <option value={r.rank}>Rank {r.rank}</option>
            {/each}
            <option value={NEW_RANK_SENTINEL}>
              + New (will become rank {nextRank})
            </option>
          </select>
          {#if !isCreating}
            <button class="recruiter-danger-btn"
                    onclick={onDelete}
                    disabled={saving}
                    title="Delete this recruiter">
              <Trash size="14" />
            </button>
          {/if}
        </div>

        {#if errorMessage}
          <div class="recruiter-error">{errorMessage}</div>
        {/if}

        <h3 class="recruiter-section">Basics</h3>
        <div class="recruiter-grid">
          <label>
            Filter
            <input type="text" bind:value={form.protofilter}
                   placeholder="cpu>=8:mem>=30" disabled={saving} />
          </label>
          <label>
            Max workers
            <input type="number" min="0" step="1" bind:value={form.maxWorkers}
                   placeholder="(unset = no cap)" disabled={saving} />
          </label>

          <label>
            Concurrency
            <input type="number" min="0" step="1" bind:value={form.concurrency}
                   placeholder="(or set per-task below)" disabled={saving} />
          </label>
          <label>
            Prefetch
            <input type="number" min="0" step="1" bind:value={form.prefetch}
                   placeholder="(or set %)" disabled={saving} />
          </label>

          <label>
            Prefetch %
            <input type="number" min="0" max="100" step="1" bind:value={form.prefetchPercent}
                   placeholder="alt. to prefetch" disabled={saving} />
          </label>
          <label>
            Rounds
            <input type="number" min="1" step="1" bind:value={form.rounds}
                   disabled={saving} />
          </label>

          <label>
            Timeout (s)
            <input type="number" min="1" step="1" bind:value={form.timeout}
                   disabled={saving} />
          </label>
          <span></span>
        </div>

        <h3 class="recruiter-section">Per-task resources <span class="recruiter-hint">(alternative to fixed concurrency — server derives it adaptively)</span></h3>
        <div class="recruiter-grid">
          <label>
            CPU per task
            <input type="number" min="0" step="1" bind:value={form.cpuPerTask}
                   disabled={saving} />
          </label>
          <label>
            Memory per task (GB)
            <input type="number" min="0" step="0.1" bind:value={form.memoryPerTask}
                   disabled={saving} />
          </label>

          <label>
            Disk per task (GB)
            <input type="number" min="0" step="0.1" bind:value={form.diskPerTask}
                   disabled={saving} />
          </label>
          <span></span>

          <label>
            Concurrency min
            <input type="number" min="0" step="1" bind:value={form.concurrencyMin}
                   disabled={saving} />
          </label>
          <label>
            Concurrency max
            <input type="number" min="0" step="1" bind:value={form.concurrencyMax}
                   disabled={saving} />
          </label>
        </div>

        {#if willBumpWorkflowMax}
          <p class="recruiter-bump-hint">
            ℹ Setting max_workers ({form.maxWorkers}) above the workflow's current cap
            ({workflowMaximumWorkers}) will auto-bump the workflow's maximum_workers.
          </p>
        {/if}
      {/if}
    </div>

    <div class="recruiter-modal-footer">
      <button class="recruiter-secondary-btn" onclick={onClose} disabled={saving}>Cancel</button>
      <button class="recruiter-primary-btn" onclick={onSave} disabled={loading || saving}>
        {#if saving}Saving…{:else if isCreating}Create{:else}Save{/if}
      </button>
    </div>
  </div>
</div>

<style>
  .recruiter-modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .recruiter-modal-card {
    background: var(--bg-primary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    width: min(680px, 92vw);
    max-height: 90vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.25);
  }
  .recruiter-modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border-color);
  }
  .recruiter-modal-header h2 {
    margin: 0;
    font-size: 1rem;
  }
  .recruiter-modal-close {
    background: transparent;
    border: none;
    color: var(--text-primary);
    cursor: pointer;
    padding: 4px;
    display: inline-flex;
    align-items: center;
  }
  .recruiter-modal-body {
    padding: 0.75rem 1rem;
    overflow-y: auto;
  }
  .recruiter-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }
  .recruiter-row select {
    flex: 1;
    padding: 4px 6px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
  }
  .recruiter-label {
    font-weight: 600;
    color: var(--text-secondary, var(--text-primary));
  }
  .recruiter-section {
    font-size: 0.85rem;
    margin: 1rem 0 0.4rem;
    border-bottom: 1px solid var(--border-color);
    padding-bottom: 0.25rem;
  }
  .recruiter-hint {
    font-weight: normal;
    font-size: 0.75rem;
    color: var(--text-secondary, var(--text-primary));
  }
  .recruiter-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.4rem 0.75rem;
  }
  .recruiter-grid label {
    display: flex;
    flex-direction: column;
    font-size: 0.78rem;
    color: var(--text-secondary, var(--text-primary));
    gap: 2px;
  }
  .recruiter-grid input {
    padding: 4px 6px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    font-size: 0.85rem;
  }
  .recruiter-grid input:disabled {
    opacity: 0.6;
  }
  .recruiter-bump-hint {
    margin-top: 0.6rem;
    padding: 0.4rem 0.6rem;
    background: var(--bg-secondary);
    border-left: 3px solid var(--primary-color, #4caf50);
    font-size: 0.78rem;
    border-radius: 3px;
  }
  .recruiter-error {
    color: #c93b3b;
    background: var(--bg-secondary);
    padding: 0.4rem 0.6rem;
    border-radius: 3px;
    font-size: 0.82rem;
    margin: 0.4rem 0;
    white-space: pre-wrap;
  }
  .recruiter-modal-footer {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    border-top: 1px solid var(--border-color);
  }
  .recruiter-primary-btn {
    padding: 5px 14px;
    background: var(--primary-color, #4caf50);
    color: white;
    border: none;
    border-radius: 4px;
    cursor: pointer;
  }
  .recruiter-primary-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  .recruiter-secondary-btn {
    padding: 5px 14px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    cursor: pointer;
  }
  .recruiter-danger-btn {
    padding: 4px 8px;
    background: transparent;
    color: #c93b3b;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
  }
  .recruiter-danger-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
