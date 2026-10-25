import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import WorkflowPage from '../pages/WorkflowPage.svelte';
import { wsConnect, wsReset, type FakeWebSocket } from './wsHarness';

let ws: FakeWebSocket;

const mockSteps= [
  { stepId: 1, stepName: 'Step A', workflowId: 10 },
  { stepId: 2, stepName: 'Step B', workflowId: 20 },
  { stepId: 3, stepName: 'Step C', workflowId: 10 },
];

const mockWorkers = [
  { workerId: 1, name: 'Worker One' },
  { workerId: 2, name: 'Worker Two' },
];

const mockWorkflows = [
  { workflowId: 10, name: 'Workflow A' },
  { workflowId: 20, name: 'Workflow B' },
];

describe('WorkflowPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getWorkers.mockResolvedValue(mockWorkers);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.getStepStats.mockImplementation(async ({ workflowId }) => {
        return mockSteps.filter(step => step.workflowId === workflowId);
    });
    ws = wsConnect();
  });
  afterEach(() => wsReset());

  it('displays the initial list of workflows', async () => {
    render(WorkflowPage);
    await waitFor(() => {
      expect(screen.getByText('Workflow A')).toBeInTheDocument();
      expect(screen.getByText('Workflow B')).toBeInTheDocument();
      expect(screen.queryByText('Step A')).not.toBeInTheDocument();
      expect(screen.queryByText('Step B')).not.toBeInTheDocument();
      expect(screen.queryByText('Step C')).not.toBeInTheDocument();
    });
  });

  it('deletes a workflow when clicking the delete button', async () => {
    render(WorkflowPage);
    await waitFor(() => screen.getByText('Workflow A'));
    
    // Click the Delete button
    const deleteButton = screen.getByTestId('delete-btn-10');
    await fireEvent.click(deleteButton);
    
    // Verify API call
    await waitFor(() => {
      expect(mockApi.delWorkflow).toHaveBeenCalledWith(10);
    });
    
    // Server event confirming the deletion
    ws.deliver({ type: 'workflow', action: 'deleted', id: 10, payload: { workflowId: 10 } });
    
    // Verify workflow was removed
    await waitFor(() => {
      expect(screen.queryByText('Workflow A')).not.toBeInTheDocument();
      expect(screen.getByText('Workflow B')).toBeInTheDocument();
    });
  });

  it('displays steps for a given workflowId when clicking on chevron right', async () => {
    render(WorkflowPage);
    await waitFor(() => {
      expect(screen.getByText('Workflow A')).toBeInTheDocument();
      expect(screen.getByText('Workflow B')).toBeInTheDocument();
      expect(screen.queryByText('Step A')).not.toBeInTheDocument();
      expect(screen.queryByText('Step B')).not.toBeInTheDocument();
      expect(screen.queryByText('Step C')).not.toBeInTheDocument();
    });

    await waitFor(() => screen.getByTestId('chevronRight-10'));
    const chevDownBtn = screen.getByTestId('chevronRight-10');
    await fireEvent.click(chevDownBtn);

    await waitFor(() => {
      expect(screen.getByText('Step A')).toBeInTheDocument();
      expect(screen.queryByText('Step B')).not.toBeInTheDocument();
      expect(screen.getByText('Step C')).toBeInTheDocument();
    });
  });

  it('hides steps for a given workflowId when clicking on chevron down', async () => {
    render(WorkflowPage);
    await waitFor(() => screen.getByTestId('chevronRight-10'));
    const chevDownBtn = screen.getByTestId('chevronRight-10');
    await fireEvent.click(chevDownBtn);

    await waitFor(() => {
      expect(screen.getByText('Workflow A')).toBeInTheDocument();
      expect(screen.getByText('Workflow B')).toBeInTheDocument();
      expect(screen.getByText('Step A')).toBeInTheDocument();
      expect(screen.queryByText('Step B')).not.toBeInTheDocument();
      expect(screen.getByText('Step C')).toBeInTheDocument();
    });

    await waitFor(() => screen.getByTestId('chevronDown-10'));
    const chevRightBtn = screen.getByTestId('chevronDown-10');
    await fireEvent.click(chevRightBtn);

    await waitFor(() => {
      expect(screen.getByText('Workflow A')).toBeInTheDocument();
      expect(screen.getByText('Workflow B')).toBeInTheDocument();
      expect(screen.queryByText('Step A')).not.toBeInTheDocument();
      expect(screen.queryByText('Step B')).not.toBeInTheDocument();
      expect(screen.queryByText('Step C')).not.toBeInTheDocument();
    });
  });
});