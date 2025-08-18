vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';
import { render, fireEvent, waitFor, screen, within } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import TaskPage from '../pages/TaskPage.svelte';
import { wsClient } from '../lib/wsClient';
import { tick } from 'svelte';

let messageHandler: (msg: any) => void;

// Simple mock data
const mockTasksFirstPage = [
  { taskId: 1, name: 'Task 1', status: 'P', workerId: 1, workflowId: 10, command: 'cmd1' },
  { taskId: 2, name: 'Task 2', status: 'S', workerId: 2, workflowId: 20, command: 'cmd2' },
  { taskId: 3, name: 'Task 3', status: 'R', workerId: 1, workflowId: 10, command: 'cmd3' }
];

const mockTasksSecondPage = [
  { taskId: 4, name: 'Task 4', status: 'F', workerId: 2, workflowId: 20, command: 'cmd4' },
  { taskId: 5, name: 'Task 5', status: 'P', workerId: 1, workflowId: 10, command: 'cmd5' }
];

const mockNewTasks = [
  { taskId: 6, name: 'New Task 6', status: 'S', workerId: 2, workflowId: 20, command: 'cmd6' }
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

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
        messageHandler = handler; // Store the handler for later use
        return () => true; // Unsubscribe function
    });
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
    // Configure mock with offset
    let callCount = 0;
    mockApi.getAllTasks.mockImplementation((workerId, wfId, stepId, status, sortBy, command, limit, offset) => {
      if (callCount === 0) {
        callCount++;
        return Promise.resolve(mockTasksFirstPage);
      }
      return Promise.resolve(mockTasksSecondPage);
    });

    render(TaskPage);
    
    // Wait for initial load
    await waitFor(() => screen.getByText('Task 1'));
    
    // Get container
    const tasksContainer = screen.getByTestId('tasks-page').querySelector('.tasks-list-container');
    if (!tasksContainer) throw new Error('Tasks container not found');

    // Simulate being at the bottom
    Object.defineProperty(tasksContainer, 'scrollHeight', { value: 1000 });
    Object.defineProperty(tasksContainer, 'scrollTop', { value: 950 }); // Almost at bottom
    Object.defineProperty(tasksContainer, 'clientHeight', { value: 200 });

    // Trigger scroll
    fireEvent.scroll(tasksContainer);

    // Wait for new tasks to load
    await waitFor(() => {
      expect(screen.getByText('Task 4')).toBeInTheDocument();
      expect(screen.getByText('Task 5')).toBeInTheDocument();
    }, { timeout: 3000 });
  });

it('should show notification when new tasks arrive', async () => {
  // Mock API initial
  mockApi.getAllTasks.mockResolvedValue(mockTasksFirstPage);

  // Simulated new task
  const newTask = {
    taskId: 999,
    command: 'new command',
    status: 'P',
    stepId: null,
    workerId: null,
    workflowId: null,
    taskName: 'New Task'
  };

  // Mock scrollTo to avoid JSDOM errors
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    value: vi.fn(),
    writable: true
  });

  render(TaskPage);

  // Wait for initial load
  await waitFor(() => {
    expect(screen.getByText('Task 1')).toBeInTheDocument();
  });

  // Simulate being scrolled down in the list
  const tasksContainer = screen.getByTestId('tasks-list');
  Object.defineProperties(tasksContainer, {
    scrollTop: { value: 100, writable: true },
    scrollHeight: { value: 1000 },
    clientHeight: { value: 500 }
  });

  // Trigger scroll → updates isScrolledToTop = false
  await fireEvent.scroll(tasksContainer);

  // Send WebSocket message
  messageHandler?.({
    type: 'task-created',
    payload: newTask
  });

  // Verify notification appears
  await waitFor(() => {
    expect(screen.getByText('1 new task available')).toBeInTheDocument();
  }, { timeout: 3000 });
});


it('should scroll to top and show new tasks when clicking notification', async () => {
  // 1. First call → initial tasks
  mockApi.getAllTasks.mockImplementationOnce(() =>
    Promise.resolve(mockTasksFirstPage)
  );

  // 2. Subsequent calls → new task + existing ones
  mockApi.getAllTasks.mockImplementation(() =>
    Promise.resolve([...mockNewTasks, ...mockTasksFirstPage])
  );

  const newTask = {
    taskId: 999,
    command: 'new command',
    status: 'P',
    stepId: null,
    workerId: null,
    workflowId: null,
    taskName: 'New Task'
  };

  // Mock scrollTo
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    value: vi.fn(),
    writable: true
  });

  const { getByTestId, getByText, queryByText } = render(TaskPage);

  // 3. Wait for initial load
  await waitFor(() => screen.getByText('Task 1'));

  // 4. Simulate being scrolled down in the list
  const tasksContainer = getByTestId('tasks-list');
  Object.defineProperties(tasksContainer, {
    scrollTop: { value: 100, writable: true },
    scrollHeight: { value: 1000 },
    clientHeight: { value: 500 }
  });
  await fireEvent.scroll(tasksContainer);

  // 5. Send WebSocket message
  messageHandler?.({
    type: 'task-created',
    payload: newTask
  });

  // 6. Verify notification appears
    await waitFor(() => {
    expect(getByText('1 new task available')).toBeInTheDocument();
    }, { timeout: 3000 });

  // 7. Click notification
  const notification = getByTestId('tasks-notification-1');
  await fireEvent.click(notification);

  // 8. Verify we scrolled to top
  await waitFor(() => {
    expect(tasksContainer.scrollTo).toHaveBeenCalledWith({
      top: 0,
      behavior: 'smooth'
    });
  });

  // 9. Verify new task is visible and notification disappeared
  await waitFor(() => {
    expect(screen.getByText('new command')).toBeInTheDocument();
    expect(queryByText('1 new task available')).not.toBeInTheDocument();
  });
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
        undefined, undefined, undefined, undefined, 'task', 'cmd2', 25, 0
      );
    });
  });
});