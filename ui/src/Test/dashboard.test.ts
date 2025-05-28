
import { mockApi } from '../mocks/api_mock';
vi.mock('../lib/api', () => mockApi);

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import Dashboard from '../pages/Dashboard.svelte';


// Define types for task status
interface TaskStatus {
  pending: number;
  assigned: number;
  accepted: number;
  downloading: number;
  running: number;
  uploadingSuccess: number;
  succeeded: number;
  uploadingFailure: number;
  failed: number;
  suspended: number;
  canceled: number;
  waiting: number;
}

interface TasksStatus {
  [workerId: number]: TaskStatus;
}

// Example task status data for testing
const tasksStatus = {
  1: {
    pending: 3,
    assigned: 2,
    accepted: 1,
    downloading: 4,
    running: 5,
    uploadingSuccess: 6,
    succeeded: 7,
    uploadingFailure: 8,
    failed: 9,
    suspended: 10,
    canceled: 11,
    waiting: 12,
  }
};

describe('Worker integration', () => {
  beforeEach(() => {
    mockApi.getWorkers.mockResolvedValue([
      { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1' }
    ]);
    mockApi.getStats.mockResolvedValue({});
    mockApi.getStatus.mockResolvedValue([{ workerId: 1, status: 'online' }]);
    mockApi.getTasks.mockReturnValue({
      pending: () => 0,
      assigned: () => 0,
      accepted: () => 0,
      downloading: () => 0,
      waiting: () => 0,
      running: () => 0,
      uploadingSuccess: () => 0,
      succeeded: () => 0,
      uploadingFailure: () => 0,
      failed: () => 0,
      suspended: () => 0,
      canceled: () => 0,
    });
  });

  it('updates concurrency on increase button click', async () => {
    const { getByTestId } = render(Dashboard);

    await waitFor(() => getByTestId('increase-concurrency-1'));

    const increaseBtn = getByTestId('increase-concurrency-1');
    await fireEvent.click(increaseBtn);

    await waitFor(() => {
      expect(mockApi.updateWorkerConfig).toHaveBeenCalledWith(1, { concurrency: 6 });
    });
  });

  it('updates prefetch on decrease button click', async () => {
    const { getByTestId } = render(Dashboard);

    await waitFor(() => getByTestId('decrease-prefetch-1'));

    const decreaseBtn = getByTestId('decrease-prefetch-1');
    await fireEvent.click(decreaseBtn);

    await waitFor(() => {
      expect(mockApi.updateWorkerConfig).toHaveBeenCalledWith(1, { prefetch: 9 });
    });
  });
});
