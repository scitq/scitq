vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import TaskPage from '../pages/TaskPage.svelte';

const mockTasks = [
  { taskId: 1, name: 'Task A', status: 'P', workerId: 1, workflowId: 10 },
  { taskId: 2, name: 'Task B', status: 'S', workerId: 2, workflowId: 20 },
  { taskId: 3, name: 'Task C', status: 'R', workerId: 1, workflowId: 10 },
];

const mockWorkers = [
  { workerId: 1, name: 'Worker One' },
  { workerId: 2, name: 'Worker Two' },
];

const mockWorkflows = [
  { workflowId: 10, name: 'Workflow A' },
  { workflowId: 20, name: 'Workflow B' },
];

describe('TaskPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.getAllTasks.mockResolvedValue(mockTasks);
    window.location.hash = '/tasks'; // reset hash
  });

  it('displays the initial list of tasks', async () => {
    render(TaskPage);
    await waitFor(() => {
      expect(screen.getByText('Task A')).toBeTruthy();
      expect(screen.getByText('Task B')).toBeTruthy();
      expect(screen.getByText('Task C')).toBeTruthy();
    });
  });

  it('filters tasks by status when clicking a status button', async () => {
    mockApi.getAllTasks.mockImplementation(
      async (_wId: number, _wfId: number, status: string) =>
        mockTasks.filter((t) => !status || t.status === status)
    );

    render(TaskPage);
    await waitFor(() => screen.getByText('Task A'));

    const statusButton = screen.getByText('Pending');
    await fireEvent.click(statusButton);

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, 'P', 'task');
      expect(screen.getByText('Task A')).toBeTruthy();
      expect(screen.queryByText('Task B')).toBeNull();
    });
  });

  it('sorts tasks correctly by taskId, workerId, and workflowId', async () => {
    const tasks = [
      { taskId: 3, name: 'Task C', workerId: 2, workflowId: 10 },
      { taskId: 1, name: 'Task A', workerId: 3, workflowId: 5 },
      { taskId: 2, name: 'Task B', workerId: 1, workflowId: 8 },
    ];

    mockApi.getAllTasks.mockImplementation(
      (_wId: number, _wfId: number, _status: string, sortBy: string) => {
        return [...tasks].sort((a, b) => {
          if (sortBy === 'worker') return a.workerId - b.workerId;
          if (sortBy === 'wf') return a.workflowId - b.workflowId;
          return a.taskId - b.taskId;
        });
      }
    );

    render(TaskPage);
    const select = screen.getByLabelText('Sort by');

    const getNames = () =>
      screen
        .getAllByTestId(/task-\d+/)
        .map((row) => row.querySelector('td:nth-child(2)')?.textContent?.trim());

    // Default sort by taskId
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'task');
      expect(getNames()).toEqual(['Task A', 'Task B', 'Task C']);
    });

    // Sort by workerId
    await fireEvent.change(select, { target: { value: 'worker' } });
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'worker');
      expect(getNames()).toEqual(['Task B', 'Task C', 'Task A']);
    });

    // Sort by workflowId
    await fireEvent.change(select, { target: { value: 'wf' } });
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'wf');
      expect(getNames()).toEqual(['Task A', 'Task B', 'Task C']);
    });
  });
});
