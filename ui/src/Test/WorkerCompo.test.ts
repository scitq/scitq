import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import WorkerCompo from '../components/WorkerCompo.svelte';



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

// Mock all functions from ../lib/api
vi.mock('../lib/api', async (importOriginal) => {
  const actual = await importOriginal() as typeof import('../lib/api');
  return {
    ...actual,
    getWorkers: vi.fn(),
    updateWorkerConfig: vi.fn(),
    delWorker: vi.fn(),
    getWorkerStatusClass: (status: string) => status,
    getWorkerStatusText: (status: string) => status,
    getStats: vi.fn(),
    getTasks: vi.fn((workerId: number) => ({
      pending: () => tasksStatus[workerId as keyof typeof tasksStatus]?.pending ?? 0,
      assigned: () => tasksStatus[workerId as keyof typeof tasksStatus]?.assigned ?? 0,
      accepted: () => tasksStatus[workerId as keyof typeof tasksStatus]?.accepted ?? 0,
      downloading: () => tasksStatus[workerId as keyof typeof tasksStatus]?.downloading ?? 0,
      running: () => tasksStatus[workerId as keyof typeof tasksStatus]?.running ?? 0,
      uploadingSuccess: () => tasksStatus[workerId as keyof typeof tasksStatus]?.uploadingSuccess ?? 0,
      succeeded: () => tasksStatus[workerId as keyof typeof tasksStatus]?.succeeded ?? 0,
      uploadingFailure: () => tasksStatus[workerId as keyof typeof tasksStatus]?.uploadingFailure ?? 0,
      failed: () => tasksStatus[workerId as keyof typeof tasksStatus]?.failed ?? 0,
      suspended: () => tasksStatus[workerId as keyof typeof tasksStatus]?.suspended ?? 0,
      canceled: () => tasksStatus[workerId as keyof typeof tasksStatus]?.canceled ?? 0,
      waiting: () => tasksStatus[workerId as keyof typeof tasksStatus]?.waiting ?? 0,
    })),
  };
});

import { getWorkers, updateWorkerConfig, delWorker, getStats } from '../lib/api';

// Example worker data
const mockWorkers = [
  {
    workerId: 1,
    name: 'Worker One',
    batch: 10,
    status: 'R',
    concurrency: 5,
    prefetch: 10,
    accepted: 5,
    running: 3,
    successes: 20,
    fail: 2,
    cpuUsage: 50,
    memUsage: 70,
    loadAvg: 1.5,
    diskUsage: 80,
    diskRW: "10MB/5MB",
    netIO: "100MB/50MB",
  },
];

// Example stats for one worker
const mockStats = {
  1: {
    cpuUsagePercent: 42.5,
    memUsagePercent: 73.1,
    load1Min: 2.5,
    disks: [
      { deviceName: 'sda1', usagePercent: 60.4 },
      { deviceName: 'sdb1', usagePercent: 88.2 },
    ],
    diskIo: {
      readBytesRate: 1048576,
      writeBytesRate: 524288,
      readBytesTotal: 1073741824,
      writeBytesTotal: 536870912,
    },
    netIo: {
      sentBytesRate: 2097152,
      recvBytesRate: 1048576,
      sentBytesTotal: 2147483648,
      recvBytesTotal: 1073741824,
    },
  },
};

