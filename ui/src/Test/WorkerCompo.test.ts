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
export const taskStatusMock: TasksStatus = {
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
    (mockApi.getStats as any).mockResolvedValue(mockStats);
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
    const metricsButton = screen.getByTestId('advanced-metrics');
    await fireEvent.click(metricsButton);
    expect(screen.queryByTestId('charts-worker-1')).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('42.5%')).toBeTruthy();
      expect(screen.getByText('73.1%')).toBeTruthy();
      expect(screen.getByText('sda1: 60.4%')).toBeTruthy();
      expect(screen.getByText('sdb1: 88.2%')).toBeTruthy();
      expect(screen.getByText('1.0/0.5 MB/s')).toBeTruthy();
      expect(screen.getByText('1.0/0.5 GB')).toBeTruthy();
      expect(screen.getByText('2.0/1.0 MB/s')).toBeTruthy();
      expect(screen.getByText('2.0/1.0 GB')).toBeTruthy();
    });
  });

  it('should display graphs statistics for a worker', async () => {
    render(WorkerCompo, { props: { workers: mockWorkers } });

    const metricsButton = screen.getByTestId('advanced-metrics');
    await fireEvent.click(metricsButton);

    await waitFor(() => {
      expect(screen.getByText('Network Sent/Recv')).toBeInTheDocument();
      expect(screen.queryByTestId('Metrics')).not.toBeInTheDocument();
      expect(screen.getByTestId('charts-worker-1')).toBeInTheDocument();
      expect(screen.queryByTestId('table-worker-1')).not.toBeInTheDocument();
      expect(screen.getByText('2.0/1.0 MB/s')).toBeInTheDocument();
      expect(screen.queryByTestId('I/O-chart-1')).not.toBeInTheDocument();
    });

    const chartsButton = screen.getByTestId('charts-worker-1');
    await fireEvent.click(chartsButton);

    await waitFor(() => {
      expect(screen.getByTestId('I/O-chart-1')).toBeInTheDocument();
      expect(screen.queryByText('2.0/1.0 MB/s')).not.toBeInTheDocument();
      expect(screen.queryByTestId('charts-worker-1')).not.toBeInTheDocument();
      expect(screen.getByTestId('table-worker-1')).toBeInTheDocument();
    });

    const tableButton = screen.getByTestId('table-worker-1');
    await fireEvent.click(tableButton);

    await waitFor(() => {
      expect(screen.queryByTestId('I/O-chart-1')).not.toBeInTheDocument();
      expect(screen.queryByText('2.0/1.0 MB/s')).toBeInTheDocument();
      expect(screen.getByTestId('charts-worker-1')).toBeInTheDocument();
      expect(screen.queryByTestId('table-worker-1')).not.toBeInTheDocument();
    });
  });

  it('should count tasks per worker (taskCountWorker)', async () => {
    render(WorkerCompo, { props: { workers: mockWorkers } });

    await waitFor(() => {
      const worker1 = taskStatusMock[1];
      const awaiting = worker1.pending + worker1.assigned + worker1.accepted;
      expect(screen.getByTestId('tasks-awaiting-execution-1').textContent).toBe(String(awaiting));

      const inProgress = worker1.downloading + worker1.running;
      expect(screen.getByTestId('tasks-in-progress-1').textContent).toBe(String(inProgress));

      const success = worker1.uploadingSuccess + worker1.succeeded;
      expect(screen.getByTestId('successful-tasks-1').textContent).toBe(String(success));

      const failed = worker1.uploadingFailure + worker1.failed;
      expect(screen.getByTestId('failed-tasks-1').textContent).toBe(String(failed));

      const inactive = worker1.waiting + worker1.suspended + worker1.canceled;
      expect(screen.getByTestId('inactive-tasks-1').textContent).toBe(String(inactive));
    });
  });

  it('should count tasks globally (allTaskCount)', async () => {
    const result = await mockApi.getTasksCount();
    const expected = Object.values(taskStatusMock).reduce((acc, curr) => {
      for (const key in curr) {
        acc[key] = (acc[key] || 0) + curr[key];
      }
      return acc;
    }, {} as TaskStatus);

    expect(result).toEqual(expected);
  });
});

