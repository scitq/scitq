import { mockApi } from '../mocks/api_mock';
import { render, fireEvent, waitFor, screen, within } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import TaskPage from '../pages/TaskPage.svelte';
import { wsConnect, wsReset, type FakeWebSocket } from './wsHarness';

let ws: FakeWebSocket;

// Simple mock data
const mockTasksFirstPage = [
  { taskId: 1, taskName: 'Task 1', status: 'P', workerId: 1, workflowId: 10, command: 'cmd1' },
  { taskId: 2, taskName: 'Task 2', status: 'S', workerId: 2, workflowId: 20, command: 'cmd2' },
  { taskId: 3, taskName: 'Task 3', status: 'R', workerId: 1, workflowId: 10, command: 'cmd3' }
];

const mockTasksSecondPage = [
  { taskId: 4, taskName: 'Task 4', status: 'F', workerId: 2, workflowId: 20, command: 'cmd4' },
  { taskId: 5, taskName: 'Task 5', status: 'P', workerId: 1, workflowId: 10, command: 'cmd5' }
];

const mockNewTasks = [
  { taskId: 6, taskName: 'New Task 6', status: 'S', workerId: 2, workflowId: 20, command: 'cmd6' }
];

const mockWorkers = [
  { workerId: 1, name: 'Worker One' },
  { workerId: 2, name: 'Worker Two' }
];

const mockWorkflows = [
  { workflowId: 10, name: 'Workflow A' },
  { workflowId: 20, name: 'Workflow B' }
];

describe('TaskPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.getAllTasks.mockResolvedValue(mockTasksFirstPage);
    window.location.hash = '/tasks';
    ws = wsConnect();
  });
  afterEach(() => wsReset());

  it('force-runs a waiting task, and shows it pending once the server confirms', async () => {
    mockApi.getAllTasks.mockResolvedValue([
      { taskId: 7, taskName: 'Waiting task', status: 'W', workflowId: 10, command: 'cmd7' },
      { taskId: 8, taskName: 'Pending task', status: 'P', workflowId: 10, command: 'cmd8' },
    ]);
    render(TaskPage);
    const button = await screen.findByTestId('force-run-7');
    expect(screen.queryByTestId('force-run-8')).toBeNull();

    await fireEvent.click(button);
    expect(mockApi.forceRunTask).toHaveBeenCalledWith(7);

    ws.deliver({ type: 'task', action: 'status', id: 7, payload: { taskId: 7, oldStatus: 'W', status: 'P' } });
    await waitFor(() => expect(screen.queryByTestId('force-run-7')).toBeNull());
  });

  it('should load first page of tasks', async () => {
    render(TaskPage);
    
    await waitFor(() => {
      expect(screen.getByText('Task 1')).toBeInTheDocument();
      expect(screen.getByText('Task 2')).toBeInTheDocument();
      expect(screen.getByText('Task 3')).toBeInTheDocument();
    });
  });

  it('should load more tasks when scrolling down', async () => {
    // A full first page (TASKS_CHUNK_SIZE = 25) means more may exist.
    const fullFirstPage = Array.from({ length: 25 }, (_, i) => (
      { taskId: 100 - i, taskName: `Task ${100 - i}`, status: 'S', workerId: 1, workflowId: 10, command: `cmd${100 - i}` }
    ));
    const secondPage = [
      { taskId: 50, taskName: 'Task 50', status: 'S', workerId: 2, workflowId: 20, command: 'cmd50' },
      { taskId: 49, taskName: 'Task 49', status: 'S', workerId: 2, workflowId: 20, command: 'cmd49' },
    ];
    mockApi.getAllTasks.mockImplementation((workerId, wfId, stepId, status, sortBy, command, limit, offset) =>
      Promise.resolve(offset === 0 ? fullFirstPage : secondPage)
    );

    // jsdom has no layout: give the list a height so the mount-time scroll
    // check sees it at the top rather than at the bottom.
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get: () => 1000 });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get: () => 200 });
    try {
      render(TaskPage);
      await waitFor(() => screen.getByText('Task 100'));
      expect(screen.queryByText('Task 50')).not.toBeInTheDocument();

      const tasksContainer = screen.getByTestId('tasks-list');
      tasksContainer.scrollTop = 950; // Almost at bottom
      await fireEvent.scroll(tasksContainer);

      await waitFor(() => {
        expect(screen.getByText('Task 50')).toBeInTheDocument();
        expect(screen.getByText('Task 49')).toBeInTheDocument();
      }, { timeout: 3000 });
      expect(mockApi.getAllTasks).toHaveBeenLastCalledWith(
        undefined, undefined, undefined, undefined, 'task', undefined, 25, 25, false
      );
    } finally {
      delete (HTMLElement.prototype as any).scrollHeight;
      delete (HTMLElement.prototype as any).clientHeight;
    }
  });

  it('shows a task created over WebSocket at the top of the list', async () => {
    render(TaskPage);
    await waitFor(() => screen.getByText('Task 1'));

    ws.deliver({
      type: 'task', action: 'created', id: 999,
      payload: { taskId: 999, command: 'new command', status: 'P', taskName: 'New Task' },
    });

    await waitFor(() => expect(screen.getByText('New Task')).toBeInTheDocument());
    const rows = screen.getAllByTestId(/^task-\d+$/);
    expect(rows[0]).toHaveAttribute('data-testid', 'task-999');
  });

    it('should sort tasks by worker when selecting sort option', async () => {
    // 1. Configure mock to respond differently based on sortBy
    mockApi.getAllTasks.mockImplementation((workerId, wfId, stepId, status, sortBy, command, limit, offset) => {
      if (sortBy === 'worker') {
        return Promise.resolve([...mockTasksFirstPage].sort((a, b) => a.workerId - b.workerId));
      }
      return Promise.resolve(mockTasksFirstPage);
    });

    render(TaskPage);
    
    // 2. Wait for initial tasks to load
    await waitFor(() => screen.getByText('Task 1'));
    
    // 3. Change sort option
    const sortSelect = screen.getByLabelText('Sort by');
    await fireEvent.change(sortSelect, { target: { value: 'worker' } });
    
    // 4. Verify API call and result
    await waitFor(() => {  
      // Optional: verify display is sorted
      const workerCells = screen.getAllByTestId(/^worker-cell-/);
      const workerNames = workerCells.map(cell => cell.textContent?.trim());
      expect(workerNames).toEqual(['Worker One', 'Worker One', 'Worker Two']);
    });
  });

  it('should filter tasks by command', async () => {
    render(TaskPage);
    
    const searchInput = screen.getByPlaceholderText('Search commands...');
    await fireEvent.input(searchInput, { target: { value: 'cmd2' } });
    await fireEvent.click(screen.getByLabelText('Search'));
    
    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(
        undefined, undefined, undefined, undefined, 'task', 'cmd2', 25, 0, false
      );
    });
  });
});