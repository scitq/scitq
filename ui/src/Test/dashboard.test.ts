vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import Dashboard from '../pages/Dashboard.svelte';
import { wsClient } from '../lib/wsClient';

let messageHandler: (msg: any) => void;

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

const mockJobs = [
  { jobId: 1, status: 'S', action: 'D', progression: 100, modifiedAt: new Date().toISOString(), workerId: 'worker-1'},
  { jobId: 2, status: 'S', action: 'C', progression: 100, modifiedAt: new Date().toISOString(), workerId: 'worker-2'}
]

vi.mock('@/lib/wsClient', () => ({
  wsClient: {
    subscribeToMessages: vi.fn(() => () => {}),
    sendMessage: vi.fn()
  }
}));


describe('Worker integration', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    // Configure API mocks
    mockApi.getWorkers.mockResolvedValue([
      { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1' }
    ]);
    mockApi.getJobs.mockResolvedValue([
      { jobId: 1, status: 'S', action: 'D', progression: 100, modifiedAt: new Date().toISOString(), workerId: 'worker-1' }
    ]);
    mockApi.getStatus.mockResolvedValue([{ workerId: 1, status: 'online' }]);
    mockApi.delJob.mockResolvedValue({});
    mockApi.delWorker.mockResolvedValue({});
    mockApi.updateWorkerConfig.mockResolvedValue({});
    mockApi.newWorker.mockResolvedValue([]);

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
      messageHandler = handler; // Store the handler for later use
      return () => true; // Unsubscribe function
    });
  });

it('updates concurrency on increase button click', async () => {
  // 1. Setup mocks
  mockApi.getWorkers.mockResolvedValue([
    { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1', status: 'online' }
  ]);
  mockApi.updateWorkerConfig.mockResolvedValue({});

  // 2. Render the component
  const { getByTestId } = render(Dashboard);

  // 3. Wait for worker to load
  await waitFor(() => {
    expect(getByTestId('worker-1')).toBeInTheDocument();
  });

  // 4. Find and click the button
  const increaseBtn = getByTestId('increase-concurrency-1');
  await fireEvent.click(increaseBtn);

  // 5. Verify API call
  await waitFor(() => {
    expect(mockApi.updateWorkerConfig).toHaveBeenCalledWith(1, { concurrency: 6 });
  });
});

it('updates prefetch on decrease button click', async () => {
  // 1. Setup mocks
  mockApi.getWorkers.mockResolvedValue([
    { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1', status: 'online' }
  ]);
  mockApi.updateWorkerConfig.mockResolvedValue({});

  // 2. Render the component
  const { getByTestId } = render(Dashboard);

  // 3. Wait for worker to load
  await waitFor(() => {
    expect(getByTestId('worker-1')).toBeInTheDocument();
  });

  // 4. Find and click the button
  const decreaseBtn = getByTestId('decrease-prefetch-1');
  await fireEvent.click(decreaseBtn);

  // 5. Verify API call
  await waitFor(() => {
    expect(mockApi.updateWorkerConfig).toHaveBeenCalledWith(1, { prefetch: 9 });
  });
});

it('should add a worker and update the worker and the jobs list', async () => {
  mockApi.newWorker.mockResolvedValue([{ workerId: 3, workerName: 'new-worker' }]);
  mockApi.getJobs.mockResolvedValue(mockJobs);

  const { getByTestId } = render(Dashboard);

  await waitFor(() => getByTestId('add-worker-button'));

  // Fill the form
  await fireEvent.input(getByTestId('concurrency-createWorker'), { target: { value: '2' } });
  await fireEvent.input(getByTestId('prefetch-createWorker'), { target: { value: '1' } });
  await fireEvent.input(getByTestId('flavor-createWorker'), { target: { value: 'flavor-a' } });
  await fireEvent.input(getByTestId('region-createWorker'), { target: { value: 'us-east' } });
  await fireEvent.input(getByTestId('provider-createWorker'), { target: { value: 'aws' } });
  await fireEvent.input(getByTestId('number-createWorker'), { target: { value: '1' } });
  await fireEvent.input(getByTestId('wfStep-createWorker'), { target: { value: 'myWorkflow.myStep' } });

  await fireEvent.click(getByTestId('add-worker-button'));

  // Simulate WebSocket message
  if (messageHandler) {
    messageHandler({
      type: 'worker-created',
      payloadWorker: {
        workerId: 3,
        name: 'new-worker',
        concurrency: 2,
        prefetch: 1,
        status: 'P'
      },
      payloadJob: {
        jobId: 3,
        action: "C",
        status: "P",
        workerID: 3,
        modifiedAt: new Date().toISOString()
      }
    });
  }

  // Wait for new worker to appear
  await waitFor(() => {
    expect(screen.getByText('new-worker')).toBeInTheDocument();
  });

  // Verify API call
  expect(mockApi.newWorker).toHaveBeenCalledWith(
    2, 1, 'flavor-a', 'us-east', 'aws', 1, 'myWorkflow.myStep'
  );
});

it('deletes a worker and update Jobs when delete button is clicked', async () => {
    const { getByTestId, queryByTestId } = render(Dashboard);

    await waitFor(() => getByTestId('worker-1'));
    
    mockApi.delWorker.mockResolvedValue({});
    const deleteBtn = getByTestId('delete-worker-1');
    await fireEvent.click(deleteBtn);

    // Simulate WebSocket message
    if (messageHandler) {
        messageHandler({
        type: 'worker-deleted',
        payloadWorker: { workerId: 1 },
        payloadJob: {
            jobId: 3,
            action: "D",
            status: "P",
            workerID: 1,
            modifiedAt: new Date().toISOString()
        }
        });
    }

    await waitFor(() => {
        expect(mockApi.delWorker).toHaveBeenCalledWith({ workerId: 1 });
        expect(queryByTestId('worker-1')).toBeNull();
    });
});
});


