vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import WorkflowList from '../components/WorkflowList.svelte';

// Mock data injected via prop
const mockWorkflows = [
  {
    workflowId: 1,
    name: 'wf A',
  },
  {
    workflowId: 2,
    name: 'wf B',
  },
];

describe('WorflowList', () => {
  it('displays worflows in the table', async () => {
    render(WorkflowList, { workflows: mockWorkflows });

    // Checks that rows are properly rendered
    const rows = await screen.findAllByTestId(/^wf-/);
    expect(rows.length).toBe(2); // 2 mocked tasks

    // Checks that contents are visible
    expect(screen.getByText('#1')).toBeInTheDocument();
    expect(screen.getByText('wf A')).toBeInTheDocument();
    expect(screen.getByText('#2')).toBeInTheDocument();
    expect(screen.getByText('wf B')).toBeInTheDocument();
  });

  it('displays a message if no workflows are present', async () => {
    render(WorkflowList, { workflows: [] });

    expect(screen.getByText('No Workflow found.')).toBeInTheDocument();
  });
});
