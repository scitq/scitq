vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen, fireEvent } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import WfTemplateList from '../components/WfTemplateList.svelte';

// Mock data injected via prop
const mockTemplates = [
  {
    workflowTemplateId: 1,
    name: 'Template A',
    version: '1.0',
    uploadedAt: '2023-01-01T00:00:00Z',
    uploadedBy: 'user1'
  },
  {
    workflowTemplateId: 2,
    name: 'Template B',
    version: '2.0',
    uploadedAt: '2023-01-02T00:00:00Z',
    uploadedBy: 'user2'
  }
];

describe('WfTemplateList', () => {
  it('displays all templates with correct information', async () => {
    render(WfTemplateList, { 
      workflowsTemp: mockTemplates,
      openParamModal: vi.fn() 
    });

    // Check all templates are rendered
    const rows = await screen.findAllByTestId(/^wfTemplate-/);
    expect(rows.length).toBe(2);

    // Verify template A data
    expect(screen.getByText('Template A')).toBeInTheDocument();
    expect(screen.getByText('v1.0')).toBeInTheDocument();
    expect(screen.getByText('01/01/2023')).toBeInTheDocument();
    expect(screen.getByText('user1')).toBeInTheDocument();
    expect(screen.getByText('#1')).toBeInTheDocument();

    // Verify template B data
    expect(screen.getByText('Template B')).toBeInTheDocument();
    expect(screen.getByText('v2.0')).toBeInTheDocument();
    expect(screen.getByText('01/02/2023')).toBeInTheDocument();
    expect(screen.getByText('user2')).toBeInTheDocument();
    expect(screen.getByText('#2')).toBeInTheDocument();

    // Verify action buttons
    expect(screen.getAllByTitle('Play').length).toBe(2);
    expect(screen.getAllByTitle('Pause').length).toBe(2);
  });

  it('displays empty state when no templates', async () => {
    render(WfTemplateList, { 
      workflowsTemp: [],
      openParamModal: vi.fn() 
    });

    expect(await screen.findByText('No Workflow Template found.')).toBeInTheDocument();
  });

  it('calls openParamModal with correct template when Play clicked', async () => {
    const mockOpen = vi.fn();
    render(WfTemplateList, { 
      workflowsTemp: mockTemplates,
      openParamModal: mockOpen 
    });

    const playButtons = await screen.findAllByTitle('Play');
    await fireEvent.click(playButtons[0]);
    
    expect(mockOpen).toHaveBeenCalledWith(mockTemplates[0]);
  });
});