// Mock data
const mockJobsFirstPage = [
  { jobId: 1, status: 'S', action: 'D', progression: 100, modifiedAt: '2023-01-01T00:00:00Z', workerId: 'worker-1' },
  { jobId: 2, status: 'P', action: 'C', progression: 0, modifiedAt: '2023-01-01T00:01:00Z', workerId: 'worker-2' },
  { jobId: 3, status: 'F', action: 'C', progression: 50, modifiedAt: '2023-01-01T00:02:00Z', workerId: 'worker-1' }
];

const mockJobsSecondPage = [
  { jobId: 4, status: 'S', action: 'D', progression: 100, modifiedAt: '2023-01-01T00:03:00Z', workerId: 'worker-2' },
  { jobId: 5, status: 'P', action: 'C', progression: 0, modifiedAt: '2023-01-01T00:04:00Z', workerId: 'worker-1' }
];

const mockNewJobs = [
  { jobId: 6, status: 'S', action: 'C', progression: 100, modifiedAt: '2023-01-01T00:05:00Z', workerId: 'worker-3' }
];

describe('Job Pagination and Notifications', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue([
      { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1' }
    ]);
    mockApi.getJobs.mockResolvedValue(mockJobsFirstPage);
    mockApi.getStatus.mockResolvedValue([{ workerId: 1, status: 'online' }]);

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
      messageHandler = handler; // Store the handler for later use
      return () => true; // Unsubscribe function
    });
  });

  it('should load initial jobs', async () => {
    render(Dashboard);
    
    await waitFor(() => {
      expect(screen.getByTestId('job-row-1')).toBeInTheDocument();
      expect(screen.getByTestId('job-row-2')).toBeInTheDocument();
      expect(screen.getByTestId('job-row-3')).toBeInTheDocument();
    });
  });

  it('should load more jobs when scrolling to bottom', async () => {
    let callCount = 0;
    mockApi.getJobs.mockImplementation((limit, offset) => {
      if (callCount === 0) {
        callCount++;
        return Promise.resolve(mockJobsFirstPage);
      }
      return Promise.resolve(mockJobsSecondPage);
    });

    const { getByTestId } = render(Dashboard);
    
    await waitFor(() => getByTestId('job-row-1'));
    
    const jobsContainer = getByTestId('dashboard-page').querySelector('.dashboard-job-section');
    if (!jobsContainer) throw new Error('Jobs container not found');

    // Mock scroll properties
    Object.defineProperty(jobsContainer, 'scrollHeight', { value: 1000 });
    Object.defineProperty(jobsContainer, 'scrollTop', { value: 950 });
    Object.defineProperty(jobsContainer, 'clientHeight', { value: 200 });

    fireEvent.scroll(jobsContainer);

    await waitFor(() => {
      expect(getByTestId('job-row-4')).toBeInTheDocument();
      expect(getByTestId('job-row-5')).toBeInTheDocument();
    }, { timeout: 3000 });
  });