describe('WorkerCompo', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('should display the list of workers', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);
    (getStats as any).mockResolvedValue(mockStats);

    const { getByText } = render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => {
      expect(getByText('Worker One')).toBeTruthy();
    });
  });

  it('should display "No workers found." when there are no workers', async () => {
    (getWorkers as any).mockResolvedValue([]);
    (getStats as any).mockResolvedValue({});

    const { getByText } = render(WorkerCompo);

    await waitFor(() => {
      expect(getByText('No workers found.')).toBeTruthy();
    });
  });

  it('should delete a worker', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);
    (getStats as any).mockResolvedValue(mockStats);

    const { getByText } = render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => getByText('Worker One'));

    const deleteButton = await screen.findByTestId('delete-worker-1');
    await fireEvent.click(deleteButton);

    await waitFor(() => {
      expect(delWorker).toHaveBeenCalledWith({ workerId: 1 });
    });
  });

  it('should display statistics for a worker', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);
    (getStats as any).mockResolvedValue({
      1: {
        cpuUsagePercent: 42.5,
        memUsagePercent: 73.1,
        load1Min: 2.5,
        disks: [
          { deviceName: 'sda1', usagePercent: 60.4 },
          { deviceName: 'sdb1', usagePercent: 88.2 },
        ],
        diskIo: {
          readBytesRate: 1048576,
          writeBytesRate: 524288,
          readBytesTotal: 1073741824,
          writeBytesTotal: 536870912,
        },
        netIo: {
          sentBytesRate: 2097152,
          recvBytesRate: 1048576,
          sentBytesTotal: 2147483648,
          recvBytesTotal: 1073741824,
        },
      },
    });

    render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => {
      expect(screen.getByText('42.5%')).toBeTruthy();
      expect(screen.getByText('73.1%')).toBeTruthy();
      expect(screen.getByText('2.5')).toBeTruthy();
      expect(screen.getByText('sda1: 60.4%')).toBeTruthy();
      expect(screen.getByText('sdb1: 88.2%')).toBeTruthy();
      expect(screen.getByText('1.0/0.5 MB/s')).toBeTruthy();
      expect(screen.getByText('1.0/0.5 GB')).toBeTruthy();
      expect(screen.getByText('2.0/1.0 MB/s')).toBeTruthy();
      expect(screen.getByText('2.0/1.0 GB')).toBeTruthy();
    });
  });

  it('should correctly display task counts by status category', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);
    (getStats as any).mockResolvedValue(mockStats);
  
    const { getByTestId } = render(WorkerCompo, { props: { workers: mockWorkers } });
  
    await waitFor(() => {
      const awaitingExecutionTotal = 3 + 2 + 1;
      expect(getByTestId('tasks-awaiting-execution-1').textContent).toBe(String(awaitingExecutionTotal));
  
      const inProgressTotal = 4 + 12 + 5;
      expect(getByTestId('tasks-in-progress-1').textContent).toBe(String(inProgressTotal));
  
      const successfulTasksTotal = 6 + 7;
      expect(getByTestId('successful-tasks-1').textContent).toBe(String(successfulTasksTotal));
  
      const failedTasksTotal = 8 + 9 + 10 + 11;
      expect(getByTestId('failed-tasks-1').textContent).toBe(String(failedTasksTotal));
    });
  });

  it('should display task breakdowns in tooltip on hover', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);
    (getStats as any).mockResolvedValue(mockStats);

    const { getByTestId } = render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => {
      const tasksAwaitingCell = getByTestId('tasks-awaiting-execution-1');
      fireEvent.mouseOver(tasksAwaitingCell);
      expect(tasksAwaitingCell).toHaveAttribute('title', expect.stringContaining('Pending: 3'));
      expect(tasksAwaitingCell).toHaveAttribute('title', expect.stringContaining('Assigned: 2'));
      expect(tasksAwaitingCell).toHaveAttribute('title', expect.stringContaining('Accepted: 1'));

      const tasksInProgressCell = getByTestId('tasks-in-progress-1');
      fireEvent.mouseOver(tasksInProgressCell);
      expect(tasksInProgressCell).toHaveAttribute('title', expect.stringContaining('Downloading: 4'));
      expect(tasksInProgressCell).toHaveAttribute('title', expect.stringContaining('Waiting: 12'));
      expect(tasksInProgressCell).toHaveAttribute('title', expect.stringContaining('Running: 5'));

      const successfulTasksCell = getByTestId('successful-tasks-1');
      fireEvent.mouseOver(successfulTasksCell);
      expect(successfulTasksCell).toHaveAttribute('title', expect.stringContaining('UploadingSuccess: 6'));
      expect(successfulTasksCell).toHaveAttribute('title', expect.stringContaining('Succeeded: 7'));

      const failedTasksCell = getByTestId('failed-tasks-1');
      fireEvent.mouseOver(failedTasksCell);
      expect(failedTasksCell).toHaveAttribute('title', expect.stringContaining('UploadingFailure: 8'));
      expect(failedTasksCell).toHaveAttribute('title', expect.stringContaining('Failed: 9'));
      expect(failedTasksCell).toHaveAttribute('title', expect.stringContaining('Suspended: 10'));
      expect(failedTasksCell).toHaveAttribute('title', expect.stringContaining('Canceled: 11'));
    });
  });

});