/**
 * Lightweight runtime diagnostics for the UI.
 *
 * Exposed on `window.__scitqDebug`. Inspect at any time from DevTools:
 *
 *     window.__scitqDebug.snapshot()        // current counts + rates
 *     window.__scitqDebug.wsByTypeAction    // raw counters
 *     window.__scitqDebug.stepListInstances // currently mounted StepLists
 *     window.__scitqDebug.recentEvents      // last N=200 ws messages (type/action/id)
 *     window.__scitqDebug.verbose = true    // re-enable noisy console.debug logs
 *
 * Counts are cheap (one map increment per event). The periodic summary
 * line is emitted via console.info every 30s so even after a freeze the
 * console buffer carries the last few summaries — useful for "when did
 * the rate spike?" forensics.
 *
 * Defaults `verbose` to false to silence the per-WS-message console.debug
 * spam that historically flooded the DevTools console (and via JSActor
 * IPC, the content process).
 */

export interface DebugSnapshot {
  ts: string;
  uptimeSec: number;
  wsMessagesTotal: number;
  wsMessagesLast30sByTypeAction: Record<string, number>;
  wsMessagesLast30sTotal: number;
  wsMessagesPerSecondLast30s: number;
  stepListMounts: number;
  stepListLiveTimers: number;
  recomputeRunningStatsCalls: number;
}

interface RecentEvent {
  ts: number;
  type: string;
  action?: string;
  id?: number;
}

const RECENT_RING_SIZE = 200;
const SUMMARY_PERIOD_MS = 30_000;

class DebugRegistry {
  // Per-(type+action) accumulators since process start.
  wsByTypeAction: Map<string, number> = new Map();

  // Ring buffer of the last RECENT_RING_SIZE messages — for "what was
  // happening right before the freeze" inspection. Tiny memory cost.
  recentEvents: RecentEvent[] = new Array(RECENT_RING_SIZE);
  private recentNext = 0;
  recentCount = 0;

  // Mount tracking — count vs N expected gives "is something leaking?".
  stepListMounts = 0;
  stepListLiveTimers = 0;
  recomputeRunningStatsCalls = 0;

  // WS connection lifecycle — incremented in wsClient on open/close. The
  // "tab away → tab back → freeze" pattern usually shows up as a close
  // followed by an open, then a burst of messages on the next summary.
  wsConnects = 0;
  wsCloses = 0;
  lastWsConnectAt: string | null = null;
  lastWsCloseAt: string | null = null;

  // Burst detector: if more than this many messages arrive within a 1-second
  // window, log a warning so we can correlate the spike with a UI freeze.
  private burstThreshold = 50;
  private burstWindowMs = 1000;
  private burstWindowStart = Date.now();
  private burstWindowCount = 0;
  bursts: { ts: string; count: number; topByTypeAction: Record<string, number> }[] = [];
  private burstWindowByTypeAction: Map<string, number> = new Map();

  // Per-30s windows.
  private windowStart = Date.now();
  private windowCount = 0;
  private windowByTypeAction: Map<string, number> = new Map();
  private startedAt = Date.now();

  verbose = false; // gate noisy console.debug

