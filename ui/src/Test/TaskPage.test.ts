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
      async (_wId: number, _wfId: number, _stepId: number, status: string) =>
        mockTasks.filter((t) => !status || t.status === status)
    );

    render(TaskPage);
    await waitFor(() => screen.getByText('Task A'));

    const statusButton = screen.getByText('Pending');
    await fireEvent.click(statusButton);

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'P', 'task');
      expect(screen.getByText('Task A')).toBeTruthy();
      expect(screen.queryByText('Task B')).not.toBeInTheDocument();

    });
  });

  it('filters tasks by workerId', async () => {
    mockApi.getAllTasks.mockImplementation(
      async (workerId: number) =>
        mockTasks.filter((t) => !workerId || t.workerId === workerId)
    );

    render(TaskPage);
    await waitFor(() => screen.getByText('Task A'));

    const workerSelect = screen.getByLabelText('Worker');
    await fireEvent.change(workerSelect, { target: { value: '1' } });

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(1, undefined, undefined, undefined, 'task');
      expect(screen.getByText('Task A')).toBeTruthy();
      expect(screen.getByText('Task C')).toBeTruthy();
      expect(screen.queryByText('Task B')).not.toBeInTheDocument();
    });
  });

  it('filters tasks by workflowId', async () => {
    mockApi.getAllTasks.mockImplementation(
      async (_wId, workflowId: number) =>
        mockTasks.filter((t) => !workflowId || t.workflowId === workflowId)
    );

    render(TaskPage);
    await waitFor(() => screen.getByText('Task A'));

    const workflowSelect = screen.getByLabelText('Workflow');
    await fireEvent.change(workflowSelect, { target: { value: '10' } });

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, 10, undefined, undefined, 'task');
      expect(screen.getByText('Task A')).toBeTruthy();
      expect(screen.getByText('Task C')).toBeTruthy();
      expect(screen.queryByText('Task B')).not.toBeInTheDocument();
    });
  });

  it('filters tasks by stepId', async () => {
    const stepTasks = [
      { taskId: 1, name: 'Task A', status: 'P', workerId: 1, workflowId: 10, stepId: 100 },
      { taskId: 2, name: 'Task B', status: 'S', workerId: 2, workflowId: 10, stepId: 200 },
    ];

    mockApi.getAllTasks.mockImplementation(
      async (_wId, _wfId, stepId: number) =>
        stepTasks.filter((t) => !stepId || t.stepId === stepId)
    );

    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.getSteps.mockImplementation(async (workflowId) => {
      if (workflowId === 10) {
        return [
          { stepId: 100, name: 'Step 1', workflowId: 10 },
          { stepId: 200, name: 'Step 2', workflowId: 10 },
        ];
      }
      return [];
    });

    render(TaskPage);
    await waitFor(() => screen.getByText('Task A'));

    const workflowSelect = screen.getByLabelText('Workflow');
    await fireEvent.change(workflowSelect, { target: { value: '10' } });

    const stepSelect = await screen.findByLabelText('Step');
    await fireEvent.change(stepSelect, { target: { value: '200' } });

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, 10, 200, undefined, 'task');
      expect(screen.getByText('Task B')).toBeTruthy();
      expect(screen.queryByText('Task A')).not.toBeInTheDocument();
    });
  });


  it('sorts tasks correctly by taskId, workerId, workflowId and stepId', async () => {
    const tasks = [
      { taskId: 3, name: 'Task C', workerId: 2, workflowId: 10 , stepId: 2},
      { taskId: 1, name: 'Task A', workerId: 3, workflowId: 5, stepId: 7 },
      { taskId: 2, name: 'Task B', workerId: 1, workflowId: 8, stepId: 3 },
    ];

    mockApi.getAllTasks.mockImplementation(
      (_wId: number, _wfId: number, _stepId:number, _status: string, sortBy: string) => {
        return [...tasks].sort((a, b) => {
          if (sortBy === 'worker') return a.workerId - b.workerId;
          if (sortBy === 'wf') return a.workflowId - b.workflowId;
          if (sortBy === 'step') return a.stepId - b.stepId;
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
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, undefined, 'task');
      expect(getNames()).toEqual(['Task A', 'Task B', 'Task C']);
    });

    // Sort by workerId
    await fireEvent.change(select, { target: { value: 'worker' } });
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, undefined, 'worker');
      expect(getNames()).toEqual(['Task B', 'Task C', 'Task A']);
    });

    // Sort by workflowId
    await fireEvent.change(select, { target: { value: 'wf' } });
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, undefined, 'wf');
      expect(getNames()).toEqual(['Task A', 'Task B', 'Task C']);
    });

    // Sort by stepId
    await fireEvent.change(select, { target: { value: 'step' } });
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, undefined, 'step');
      expect(getNames()).toEqual(['Task C', 'Task B', 'Task A']);
    });
  });

  it('opens log modal when clicking on eye icon', async () => {
    mockApi.getLogsBatch.mockResolvedValue([{
      taskId: 1,
      stdout: ['log line 1', 'log line 2'],
      stderr: ['error line 1']
    }]);

    render(TaskPage);
    await waitFor(() => screen.getByText('Task A'));
    
    const eyeIcons = screen.getAllByTestId('eye-icon');
    await fireEvent.click(eyeIcons[0]);
    
    await waitFor(() => {
      expect(screen.getByText('📜 Logs for Task 1')).toBeInTheDocument();
      expect(screen.getByText('log line 1')).toBeInTheDocument();
      expect(screen.getByText('error line 1')).toBeInTheDocument();
    });
  });
  
  it('closes log modal when clicking close button', async () => {
    render(TaskPage);
    await fireEvent.click(screen.getByTestId('eye-icon'));
    await waitFor(() => screen.getByText('📜 Logs for Task 1'));
    
    await fireEvent.click(screen.getByText('Close'));
    
    await waitFor(() => {
      expect(screen.queryByText('📜 Logs for Task 1')).not.toBeInTheDocument();
    });
  });

});
