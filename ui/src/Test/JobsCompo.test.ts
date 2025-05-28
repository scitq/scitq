import { render, screen, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import JobsCompo from '../components/JobsCompo.svelte';
import { getJobs, delJob } from '../lib/api';

// Mock the `getJobs` function and utility helpers
vi.mock('../lib/api', () => ({
  getJobs: vi.fn(),
  getJobStatusClass: (status: string) => {
    switch (status) {
      case 'R': return 'running';
      case 'S': return 'succeeded';
      default: return 'unknown';
    }
  },
  delJob: vi.fn(),
  getJobStatusText: (status: string) => status,
}));

describe('JobsCompo', () => {
  it('should display the job information correctly', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'R', // "Running" status
        action: 'C', // Deploy Worker
        progression: 50,
        modifiedAt: new Date('2025-03-10T21:12:30').toISOString(),
        workerId: 'worker-1',
      },
      {
        jobId: 'job-2',
        status: 'S', // "Succeeded" status
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
        status: 'F', // Failed => Refresh button should be visible
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

      // Job-2 (succeeded) => should only have Trash button
      expect(screen.queryByTestId('refresh-button-job-2')).toBeNull();
      expect(screen.getByTestId('trash-button-job-2')).toBeInTheDocument();
    });
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
      },
      {
        jobId: 2,
        status: 'S',
        action: 'C',
        progression: 100,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-2',
      }
    ];

    // Mock API responses
    (getJobs as any).mockResolvedValue(mockJobs);

    render(JobsCompo);

    // Wait until jobs appear
    await waitFor(() => {
      expect(screen.getByTestId('job-row-1')).toBeInTheDocument();
      expect(screen.getByTestId('job-row-2')).toBeInTheDocument();
    });

    // Click the delete button for jobId = 1
    const deleteButton = screen.getByTestId('trash-button-1');
    await deleteButton.click();

    // Wait for DOM update
    await waitFor(() => {
      expect(screen.queryByTestId('job-row-1')).toBeNull(); // Disappeared
      expect(screen.queryByTestId('job-row-2')).toBeInTheDocument(); // Still present
    });

    // Confirm delJob was called with correct jobId
    expect(delJob).toHaveBeenCalledWith({ jobId: 1 });
  });
});
