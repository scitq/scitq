import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import WorkerCompo from '../components/WorkerCompo.svelte';

// Task counts as getAllTaskStats returns them: keyed by task status letter.
const perWorkerStatusCounts: Record<number, Record<string, number>> = {
  1: { P: 3, A: 2, C: 1, D: 4, O: 2, R: 5, U: 6, V: 8, S: 7, F: 9, I: 1, Z: 10, X: 11, W: 12 },
  2: { P: 1, A: 1, C: 2, D: 2, O: 0, R: 3, U: 4, V: 1, S: 5, F: 2, I: 0, Z: 1, X: 0, W: 1 },
};
const globalStatusCounts: Record<string, number> = {};
for (const counts of Object.values(perWorkerStatusCounts)) {
  for (const [status, n] of Object.entries(counts)) globalStatusCounts[status] = (globalStatusCounts[status] || 0) + n;
}
const totalCount = Object.values(globalStatusCounts).reduce((a, b) => a + b, 0);

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

// Renders the component and waits until the workers and their stats are shown.
async function renderLoaded() {
  const result = render(WorkerCompo);
  await waitFor(() => expect(screen.getByText('sda1: 60.4%')).toBeInTheDocument());
  return result;
}

describe('WorkerCompo', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    (mockApi.getStats as any).mockResolvedValue(mockStats);
    mockApi.getAllTaskStats.mockResolvedValue({ perWorkerStatusCounts, globalStatusCounts, totalCount });
  });

  it('should display the list of workers', async () => {
    (mockApi.getWorkers as any).mockResolvedValue(mockWorkers);
    (mockApi.getStats as any).mockResolvedValue(mockStats);

    const { getByText } = render(WorkerCompo);

    await waitFor(() => {
      expect(getByText('Worker One')).toBeTruthy();
    });
  });

  it('should display "No workers found." when there are no workers', async () => {
    mockApi.getWorkers.mockResolvedValue([]);
    const { getByText } = render(WorkerCompo);

    await waitFor(() => expect(mockApi.getWorkers).toHaveBeenCalled());
    await waitFor(() => {
      expect(getByText('No workers found.')).toBeTruthy();
    });
  });

  it('should display statistics for a worker', async () => {
    await renderLoaded();

    // Table mode (the default) shows CPU, memory and per-disk usage.
    expect(screen.getByTestId('charts-worker-1')).toBeInTheDocument();
    expect(screen.getByText('42.5%')).toBeInTheDocument();
    expect(screen.getByText('73.1%')).toBeInTheDocument();
    expect(screen.getByText('sda1: 60.4%')).toBeInTheDocument();
    expect(screen.getByText('sdb1: 88.2%')).toBeInTheDocument();
  });

  it('should display graphs statistics for a worker', async () => {
    await renderLoaded();

    expect(screen.getByTestId('charts-worker-1')).toBeInTheDocument();
    expect(screen.queryByTestId('table-worker-1')).not.toBeInTheDocument();
    expect(screen.queryByTestId('I/O-chart-1')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('charts-worker-1'));

    await waitFor(() => {
      expect(screen.getByTestId('I/O-chart-1')).toBeInTheDocument();
      expect(screen.getByText('Network I/O')).toBeInTheDocument();
      expect(screen.getByText('CPU: 42.5%')).toBeInTheDocument();
      expect(screen.queryByText('sda1: 60.4%')).not.toBeInTheDocument();
      expect(screen.queryByTestId('charts-worker-1')).not.toBeInTheDocument();
      expect(screen.getByTestId('table-worker-1')).toBeInTheDocument();
    });

    await fireEvent.click(screen.getByTestId('table-worker-1'));

    await waitFor(() => {
      expect(screen.queryByTestId('I/O-chart-1')).not.toBeInTheDocument();
      expect(screen.getByText('sda1: 60.4%')).toBeInTheDocument();
      expect(screen.getByTestId('charts-worker-1')).toBeInTheDocument();
      expect(screen.queryByTestId('table-worker-1')).not.toBeInTheDocument();
    });
  });

  it('should count tasks per worker (taskCountWorker)', async () => {
    await renderLoaded();

    const w = perWorkerStatusCounts[1];
    await waitFor(() => {
      // Starting = accepted + downloading + on hold
      expect(screen.getByTestId('tasks-awaiting-execution-1').textContent!.trim()).toBe(String(w.C + w.D + w.O));
      // Running = running + uploading (after success or failure)
      expect(screen.getByTestId('tasks-in-progress-1').textContent!.trim()).toBe(String(w.R + w.U + w.V));
      expect(screen.getByTestId('successful-tasks-1').textContent!.trim()).toBe(String(w.S));
      expect(screen.getByTestId('failed-tasks-1').textContent!.trim()).toBe(String(w.F));
      // Inactive = I + suspended + canceled
      expect(screen.getByTestId('inactive-tasks-1').textContent!.trim()).toBe(String(w.I + w.Z + w.X));
    });
  });

  it('should count tasks globally (allTaskCount)', async () => {
    await renderLoaded();

    expect(screen.getByText(`All: ${totalCount}`)).toBeInTheDocument();
    expect(screen.getByTestId('pending-link-dashboard').textContent!.trim()).toBe(`Pending: ${globalStatusCounts.P}`);
    expect(screen.getByTestId('count-A').textContent!.trim()).toBe(`Assigned: ${globalStatusCounts.A}`);
    expect(screen.getByTestId('count-C').textContent!.trim()).toBe(`Accepted: ${globalStatusCounts.C}`);
    expect(screen.getByTestId('count-U').textContent!.trim()).toBe(`Uploading: ${globalStatusCounts.U + globalStatusCounts.V}`);
    expect(screen.getByTestId('count-F').textContent!.trim()).toBe(`Failed: ${globalStatusCounts.F}`);
  });
});

describe('Zoom Management', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    (mockApi.getStats as any).mockResolvedValue(mockStats);
    mockApi.getAllTaskStats.mockResolvedValue({ perWorkerStatusCounts, globalStatusCounts, totalCount });
  });

  async function switchToChartMode() {
    await fireEvent.click(screen.getByTestId('charts-worker-1'));
    await waitFor(() => {
      expect(screen.getByTestId('I/O-chart-1')).toBeInTheDocument();
    });
  }

  it('should initialize with auto-zoom enabled for both charts', async () => {
    await renderLoaded();

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
    await renderLoaded();
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
    await renderLoaded();
    
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
});
