vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';
import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import TaskPage from '../pages/TaskPage.svelte';

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
    // 1. First call - initial tasks
    mockApi.getAllTasks.mockImplementationOnce(() => 
      Promise.resolve(mockTasksFirstPage)
    );
    
    // 2. Subsequent calls - new tasks + old ones
    mockApi.getAllTasks.mockImplementation(() => 
      Promise.resolve([...mockNewTasks, ...mockTasksFirstPage])
    );

    render(TaskPage);
    
    // 3. Wait for initial load
    await waitFor(() => screen.getByText('Task 1'));
    
    // 4. Simulate scrolling to bottom
    const tasksContainer = screen.getByTestId('tasks-page').querySelector('.tasks-list-container');
    if (!tasksContainer) throw new Error('Container not found');
    
    // Set properties to simulate being at bottom
    Object.defineProperty(tasksContainer, 'scrollHeight', { value: 1000 });
    Object.defineProperty(tasksContainer, 'scrollTop', { value: 800 }); // Scrolled down
    Object.defineProperty(tasksContainer, 'clientHeight', { value: 200 });
    
    // 5. Wait for notification to appear
      await waitFor(() => {
        expect(screen.getByText('1 new task available')).toBeInTheDocument();
      }, { timeout: 3000 });
  });

  it('should scroll to top and show new tasks when clicking notification', async () => {
    // 1. First call - initial tasks
    mockApi.getAllTasks.mockImplementationOnce(() => 
      Promise.resolve(mockTasksFirstPage)
    );
    
    // 2. Subsequent calls - new tasks + old ones
    mockApi.getAllTasks.mockImplementation(() => 
      Promise.resolve([...mockNewTasks, ...mockTasksFirstPage])
    );

    render(TaskPage);
    
    // 3. Wait for initial load
    await waitFor(() => screen.getByText('Task 1'));
    
    // 4. Get the container and mock its properties
    const tasksContainer = screen.getByTestId('tasks-page').querySelector('.tasks-list-container');
    if (!tasksContainer) throw new Error('Container not found');
    
    // Create a mock scrollTop that we can modify
    let mockScrollTop = 800;
    
    // Mock scrollTo method
    tasksContainer.scrollTo = vi.fn((options) => {
      mockScrollTop = options.top;
      // Trigger scroll event
      fireEvent.scroll(tasksContainer);
    });
    
    // Make scrollTop configurable so we can modify it
    Object.defineProperty(tasksContainer, 'scrollTop', {
      get: () => mockScrollTop,
      set: (value) => { mockScrollTop = value; },
      configurable: true
    });
    
    Object.defineProperty(tasksContainer, 'scrollHeight', { value: 1000 });
    Object.defineProperty(tasksContainer, 'clientHeight', { value: 200 });
    
    // 5. Wait for notification to appear
    await waitFor(() => {
      expect(screen.getByText('1 new task available')).toBeInTheDocument();
    }, { timeout: 3000 });

    // 6. Click the notification
    const notification = screen.getByTestId('tasks-notification-1');
    await fireEvent.click(notification);

    // 7. Verify scroll behavior
    await waitFor(() => {
      expect(tasksContainer.scrollTo).toHaveBeenCalledWith({
        top: 0,
        behavior: 'smooth'
      });
      expect(mockScrollTop).toBe(0);
    });

    // 8. Verify new tasks are visible
    await waitFor(() => {
      expect(screen.getByText('New Task 6')).toBeInTheDocument();
      expect(screen.queryByText('1 new task available')).not.toBeInTheDocument();
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