it('should show notification when new jobs are added', async () => {
  // Initial setup
  mockApi.getWorkers.mockResolvedValue([{ workerId: 1, name: 'Worker 1', status: 'online' }]);
  mockApi.getJobs.mockResolvedValue(mockJobsFirstPage);
  
  const { getByTestId, getByText } = render(Dashboard);
  
  await waitFor(() => getByTestId('job-row-1'));
  
  await waitFor(() => getByTestId('job-row-1'));

  // Simulate being scrolled to bottom
  const jobsContainer = getByTestId('jobs-container');
  Object.defineProperties(jobsContainer, {
    scrollTop: { value: 100, writable: true },
    scrollHeight: { value: 1000 },
    clientHeight: { value: 500 }
  });
  await fireEvent.scroll(jobsContainer); // updates isScrolledToTop

  // Now simulate WebSocket message
  if (messageHandler) {
    messageHandler({
      type: 'worker-created',
      payloadWorker: {
        workerId: 3,
        name: 'New Worker',
        concurrency: 2,
        prefetch: 1,
        status: 'online'
      },
      payloadJob: {
        jobId: 6,
        status: "S",
        action: "C",
        workerID: 3,
        modifiedAt: new Date().toISOString()
      }
    });
  }

  // Verify notification appears
  await waitFor(() => {
    expect(getByText('1 new job available')).toBeInTheDocument();
  });
});

it('should load new jobs when clicking notification', async () => {
  // Initial setup
  mockApi.getWorkers.mockResolvedValue([{ workerId: 1, name: 'Worker 1', status: 'online' }]);
  mockApi.getJobs.mockResolvedValue(mockJobsFirstPage);
  
  const { getByTestId, getByText, queryByText, queryByTestId } = render(Dashboard);
  
  await waitFor(() => getByTestId('job-row-1'));
  
  // Simulate being scrolled to bottom
  const jobsContainer = getByTestId('jobs-container');
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    value: vi.fn(),
    writable: true
  });
  Object.defineProperties(jobsContainer, {
    scrollTop: { value: 100, writable: true },
    scrollHeight: { value: 1000 },
    clientHeight: { value: 500 }
  });
  await fireEvent.scroll(jobsContainer); // updates isScrolledToTop

  // Simulate new job
  if (messageHandler) {
    messageHandler({
      type: 'worker-created',
      payloadWorker: {
        workerId: 3,
        name: 'New Worker',
        concurrency: 2,
        prefetch: 1,
        status: 'online'
      },
      payloadJob: {
        jobId: 6,
        status: "S",
        action: "C",
        workerID: 3,
        modifiedAt: new Date().toISOString()
      }
    });
  }
  
  // Verify notification appears first
  await waitFor(() => {
    expect(getByText('1 new job available')).toBeInTheDocument();
    expect(queryByTestId('job-row-6')).not.toBeInTheDocument(); // New job shouldn't be visible yet
  }, { timeout: 3000 });
  
  // Now click the notification
  const notification = getByText('1 new job available');
  await fireEvent.click(notification);
  
  // Verify changes after click
  await waitFor(() => {
    expect(getByTestId('job-row-6')).toBeInTheDocument();
    expect(queryByText('1 new job available')).not.toBeInTheDocument();
  });
});
});