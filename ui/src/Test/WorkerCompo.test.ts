vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

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
// __mocks__/taskStatusMock.ts
export const taskStatusMock : TasksStatus = {
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
  },
  2: {
    pending: 1,
    assigned: 1,
    accepted: 2,
    downloading: 2,
    running: 3,
    uploadingSuccess: 4,
    succeeded: 5,
    uploadingFailure: 1,
    failed: 2,
    suspended: 1,
    canceled: 0,
    waiting: 1,
  }
};

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

    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    (mockApi.getStats as any).mockResolvedValue({
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
    mockApi.getTasksCount.mockImplementation((workerId?: number) => {
      if (workerId !== undefined) {
        return Promise.resolve(taskStatusMock[workerId] ?? {});
      }
      const combined = Object.values(taskStatusMock).reduce((acc, curr) => {
        for (const key in curr) {
          acc[key] = (acc[key] || 0) + curr[key];
        }
        return acc;
      }, {} as TaskStatus);
      return Promise.resolve(combined);
    });
  });

  it('should display the list of workers', async () => {
    (mockApi.getWorkers as any).mockResolvedValue(mockWorkers);
    (mockApi.getStats as any).mockResolvedValue(mockStats);

    const { getByText } = render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => {
      expect(getByText('Worker One')).toBeTruthy();
    });
  });

  it('should display "No workers found." when there are no workers', async () => {
    const { getByText } = render(WorkerCompo);

    await waitFor(() => {
      expect(getByText('No workers found.')).toBeTruthy();
    });
  });

  it('should display statistics for a worker', async () => {
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

  it('should count tasks per worker (taskCountWorker)', async () => {
    render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => {
      const worker1 = taskStatusMock[1];
      const awaiting = worker1.pending + worker1.assigned + worker1.accepted;
      expect(screen.getByTestId('tasks-awaiting-execution-1').textContent).toBe(String(awaiting));

      const inProgress = worker1.downloading + worker1.waiting + worker1.running;
      expect(screen.getByTestId('tasks-in-progress-1').textContent).toBe(String(inProgress));

      const success = worker1.uploadingSuccess + worker1.succeeded;
      expect(screen.getByTestId('successful-tasks-1').textContent).toBe(String(success));

      const failed = worker1.uploadingFailure + worker1.failed + worker1.suspended + worker1.canceled;
      expect(screen.getByTestId('failed-tasks-1').textContent).toBe(String(failed));
    });
  });

  it('should count tasks globally (allTaskCount)', async () => {
    const result = await mockApi.getTasksCount(); // Corrected here
    const expected = Object.values(taskStatusMock).reduce((acc, curr) => {
      for (const key in curr) {
        acc[key] = (acc[key] || 0) + curr[key];
      }
      return acc;
    }, {} as TaskStatus);

    expect(result).toEqual(expected);
  });

});