describe('Zoom Management', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    (mockApi.getStats as any).mockResolvedValue(mockStats);
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

  async function switchToChartMode() {
    await fireEvent.click(screen.getByTestId('charts-worker-1'));
    await waitFor(() => {
      expect(screen.getByTestId('I/O-chart-1')).toBeInTheDocument();
    });
  }

  it('should initialize with auto-zoom enabled for both charts', async () => {
    render(WorkerCompo, { props: { workers: mockWorkers } });

    await switchToChartMode();
    
    await waitFor(() => {
      // Check that auto-zoom is enabled by default
      const diskAutoZoomToggle = screen.getByTestId('read-auto-zoom-toggle');
      const networkAutoZoomToggle = screen.getByTestId('sent-auto-zoom-toggle');
      
      expect(diskAutoZoomToggle).toHaveClass('active');
      expect(networkAutoZoomToggle).toHaveClass('active');
      expect(diskAutoZoomToggle).toHaveTextContent('Auto Zoom (ON)');
      expect(networkAutoZoomToggle).toHaveTextContent('Auto Zoom (ON)');
    });
  });

  it('should toggle auto-zoom for disk chart', async () => {
    render(WorkerCompo, { props: { workers: mockWorkers } });
    await switchToChartMode();
    
    await waitFor(async () => {
      const toggleButton = screen.getByTestId('read-auto-zoom-toggle');
      
      // Disable auto-zoom
      await fireEvent.click(toggleButton);
      expect(toggleButton).not.toHaveClass('active');
      expect(toggleButton).toHaveTextContent('Auto Zoom (OFF)');
      
      // Check that manual controls appear
      expect(screen.getByTestId('read-zoom-out')).toBeInTheDocument();
      expect(screen.getByTestId('read-zoom-level')).toBeInTheDocument();
      expect(screen.getByTestId('read-zoom-in')).toBeInTheDocument();
      expect(screen.getByTestId('read-zoom-reset')).toBeInTheDocument();
      
      // Re-enable auto-zoom
      await fireEvent.click(toggleButton);
      expect(toggleButton).toHaveClass('active');
      expect(toggleButton).toHaveTextContent('Auto Zoom (ON)');
    });
  });

  it('should handle manual zoom controls for disk chart', async () => {
    render(WorkerCompo, { props: { workers: mockWorkers } });
    
    await switchToChartMode();
    await fireEvent.click(screen.getByTestId('read-auto-zoom-toggle'));

    const getZoom = () => {
      const text = screen.getByTestId('read-zoom-level').textContent || '';
      return parseFloat(text.split(' ')[1]);
    };

    // Test reset (verify returns to 1)
    await fireEvent.click(screen.getByTestId('read-zoom-reset'));
    await waitFor(() => expect(getZoom()).toBe(1));

    // Test zoom in (verify it increases)
    const beforeZoomIn = getZoom();
    await fireEvent.click(screen.getByTestId('read-zoom-in'));
    await waitFor(() => expect(getZoom()).toBeGreaterThan(beforeZoomIn));

    // Test zoom out (verify it decreases)
    const afterZoomIn = getZoom();
    await fireEvent.click(screen.getByTestId('read-zoom-out'));
    await waitFor(() => expect(getZoom()).toBeLessThan(afterZoomIn));
  });

  it('should calculate and apply auto-zoom based on data', async () => {
    // Mock data with significant variation
    mockApi.getStats.mockResolvedValue({
      1: {
        ...mockStats[1],
        diskIo: {
          readBytesRate: 10485760, // 10MB/s
          writeBytesRate: 5242880, // 5MB/s
          readBytesTotal: 10737418240,
          writeBytesTotal: 5368709120
        },
        netIo: {
          sentBytesRate: 20971520, // 20MB/s
          recvBytesRate: 10485760, // 10MB/s
          sentBytesTotal: 21474836480,
          recvBytesTotal: 10737418240
        }
      }
    });

    render(WorkerCompo, { props: { workers: mockWorkers } });

    await switchToChartMode();

    // Disable auto-zoom for disk
    await fireEvent.click(screen.getByTestId('read-auto-zoom-toggle'));
    
    // Disable auto-zoom for network
    await fireEvent.click(screen.getByTestId('sent-auto-zoom-toggle'));

    await waitFor(() => {
      // Verify auto-zoom adapts to new data
      const diskZoomText = screen.getByTestId('read-zoom-level').textContent;
      const diskZoom = parseFloat(diskZoomText?.match(/[\d.]+/)?.[0] || '0');
      expect(diskZoom).toBeGreaterThan(1);
      expect(diskZoom).toBeLessThanOrEqual(15);

      const networkZoomText = screen.getByTestId('sent-zoom-level').textContent;
      const networkZoom = parseFloat(networkZoomText?.match(/[\d.]+/)?.[0] || '0');
      expect(networkZoom).toBeGreaterThan(1);
      expect(networkZoom).toBeLessThanOrEqual(15);
    });
  });
});