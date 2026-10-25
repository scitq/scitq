// Workflow list counters fed with step-stats messages as the server emits
// them: deltas only when a task is created, snapshots (with the workflow's
// totals) whenever a step changes.
import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mockApi } from '../mocks/api_mock';
import { wfCounters } from '../lib/wfCounters';
import WorkflowPage from '../pages/WorkflowPage.svelte';
import { wsConnect, wsReset, wsPlay, settle, watchAttr, type FakeWebSocket } from './wsHarness';

const snapshot = (succeeded: number, running: number, failed = 0) => ({
  type: 'step-stats', action: 'snapshot', id: 5,
  payload: {
    steps: [{ stepId: 9, totalTasks: 3, successfulTasks: succeeded, runningTasks: running }],
    workflow: { totalTasks: 3, succeededTasks: succeeded, failedTasks: failed, runningTasks: running, retryingTasks: 0 },
  },
});
const delta = (oldStatus: string | null, newStatus: string) => ({
  type: 'step-stats', action: 'delta', id: 5,
  payload: { workflowId: 5, stepId: 9, oldStatus, newStatus },
});

let ws: FakeWebSocket;
beforeEach(async () => {
  vi.useFakeTimers();
  wfCounters.clear();
  mockApi.getWorkFlow.mockResolvedValue([{
    workflowId: 5, name: 'wf', status: 'R', totalTasks: 3, succeededTasks: 0, failedTasks: 0, runningTasks: 0, retryingTasks: 0,
  }]);
  mockApi.getWorkers.mockResolvedValue([]);
  mockApi.getListUser.mockResolvedValue([]);
  ws = wsConnect();
  render(WorkflowPage);
  await settle();
});
afterEach(() => {
  wsReset();
  vi.useRealTimers();
});

describe('workflow list counters', () => {
  it('follow the snapshots, whatever deltas arrive', async () => {
    const title = watchAttr(screen.getByTestId('wf-progress-5'), 'title');
    await wsPlay(ws, [
      [0, snapshot(0, 1)],
      [500, delta('P', 'A')],
      [500, snapshot(1, 1)],
      [500, snapshot(2, 1)],
      [500, snapshot(2, 0, 1)],
    ]);
    expect(title).toEqual([
      '0/3 succeeded, 0 failed, 0 running',
      '0/3 succeeded, 0 failed, 1 running',
      '1/3 succeeded, 0 failed, 1 running',
      '2/3 succeeded, 0 failed, 1 running',
      '2/3 succeeded, 1 failed, 0 running',
    ]);
  });
});
