import { mockApi } from '../mocks/api_mock';

import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import StepList from '../components/StepList.svelte';
import { wsConnect, wsReset, type FakeWebSocket } from './wsHarness';

let ws: FakeWebSocket;


// Mock data injected via prop
const mockSteps = [
  {
    stepId: 1,
    stepName: 'step A',
    workflowId: 5,
  },
  {
    stepId: 2,
    stepName: 'step B',
    workflowId: 5,
  },
    {
    stepId: 3,
    stepName: 'step C',
    workflowId: 10,
  },
];

describe('StepList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    ws = wsConnect();
  });
  afterEach(() => wsReset());
  
  it('displays steps for a given workflowId', async () => {
    mockApi.getStepStats.mockImplementation(async ({ workflowId }) => {
        return mockSteps.filter(step => step.workflowId === workflowId);
    });
    render(StepList, { workflowId: 5 });

    // Checks that rows are properly rendered
    const rows = await screen.findAllByTestId(/^step-/);
    expect(rows.length).toBe(2); // Should render 2 steps

    // Check for specific content
    expect(screen.getByText('step A')).toBeInTheDocument();
    expect(screen.getByText('step B')).toBeInTheDocument();
    expect(screen.queryByText('step C')).not.toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.queryByText('3')).not.toBeInTheDocument();
  });

  it('shows a step created while the table is scrolled down', async () => {
    mockApi.getStepStats.mockImplementation(async ({ workflowId }) =>
      mockSteps.filter(step => step.workflowId === workflowId));
    const { container } = render(StepList, { workflowId: 5 });
    await screen.findAllByTestId(/^step-/);

    const table = container.querySelector('.steps-table-wrapper')!;
    table.scrollTop = 500;
    await fireEvent.scroll(table);
    ws.deliver({ type: 'step', action: 'created', id: 5, payload: { stepId: 4, workflowId: 5, name: 'step D' } });

    await waitFor(() => expect(screen.getByText('step D')).toBeInTheDocument());
  });

  it('deletes a step when clicking the clear button and confirms', async () => {
    mockApi.getStepStats.mockImplementation(async ({ workflowId }) => {
        return mockSteps.filter(step => step.workflowId === workflowId);
    });
    render(StepList, { workflowId: 5 });

    await waitFor(() => {
      expect(screen.getByText('step A')).toBeInTheDocument();
      expect(screen.getByTestId('delete-step-1')).toBeInTheDocument();
    });

    // Find and click the Clear button (Eraser icon)
    const clearButton = screen.getByTestId('delete-step-1');
    await fireEvent.click(clearButton); // First button for step A

    // Verify API was called with correct stepId
    await waitFor(() => {
      expect(mockApi.delStep).toHaveBeenCalledWith(1);
    });

    // Server event: id is the workflow, payload carries the step
    ws.deliver({ type: 'step', action: 'deleted', id: 5, payload: { stepId: 1 } });

    // Verify step was removed from the list
    await waitFor(() => {
      expect(screen.queryByText('step A')).not.toBeInTheDocument();
      expect(screen.getByText('step B')).toBeInTheDocument();
    });
  });

  it('displays a message if no steps are present', async () => {
    mockApi.getStepStats.mockResolvedValue([]);
    render(StepList, { workflowId: 99 });

    expect(await screen.findByText('No steps found for workflow #99')).toBeInTheDocument();
  });
});