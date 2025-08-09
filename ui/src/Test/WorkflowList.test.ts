vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import WorkflowList from '../components/WorkflowList.svelte';

// Test data for workflows
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

describe('WorkflowList', () => {
  it('displays workflows in the table', async () => {
    render(WorkflowList, { workflows: mockWorkflows });

    // Verify all workflow rows are rendered
    const rows = await screen.findAllByTestId(/^wf-/);
    expect(rows.length).toBe(2); // Should render 2 workflows

    // Check workflow details are displayed correctly
    expect(screen.getByText('#1')).toBeInTheDocument();
    expect(screen.getByText('wf A')).toBeInTheDocument();
    expect(screen.getByText('#2')).toBeInTheDocument();
    expect(screen.getByText('wf B')).toBeInTheDocument();
  });

  it('displays a message if no workflows are present', async () => {
    render(WorkflowList, { workflows: [] });

    // Verify empty state message appears
    expect(screen.getByText('No Workflow found.')).toBeInTheDocument();
  });
});