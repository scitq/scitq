vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';
import { render, fireEvent, waitFor, screen, queryByTestId, getByTestId } from '@testing-library/svelte';
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

const mockJobs = [
  { jobId: 1, status: 'S', action: 'D', progression: 100, modifiedAt: new Date().toISOString(), workerId: 'worker-1'},
  { jobId: 2, status: 'S', action: 'C', progression: 100, modifiedAt: new Date().toISOString(), workerId: 'worker-2'}
]

describe('Worker integration', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue([
      { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1' }
    ]);
    mockApi.getJobs.mockResolvedValue(mockJobs);
    mockApi.getStatus.mockResolvedValue([{ workerId: 1, status: 'online' }]);
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

  it('deletes a worker and update Jobs when delete button is clicked', async () => {
    const { getByTestId, queryByTestId } = render(Dashboard);

    await waitFor(() => getByTestId('worker-1'));
    mockApi.delWorker.mockResolvedValue('3');
    const deleteBtn = getByTestId('delete-worker-1');
    await fireEvent.click(deleteBtn);

    await waitFor(() => {
      expect(mockApi.delWorker).toHaveBeenCalledWith({ workerId: 1 });
    });

    await waitFor(() => {
      expect(queryByTestId('worker-1')).toBeNull();
      expect(queryByTestId('job-row-3')).toBeInTheDocument();
    });
    
    expect(screen.getByText('Worker deleted / Job Created')).toBeInTheDocument();
  });

  it('should add a worker and update the worker and the jobs list', async () => {
    // 1. Mock newWorker to return a new worker
    mockApi.newWorker.mockResolvedValue([
      {
        workerId: 3,
        workerName: 'new-worker'
      }
    ]);

    // Mock API responses
    mockApi.getJobs.mockResolvedValue(mockJobs);

    // 2. Render the dashboard
    const { getByTestId, findByText, queryByText, queryByTestId } = render(Dashboard);

    // 3. Ensure the form is visible
    await waitFor(() => {
      expect(getByTestId('add-worker-button')).toBeInTheDocument();
      expect(screen.getByTestId('job-row-1')).toBeInTheDocument();
      expect(screen.getByTestId('job-row-2')).toBeInTheDocument();
      expect(queryByText('new-worker')).not.toBeInTheDocument();
      expect(queryByTestId('job-row-3')).not.toBeInTheDocument();
    });

    // 4. Fill the inputs
    await fireEvent.input(getByTestId('concurrency-createWorker'), { target: { value: '2' } });
    await fireEvent.input(getByTestId('prefetch-createWorker'), { target: { value: '1' } });
    await fireEvent.input(getByTestId('flavor-createWorker'), { target: { value: 'flavor-a' } });
    await fireEvent.input(getByTestId('region-createWorker'), { target: { value: 'us-east' } });
    await fireEvent.input(getByTestId('provider-createWorker'), { target: { value: 'aws' } });
    await fireEvent.input(getByTestId('number-createWorker'), { target: { value: '1' } });
    await fireEvent.input(getByTestId('wfStep-createWorker'), { target: { value: 'myWorkflow.myStep' } });

    // 5. Click on submit
    await fireEvent.click(getByTestId('add-worker-button'));

    // 6. Check that success message appears
    expect(await findByText('Worker/Job Added')).toBeInTheDocument();

    // 7. Check that newWorker was called correctly
    expect(mockApi.newWorker).toHaveBeenCalledWith(
      2,           // concurrency
      1,           // prefetch
      'flavor-a',  // flavor
      'us-east',   // region
      'aws',       // provider
      1,           // number
      'myWorkflow.myStep' // wfStep
    );

    // 8. Check that the worker is now in the DOM
    const newWorkerName = await screen.findByText('new-worker');
    expect(newWorkerName).toBeInTheDocument();

    // 9. Check that the job is now in the DOM
    const newJob = await screen.findByText('3');
    expect(newJob).toBeInTheDocument();
  });

  it('should delete a job when trash button is clicked', async () => {
    const mockJobs = [
      {
        jobId: 1,
        status: 'S',
        action: 'D',
        progression: 100,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-1',
      }
    ];

    // Mock API responses
    mockApi.getJobs.mockResolvedValue(mockJobs);
    mockApi.delJob.mockResolvedValue({});
    
    render(Dashboard);

    // Wait until jobs appear
    await waitFor(() => {
      expect(screen.getByTestId('job-row-1')).toBeInTheDocument();
    });

    // Click the delete button for jobId = 1
    const deleteButton = screen.getByTestId('trash-button-1');
    await fireEvent.click(deleteButton);

    // Wait for API call
    await waitFor(() => {
      expect(mockApi.delJob).toHaveBeenCalledWith({ jobId: 1 });
      expect(screen.queryByTestId('job-row-1')).toBeNull();
    });
  });

});
