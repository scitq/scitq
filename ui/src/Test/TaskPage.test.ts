vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { tick } from 'svelte';
import TaskPage from '../pages/TaskPage.svelte';

const mockTasks = [
  { taskId: 1, name: 'Task A', status: 'P', workerId: 1, workflowId: 10 },
  { taskId: 2, name: 'Task B', status: 'S', workerId: 2, workflowId: 20 },
  { taskId: 3, name: 'Task C', status: 'R', workerId: 1, workflowId: 10 },
  { taskId: 4, name: 'Task D', status: 'S', workerId: 4, workflowId: 30 },
];

const mockWorkers = [
  { workerId: 1, name: 'Worker One' },
  { workerId: 2, name: 'Worker Two' },
];

const mockWorkflows = [
  { workflowId: 10, name: 'Workflow A' },
  { workflowId: 20, name: 'Workflow B' },
];

const mockLog = [
  {taskId: 1, stdout: ['log line 1', 'log line 2'], stderr: ['error line 1']}
]

describe('TaskPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.getAllTasks.mockResolvedValue(mockTasks);
    mockApi.getLogsBatch.mockImplementation((taskIds, limit, skip, logType) => {
    const logs = mockLog.filter(log => taskIds.includes(log.taskId));
      return Promise.resolve(
        logs.map(log => ({
          taskId: log.taskId,
          stdout: logType === 'stdout' ? log.stdout.slice(skip, skip + limit) : [],
          stderr: logType === 'stderr' ? log.stderr.slice(skip, skip + limit) : [],
        }))
      );
    });
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
    render(TaskPage);
    
    await waitFor(() => {
      expect(screen.getByText('Task A')).toBeInTheDocument();
    });

    const eyeIcon = screen.getByTestId("full-log-1");
    await fireEvent.click(eyeIcon);

    await waitFor(() => {
      expect(screen.getByTestId('modal-log-1')).toBeInTheDocument();
    });

    await waitFor(() => {
      expect(screen.getByText((content) => content.includes('log line 1'))).toBeInTheDocument();
      expect(screen.getByText((content) => content.includes('log line 2'))).toBeInTheDocument();
      expect(screen.getByText((content) => content.includes('error line 1'))).toBeInTheDocument();
    });
  });

  it('closes log modal when clicking close button', async () => {
    render(TaskPage);

    await waitFor(() => {
      expect(screen.getByText('Task A')).toBeInTheDocument();
    });

    const eyeIcon = screen.getByTestId("full-log-1");
    await fireEvent.click(eyeIcon);

    await waitFor(() => {
      expect(screen.getByTestId('modal-log-1')).toBeInTheDocument();
    });
    
    await fireEvent.click(screen.getByText('Close'));
    
    await waitFor(() => {
      expect(screen.queryByTestId('modal-log-1')).not.toBeInTheDocument();
    });
  });

  it('loads more logs when clicking load more button', async () => {
    // Clear any previous mock implementations
    mockApi.getLogsBatch.mockReset();
    
    // Set up sequential mock responses
    mockApi.getLogsBatch.mockImplementation((taskIds, limit, skip, logType) => {
      if (skip === 0) {
        return Promise.resolve([{
          taskId: 4,
          stdout: ['log line 1'],
          stderr: []
        }]);
      }
      if (skip > 0) {
        return Promise.resolve([{
          taskId: 4,
          stdout: ['older log line 1', 'older log line 2'],
          stderr: []
        }]);
      }
      return Promise.resolve([]);
    });

    render(TaskPage);
    await waitFor(() => expect(screen.getByText('Task D')).toBeInTheDocument());

    // Open the log modal
    await fireEvent.click(screen.getByTestId('full-log-4'));
    await waitFor(() => expect(screen.getByText('log line 1')).toBeInTheDocument());
    expect(screen.queryByText((content) => content.includes('older log line 1'))).not.toBeInTheDocument();
    expect(screen.queryByText((content) => content.includes('older log line 2'))).not.toBeInTheDocument();

    // Click load more
    await waitFor(() => expect(screen.getByTestId('load-more-output-4')).toBeInTheDocument());
    const loadMoreArrow = await screen.findByTestId('load-more-output-4');
    await fireEvent.click(loadMoreArrow);
    
    // Wait for updates
    await waitFor(() => {
      expect(screen.getByText((content) => content.includes('older log line 1'))).toBeInTheDocument();
      expect(screen.getByText((content) => content.includes('older log line 2'))).toBeInTheDocument();
    });
  });

  it('streams logs for running tasks', async () => {
    const mockStreamCallback = vi.fn();
    mockApi.streamTaskLogsOutput.mockImplementation((taskId, callback) => {
      callback({ logType: 'stdout', logText: 'streamed log' });
      return mockStreamCallback;
    });

    render(TaskPage);
    await waitFor(() => screen.getByText('Task C'));
    
    await fireEvent.click(screen.getByTestId('full-log-3'));
    
    await waitFor(() => {
      expect(screen.getByText('streamed log')).toBeInTheDocument();
    });
  });

});
