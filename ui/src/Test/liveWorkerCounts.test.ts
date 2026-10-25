// Dashboard task counters fed with the WebSocket sequences a real server
// emits (recorded from a local server running bare tasks).
import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mockApi } from '../mocks/api_mock';
import WorkerCompo from '../components/WorkerCompo.svelte';
import { wsConnect, wsReset, wsPlay, settle, watchText, type FakeWebSocket } from './wsHarness';

const st = (id: number, oldStatus: string, status: string, extra = {}) =>
  ({ type: 'task', action: 'status', id, payload: { taskId: id, workerId: 1, oldStatus, status, ...extra } });

let ws: FakeWebSocket;
async function mountDashboard(worker1: Record<string, number>, global: Record<string, number>) {
  mockApi.getWorkers.mockResolvedValue([{ workerId: 1, name: 'w1', status: 'R', concurrency: 1, prefetch: 0 }]);
  mockApi.getAllTaskStats.mockResolvedValue({
    perWorkerStatusCounts: { 1: worker1 }, globalStatusCounts: global,
    totalCount: Object.values(global).reduce((a, b) => a + b, 0),
  });
  ws = wsConnect();
  render(WorkerCompo);
  await settle();
}
beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  wsReset();
  vi.useRealTimers();
});

describe('dashboard task counters', () => {
  it('never show a state a task passes through in milliseconds', async () => {
    await mountDashboard({ A: 1 }, { A: 1, P: 1 });
    const accepted = watchText(screen.getByTestId('count-C'));
    const uploading = watchText(screen.getByTestId('count-U'));
    const awaiting = watchText(screen.getByTestId('tasks-awaiting-execution-1'));
    const inProgress = watchText(screen.getByTestId('tasks-in-progress-1'));
    const succeeded = watchText(screen.getByTestId('successful-tasks-1'));

    await wsPlay(ws, [
      [0, st(1, 'A', 'C')],
      [9, st(1, 'C', 'R')],
      [310, st(1, 'R', 'U')],
      [18, st(1, 'U', 'S')],
    ]);

    expect(accepted).toEqual(['Accepted: 0']);
    expect(uploading).toEqual(['Uploading: 0']);
    expect(awaiting).toEqual(['']);
    expect(inProgress).toEqual(['', '1', '']);
    expect(succeeded[succeeded.length - 1]).toContain('1');
    expect(succeeded.filter((t) => t.includes('1'))).toHaveLength(succeeded.length - 1);
  });

  it('counts an automatic retry as a new pending task, leaving the failure counted', async () => {
    await mountDashboard({ A: 1 }, { A: 1, P: 1 });
    const failed = watchText(screen.getByTestId('count-F'));
    const pending = watchText(screen.getByTestId('pending-link-dashboard'));

    await wsPlay(ws, [
      [0, st(1, 'A', 'C')],
      [9, st(1, 'C', 'R')],
      [220, st(1, 'R', 'V')],
      [22, st(1, 'V', 'F')],
      [1, { type: 'task', action: 'status', id: 13, payload: { taskId: 13, parentTaskId: 1, workerId: 0, oldStatus: 'F', status: 'P' } }],
    ]);

    expect(failed).toEqual(['Failed: 0', 'Failed: 1']);
    expect(pending).toEqual(['Pending: 1', 'Pending: 2']);
  });

  it('counts a manual retry once: the hidden parent leaves, the new task arrives', async () => {
    await mountDashboard({ A: 2 }, { A: 2, P: 1 });
    const assigned = watchText(screen.getByTestId('count-A'));
    const pending = watchText(screen.getByTestId('pending-link-dashboard'));
    // Task 2 (assigned) is retried by hand: 'hidden' removes it, then the
    // new task arrives in P with the parent's status as oldStatus.
    await wsPlay(ws, [
      [0, { type: 'task', action: 'hidden', id: 2, payload: { taskId: 2, workerId: 1, oldStatus: 'A' } }],
      [1, { type: 'task', action: 'status', id: 14, payload: { taskId: 14, parentTaskId: 2, workerId: 0, oldStatus: 'A', status: 'P' } }],
    ]);
    expect(assigned).toEqual(['Assigned: 2', 'Assigned: 1']);
    expect(pending).toEqual(['Pending: 1', 'Pending: 2']);
  });
});
