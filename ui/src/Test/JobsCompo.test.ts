vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import JobsCompo from '../components/JobsCompo.svelte';

describe('JobsCompo', () => {
  const mockOnJobDeleted = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('should display the job information correctly', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'R',
        action: 'C',
        progression: 50,
        modifiedAt: new Date('2025-03-10T21:12:30').toISOString(),
        workerId: 'worker-1',
      },
      {
        jobId: 'job-2',
        status: 'S',
        action: 'D',
        progression: 100,
        modifiedAt: new Date('2025-04-28T21:12:30').toISOString(),
        workerId: 'worker-2',
      }
    ];

    render(JobsCompo, {
      props: {
        jobs: mockJobs,
        onJobDeleted: mockOnJobDeleted
      }
    });

    const job1ModifiedDate = new Date('2025-03-10T21:12:30').toLocaleString();
    const job2ModifiedDate = new Date('2025-04-28T21:12:30').toLocaleString();

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
    render(JobsCompo, {
      props: {
        jobs: [],
        onJobDeleted: mockOnJobDeleted
      }
    });

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

    render(JobsCompo, {
      props: {
        jobs: mockJobs,
        onJobDeleted: mockOnJobDeleted
      }
    });

    await waitFor(() => {
      const progressDiv = screen.getByTestId('progress-bar-job-1');
      expect(progressDiv).toHaveStyle('width: 50%');
    });
  });

  it('should have refresh button only for failed jobs', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'F',
        action: 'C',
        progression: 50,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-1',
      },
      {
        jobId: 'job-2',
        status: 'S',
        action: 'D',
        progression: 100,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-2',
      }
    ];

    render(JobsCompo, {
      props: {
        jobs: mockJobs,
        onJobDeleted: mockOnJobDeleted
      }
    });

    await waitFor(() => {
      expect(screen.getByTestId('refresh-button-job-1')).toBeInTheDocument();
      expect(screen.getByTestId('trash-button-job-1')).toBeInTheDocument();
      expect(screen.queryByTestId('refresh-button-job-2')).toBeNull();
      expect(screen.getByTestId('trash-button-job-2')).toBeInTheDocument();
    });
  });

  it('should call onJobDeleted when delete button is clicked', async () => {
    const mockJobs = [
      {
        jobId: 'job-1',
        status: 'S',
        action: 'D',
        progression: 100,
        modifiedAt: new Date().toISOString(),
        workerId: 'worker-1',
      }
    ];

    mockApi.delJob.mockResolvedValue({});

    render(JobsCompo, {
      props: {
        jobs: mockJobs,
        onJobDeleted: mockOnJobDeleted
      }
    });

    await waitFor(() => {
      fireEvent.click(screen.getByTestId('trash-button-job-1'));
    });

    await waitFor(() => {
      expect(mockOnJobDeleted).toHaveBeenCalledWith({
        detail: { jobId: 'job-1' }
      });
    });
  });
});