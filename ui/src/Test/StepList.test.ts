vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import StepList from '../components/StepList.svelte';

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

  it('displays a message if no steps are present', async () => {
    mockApi.getSteps.mockResolvedValue([]);
    render(StepList, { workflowId: 99 });

    expect(await screen.findByText('No steps found for workflow #99')).toBeInTheDocument();
  });
});