  recordWs(message: any) {
    const key = `${message?.type ?? 'unknown'}/${message?.action ?? '-'}`;
    this.wsByTypeAction.set(key, (this.wsByTypeAction.get(key) ?? 0) + 1);
    this.windowByTypeAction.set(key, (this.windowByTypeAction.get(key) ?? 0) + 1);
    this.windowCount++;

    // Loud single-line trace for every non-worker/stats event. Worker
    // stats heartbeats are noisy and irrelevant (filtered by StepList);
    // anything else (step-stats deltas, task transitions, workflow
    // status changes) is rare enough to log individually — and is
    // exactly the kind of event that could spike right before a freeze.
    // Console history preserves these even when the page dies. We log
    // the full payload (truncated if huge) so an unusually-large
    // payload — the kind that could cause a single-event explosion —
    // shows up as a giant log line right before the crash.
    if (key !== 'worker/stats') {
      try {
        const json = JSON.stringify(message);
        const size = json.length;
        console.info('[scitq-debug] ws.' + key,
          'id=' + (message?.id ?? '?'),
          'size=' + size,
          'payload=' + (size > 5000 ? json.slice(0, 5000) + '…(truncated)' : json));
      } catch (e) {
        console.info('[scitq-debug] ws.' + key,
          'id=' + (message?.id ?? '?'),
          '(unserialisable payload)');
      }
    }

    // Burst detection — sliding 1s window.
    const now = Date.now();
    if (now - this.burstWindowStart > this.burstWindowMs) {
      this.burstWindowStart = now;
      this.burstWindowCount = 0;
      this.burstWindowByTypeAction.clear();
    }
    this.burstWindowCount++;
    this.burstWindowByTypeAction.set(key, (this.burstWindowByTypeAction.get(key) ?? 0) + 1);
    if (this.burstWindowCount === this.burstThreshold) {
      // Snapshot the breakdown at the moment of detection — the rest of
      // the burst may continue but the topByTypeAction at threshold is
      // representative.
      const topByTA: Record<string, number> = {};
      for (const [k, v] of this.burstWindowByTypeAction) topByTA[k] = v;
      const entry = { ts: new Date(now).toISOString(), count: this.burstWindowCount, topByTypeAction: topByTA };
      this.bursts.push(entry);
      if (this.bursts.length > 50) this.bursts.shift();
      // Loud console.warn so it's visible without polling the snapshot.
      console.warn('[scitq-debug] WS burst:', this.burstWindowCount, 'msgs in <1s', topByTA);
    }

    const slot: RecentEvent = {
      ts: now,
      type: message?.type,
      action: message?.action,
      id: typeof message?.id === 'number' ? message.id : undefined,
    };
    this.recentEvents[this.recentNext] = slot;
    this.recentNext = (this.recentNext + 1) % RECENT_RING_SIZE;
    if (this.recentCount < RECENT_RING_SIZE) this.recentCount++;
  }

  recordWsOpen() {
    this.wsConnects++;
    this.lastWsConnectAt = new Date().toISOString();
    console.info('[scitq-debug] WS open #' + this.wsConnects);
  }

  recordWsClose(code?: number) {
    this.wsCloses++;
    this.lastWsCloseAt = new Date().toISOString();
    console.info('[scitq-debug] WS close #' + this.wsCloses + ' code=' + (code ?? '?'));
  }

  // Event-loop watchdog. A 100ms setInterval that records how late each
  // tick fires. In a healthy page the gap is ~100ms; if the main thread
  // stalls (long GC, infinite loop in a render, a synchronous DOM-heavy
  // burst), the gap balloons and we log it via console.warn.
  //
  // Crucially, the warning fires AFTER the stall — meaning the message
  // lands in the console buffer right at the moment the page recovers.
  // Combined with the periodic summaries (which also fire after each
  // stall ends), this gives a timeline: "30s summary at T+30s — STALL
  // 4200ms at T+58s — 60s summary at T+62s" tells us exactly when the
  // freeze hit and how long it lasted.
  loopStalls: { ts: string; gapMs: number }[] = [];
  startWatchdog() {
    let lastBeat = performance.now();
    const TICK = 100;
    const STALL_THRESHOLD = 250; // gaps above this count as a stall
    setInterval(() => {
      const now = performance.now();
      const gap = Math.round(now - lastBeat);
      lastBeat = now;
      if (gap > STALL_THRESHOLD) {
        const entry = { ts: new Date().toISOString(), gapMs: gap };
        this.loopStalls.push(entry);
        // Cap retention so the array can't itself become a leak.
        if (this.loopStalls.length > 100) this.loopStalls.shift();
        console.warn('[scitq-debug] event-loop stall:', gap, 'ms');
      }
    }, TICK);
  }

