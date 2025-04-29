import { render, screen, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import JobsCompo from '../components/JobsCompo.svelte';
import { getJobs } from '../lib/api';

// Mock the `getJobs` function and utility helpers
vi.mock('../lib/api', () => ({
  getJobs: vi.fn(),
  getStatusClass: (status: string) => {
    switch (status) {
      case 'R': return 'running';
      case 'S': return 'succeeded';
      default: return 'unknown';
    }
  },
  getStatusText: (status: string) => status,
}));

describe('JobsCompo', () => {
  it('should display the job information correctly', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'R', // Status "running"
        action: 'C', // Deploy Worker
        progression: 50,
        modifiedAt: new Date('2025-03-10T21:12:30').toISOString(),
        workerId: 'worker-1',
      },
      {
        jobId: 'job-2',
        status: 'S', // Status "succeeded"
        action: 'D', // Destroy Worker
        progression: 100,
        modifiedAt: new Date('2025-04-28T21:12:30').toISOString(),
        workerId: 'worker-2',
      }
    ];

    (getJobs as any).mockResolvedValue(mockJobs);

    render(JobsCompo);

    const job1ModifiedDate = new Date('2025-03-10T21:12:30').toLocaleString('fr-FR');
    const job2ModifiedDate = new Date('2025-04-28T21:12:30').toLocaleString('fr-FR');

    await waitFor(() => {
      expect(screen.getByText('Deploy Worker')).toBeInTheDocument();
      expect(screen.getByText('worker-1')).toBeInTheDocument();
      expect(screen.getByTestId('status-pill-job-1')).toHaveClass('running');
      expect(screen.getByText(job1ModifiedDate)).toBeInTheDocument();
      expect(screen.getByText('Destroy Worker')).toBeInTheDocument();
      expect(screen.getByText('worker-2')).toBeInTheDocument();
      expect(screen.getByTestId('status-pill-job-2')).toHaveClass('succeeded');
      expect(screen.getByText(job2ModifiedDate)).toBeInTheDocument();
    });
  });

  it('should show a message when there are no jobs', async () => {
    (getJobs as any).mockResolvedValue([]);

    render(JobsCompo);

    await waitFor(() => {
      expect(screen.getByText('No jobs currently running.')).toBeInTheDocument();
    });
  });

  it('should display a progress bar when applicable', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'R',
        action: 'C',
        progression: 50,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-1',
      }
    ];

    (getJobs as any).mockResolvedValue(mockJobs);

    render(JobsCompo);

    await waitFor(() => {
      const progressDiv = screen.getByTestId('progress-bar-job-1');
      expect(progressDiv).toHaveStyle('width: 50%');
    });
  });

  it('should have refresh button only for failed jobs', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'F', // Failed => Refresh button should appear
        action: 'C',
        progression: 50,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-1',
      },
      {
        jobId: 'job-2',
        status: 'S', // Succeeded => No refresh button
        action: 'D',
        progression: 100,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-2',
      }
    ];

    (getJobs as any).mockResolvedValue(mockJobs);

    render(JobsCompo);

    await waitFor(() => {
      // Job-1 (failed) => should have both Refresh and Trash buttons
      expect(screen.getByTestId('refresh-button-job-1')).toBeInTheDocument();
      expect(screen.getByTestId('trash-button-job-1')).toBeInTheDocument();

      // Job-2 (succeeded) => should have only Trash button
      expect(screen.queryByTestId('refresh-button-job-2')).toBeNull();
      expect(screen.getByTestId('trash-button-job-2')).toBeInTheDocument();
    });
  });
});
