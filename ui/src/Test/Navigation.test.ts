// Navigation.test.ts
vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';
import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import App from '../App.svelte';
import { isLoggedIn } from '../lib/Stores/user';

const mockTasks = [
  { taskId: 1, name: 'Task A', status: 'P', workerId: 1, workflowId: 30, stepId: 100, command: 'cmd1' },
  { taskId: 2, name: 'Task B', status: 'S', workerId: 2, workflowId: 10, stepId: 200, command: 'cmd2' },
  { taskId: 3, name: 'Task C', status: 'R', workerId: 1, workflowId: 10, stepId: 100, command: 'cmd3' },
];

const mockWorkers = [
  { workerId: 1, name: 'Worker 1' },
  { workerId: 2, name: 'Worker 2' }
];

const mockWorkflows = [
  { workflowId: 10, name: 'Workflow A' },
  { workflowId: 20, name: 'Workflow B' },
  { workflowId: 30, name: 'Workflow C' }
];

describe('Navigation integration', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    isLoggedIn.set(true);
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.getAllTasks.mockResolvedValue(mockTasks);
  });

  it('should display Dashboard page when clicking "Dashboard" in the ToolBar', async () => {
    const { getByTestId, getByText, queryByText } = render(App);

    // Wait for dashboard content to appear after login
    await waitFor(() => {
      expect(queryByText('Dashboard')).toBeInTheDocument();
    });

    // Click the "Dashboard" button
    const dashboardButton = getByText('Dashboard');
    await fireEvent.click(dashboardButton);

    // Wait for the Dashboard page to be displayed
    await waitFor(() => {
      expect(getByTestId('dashboard-page')).toBeInTheDocument();
    });
  });

  it('should display Setting page when clicking "Settings" in the ToolBar', async () => {
    const { getByTestId, getByText, queryByText } = render(App);

    // Wait for dashboard content to appear after login
    await waitFor(() => {
      expect(queryByText('Settings')).toBeInTheDocument();
    });

    // Click the "Settings" button
    const settingsButton = getByText('Settings');
    await fireEvent.click(settingsButton);

    // Wait for the Settings page to be displayed
    await waitFor(() => {
      expect(getByTestId('settings-page')).toBeInTheDocument();
    });
  });

  it('should display Tasks page when clicking "Tasks" in the ToolBar', async () => {
    const { getByTestId, getByText, queryByText } = render(App);

    // Wait for dashboard content to appear after login
    await waitFor(() => {
      expect(queryByText('Tasks')).toBeInTheDocument();
    });

    // Click the "Tasks" button
    const tasksButton = getByText('Tasks');
    await fireEvent.click(tasksButton);

    // Wait for the Tasks page to be displayed
    await waitFor(() => {
      expect(getByTestId('tasks-page')).toBeInTheDocument();
    });
  });

  it('should navigate to Pending tasks when clicking through Starting chevron > Pending', async () => {
    mockApi.getAllTasks.mockImplementation((workerId, wfId, stepId, status, sortBy, command, limit, offset) => {
      if (status === 'P') {
        return Promise.resolve(mockTasks.filter(t => t.status === 'P'));
      }
      return Promise.resolve(mockTasks);
    });

    render(App);

    await waitFor(() => {
      expect(screen.getByText('Tasks')).toBeInTheDocument();
    });

    const tasksChev = screen.getByTestId('tasks-chevron');
    await fireEvent.click(tasksChev);

    await waitFor(() => {
      expect(screen.getByText('Starting')).toBeInTheDocument();
    });

    const startingButton = screen.getByTestId('starting-button');
    await fireEvent.click(startingButton);

    await waitFor(() => {
      expect(screen.getByTestId('pending-link')).toBeInTheDocument();
    });

    const pendingLink = screen.getByTestId('pending-link');
    await fireEvent.click(pendingLink);

    await waitFor(() => {
      console.log(mockApi.getAllTasks.mock.calls);
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(
        undefined, undefined, undefined, 'P', 'task', undefined, 25, 0
      );
      expect(screen.getByText('Task A')).toBeInTheDocument();
      expect(screen.queryByText('Task B')).not.toBeInTheDocument();
      expect(screen.queryByText('Task C')).not.toBeInTheDocument();
    });
  });

  it('should navigate to worker tasks when clicking through worker name in dashboard', async () => {
    mockApi.getAllTasks.mockImplementation((workerId) => {
      return Promise.resolve(mockTasks.filter(t => t.workerId === workerId));
    });

    render(App);

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument();
    });

    const dashboardButton = screen.getByText('Dashboard');
    await fireEvent.click(dashboardButton);

    await waitFor(() => {
      expect(screen.getByTestId('worker-name-1')).toBeInTheDocument();
    });

    const workerLink = screen.getByTestId('worker-name-1');
    await fireEvent.click(workerLink);

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(1, undefined, undefined, undefined, 'task', undefined, 25, 0);
      console.log(mockApi.getAllTasks.mock.calls);
      expect(screen.getByText('Task A')).toBeInTheDocument();
      expect(screen.queryByText('Task B')).not.toBeInTheDocument();
      expect(screen.getByText('Task C')).toBeInTheDocument();
    });
  });

  it('should navigate to Pending tasks when clicking through dashboard > Pending', async () => {
    (mockApi.getAllTasks as any).mockImplementation(
        async (_wId: number, _wfId: number, _stepId: number, status: string) =>
        mockTasks.filter((t) => !status || t.status === status)
    );

    const { queryByText, getByTestId } = render(App);
    
    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument();
    });

    const dashboardButton = screen.getByText('Dashboard');
    await fireEvent.click(dashboardButton);

    await waitFor(() => {
      expect(getByTestId('dashboard-page')).toBeInTheDocument();
      expect(getByTestId('pending-link-dashboard')).toBeInTheDocument();
    });

    const pendingDashboard = getByTestId('pending-link-dashboard');
    await fireEvent.click(pendingDashboard);
    await waitFor(() => {
        expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'P', 'task', undefined, 25, 0);
        expect(queryByText('Task A')).toBeInTheDocument();
        expect(queryByText('Task B')).not.toBeInTheDocument();
        expect(queryByText('Task C')).not.toBeInTheDocument();
    });
  });

  it('should display Workflow page when clicking "Workflows" in the ToolBar', async () => {
    const { getByTestId, getByText, queryByText } = render(App);

    await waitFor(() => {
      expect(queryByText('Workflows')).toBeInTheDocument();
    });

    const wfButton = getByText('Workflows');
    await fireEvent.click(wfButton);

    await waitFor(() => {
      expect(getByTestId('wf-page')).toBeInTheDocument();
    });
  });

  it('should navigate to workflow tasks when clicking through workflow name in Workflows', async () => {
    mockApi.getAllTasks.mockImplementation((workerId, wfId) => {
      return Promise.resolve(mockTasks.filter(t => t.workflowId === wfId));
    });

    render(App);

    await waitFor(() => {
      expect(screen.getByText('Workflows')).toBeInTheDocument();
    });

    const workflowsButton = screen.getByText('Workflows');
    await fireEvent.click(workflowsButton);

    await waitFor(() => {
      expect(screen.getByText('Workflow A')).toBeInTheDocument();
    });

    const workflowName = screen.getByText('Workflow A');
    await fireEvent.click(workflowName);

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(
        undefined, 10, undefined, undefined, 'task', undefined, 25, 0
      );
      expect(screen.queryByText('Task A')).not.toBeInTheDocument();
      expect(screen.getByText('Task B')).toBeInTheDocument();
      expect(screen.getByText('Task C')).toBeInTheDocument();
    });
  });

  it('should navigate to step tasks when clicking through step name in Workflows', async () => {
    mockApi.getSteps.mockResolvedValue([
      { stepId: 100, name: 'Step A', workflowId: 10 },
      { stepId: 200, name: 'Step B', workflowId: 10 }
    ]);

    mockApi.getAllTasks.mockImplementation((workerId, wfId, stepId) => {
      return Promise.resolve(mockTasks.filter(t => t.stepId === stepId));
    });

    render(App);

    await waitFor(() => {
      expect(screen.getByText('Workflows')).toBeInTheDocument();
    });

    const workflowsButton = screen.getByText('Workflows');
    await fireEvent.click(workflowsButton);

    await waitFor(() => {
      expect(screen.getByTestId('chevronRight-10')).toBeInTheDocument();
    });

    const chevronBtn = screen.getByTestId('chevronRight-10');
    await fireEvent.click(chevronBtn);

    await waitFor(() => {
      expect(screen.getByText('Step A')).toBeInTheDocument();
    });

    const stepBtn = screen.getByText('Step A');
    await fireEvent.click(stepBtn);

    await waitFor(() => {
      expect(mockApi.getAllTasks).toHaveBeenCalledWith(
        undefined, 10, 100, undefined, 'task', undefined, 25, 0
      );
      expect(screen.getByText('Task A')).toBeInTheDocument();
      expect(screen.queryByText('Task B')).not.toBeInTheDocument();
      expect(screen.getByText('Task C')).toBeInTheDocument();
    });
  });
  
  it('should display Workflow Template page when clicking "Templates" in the ToolBar', async () => {
    const { getByTestId, getByText, queryByText } = render(App);

    await waitFor(() => {
      expect(queryByText('Templates')).toBeInTheDocument();
    });

    const wfTempButton = getByText('Templates');
    await fireEvent.click(wfTempButton);

    await waitFor(() => {
      expect(getByTestId('wfTemp-page')).toBeInTheDocument();
    });
  });

  it('should switch between light and dark modes', async () => {
    const { getByTestId } = render(App);
    const toggle = getByTestId("theme-button");
    
    // Initial state (dark mode from mock)
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    
    // Click to toggle theme
    await fireEvent.click(toggle);
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
  });
});