vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import WorkflowPage from '../pages/WorkflowPage.svelte';

const mockSteps= [
  { stepId: 1, name: 'Step A', workflowId: 10 },
  { stepId: 2, name: 'Step B', workflowId: 20 },
  { stepId: 3, name: 'Step C', workflowId: 10 },
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
    mockApi.getSteps.mockImplementation(async (workflowId) => {
        return mockSteps.filter(step => step.workflowId === workflowId);
    });
  });

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