  // In-process probes that don't need IPC and so survive a frozen state.
  // The vmmap snapshot showed 1.08 GB of mostly-opaque VM_ALLOCATE memory
  // (jemalloc-backed) — these probes split out which jemalloc-side bucket
  // is the offender (DOM nodes? layout structs? JS heap?). All three are
  // O(1) reads from already-tracked browser state.
  domNodes = 0;
  domDetachedNodes = 0;  // a tell for detached-tree leaks
  jsHeapMB: number | null = null;
  startMemoryProbes() {
    // Tick every 2s — fast enough to catch the explosion moment, cheap
    // enough that the probes themselves never become hot.
    setInterval(() => {
      try {
        // 1. DOM node count. A typical scitq UI page has ~3,000–10,000
        // nodes; 100k+ would scream "DOM tree leak". O(1) on Firefox —
        // querySelectorAll('*') just counts the document tree.
        this.domNodes = document.getElementsByTagName('*').length;
      } catch (e) { /* never throw out of a probe */ }

      // 2. Detached-tree heuristic: walk the few well-known root
      // attachment points (StepList rows, WorkflowList rows). If their
      // count diverges hard from steps-known-to-exist, something's
      // creating + abandoning rows. Cheap because we only look at our
      // own data-testid-tagged elements.
      try {
        this.domDetachedNodes = document.querySelectorAll(
          '[data-testid^="step-"],[data-testid^="wf-"]'
        ).length;
      } catch (e) { /* idem */ }

      // 3. JS heap size, when the browser exposes it. Chrome has
      // `performance.memory.usedJSHeapSize` (non-standard, behind flag).
      // Firefox exposes nothing equivalent for normal pages. We try; if
      // missing, we just leave jsHeapMB null and rely on (1)+(2) plus
      // OS-level RSS for the JS-heap channel.
      try {
        const pm: any = (performance as any).memory;
        if (pm && typeof pm.usedJSHeapSize === 'number') {
          this.jsHeapMB = Math.round(pm.usedJSHeapSize / 1024 / 1024);
        }
      } catch (e) { /* idem */ }
    }, 2000);
  }

  snapshot(): DebugSnapshot {
    const now = Date.now();
    const windowSec = Math.max(0.001, (now - this.windowStart) / 1000);
    const rate = this.windowCount / windowSec;
    const byTA: Record<string, number> = {};
    for (const [k, v] of this.windowByTypeAction) byTA[k] = v;
    let total = 0;
    for (const v of this.wsByTypeAction.values()) total += v;
    return {
      ts: new Date(now).toISOString(),
      uptimeSec: Math.round((now - this.startedAt) / 1000),
      wsMessagesTotal: total,
      wsMessagesLast30sByTypeAction: byTA,
      wsMessagesLast30sTotal: this.windowCount,
      wsMessagesPerSecondLast30s: Math.round(rate * 10) / 10,
      stepListMounts: this.stepListMounts,
      stepListLiveTimers: this.stepListLiveTimers,
      recomputeRunningStatsCalls: this.recomputeRunningStatsCalls,
      wsConnects: this.wsConnects,
      wsCloses: this.wsCloses,
      lastWsConnectAt: this.lastWsConnectAt,
      lastWsCloseAt: this.lastWsCloseAt,
      burstsRecorded: this.bursts.length,
      loopStallsRecorded: this.loopStalls.length,
      // The 3 most recent stalls, inline in the summary so we don't have
      // to dig into __scitqDebug.loopStalls to see them on a frozen tab.
      recentLoopStalls: this.loopStalls.slice(-3),
      // In-process memory probes (survive a frozen state — no IPC).
      domNodes: this.domNodes,
      ourDomElements: this.domDetachedNodes,
      jsHeapMB: this.jsHeapMB,
    } as any;
  }

  // Periodic summary so a frozen-tab console history still shows what
  // happened in the seconds leading up to the freeze. Emitted as
  // console.info so it lands in the "Info" filter, not "Debug".
  startPeriodicSummary() {
    setInterval(() => {
      const snap = this.snapshot();
      // Reset the 30s window after each summary so rates are
      // representative of the next interval.
      this.windowStart = Date.now();
      this.windowCount = 0;
      this.windowByTypeAction.clear();
      console.info('[scitq-debug]', JSON.stringify(snap));
    }, SUMMARY_PERIOD_MS);
  }
}

const registry = new DebugRegistry();
registry.startPeriodicSummary();
registry.startWatchdog();
registry.startMemoryProbes();

// Expose on window for ad-hoc inspection. The naming uses double
// underscore + scitq so it's unambiguous in DevTools autocomplete.
if (typeof window !== 'undefined') {
  (window as any).__scitqDebug = registry;
}

export const scitqDebug = registry;
