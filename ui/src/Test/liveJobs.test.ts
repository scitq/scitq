// The dashboard's job list fed with job events as the server emits them.
import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mockApi } from '../mocks/api_mock';
import Dashboard from '../pages/Dashboard.svelte';
import { wsConnect, wsReset, wsPlay, settle, watchText, type FakeWebSocket } from './wsHarness';

const existing = {
  jobId: 1, action: 'C', status: 'S', workerId: 3, workerName: 'old-worker', progression: 100,
  modifiedAt: '2026-10-25T10:00:00Z',
};
const created = {
  type: 'job', action: 'created', id: 2,
  payload: {
    jobId: 2, action: 'C', status: 'P', workerId: 7, workerName: 'new-worker',
    flavorInfo: 'b2-7 — 2 CPU, 7GB mem', progression: 0,
    createdAt: '2026-10-25T11:00:00Z', modifiedAt: '2026-10-25T11:00:00Z',
  },
};
const updated = (payload: object) => ({ type: 'job', action: 'updated', id: 2, payload: { jobId: 2, ...payload } });

let ws: FakeWebSocket;
beforeEach(async () => {
  vi.useFakeTimers();
  mockApi.getJobs.mockResolvedValue([existing]);
  ws = wsConnect();
  render(Dashboard);
  await settle();
});
afterEach(() => {
  wsReset();
  vi.useRealTimers();
});

describe('dashboard job list', () => {
  it('shows a new job complete from its first render, then follows its updates', async () => {
    const container = screen.getByTestId('jobs-container');
    const targets: string[] = [];
    new MutationObserver(() => {
      const cell = container.querySelector('[data-testid="job-target-2"]');
      if (cell) targets.push(cell.textContent!.trim());
    }).observe(container, { subtree: true, childList: true, characterData: true });

    await wsPlay(ws, [[0, created]]);
    const progress = watchText(screen.getByTestId('job-row-2'));
    await wsPlay(ws, [
      [1000, updated({ status: 'R', progression: 40, modifiedAt: '2026-10-25T11:00:10Z' })],
      [1000, updated({ progression: 80 })],
      [1000, updated({ status: 'S', progression: 100 })],
    ]);

    expect(new Set(targets)).toEqual(new Set(['new-worker']));
    expect(progress.filter((t) => t.includes('%')).map((t) => t.match(/(\d+)%/)![1])).toEqual(['0', '40', '80']);
    expect(screen.getByTestId('status-pill-2').getAttribute('title')).toBe(mockApi.getJobStatusText('S'));
  });

  it('keeps a live-added job when another job is deleted', async () => {
    await wsPlay(ws, [
      [0, created],
      [1000, { type: 'job', action: 'deleted', id: 1, payload: { jobId: 1 } }],
    ]);
    expect(screen.queryByTestId('job-row-1')).toBeNull();
    expect(screen.getByTestId('job-row-2')).toBeInTheDocument();
  });

  it('raises the provider alert when a job fails with an auth error', async () => {
    expect(screen.queryByText(/Provider authentication failed/)).toBeNull();
    await wsPlay(ws, [
      [0, created],
      [1000, updated({ status: 'F', errorClass: 'auth', errorMessage: 'invalid client secret', modifiedAt: new Date().toISOString() })],
    ]);
    expect(screen.getByTestId('error-class-2').textContent).toBe('auth');
    expect(screen.getByText(/Provider authentication failed/)).toBeInTheDocument();
  });
});
