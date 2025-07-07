// Navigation.test.ts
vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import App from '../App.svelte';
import { isLoggedIn, userInfo } from '../lib/Stores/user';



const mockTasks = [
  { taskId: 1, name: 'Task A', status: 'P', workerId: 1, workflowId: 30, stepId: 100 },
  { taskId: 2, name: 'Task B', status: 'S', workerId: 2, workflowId: 10, stepId: 200 },
  { taskId: 3, name: 'Task C', status: 'R', workerId: 1, workflowId: 10, stepId: 100  },
];

describe('Navigation integration', () => {
  beforeEach(() => {
    // Reset stores before each test
    isLoggedIn.set(true);
    userInfo.set({ token: 'token' });
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
    (mockApi.getAllTasks as any).mockImplementation(
        async (_wId: number, _wfId: number, _stepId: number, status: string) =>
        mockTasks.filter((t) => !status || t.status === status)
    );

    const { queryByText, getByTestId } = render(App);

    // Wait for "Tasks" to appear
    await waitFor(() => {
        expect(queryByText('Tasks')).toBeInTheDocument();
    });

    // 1. Wait for "Tasks" to be clickable
    const tasksChev = getByTestId('tasks-chevron');
    await fireEvent.click(tasksChev);

    await waitFor(() => {
        expect(queryByText('Starting')).toBeInTheDocument();
    });

    // 2. Click on "Starting"
    const startingButton = getByTestId('starting-button');
    await fireEvent.click(startingButton);

    // Wait for the "Pending" link to be visible in the DOM
    await waitFor(() => {
        expect(getByTestId('pending-link')).toBeInTheDocument();
    });

    // 3. Wait for "Pending" to be visible
    const pendingLink = getByTestId('pending-link');
    await fireEvent.click(pendingLink);

    // 4. Verify the filtered tasks are displayed correctly
    await waitFor(() => {
        expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'P', 'task');
        expect(queryByText('Task A')).toBeInTheDocument();
        expect(queryByText('Task B')).not.toBeInTheDocument();
        expect(queryByText('Task C')).not.toBeInTheDocument();
    });
  });

  it('should navigate to worker tasks when clicking through worker name in dashboard', async () => {
    
    (mockApi.getAllTasks as any).mockImplementation(
      async (workerId : number) =>
        mockTasks.filter((t) => !workerId || t.workerId === workerId)
    );
    
    mockApi.getWorkers.mockResolvedValue([
      { workerId: 1, concurrency: 5, prefetch: 10, name: 'Worker 1' }
    ]);
    window.location.hash = '#/tasks?workerId=1';
    const { queryByText, getByText, getByTestId } = render(App);

    // Wait for "Dashboard" to appear
    await waitFor(() => {
        expect(queryByText('Dashboard')).toBeInTheDocument();
    });

    // Click the "Dashboard" button
    const dashboardButton = getByText('Dashboard');
    await fireEvent.click(dashboardButton);

    await waitFor(() => {
        expect(getByText('Worker 1')).toBeInTheDocument();
    });

    // 2. Click on "Worker 1"
    const workerLink = getByText('Worker 1');
    await fireEvent.click(workerLink);

    // 4. Verify the filtered tasks are displayed correctly
    await waitFor(() => {
        expect(mockApi.getAllTasks).toHaveBeenCalledWith(1, undefined, undefined, undefined, 'task');
        expect(queryByText('Task A')).toBeInTheDocument();
        expect(queryByText('Task B')).not.toBeInTheDocument();
        expect(queryByText('Task C')).toBeInTheDocument();
    });
  });

  it('should navigate to Pending tasks when clicking through dashboard > Pending', async () => {
    (mockApi.getAllTasks as any).mockImplementation(
        async (_wId: number, _wfId: number, _stepId: number, status: string) =>
        mockTasks.filter((t) => !status || t.status === status)
    );

    const { queryByText, getByTestId } = render(App);

    await waitFor(() => {
      expect(getByTestId('dashboard-page')).toBeInTheDocument();
      expect(getByTestId('pending-link-dashboard')).toBeInTheDocument();
    });

    const pendingDashboard = getByTestId('pending-link-dashboard');
    await fireEvent.click(pendingDashboard);
    await waitFor(() => {
        expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, undefined, undefined, 'P', 'task');
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
  
    (mockApi.getAllTasks as any).mockImplementation(
        async (_wId: number, _wfId: number, _stepId: number, status: string) =>
        mockTasks.filter((t) => !_wfId || t.workflowId === _wfId)
    );
    
    mockApi.getWorkFlow.mockResolvedValue([
      { workflowId: 10, name: 'Workflow A' },
      { workflowId: 20, name: 'Workflow B' },
    ]);
    window.location.hash = '#/tasks?workflowId=10';
    const { queryByText, getByText, getByTestId } = render(App);

    await waitFor(() => {
        expect(queryByText('Workflows')).toBeInTheDocument();
    });

    const workflowsButton = getByText('Workflows');
    await fireEvent.click(workflowsButton);

    await waitFor(() => {
        expect(getByText('Workflow A')).toBeInTheDocument();
    });

    const workflowName = getByText('Workflow A');
    await fireEvent.click(workflowName);

    await waitFor(() => {
        expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, 10, undefined, undefined, 'task');
        expect(queryByText('Task A')).not.toBeInTheDocument();
        expect(queryByText('Task B')).toBeInTheDocument();
        expect(queryByText('Task C')).toBeInTheDocument();
    });
  });

  it('should navigate to step tasks when clicking through step name in Workflows', async () => {
  
    (mockApi.getAllTasks as any).mockImplementation(
        async (_wId: number, _wfId: number, _stepId: number, status: string) =>
        mockTasks.filter((t) => !_stepId || t.stepId === _stepId)
    );
    
    mockApi.getWorkFlow.mockResolvedValue([
      { workflowId: 10, name: 'Workflow A' },
      { workflowId: 20, name: 'Workflow B' },
    ]);
        
      mockApi.getSteps.mockImplementation(async (workflowId) => {
        if (workflowId === 10) {
          return [
            { stepId: 100, name: 'Step A', workflowId: 10 },
            { stepId: 200, name: 'Step B', workflowId: 10 },
          ];
        }
        return [];
      });
    window.location.hash = '#/tasks?workflowId=10&stepId=1';
    const { queryByText, getByText, getByTestId } = render(App);

    await waitFor(() => {
        expect(queryByText('Workflows')).toBeInTheDocument();
    });

    const workflowsButton = getByText('Workflows');
    await fireEvent.click(workflowsButton);

    await waitFor(() => {
        expect(getByTestId('chevronRight-10')).toBeInTheDocument();
    });

    const chevronBtn = getByTestId('chevronRight-10');
    await fireEvent.click(chevronBtn);

    await waitFor(() => {
        expect(queryByText('Step A')).toBeInTheDocument();
        expect(queryByText('Step B')).toBeInTheDocument();
    });

    const stepBtn = getByText('Step A');
    await fireEvent.click(stepBtn);

    await waitFor(() => {
        expect(mockApi.getAllTasks).toHaveBeenCalledWith(undefined, 10, 100, undefined, 'task');
        expect(queryByText('Task A')).toBeInTheDocument();
        expect(queryByText('Task B')).not.toBeInTheDocument();
        expect(queryByText('Task C')).toBeInTheDocument();
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
    
    // Click to toggle
    await fireEvent.click(toggle);
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
  });
  
});
