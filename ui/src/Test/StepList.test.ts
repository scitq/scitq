vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach} from 'vitest';
import StepList from '../components/StepList.svelte';
import { wsClient } from '../lib/wsClient';

let messageHandler: (msg: any) => void;


// Mock data injected via prop
const mockSteps = [
  {
    stepId: 1,
    name: 'step A',
    workflowId: 5,
  },
  {
    stepId: 2,
    name: 'step B',
    workflowId: 5,
  },
    {
    stepId: 3,
    name: 'step C',
    workflowId: 10,
  },
];

describe('StepList', () => {
  beforeEach(() => {
    vi.clearAllMocks();

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
      messageHandler = handler; // Store the handler for later use
      return () => true; // Unsubscribe function
    });
  });
  
  it('displays steps for a given workflowId', async () => {
    mockApi.getSteps.mockImplementation(async (workflowId) => {
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

  it('deletes a step when clicking the clear button and confirms', async () => {
    mockApi.getSteps.mockImplementation(async (workflowId) => {
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

    // Simulate WebSocket response
    if (messageHandler) {
      messageHandler({
        type: 'step-deleted',
        payload: {
          stepId: 1
        }
      });
    }

    // Verify step was removed from the list
    await waitFor(() => {
      expect(screen.queryByText('step A')).not.toBeInTheDocument();
      expect(screen.getByText('step B')).toBeInTheDocument();
    });
  });

  it('displays a message if no steps are present', async () => {
    mockApi.getSteps.mockResolvedValue([]);
    render(StepList, { workflowId: 99 });

    expect(await screen.findByText('No steps found for workflow #99')).toBeInTheDocument();
  });
});